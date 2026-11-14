// Package importer best-effort imports Claude Code provider profiles out of
// the legacy cc-switch SQLite database (design §8: 從 cc-switch 匯入按鈕).
//
// cc-switch is a third-party internal tool whose schema may change between
// versions, so everything here is deliberately lenient: columns are looked
// up by name (never by position), unknown columns are ignored, and the only
// hard requirements are an existing providers table and a readable
// settings_config blob. Any failure is a definite error for the settings
// window to show — manual profile entry always remains the guaranteed
// fallback path (card: 手動輸入是保底路徑,驗收以它為準).
package importer

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"cc-fuse/internal/domain"

	_ "modernc.org/sqlite" // pure-Go SQLite driver (no CGO), import-only use
)

// ErrNotExist reports the cc-switch database file itself is missing (cc-switch
// never installed, or the db was moved). errors.Is-able so the settings window
// can show a friendly "nothing to import" instead of a scary failure.
var ErrNotExist = errors.New("cc-switch database does not exist")

// expandTilde resolves a leading ~ (~/rest, ~\rest or bare ~) to the user's
// home directory; anything else returns trimmed but otherwise untouched.
// os.UserHomeDir failing keeps the original path — the caller's error message
// then still shows what the user typed.
func expandTilde(p string) string {
	p = strings.TrimSpace(p)
	if p != "~" && !strings.HasPrefix(p, "~/") && !strings.HasPrefix(p, `~\`) {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return p
	}
	if p == "~" {
		return home
	}
	return filepath.Join(home, p[2:])
}

// env key boundaries inside settings_config.env: the provider identity fields
// are read out as Profile fields, ANTHROPIC_DEFAULT_* model mappings go into
// ModelEnv verbatim, everything else is ignored (design §5 field set).
const (
	apiKeyEnvKey       = "ANTHROPIC_API_KEY"
	baseURLEnvKey      = "ANTHROPIC_BASE_URL"
	defaultModelPrefix = "ANTHROPIC_DEFAULT_"
)

// claudeAppType is the app_type value marking a Claude Code provider row.
// Comparison is case-insensitive; a row with an EMPTY/absent app_type column
// (older cc-switch schema) is treated as Claude too — lenient beats exact
// here because manual entry is the fallback, not the importer.
const claudeAppType = "claude"

// ReadCcSwitch reads Claude Code provider profiles out of the cc-switch
// SQLite database at dbPath.
//
// Semantics:
//   - absent file → errors.Is(err, ErrNotExist) (caller shows "nothing to
//     import"; the database is never created implicitly);
//   - corrupt / non-SQLite file → definite error, never a panic;
//   - missing providers table (cc-switch schema drift) → definite error;
//   - rows whose app_type names another tool (e.g. codex) are skipped;
//   - a row without a usable ANTHROPIC_API_KEY is skipped — a keyless
//     profile could never be identified in settings.json, and importing it
//     would let a switch write an empty key;
//   - Profile.ID reuses the row's id column value when present, else falls
//     back to "ccswitch-<n>"; the settings window assigns final A/B ids.
func ReadCcSwitch(dbPath string) ([]domain.Profile, error) {
	// 設定頁的預設值是 "~/.cc-switch/cc-switch.db":Windows 原生不展開 ~,
	// 不先解析成家目錄,存在性檢查永遠當成相對路徑裡名叫 "~" 的資料夾 →
	// 明明有 db 卻報「不存在」。
	dbPath = expandTilde(dbPath)
	// 先查存在性:SQLite 驅動對不存在的路徑會直接建一個空檔,
	// 「缺檔」必須在開檔前攔下來,否則 ErrNotExist 語義塌掉。
	if _, err := os.Stat(dbPath); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("%w: %s", ErrNotExist, dbPath)
		}
		return nil, fmt.Errorf("stat cc-switch db %s: %w", dbPath, err)
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("open cc-switch db %s: %w", dbPath, err)
	}
	defer db.Close()

	// database/sql 惰性連線:不強制讀檔,壞檔要靠這個探測查詢逼出錯誤。
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM sqlite_master`).Scan(&n); err != nil {
		return nil, fmt.Errorf("cc-switch db %s is not a readable SQLite file: %w", dbPath, err)
	}

	rows, err := db.Query(`SELECT * FROM providers`)
	if err != nil {
		return nil, fmt.Errorf("cc-switch db %s has no readable providers table (schema changed?): %w", dbPath, err)
	}
	defer rows.Close()

	cols, err := rows.Columns()
	if err != nil {
		return nil, fmt.Errorf("read providers columns from %s: %w", dbPath, err)
	}

	// 逐列讀成「欄名 → 值」的 map:欄位順序與欄位增減都不影響映射(best-effort)。
	var out []domain.Profile
	for i := 1; rows.Next(); i++ {
		vals := make([]any, len(cols))
		scan := make([]any, len(cols))
		for j := range vals {
			scan[j] = &vals[j]
		}
		if err := rows.Scan(scan...); err != nil {
			return nil, fmt.Errorf("scan providers row from %s: %w", dbPath, err)
		}
		row := make(map[string]any, len(cols))
		for j, c := range cols {
			row[c] = vals[j]
		}

		if at := stringOf(row["app_type"]); at != "" && !strings.EqualFold(at, claudeAppType) {
			continue // 其他工具(codex 等)不是本工具的供應商,整列跳過
		}
		p := profileFromRow(row, i)
		if p.APIKey == "" {
			continue // 沒有 key 的列切換後無法被身分比對認得,跳過
		}
		out = append(out, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate providers from %s: %w", dbPath, err)
	}
	return out, nil
}

// profileFromRow maps one providers row onto a domain.Profile: name, API key
// and base URL come from the settings_config env blob, model mappings carry
// over as the ANTHROPIC_DEFAULT_* set. id falls back to the 1-based row
// number when the row id column is empty.
func profileFromRow(row map[string]any, n int) domain.Profile {
	p := domain.Profile{
		ID:       stringOf(row["id"]),
		Name:     stringOf(row["name"]),
		ModelEnv: map[string]string{},
	}
	if p.ID == "" {
		p.ID = fmt.Sprintf("ccswitch-%d", n)
	}

	var blob struct {
		Env map[string]json.RawMessage `json:"env"`
	}
	if err := json.Unmarshal([]byte(stringOf(row["settings_config"])), &blob); err != nil {
		return p // settings_config 不是 JSON:回傳僅存 id/name 的殘值(best-effort)
	}
	for k, raw := range blob.Env {
		var v string
		if json.Unmarshal(raw, &v) != nil {
			continue // 非字串 env 值不屬於供應商欄位,忽略
		}
		switch {
		case k == apiKeyEnvKey:
			p.APIKey = v
		case k == baseURLEnvKey:
			p.BaseURL = v
		case strings.HasPrefix(k, defaultModelPrefix):
			// 只收模型鍵:key/url 已另置,絕不混入 ModelEnv
			// (switcher 會把 ModelEnv 全數抄進 env,混入空值會覆掉真值)。
			p.ModelEnv[k] = v
		}
	}
	return p
}

// stringOf coerces a driver-returned cell (string / []byte / nil / other) to
// its string form; anything unreadable becomes the empty string.
func stringOf(v any) string {
	switch s := v.(type) {
	case nil:
		return ""
	case string:
		return s
	case []byte:
		return string(s)
	default:
		return fmt.Sprintf("%v", s)
	}
}
