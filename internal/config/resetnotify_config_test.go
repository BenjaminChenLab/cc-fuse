package config_test

// Acceptance tests — 2026-11-21 RESET notify config fields (spec decision 9):
// ResetNotify (default on), ResetNotifyAfterPct (default 99, the wall line),
// ResetNotifyBufferMin (default 1 minute — the user-approved amendment; the
// skill text says 3) and ResetNotifyMinGapMin (default 30, the anti-spam gap
// between RESET notifies). Wire names follow the existing camelCase json tag
// convention; a partial config layers onto the defaults; Save/Load roundtrip
// is lossless (audit M10 idiom).

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"cc-fuse/internal/config"
)

// TestDefaultResetNotifyFields pins all four defaults: notify on, wall line
// 99, buffer 1 minute (Benjamin's approved value), gap 30 minutes.
func TestDefaultResetNotifyFields(t *testing.T) {
	c := config.Default()
	if !c.ResetNotify {
		t.Fatalf("default ResetNotify must be on (true)")
	}
	if c.ResetNotifyAfterPct != 99 {
		t.Fatalf("default ResetNotifyAfterPct = %v, want 99", c.ResetNotifyAfterPct)
	}
	if c.ResetNotifyBufferMin != 1 {
		t.Fatalf("default ResetNotifyBufferMin = %v, want 1 (the approved buffer amendment)", c.ResetNotifyBufferMin)
	}
	if c.ResetNotifyMinGapMin != 30 {
		t.Fatalf("default ResetNotifyMinGapMin = %v, want 30", c.ResetNotifyMinGapMin)
	}
}

// TestResetNotifyRoundTripAndWireNames: Save/Load is lossless for all four
// fields, and the JSON wire names are exactly the camelCase keys — the
// settings window posts these keys into a zero-value Config, so a tag typo
// would silently drop the field on every save.
func TestResetNotifyRoundTripAndWireNames(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	c := config.Default()
	c.ResetNotify = false
	c.ResetNotifyAfterPct = 97.5
	c.ResetNotifyBufferMin = 2
	c.ResetNotifyMinGapMin = 45
	if err := config.Save(path, c); err != nil {
		t.Fatalf("save: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read saved config: %v", err)
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("saved config must be JSON: %v", err)
	}
	if v, ok := raw["resetNotify"].(bool); !ok || v {
		t.Fatalf(`JSON key must be "resetNotify" = false, got %v`, raw["resetNotify"])
	}
	if v, ok := raw["resetNotifyAfterPct"].(float64); !ok || v != 97.5 {
		t.Fatalf(`JSON key must be "resetNotifyAfterPct" = 97.5, got %v`, raw["resetNotifyAfterPct"])
	}
	if v, ok := raw["resetNotifyBufferMin"].(float64); !ok || v != 2 {
		t.Fatalf(`JSON key must be "resetNotifyBufferMin" = 2, got %v`, raw["resetNotifyBufferMin"])
	}
	if v, ok := raw["resetNotifyMinGapMin"].(float64); !ok || v != 45 {
		t.Fatalf(`JSON key must be "resetNotifyMinGapMin" = 45, got %v`, raw["resetNotifyMinGapMin"])
	}
	got, err := config.Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got.ResetNotify || got.ResetNotifyAfterPct != 97.5 || got.ResetNotifyBufferMin != 2 || got.ResetNotifyMinGapMin != 45 {
		t.Fatalf("roundtrip lost the fields: %+v", got)
	}
}

// TestPartialConfigKeepsResetNotifyDefaults: a hand-written config without
// the new keys layers onto the defaults — all four keep their default values
// while the file's own values still take effect.
func TestPartialConfigKeepsResetNotifyDefaults(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"pollIntervalSec":30}`), 0o644); err != nil {
		t.Fatalf("write partial config: %v", err)
	}
	got, err := config.Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if !got.ResetNotify || got.ResetNotifyAfterPct != 99 || got.ResetNotifyBufferMin != 1 || got.ResetNotifyMinGapMin != 30 {
		t.Fatalf("partial config must keep the RESET notify defaults: %+v", got)
	}
	if got.PollIntervalSec != 30 {
		t.Fatalf("layering must still take the file's own values: %+v", got)
	}
}
