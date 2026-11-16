// Package config loads and saves %APPDATA%\cc-fuse\config.json (design §9).
//
// Contract: a missing file yields the defaults with no error (cold start);
// a corrupt file is a definite error — the caller decides whether to fall
// back to defaults. Save writes atomically (temp file + rename in the same
// directory) so a crash never leaves a half-written config.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"cc-fuse/internal/domain"
)

// Budget is the §7 pacing-budget block for account B: how much of the
// weekly pool each budget day may burn, with a daily floor so the pace is
// never zero. All values are user-settable; defaults live in Default.
type Budget struct {
	WeeklyTotalPct       float64 `json:"weeklyTotalPct"`       // weekly wall the budget protects (default 90, leaves headroom before 100)
	MinDailyAllowancePct float64 `json:"minDailyAllowancePct"` // daily floor in pct points; 0 = strict to the wall
	WorkdaysOnly         bool    `json:"workdaysOnly"`         // count only workdays as remaining budget days
	// EnforceDailyCeiling gates whether the daily ceiling affects switching at
	// all (2026-11-16 plan R2, user decision: 開關關=全面不影響切換). true
	// (default — missing key in a config file layers onto the default, so
	// existing users keep today's behavior): P5 forced return, P6 refuge-
	// candidacy loss and the ceiling term of B's availability all apply.
	// false: the ceiling is display+accounting only, never a switching input.
	EnforceDailyCeiling bool `json:"enforceDailyCeiling"`
}

// Config mirrors every key of config.json: profiles, polling rhythm,
// thresholds, budget pacing, staleness, quota-mode file location, autostart
// and the optional proxy (PD1). Every scalar is persisted by Save, so a
// Save/Load round trip is lossless (audit M10).
type Config struct {
	Profiles                []domain.Profile `json:"profiles"`
	PollIntervalSec         int              `json:"pollIntervalSec"`         // normal poll period
	FastPollSec             int              `json:"fastPollSec"`             // period while the active account runs hot
	FastPollAbovePct        float64          `json:"fastPollAbovePct"`        // active-window pct that triggers fast polling
	SwitchThreshold5h       float64          `json:"switchThreshold5h"`       // P3: A 5h% at/above this → switch to B
	SwitchThresholdWeekly   float64          `json:"switchThresholdWeekly"`   // P3: A weekly% at/above this → switch to B
	RecoveryThreshold5h     float64          `json:"recoveryThreshold5h"`     // P4: A 5h% at/below this → switch back
	RecoveryThresholdWeekly float64          `json:"recoveryThresholdWeekly"` // P4: A weekly% at/below this → switch back
	WarnThreshold           float64          `json:"warnThreshold"`           // P7: one-shot warning when the active window crosses this
	Budget                  Budget           `json:"budget"`
	StaleAfterMin           int              `json:"staleAfterMin"`       // data older than this pauses all decisions (§4)
	ParseFailAlertLimit     int              `json:"parseFailAlertLimit"` // consecutive parse failures before the red-alert toast (§11.8, PD2)
	QuotaModeFile           string           `json:"quotaModeFile"`       // quota-mode.md the writer flips the mode: line in
	AutoStart               bool             `json:"autoStart"`           // launch at Windows logon (Run registry key)
	ProxyURL                string           `json:"proxyUrl"`            // empty = direct connection (PD1)
}

// Default returns the §9 default table: polling 60/20/80, switch lines 90/90,
// recovery lines 30/60, warn 80, budget 90/3/workdays-only, staleness 10 min,
// parse-fail alert after 5 misses, the standard quota-mode.md path, autostart
// on, direct connection, and zero profiles (the user fills A/B in the UI).
func Default() Config {
	return Config{
		PollIntervalSec:         60,
		FastPollSec:             20,
		FastPollAbovePct:        80,
		SwitchThreshold5h:       90,
		SwitchThresholdWeekly:   90,
		RecoveryThreshold5h:     30,
		RecoveryThresholdWeekly: 60,
		WarnThreshold:           80,
		Budget: Budget{
			WeeklyTotalPct:       90,
			MinDailyAllowancePct: 3,
			WorkdaysOnly:         true,
			EnforceDailyCeiling:  true,
		},
		StaleAfterMin:       10,
		ParseFailAlertLimit: 5,
		QuotaModeFile:       `S:/claude-cache/quota-mode.md`,
		AutoStart:           true,
	}
}

// Load reads config.json from path. Missing file → defaults + nil error
// (cold start). Corrupt JSON → a definite error with a zero Config, the
// caller decides whether to fall back to defaults. A valid file that omits
// fields is layered over the defaults, so hand-written partial configs keep
// sane values for everything the user did not spell out.
func Load(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Default(), nil
		}
		return Config{}, fmt.Errorf("read config %s: %w", path, err)
	}
	c := Default()
	if err := json.Unmarshal(data, &c); err != nil {
		return Config{}, fmt.Errorf("config %s is not valid JSON: %w", path, err)
	}
	return c, nil
}

// Save writes c to path atomically (temp file + rename in the same
// directory), creating the parent directory when needed.
func Save(path string, c Config) error {
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return fmt.Errorf("encode config: %w", err)
	}
	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("create config dir %s: %w", dir, err)
		}
	}
	return atomicWrite(path, data)
}

// atomicWrite replaces path with data via a temp file in the same directory
// plus rename — readers of path never see a partial file.
func atomicWrite(path string, data []byte) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".cc-fuse-*.tmp")
	if err != nil {
		return fmt.Errorf("create temp in %s: %w", dir, err)
	}
	tmpName := tmp.Name()
	// Rename 之後這個 Remove 只是無害的 not-exist;失敗路徑上它清掉暫存殘物。
	defer func() {
		if tmpName != "" {
			_ = os.Remove(tmpName)
		}
	}()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write temp %s: %w", tmpName, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp %s: %w", tmpName, err)
	}
	// 沿用目標檔既有權限;新檔給一般讀寫權限。
	mode := os.FileMode(0o644)
	if st, err := os.Stat(path); err == nil {
		mode = st.Mode().Perm()
	}
	if err := os.Chmod(tmpName, mode); err != nil {
		return fmt.Errorf("chmod temp %s: %w", tmpName, err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("rename %s -> %s: %w", tmpName, path, err)
	}
	tmpName = "" // 已改名,deferred cleanup 不用再清
	return nil
}
