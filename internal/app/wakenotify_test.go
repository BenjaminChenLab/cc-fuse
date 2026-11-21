package app_test

// Acceptance tests (black-box) — 2026-11-20 RACING/SAVING wake notify, both
// directions. Spec: docs/blueprints/2026-11-20-racing-wake-notify.md
// (decisions 1, 2, 7; acceptance criteria). After a SUCCESSFUL quota-mode
// flip inside doSwitch, the Loop must call the Wake seam exactly once per
// switched round, carrying the config's watchers file and the flip
// direction; a failed flip, the master switch off, or an absent seam must
// skip it. Reuses the shared fakes from acceptance_test.go (same package)
// plus a recording waker and an always-failing flipper.

import (
	"context"
	"errors"
	"testing"

	"cc-fuse/internal/app"
	"cc-fuse/internal/config"
	"cc-fuse/internal/policy"
)

// fakeWaker records Wake calls as (watchersFile, mode) pairs and joins the
// shared call-sequence log so the flip→wake ordering is assertable; failErr
// turns it into a failing waker (the notify failure must be log-only).
type fakeWaker struct {
	calls   [][2]string
	log     *[]string
	failErr error
}

// Wake implements app.WakeNotifier (recording fake).
func (w *fakeWaker) Wake(watchersFile, mode string) error {
	w.calls = append(w.calls, [2]string{watchersFile, mode})
	mark(w.log, "wake")
	return w.failErr
}

// failFlipper always fails the mode flip: the already-completed settings
// switch must survive it (§11.5) and the wake must be skipped — an early
// notify would act on the old account's state.
type failFlipper struct {
	log *[]string
}

// Flip implements app.QuotaFlipper (always failing).
func (fl *failFlipper) Flip(path, mode string) error {
	mark(fl.log, "flip")
	return errors.New("mode file unwritable")
}

// firstIndex returns the first position of e in seq, or -1 when absent.
func firstIndex(seq []string, e string) int {
	for i, v := range seq {
		if v == e {
			return i
		}
	}
	return -1
}

// TestTickWakeSavingOnFlee: the flee-to-B round flips SAVING and then wakes
// exactly once, carrying the config's watchers file and the SAVING
// direction, with the flip strictly before the wake (notify after the mode
// write).
func TestTickWakeSavingOnFlee(t *testing.T) {
	var seq []string
	mon := &fakeMonitor{a: fresh("A", f(95), f(10), base), b: fresh("B", f(5), f(12), base), log: &seq}
	sw, fl, nt := &fakeSwitcher{log: &seq}, &fakeFlipper{log: &seq}, &fakeNotifier{log: &seq}
	wk := &fakeWaker{log: &seq}
	l := newLoop(mon, sw, fl, nt)
	l.Wake = app.WakeNotifierFunc(wk.Wake)
	cfg := config.Default()
	cfg.WatchersFile = `D:/custom/watchers.md`
	l.UpdateConfig(cfg)

	r := l.Tick(context.Background(), base)
	if !r.Switched || r.Target != "B" {
		t.Fatalf("flee round must switch to B: %+v", r)
	}
	if len(wk.calls) != 1 {
		t.Fatalf("exactly one wake per switched round, got %v", wk.calls)
	}
	if wk.calls[0][0] != cfg.WatchersFile || wk.calls[0][1] != "SAVING" {
		t.Fatalf("wake must carry (cfg.WatchersFile, SAVING), got %v", wk.calls[0])
	}
	fi, wi := firstIndex(seq, "flip"), firstIndex(seq, "wake")
	if fi < 0 || wi < 0 || fi > wi {
		t.Fatalf("flip must precede wake (notify after the mode write), seq=%v", seq)
	}
}

// TestTickWakeRacingOnManualReturn: a manual back-to-A round flips RACING
// and wakes with the RACING direction — contract v2: BOTH directions notify.
func TestTickWakeRacingOnManualReturn(t *testing.T) {
	mon := &fakeMonitor{a: fresh("A", f(10), f(10), base), b: fresh("B", f(5), f(12), base)}
	sw, fl, nt := &fakeSwitcher{}, &fakeFlipper{}, &fakeNotifier{}
	wk := &fakeWaker{}
	l := newLoop(mon, sw, fl, nt)
	l.Wake = app.WakeNotifierFunc(wk.Wake)
	l.Current = "B"
	l.Pending = policy.ManualCmd{Target: "A"}

	if r := l.Tick(context.Background(), base); !r.Switched || r.Target != "A" {
		t.Fatalf("manual round must switch to A: %+v", r)
	}
	if len(wk.calls) != 1 || wk.calls[0][1] != "RACING" {
		t.Fatalf("RACING direction must wake exactly once with RACING, got %v", wk.calls)
	}
}

