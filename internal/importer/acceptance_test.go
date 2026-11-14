package importer_test

// 驗收測試(黑箱)——cc-fuse T03:cc-switch 設定匯入器(best-effort)。
// 契約:ReadCcSwitch(dbPath) 讀 SQLite 的 providers 表,回傳 Claude Code 類供應商;
// 缺表/壞檔 → 明確錯誤不 panic。fixture 以最小 providers 表定義本契約的規範形;
// 實作負責把真實 cc-switch 資料庫的實際 schema 映射到這些語義(手動輸入永遠是保底)。

import (
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"

	"cc-fuse/internal/importer"
)

func buildFixture(t *testing.T, withTable bool) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "cc-switch.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if !withTable {
		// database/sql 惰性連線:不 Ping 就關閉,SQLite 檔根本不會被建出來,
		// 「缺表」會退化成「缺檔」,兩條錯誤路徑塌縮。Ping 一次強制建檔(無表)。
		if err := db.Ping(); err != nil {
			t.Fatal(err)
		}
	}
	if withTable {
		_, err = db.Exec(`CREATE TABLE providers (
			id TEXT PRIMARY KEY, name TEXT, app_type TEXT, settings_config TEXT)`)
		if err != nil {
			t.Fatal(err)
		}
		_, err = db.Exec(`INSERT INTO providers VALUES
			('p1','team zai','claude','{"env":{"ANTHROPIC_API_KEY":"key-a","ANTHROPIC_BASE_URL":"https://api.z.ai/api/anthropic","ANTHROPIC_DEFAULT_OPUS_MODEL":"glm-5.3"}}'),
			('p2','personal zai','claude','{"env":{"ANTHROPIC_API_KEY":"key-b","ANTHROPIC_BASE_URL":"https://api.z.ai/api/anthropic"}}'),
			('p3','codex thing','codex','{"model":"gpt-x"}')`)
		if err != nil {
			t.Fatal(err)
		}
	}
	return path
}

// 正路:讀出 claude 類供應商(跳過其他工具),欄位正確映射。
func TestReadCcSwitchProfiles(t *testing.T) {
	got, err := importer.ReadCcSwitch(buildFixture(t, true))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("want 2 claude profiles, got %d: %+v", len(got), got)
	}
	byKey := map[string]bool{}
	names := map[string]string{}
	for _, p := range got {
		byKey[p.APIKey] = true
		names[p.APIKey] = p.Name
		if p.BaseURL != "https://api.z.ai/api/anthropic" {
			t.Fatalf("base url mapping broken: %+v", p)
		}
		// 模型 env 允許含 DEFAULT_* 鍵;key/url 本身不得混入 ModelEnv
		// (空字串也算洩漏:switcher 之後會把 ModelEnv 抄進 env,空值會覆掉真 key)。
		if _, ok := p.ModelEnv["ANTHROPIC_API_KEY"]; ok {
			t.Fatalf("API key must not leak into ModelEnv: %+v", p)
		}
		if _, ok := p.ModelEnv["ANTHROPIC_BASE_URL"]; ok {
			t.Fatalf("base url must not leak into ModelEnv: %+v", p)
		}
	}
	if !byKey["key-a"] || !byKey["key-b"] {
		t.Fatalf("both api keys must be read: %v", byKey)
	}
	if names["key-a"] != "team zai" || names["key-b"] != "personal zai" {
		t.Fatalf("name mapping broken (settings window needs it): %v", names)
	}
	// 模型鍵映射:team profile 的 OPUS 模型鍵要在。
	for _, p := range got {
		if p.APIKey == "key-a" && p.ModelEnv["ANTHROPIC_DEFAULT_OPUS_MODEL"] != "glm-5.3" {
			t.Fatalf("model env mapping broken: %+v", p)
		}
	}
}

// 缺 providers 表 → 明確錯誤(cc-switch 改版情境;手動輸入是保底)。
func TestReadCcSwitchMissingTable(t *testing.T) {
	_, err := importer.ReadCcSwitch(buildFixture(t, false))
	if err == nil {
		t.Fatal("missing table must error")
	}
}

// 壞檔/非 SQLite → 明確錯誤不 panic。
func TestReadCcSwitchCorruptFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "junk.db")
	if err := os.WriteFile(path, []byte("this is not sqlite"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := importer.ReadCcSwitch(path); err == nil {
		t.Fatal("corrupt db must error")
	}
	if _, err := importer.ReadCcSwitch(filepath.Join(t.TempDir(), "absent.db")); !errors.Is(err, importer.ErrNotExist) {
		t.Fatalf("absent file must be ErrNotExist, got %v", err)
	}
}
