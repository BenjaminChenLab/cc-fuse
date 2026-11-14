package config_test

// 驗收測試(黑箱)——cc-fuse T01:設定檔。
// 契約:無檔案 → 全預設(設計 §9 全表);Save/Load 往返不失真。

import (
	"path/filepath"
	"reflect"
	"testing"

	"cc-fuse/internal/config"
	"cc-fuse/internal/domain"
)

// 預設值=設計 §9 每欄(含 PD1/PD2 新增欄)。
func TestDefaults(t *testing.T) {
	c := config.Default()

	if c.PollIntervalSec != 60 || c.FastPollSec != 20 || c.FastPollAbovePct != 80 {
		t.Fatalf("poll defaults wrong: %+v", c)
	}
	if c.SwitchThreshold5h != 90 || c.SwitchThresholdWeekly != 90 {
		t.Fatalf("switch thresholds wrong: %+v", c)
	}
	if c.RecoveryThreshold5h != 30 || c.RecoveryThresholdWeekly != 60 {
		t.Fatalf("recovery thresholds wrong: %+v", c)
	}
	if c.WarnThreshold != 80 {
		t.Fatalf("warn threshold wrong: %+v", c)
	}
	if c.StaleAfterMin != 10 {
		t.Fatalf("stale window wrong: %+v", c)
	}
	if c.ParseFailAlertLimit != 5 {
		t.Fatalf("parse-fail alert limit wrong (PD2): %+v", c)
	}
	if c.ProxyURL != "" {
		t.Fatalf("proxy must default to direct (PD1): %+v", c)
	}
	if c.Budget.WeeklyTotalPct != 90 || c.Budget.MinDailyAllowancePct != 3 || !c.Budget.WorkdaysOnly {
		t.Fatalf("budget defaults wrong (D5): %+v", c.Budget)
	}
	if c.QuotaModeFile != `S:/claude-cache/quota-mode.md` {
		t.Fatalf("quota mode file default wrong: %q", c.QuotaModeFile)
	}
	if !c.AutoStart {
		t.Fatal("autostart must default on")
	}
	if len(c.Profiles) != 0 {
		t.Fatalf("default must ship zero profiles (user fills A/B): %+v", c.Profiles)
	}
}

// Save → Load 往返:每個欄位含 profiles/modelEnv 全等——所有純量都改離預設值再存
// (審計 M10:留在預設值的欄位無法證明有被持久化)。
func TestSaveLoadRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	c := config.Default()
	c.PollIntervalSec = 45
	c.FastPollSec = 15
	c.FastPollAbovePct = 70
	c.SwitchThreshold5h = 85
	c.SwitchThresholdWeekly = 88
	c.RecoveryThreshold5h = 25
	c.RecoveryThresholdWeekly = 55
	c.WarnThreshold = 75
	c.StaleAfterMin = 8
	c.Budget.WeeklyTotalPct = 80
	c.Budget.MinDailyAllowancePct = 5
	c.Budget.WorkdaysOnly = false
	c.QuotaModeFile = "X:/somewhere/quota-mode.md"
	c.AutoStart = false
	c.ProxyURL = "http://127.0.0.1:7890"
	c.ParseFailAlertLimit = 7
	c.Profiles = []domain.Profile{
		{ID: "A", Name: "team shared", APIKey: "key-a", BaseURL: "https://api.z.ai/api/anthropic",
			ModelEnv: map[string]string{
				"ANTHROPIC_DEFAULT_HAIKU_MODEL": "glm-5.3-flash",
				"ANTHROPIC_DEFAULT_OPUS_MODEL":  "glm-5.3",
			}},
		{ID: "B", Name: "personal", APIKey: "key-b", BaseURL: "https://api.z.ai/api/anthropic",
			ModelEnv: map[string]string{}},
	}

	if err := config.Save(path, c); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err := config.Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if !reflect.DeepEqual(c, got) {
		t.Fatalf("round-trip mismatch:\nwant %+v\ngot  %+v", c, got)
	}
}

// 無檔案 → 預設值、無錯誤(冷啟動路徑)。
func TestLoadMissingFileGivesDefaults(t *testing.T) {
	got, err := config.Load(filepath.Join(t.TempDir(), "absent.json"))
	if err != nil {
		t.Fatalf("missing file must not error: %v", err)
	}
	if !reflect.DeepEqual(got, config.Default()) {
		t.Fatalf("missing file must yield defaults: %+v", got)
	}
}

// 壞 JSON → 明確錯誤(呼叫端自行決定走預設),不 panic。
func TestLoadCorruptFileErrors(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := write(path, "{not json"); err != nil {
		t.Fatal(err)
	}
	if _, err := config.Load(path); err == nil {
		t.Fatal("corrupt config must error")
	}
}

func write(path, s string) error {
	// 小工具:測試內寫壞檔
	return osWriteFile(path, s)
}
