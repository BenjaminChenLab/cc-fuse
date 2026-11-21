// Package watchers spawns cc-fuse's wake-notify headless session (2026-11-20
// racing-wake-notify): after a successful quota-mode flip, ONE fire-and-forget
// `claude -p "<notify prompt>"` session distributes the flip order to every
// chain registered in the go-when-racing watcher registry — RACING flips carry
// the go prompt, SAVING flips the stop prompt (contract v2: both directions
// notify). The 2026-11-21 reset-notify reuses the same spawn machinery for
// the third mode RESET (the A-account 429-wall retry order, fired by the app
// loop with NO mode write involved). The registry file is the cost gate: no
// content lines → no spawn.
// Specs: docs/blueprints/2026-11-20-racing-wake-notify.md (decision 3),
// docs/blueprints/2026-11-21-reset-notify.md (decisions 5 and 7).
package watchers

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// startProcess is the single white-box seam: production wraps cmd.Start so
// tests can capture the constructed command (or simulate a slow start)
// without launching a real claude session.
var startProcess = func(cmd *exec.Cmd) error { return cmd.Start() }

// startGraceWindow bounds how long Spawn waits for the process start to
// report its result (design: never let a wedged launch freeze an account
// switch). Local CreateProcess calls return in single-digit milliseconds, so
// the window is generous headroom; a start still running past it is treated
// as launched-and-forgotten (fire-and-forget contract).
const startGraceWindow = 250 * time.Millisecond

// Spawn launches exactly ONE fire-and-forget headless claude session that
// notifies every chain registered in the watchers registry file, carrying the
// direction embedded in the prompt (RACING go order / SAVING stop order /
// RESET retry order — the 2026-11-21 429-wall reset notify reuses the same
// machinery with no mode write involved).
//
// Cost gates, in order:
//  1. the mode must be exactly "RACING", "SAVING" or "RESET" — anything else
//     is an error with zero spawns (defensive; only the exact-case values are
//     ever emitted by the app loop);
//  2. a missing registry means nothing is parked: silent no-op, nil;
//  3. an empty or whitespace-only registry (blank lines included) also means
//     nothing to notify: nil with zero spawns — a blank-registry notify would
//     burn a headless session turn for nothing;
//  4. any OTHER registry read error is returned (zero spawn): an unreadable
//     registry cannot prove there is anything to notify, so it must not
//     notify blindly.
//
// Spawn never waits for the child session itself: the process start reports
// through a short grace window (a fast start failure propagates as the
// returned error; a start still running past the window is treated as
// launched-and-forgotten), and the reaping Wait() runs on the start
// goroutine — nothing ever blocks the switch on the headless session's
// lifetime. The three pre-spawn gates surface their failures synchronously;
// the app loop logs every failure without touching the already-completed
// switch.
func Spawn(watchersFile, mode string) error {
	// Gate 1 — mode: exact-case match only, rejected before anything is read.
	if mode != "RACING" && mode != "SAVING" && mode != "RESET" {
		return fmt.Errorf("watchers: invalid mode %q (want RACING, SAVING or RESET)", mode)
	}

	// Gate 2 — registry read: missing file = nothing parked = silent no-op.
	data, err := os.ReadFile(watchersFile)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("watchers: read registry %s: %w", watchersFile, err)
	}

	// Gate 3 — content: blank (all-whitespace) registry → zero spawns.
	if !hasContent(data) {
		return nil
	}

	// Child working dir: the user home, so the headless transcript never
	// lands inside the cc-fuse project directory.
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("watchers: resolve user home: %w", err)
	}

	// Mode → prompt, byte-verbatim from the go-when-racing skill file (RESET
	// = the 2026-11-21 429-wall retry order, same authority block).
	prompt := savingNotifyPrompt
	switch mode {
	case "RACING":
		prompt = racingNotifyPrompt
	case "RESET":
		prompt = resetNotifyPrompt
	}

	// Exe resolution: PATH first, then the documented native-install
	// fallback %USERPROFILE%\.local\bin\claude.exe.
	exe, err := exec.LookPath("claude")
	if err != nil {
		exe = filepath.Join(home, ".local", "bin", "claude.exe")
	}

	// Build the command: args passed as an array (no cmd.exe in between, so
	// Chinese text, newlines and 「」 need no escaping); child env stripped of
	// every ANTHROPIC_* so the child reads the freshly switched settings.json
	// (= the account just switched to — the skill's explicit design);
	// HideWindow + CREATE_NO_WINDOW (0x08000000) so the windowsgui tray never
	// flashes a console window while the child runs.
	cmd := exec.Command(exe, "-p", prompt)
	cmd.Env = stripAnthropic(os.Environ())
	cmd.Dir = home
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}

	// Fire and forget with a start-report grace window: the start runs on its
	// own goroutine (a synchronous start would let a wedged launch freeze
	// every account switch), and Spawn gives it a short window to report its
	// result — a fast failure propagates so the caller can log it, a start
	// still running past the window is treated as launched and forgotten
	// (the notify is auxiliary; the go-when-racing contract's hourly cron
	// backup bounds that hole). Either way the child is reaped by the same
	// goroutine once the start returns, so no process handle leaks.
	started := make(chan error, 1)
	go func() {
		err := startProcess(cmd)
		started <- err
		if err == nil {
			_ = cmd.Wait() // reap the headless session; nobody waits for it
		}
	}()
	timer := time.NewTimer(startGraceWindow)
	defer timer.Stop()
	select {
	case err := <-started:
		if err != nil {
			return fmt.Errorf("watchers: start headless notify: %w", err)
		}
	case <-timer.C:
		// Still starting past the grace window: fire-and-forget from here.
	}
	return nil
}

// hasContent reports whether the registry carries at least one line with
// non-whitespace content (LF and CRLF endings both count).
func hasContent(data []byte) bool {
	for _, ln := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(ln) != "" {
			return true
		}
	}
	return false
}

// stripAnthropic builds the child env: os.Environ() minus every ANTHROPIC_*
// entry, explicitly non-nil — a nil Env would mean "inherit everything" and
// silently keep the previous account's credentials.
func stripAnthropic(environ []string) []string {
	out := make([]string, 0, len(environ))
	for _, kv := range environ {
		if strings.HasPrefix(kv, "ANTHROPIC_") {
			continue
		}
		out = append(out, kv)
	}
	return out
}