// TestTickFlipFailureSkipsWake: a FAILED mode write must not notify, even
// though the switch itself completes and the flip was attempted.
func TestTickFlipFailureSkipsWake(t *testing.T) {
	var seq []string
	mon := &fakeMonitor{a: fresh("A", f(95), f(10), base), b: fresh("B", f(5), f(12), base), log: &seq}
	sw, nt := &fakeSwitcher{log: &seq}, &fakeNotifier{log: &seq}
	wk := &fakeWaker{log: &seq}
	// Built inline (not via newLoop) because the quota flipper must fail.
	l := &app.Loop{
		Mon: mon, Sw: sw, Qm: &failFlipper{log: &seq}, Not: nt,
		Cfg:          config.Default(),
		Profiles:     profiles(),
		StatePath:    "",
		SettingsPath: "C:\\nonexistent\\settings.json",
		Autopilot:    true,
		Current:      "A",
	}
	l.Wake = app.WakeNotifierFunc(wk.Wake)

	if r := l.Tick(context.Background(), base); !r.Switched {
		t.Fatalf("the completed settings switch must survive a flip failure: %+v", r)
	}
	if firstIndex(seq, "flip") < 0 {
		t.Fatalf("the flip must have been attempted: %v", seq)
	}
	if len(wk.calls) != 0 {
		t.Fatalf("a failed flip must skip the wake, got %v", wk.calls)
	}
}

// TestTickRacingNotifyOffSkipsWake: master switch off = no wake in either
// direction; the switch itself still happens.
func TestTickRacingNotifyOffSkipsWake(t *testing.T) {
	mon := &fakeMonitor{a: fresh("A", f(95), f(10), base), b: fresh("B", f(5), f(12), base)}
	sw, fl, nt := &fakeSwitcher{}, &fakeFlipper{}, &fakeNotifier{}
	wk := &fakeWaker{}
	l := newLoop(mon, sw, fl, nt)
	l.Wake = app.WakeNotifierFunc(wk.Wake)
	cfg := config.Default()
	cfg.RacingNotify = false
	l.UpdateConfig(cfg)

	if r := l.Tick(context.Background(), base); !r.Switched {
		t.Fatalf("the switch must still happen with notify off: %+v", r)
	}
	if len(wk.calls) != 0 {
		t.Fatalf("notify off must skip the wake, got %v", wk.calls)
	}
}

// TestTickWakeOnlyOnSuccessfulFlip: the wake lives inside doSwitch's
// successful path, after a successful flip — an Apply-failed round (no flip
// attempted) and a non-switch round (stale) must never wake, even with the
// seam wired (decision 1 hook point; an early notify would act on the old
// account's state).
func TestTickWakeOnlyOnSuccessfulFlip(t *testing.T) {
	t.Run("apply failure", func(t *testing.T) {
		mon := &fakeMonitor{a: fresh("A", f(95), f(10), base), b: fresh("B", f(5), f(12), base)}
		sw, fl, nt := &fakeSwitcher{failErr: errors.New("settings write failed")}, &fakeFlipper{}, &fakeNotifier{}
		wk := &fakeWaker{}
		l := newLoop(mon, sw, fl, nt)
		l.Wake = app.WakeNotifierFunc(wk.Wake)

		r := l.Tick(context.Background(), base)
		if r.Switched {
			t.Fatalf("apply-failed round must not switch: %+v", r)
		}
		if len(fl.modes) != 0 {
			t.Fatalf("apply failure must not reach the flip: %v", fl.modes)
		}
		if len(wk.calls) != 0 {
			t.Fatalf("no wake without a successful flip, got %v", wk.calls)
		}
	})
	t.Run("stale round", func(t *testing.T) {
		mon := &fakeMonitor{} // all-nil data → stale, no decision
		sw, fl, nt := &fakeSwitcher{}, &fakeFlipper{}, &fakeNotifier{}
		wk := &fakeWaker{}
		l := newLoop(mon, sw, fl, nt)
		l.Wake = app.WakeNotifierFunc(wk.Wake)

		r := l.Tick(context.Background(), base)
		if r.Switched {
			t.Fatalf("stale round must not switch: %+v", r)
		}
		if len(wk.calls) != 0 {
			t.Fatalf("no wake on a non-switch round, got %v", wk.calls)
		}
	})
}

