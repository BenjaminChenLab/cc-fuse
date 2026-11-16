package config_test

// 驗收測試(黑箱)——2026-11-16 計畫 R2:budget.enforceDailyCeiling 開關。
// 規格:docs/blueprints/2026-11-16-cc-fuse-budget-ui.md §2.1。預設 true
// (既有使用者行為不變);檔案缺欄位 → 疊在預設上仍 true;往返保留 false。

import (
	"path/filepath"
	"testing"

	"cc-fuse/internal/config"
)

func TestEnforceDailyCeilingDefaultTrue(t *testing.T) {
	if !config.Default().Budget.EnforceDailyCeiling {
		t.Fatalf("預設必須為 true(現行行為不變)")
	}
}

func TestEnforceDailyCeilingRoundtripFalse(t *testing.T) {
	c := config.Default()
	c.Budget.EnforceDailyCeiling = false
	path := filepath.Join(t.TempDir(), "config.json")
	if err := config.Save(path, c); err != nil {
		t.Fatal(err)
	}
	got, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.Budget.EnforceDailyCeiling {
		t.Fatalf("false 必須往返保留")
	}
}

func TestEnforceDailyCeilingPartialFileKeepsTrue(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := osWriteFile(path, `{"budget":{"weeklyTotalPct":80}}`); err != nil {
		t.Fatal(err)
	}
	got, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Budget.EnforceDailyCeiling {
		t.Fatalf("部分設定檔缺欄位 → 疊預設仍 true")
	}
	if got.Budget.WeeklyTotalPct != 80 {
		t.Fatalf("同檔其他預算欄位照常生效:weeklyTotalPct=%v", got.Budget.WeeklyTotalPct)
	}
}
