package config_test

// Acceptance tests — 2026-11-20 wake-notify config fields: the watchers
// registry path (JSON-only, deliberately no UI row) and the RacingNotify
// master switch defaulting to on. Spec:
// docs/blueprints/2026-11-20-racing-wake-notify.md decision 4. Roundtrip,
// wire-name and layering checks follow the package contract (missing file →
// defaults; Save/Load lossless — audit M10 idiom).

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"cc-fuse/internal/config"
)

// Default pins both new fields: the standard registry path and the switch on.
func TestDefaultWatchersNotifyFields(t *testing.T) {
	c := config.Default()
	if c.WatchersFile != `S:/claude-cache/go-when-racing/watchers.md` {
		t.Fatalf("default WatchersFile = %q", c.WatchersFile)
	}
	if !c.RacingNotify {
		t.Fatalf("default RacingNotify must be on (true)")
	}
}

// Save/Load roundtrip is lossless for both fields, and the JSON wire names
// are exactly "racingNotify" / "watchersFile" — the settings window posts
// these keys into a zero-value Config, so a tag typo would silently drop the
// field on every save.
func TestWatchersNotifyRoundTripAndWireNames(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	c := config.Default()
	c.WatchersFile = `D:/custom/watchers.md`
	c.RacingNotify = false
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
	if v, ok := raw["racingNotify"].(bool); !ok || v {
		t.Fatalf(`JSON key must be "racingNotify" = false, got %v`, raw["racingNotify"])
	}
	if v, _ := raw["watchersFile"].(string); v != `D:/custom/watchers.md` {
		t.Fatalf(`JSON key must be "watchersFile" with the saved value, got %v`, raw["watchersFile"])
	}
	got, err := config.Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got.WatchersFile != c.WatchersFile || got.RacingNotify {
		t.Fatalf("roundtrip lost the fields: %+v", got)
	}
}

// A hand-written config without the new keys layers onto the defaults: the
// switch stays on and the registry path keeps the standard value, while the
// file's own values still take effect.
func TestPartialConfigKeepsNotifyDefaults(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"pollIntervalSec":30}`), 0o644); err != nil {
		t.Fatalf("write partial config: %v", err)
	}
	got, err := config.Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if !got.RacingNotify || got.WatchersFile != `S:/claude-cache/go-when-racing/watchers.md` {
		t.Fatalf("partial config must keep the notify defaults: %+v", got)
	}
	if got.PollIntervalSec != 30 {
		t.Fatalf("layering must still take the file's own values: %+v", got)
	}
}

// An explicit empty watchersFile in a config file (what a save from a client
// that dropped the field produces — the enforceDailyCeiling zero-value
// lesson) must heal back to the standard registry path: an explicit ""
// overrides the default during layering, and Spawn("") would then silently
// no-op through the missing-file gate on every later switch (TOP audit
// finding F1, defense in depth behind the UI's collect() passthrough).
func TestEmptyWatchersFileHealsToDefault(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"watchersFile":""}`), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	got, err := config.Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got.WatchersFile != `S:/claude-cache/go-when-racing/watchers.md` {
		t.Fatalf("an explicit empty watchersFile must heal to the default, got %q", got.WatchersFile)
	}
}