// TestTickRacingNotifyOffSkipsWakeRacingDirection: the master switch also
// gates the RACING branch (mirror of the SAVING-direction off test).
func TestTickRacingNotifyOffSkipsWakeRacingDirection(t *testing.T) {
	mon := &fakeMonitor{a: fresh("A", f(10), f(10), base), b: fresh("B", f(5), f(12), base)}
	sw, fl, nt := &fakeSwitcher{}, &fakeFlipper{}, &fakeNotifier{}
	wk := &fakeWaker{}
	l := newLoop(mon, sw, fl, nt)
	l.Wake = app.WakeNotifierFunc(wk.Wake)
	l.Current = "B"
	l.Pending = policy.ManualCmd{Target: "A"}
	cfg := config.Default()
	cfg.RacingNotify = false
	l.UpdateConfig(cfg)

	if r := l.Tick(context.Background(), base); !r.Switched {
		t.Fatalf("the switch must still happen with notify off: %+v", r)
	}
	if len(wk.calls) != 0 {
		t.Fatalf("notify off must skip the wake in the RACING direction too, got %v", wk.calls)
	}
}

// TestTickFlipFailureSkipsWakeRacingDirection: the err==nil flip gate covers
// the RACING branch too — a manual back-to-A round with a failing flip must
// complete the switch and skip the wake.
func TestTickFlipFailureSkipsWakeRacingDirection(t *testing.T) {
	var seq []string
	mon := &fakeMonitor{a: fresh("A", f(10), f(10), base), b: fresh("B", f(5), f(12), base), log: &seq}
	sw, nt := &fakeSwitcher{log: &seq}, &fakeNotifier{log: &seq}
	wk := &fakeWaker{log: &seq}
	// Built inline (not via newLoop) because the quota flipper must fail.
	l := &app.Loop{
		Mon: mon, Sw: sw, Qm: &failFlipper{log: &seq}, Not: nt,
		Cfg:          config.Default(),
		Profiles:     profiles(),
		StatePath:    "",
		SettingsPath: "C:\\nonexistent\\settings.json",
		Autopilot:    true,
		Current:      "B",
	}
	l.Wake = app.WakeNotifierFunc(wk.Wake)
	l.Pending = policy.ManualCmd{Target: "A"}

	if r := l.Tick(context.Background(), base); !r.Switched {
		t.Fatalf("the completed settings switch must survive a flip failure: %+v", r)
	}
	if firstIndex(seq, "flip") < 0 {
		t.Fatalf("the flip must have been attempted: %v", seq)
	}
	if len(wk.calls) != 0 {
		t.Fatalf("a failed flip must skip the wake in the RACING direction too, got %v", wk.calls)
	}
}

// TestTickWakeErrorDoesNotBreakSwitch: a failed wake (e.g. the headless
// spawn fails) is log-only — the completed switch must survive it and no
// extra error toast may be added beyond the normal switch notification
// (decision 1: notify failure must never affect the finished switch).
func TestTickWakeErrorDoesNotBreakSwitch(t *testing.T) {
	mon := &fakeMonitor{a: fresh("A", f(95), f(10), base), b: fresh("B", f(5), f(12), base)}
	sw, fl, nt := &fakeSwitcher{}, &fakeFlipper{}, &fakeNotifier{}
	wk := &fakeWaker{failErr: errors.New("headless spawn failed")}
	l := newLoop(mon, sw, fl, nt)
	l.Wake = app.WakeNotifierFunc(wk.Wake)

	r := l.Tick(context.Background(), base)
	if !r.Switched {
		t.Fatalf("the switch must survive a wake failure: %+v", r)
	}
	if len(wk.calls) != 1 {
		t.Fatalf("the wake must have been attempted exactly once: %v", wk.calls)
	}
	if len(nt.msgs) != 1 {
		t.Fatalf("a wake failure must be log-only (no extra toast), msgs=%v", nt.msgs)
	}
}

// TestTickNilWakeStillSwitches: a Loop wired without a waker (zero value —
// the shape of every pre-existing test) must keep switching; the seam is
// optional at the call site.
func TestTickNilWakeStillSwitches(t *testing.T) {
	mon := &fakeMonitor{a: fresh("A", f(95), f(10), base), b: fresh("B", f(5), f(12), base)}
	sw, fl, nt := &fakeSwitcher{}, &fakeFlipper{}, &fakeNotifier{}
	l := newLoop(mon, sw, fl, nt)

	if r := l.Tick(context.Background(), base); !r.Switched {
		t.Fatalf("nil Wake must not break the switch: %+v", r)
	}
}
