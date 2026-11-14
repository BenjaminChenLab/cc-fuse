package main

// 開機輔助:路徑、log、profile 表正規化、初始身分判定、無匣時的錯誤框。

import (
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"unsafe"

	"golang.org/x/sys/windows"

	"cc-fuse/internal/config"
	"cc-fuse/internal/domain"
	"cc-fuse/internal/switcher"
)

// appDataDir is %APPDATA%\cc-fuse — config, state, backups and logs all
// live under it (design §9/§10).
func appDataDir() string {
	if base, err := os.UserConfigDir(); err == nil && base != "" {
		return filepath.Join(base, "cc-fuse")
	}
	return filepath.Join(os.Getenv("APPDATA"), "cc-fuse")
}

// defaultConfigPath is %APPDATA%\cc-fuse\config.json.
func defaultConfigPath() string {
	return filepath.Join(appDataDir(), "config.json")
}

// defaultStatePath is %APPDATA%\cc-fuse\state.json.
func defaultStatePath() string {
	return filepath.Join(appDataDir(), "state.json")
}

// settingsPath is Claude Code's settings.json (design §5). --settings flag
// overrides it so testing can point at a scratch copy without touching the
// real file.
var settingsFlag = flag.String("settings", "", "override path to Claude Code settings.json (testing)")

// settingsPath resolves the live settings.json path.
func settingsPath() string {
	if *settingsFlag != "" {
		return *settingsFlag
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(os.Getenv("USERPROFILE"), ".claude", "settings.json")
	}
	return filepath.Join(home, ".claude", "settings.json")
}

// bootLogger wires the rotating log writer into a standard logger.
func bootLogger() *log.Logger {
	w, err := newRotatingWriter(filepath.Join(appDataDir(), "logs"), "cc-fuse.log")
	if err != nil {
		// log 立不起來也要活:退回 stderr。
		return log.New(os.Stderr, "cc-fuse ", log.LstdFlags)
	}
	return log.New(w, "", log.LstdFlags)
}

// normalizeProfiles ensures the config's profile table is exactly the two
// stable slots the whole app keys on: ID "A" (first) and "B" (second).
// Hand-edited configs missing a slot get an empty placeholder so the Loop
// can gate on "account not configured" instead of crashing on lookups.
func normalizeProfiles(c config.Config) []domain.Profile {
	out := make([]domain.Profile, 2)
	for _, p := range c.Profiles {
		switch p.ID {
		case "A":
			out[0] = p
		case "B":
			out[1] = p
		}
	}
	if out[0].ID == "" {
		out[0] = domain.Profile{ID: "A"}
	}
	if out[1].ID == "" {
		out[1] = domain.Profile{ID: "B"}
	}
	return out
}

// currentFromSettings decides who settings.json points at right now (boot
// reality, design §5: 以 ANTHROPIC_API_KEY 比對). Unknown/unreadable key
// stays on A (the always-preferred account) and the first watch event or
// policy round sorts it out.
func currentFromSettings(profiles []domain.Profile, logger *log.Logger) string {
	data, err := os.ReadFile(settingsPath())
	if err != nil {
		logger.Printf("初始身分:讀 settings.json 失敗(%v),預設 A", err)
		return "A"
	}
	if id, ok := switcher.Identify(data, profiles); ok {
		return id
	}
	logger.Printf("初始身分:settings.json 的 key 不屬於任何 profile,預設 A")
	return "A"
}

// profileSummary renders one profile for the boot log line.
func profileSummary(profiles []domain.Profile, id string) string {
	for _, p := range profiles {
		if p.ID == id {
			if p.APIKey == "" {
				return id + "(未設定)"
			}
			return fmt.Sprintf("%s(%s …%s)", id, p.Name, last4(p.APIKey))
		}
	}
	return id + "(未設定)"
}

// last4 keeps only a key's tail for logs (never log the whole key).
func last4(s string) string {
	if len(s) <= 4 {
		return "****"
	}
	return s[len(s)-4:]
}

// failBox reports a fatal boot problem when the tray cannot be started at
// all. The exe is a GUI-subsystem binary (no console), so stderr vanishes —
// show a real message box instead (user32 MessageBoxW via x/sys/windows,
// already a dependency for the registry writer).
func failBox(msg string) {
	const mbIconError = 0x00000010
	title, _ := windows.UTF16PtrFromString("cc-fuse")
	text, _ := windows.UTF16PtrFromString(msg)
	mb := windows.NewLazySystemDLL("user32.dll").NewProc("MessageBoxW")
	_, _, _ = mb.Call(0, uintptr(unsafe.Pointer(text)), uintptr(unsafe.Pointer(title)), mbIconError)
	fmt.Fprintln(os.Stderr, msg) // console attached (dev runs): keep a copy too
}
