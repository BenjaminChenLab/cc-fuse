package importer_test

// 單元測試(白箱):cc-switch 資料庫的 schema 變體映射。
// 驗收測試釘了語義;這裡釘「欄位順序/欄位增減/舊 schema 不影響讀取」的
// best-effort 契約(卡片設計注意 3:匯入為 best-effort,手動輸入是保底)。

import (
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"

	"cc-fuse/internal/importer"
)

// makeDB builds a cc-switch-shaped database from raw statements.
func makeDB(t *testing.T, ddl string, inserts ...string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "cc-switch.db")
	db := openSQLite(t, path)
	defer db.Close()
	if ddl != "" {
		if _, err := db.Exec(ddl); err != nil {
			t.Fatal(err)
		}
	}
	for _, q := range inserts {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	return path
}

// openSQLite opens a raw database/sql handle for fixture assembly.
func openSQLite(t *testing.T, path string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	return db
}

// 欄位順序不同 + 多餘欄位:按欄名映射,順序與額外欄位必須無影響。
func TestReadSchemaColumnOrderAndExtras(t *testing.T) {
	path := makeDB(t,
		`CREATE TABLE providers (settings_config TEXT, id TEXT, app_type TEXT, extra_col TEXT, name TEXT)`,
		`INSERT INTO providers VALUES
		 ('{"env":{"ANTHROPIC_API_KEY":"k1","ANTHROPIC_BASE_URL":"u1","ANTHROPIC_DEFAULT_SONNET_MODEL":"m1"}}','r1','claude','x','team')`)
	got, err := importer.ReadCcSwitch(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(got) != 1 || got[0].ID != "r1" || got[0].Name != "team" || got[0].APIKey != "k1" ||
		got[0].BaseURL != "u1" || got[0].ModelEnv["ANTHROPIC_DEFAULT_SONNET_MODEL"] != "m1" {
		t.Fatalf("mapping broken: %+v", got)
	}
}

// 舊 schema:沒有 app_type 欄 → 全列視為 claude 收進來。
func TestReadSchemaNoAppTypeColumn(t *testing.T) {
	path := makeDB(t,
		`CREATE TABLE providers (id TEXT PRIMARY KEY, name TEXT, settings_config TEXT)`,
		`INSERT INTO providers VALUES ('o1','old row','{"env":{"ANTHROPIC_API_KEY":"ko"}}')`)
	got, err := importer.ReadCcSwitch(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(got) != 1 || got[0].APIKey != "ko" {
		t.Fatalf("old schema rows must import: %+v", got)
	}
}

// app_type 大小寫不敏感:claude 家族才收,CODEX 等其他工具跳過。
func TestReadSchemaAppTypeCaseInsensitive(t *testing.T) {
	path := makeDB(t,
		`CREATE TABLE providers (id TEXT, app_type TEXT, settings_config TEXT)`,
		`INSERT INTO providers VALUES ('c1','Claude','{"env":{"ANTHROPIC_API_KEY":"kc"}}'),
		 ('x1','CODEX','{"env":{"ANTHROPIC_API_KEY":"kx"}}')`)
	got, err := importer.ReadCcSwitch(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(got) != 1 || got[0].APIKey != "kc" {
		t.Fatalf("case-insensitive claude filter broken: %+v", got)
	}
}

// 沒 key 的列必須跳過:切到無 key 的 profile 等於把 settings.json 寫壞。
func TestReadSchemaSkipsKeylessRows(t *testing.T) {
	path := makeDB(t,
		`CREATE TABLE providers (id TEXT, app_type TEXT, settings_config TEXT)`,
		`INSERT INTO providers VALUES ('n1','claude','{"env":{"ANTHROPIC_BASE_URL":"only-url"}}'),
		 ('n2','claude','not even json'),
		 ('n3','claude','{"env":{"ANTHROPIC_API_KEY":"real"}}')`)
	got, err := importer.ReadCcSwitch(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(got) != 1 || got[0].APIKey != "real" {
		t.Fatalf("keyless rows must be skipped: %+v", got)
	}
}

// 非字串的 env 值忽略,不炸、不混入 ModelEnv。
func TestReadSchemaIgnoresNonStringEnv(t *testing.T) {
	path := makeDB(t,
		`CREATE TABLE providers (id TEXT, app_type TEXT, settings_config TEXT)`,
		`INSERT INTO providers VALUES ('v1','claude',
		 '{"env":{"ANTHROPIC_API_KEY":"kv","ANTHROPIC_TIMEOUT":42,"ANTHROPIC_DEFAULT_OPUS_MODEL":"mm","ANTHROPIC_DEFAULT_BROKEN":[1,2]}}')`)
	got, err := importer.ReadCcSwitch(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("row must import: %+v", got)
	}
	p := got[0]
	if _, leaked := p.ModelEnv["ANTHROPIC_TIMEOUT"]; leaked {
		t.Fatalf("non-DEFAULT key must not enter ModelEnv: %+v", p)
	}
	if _, leaked := p.ModelEnv["ANTHROPIC_DEFAULT_BROKEN"]; leaked {
		t.Fatalf("non-string value must not enter ModelEnv: %+v", p)
	}
	if p.ModelEnv["ANTHROPIC_DEFAULT_OPUS_MODEL"] != "mm" {
		t.Fatalf("string model key must survive: %+v", p)
	}
}

// id 欄為 NULL → 後備 ID 形如 ccswitch-N,不空串。
func TestReadSchemaNullIDFallback(t *testing.T) {
	path := makeDB(t,
		`CREATE TABLE providers (id TEXT, name TEXT, app_type TEXT, settings_config TEXT)`,
		`INSERT INTO providers VALUES (NULL,'nn','claude','{"env":{"ANTHROPIC_API_KEY":"kn"}}')`)
	got, err := importer.ReadCcSwitch(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if len(got) != 1 || !strings.HasPrefix(got[0].ID, "ccswitch-") {
		t.Fatalf("null id must fall back to ccswitch-N: %+v", got)
	}
}

// 檔案存在但完全空的 SQLite(有 schema 無 providers 表)→ 明確錯誤;
// 缺檔 → ErrNotExist(與驗收測試同語義,這裡用 helpers 的另一條路徑驗一次)。
func TestReadSchemaEmptyFileAndAbsent(t *testing.T) {
	path := makeDB(t, `CREATE TABLE other (x TEXT)`)
	if _, err := importer.ReadCcSwitch(path); err == nil {
		t.Fatal("db without providers table must error")
	}
	_, err := importer.ReadCcSwitch(filepath.Join(t.TempDir(), "nope.db"))
	if !errors.Is(err, importer.ErrNotExist) {
		t.Fatalf("absent file must be ErrNotExist, got %v", err)
	}
}
