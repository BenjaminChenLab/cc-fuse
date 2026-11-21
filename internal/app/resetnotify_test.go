package app_test

// Acceptance tests (black-box) — 2026-11-21 RESET notify (A-account 429 wall).
// Spec: docs/blueprints/2026-11-21-reset-notify.md (decisions 1-9, acceptance
// criteria). When account A is Current and its FRESH 5h sample crosses the
// wall line, the Loop opens ONE wall episode anchored to the quota API's
// FiveHourReset + buffer and, at that instant, wakes the go-when-racing
// registry with the "RESET" retry order through the existing Wake seam —
// never touching the quota-mode file. Repeated in-wall detections never
// re-fire; Current leaving A cancels; an early drop below the wall fires
// immediately (once per episode); a minimum gap between RESET notifies
// holds; stale rounds change nothing. Real timers are exercised with fire
// instants placed well inside a two-second window (a past fireAt fires
// immediately — contract point 5), so every wait stays bounded.

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"cc-fuse/internal/app"
	"cc-fuse/internal/config"
	"cc-fuse/internal/domain"
)

// resetWaker is a goroutine-safe recording waker: the episode machine fires
// from a time.AfterFunc goroutine, so an unsynchronized slice would race the
// test's reads; calls are mutex-guarded and every Wake signals fireCh. fail
// turns it into a failing waker (the fire failure must be log-only).
type resetWaker struct {
	mu     sync.Mutex
	calls  [][2]string
	fireCh chan struct{}
	fail   bool
}

// newResetWaker builds the recording waker.
func newResetWaker() *resetWaker {
	return &resetWaker{fireCh: make(chan struct{}, 64)}
}

// Wake implements app.WakeNotifier (recording fake).
func (w *resetWaker) Wake(watchersFile, mode string) error {
	w.mu.Lock()
	w.calls = append(w.calls, [2]string{watchersFile, mode})
	w.mu.Unlock()
	select {
	case w.fireCh <- struct{}{}:
	default:
	}
	if w.fail {
		return errors.New("headless spawn failed")
	}
	return nil
}

// count returns how many wake calls landed so far.
func (w *resetWaker) count() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return len(w.calls)
}

// last returns the most recent (watchersFile, mode) pair.
func (w *resetWaker) last() [2]string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.calls[len(w.calls)-1]
}

// waitWake blocks until the waker records n calls, or fails the test.
func waitWake(t *testing.T, w *resetWaker, n int, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if w.count() >= n {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %d wake calls, got %d after %v", n, w.count(), timeout)
}

// assertWakeCount gives any mis-scheduled timer a settle window (longer than
// every fire horizon the tests arm, shorter than every "must not fire"
// schedule) and then pins the exact call count.
func assertWakeCount(t *testing.T, w *resetWaker, want int, why string) {
	t.Helper()
	time.Sleep(700 * time.Millisecond)
	if got := w.count(); got != want {
		t.Fatalf("%s: wake calls = %d, want %d", why, got, want)
	}
}

// newResetLoop builds a Loop parked on A with autopilot OFF: the flee gate is
// closed so a hot A cannot switch away mid-episode — every episode test needs
// Current to stay A. The quota flipper is injected so the no-Flip guarantee
// is observable.
func newResetLoop(mon *fakeMonitor, wk *resetWaker, fl *fakeFlipper) *app.Loop {
	l := &app.Loop{
		Mon: mon, Sw: &fakeSwitcher{}, Qm: fl, Not: &fakeNotifier{},
		Cfg:          config.Default(),
		Profiles:     profiles(),
		StatePath:    "",
		SettingsPath: "C:\\nonexistent\\settings.json",
		Autopilot:    false, // flee-to-B blocked: the episode must survive the whole test
		Current:      "A",
	}
	l.Wake = app.WakeNotifierFunc(wk.Wake)
	return l
}

// cfgFor builds the config variant the episode tests run with: notify on,
// the wall line at `line`, buffer/gap overridden per test. Buffer 0 puts the
// fire instant at the reset moment itself so real timers stay in the
// millisecond range; the production default (1 minute) is pinned separately.
func cfgFor(line float64, bufferMin, gapMin int) config.Config {
	c := config.Default()
	c.ResetNotify = true
	c.ResetNotifyAfterPct = line
	c.ResetNotifyBufferMin = bufferMin
	c.ResetNotifyMinGapMin = gapMin
	return c
}

// hotA is A's walled fresh sample: 5h usage over the line, the reset instant
// placed at `reset`.
func hotA(reset time.Time) *domain.QuotaSnapshot {
	s := fresh("A", f(99.5), f(10), base)
	s.FiveHourReset = &reset
	return s
}

// TestResetNotifyImmediateFireOnPastReset: A walled with a FiveHourReset
// already past the buffer → the fire instant is in the past → the notify
// fires immediately (contract point 5), exactly once, carrying the config's
// watchers file and the RESET direction; repeated in-wall detections never
// re-fire; and the whole episode makes zero Flip calls (the RESET path is a
// notify, never a mode write).
func TestResetNotifyImmediateFireOnPastReset(t *testing.T) {
	reset := base.Add(-2 * time.Hour) // fireAt = reset+1min still far past
	mon := &fakeMonitor{a: hotA(reset), b: fresh("B", f(5), f(12), base)}
	wk := newResetWaker()
	fl := &fakeFlipper{}
	l := newResetLoop(mon, wk, fl)
	cfg := cfgFor(99, 1, 30)
	l.UpdateConfig(cfg)

	l.Tick(context.Background(), base)
	waitWake(t, wk, 1, 3*time.Second)
	if got := wk.last(); got[0] != cfg.WatchersFile || got[1] != "RESET" {
		t.Fatalf("wake must carry (cfg.WatchersFile, RESET), got %v", got)
	}
	// In-wall re-detection on an already-fired episode: no re-fire, no re-arm
	// (the fired flag alone keeps it inert — the UNFIRED same-reset case has
	// its own dedicated killing assertion further below).
	l.Tick(context.Background(), base.Add(time.Second))
	assertWakeCount(t, wk, 1, "repeated in-wall detection must not re-fire")
	if n := len(fl.modes); n != 0 {
		t.Fatalf("the RESET path must never touch the quota-mode file, flips=%v", fl.modes)
	}
}

// TestResetNotifyScheduledFireAtResetPlusBuffer: with the reset instant just
// ahead, the notify must NOT fire at detection (the schedule is real) and
// must wait out the configured buffer — buffer 1 puts the fire a full minute
// past a reset 150ms ahead (nothing inside the window), which kills both
// "fires immediately on detection" and "buffer not taken from config".
func TestResetNotifyScheduledFireAtResetPlusBuffer(t *testing.T) {
	reset := base.Add(150 * time.Millisecond)
	mon := &fakeMonitor{a: hotA(reset), b: fresh("B", f(5), f(12), base)}
	wk := newResetWaker()
	l := newResetLoop(mon, wk, &fakeFlipper{})
	l.UpdateConfig(cfgFor(99, 1, 30))

	l.Tick(context.Background(), base)
	assertWakeCount(t, wk, 0, "buffer 1 must schedule at reset+1min — nothing fires at detection")
}

// TestResetNotifyBufferZeroFiresAtResetMoment: the buffer comes from config —
// buffer 0 puts the fire instant at the reset moment itself, so with a reset
// 150ms ahead the notify lands inside the window (and only once).
func TestResetNotifyBufferZeroFiresAtResetMoment(t *testing.T) {
	reset := base.Add(150 * time.Millisecond)
	mon := &fakeMonitor{a: hotA(reset), b: fresh("B", f(5), f(12), base)}
	wk := newResetWaker()
	l := newResetLoop(mon, wk, &fakeFlipper{})
	l.UpdateConfig(cfgFor(99, 0, 30))

	l.Tick(context.Background(), base)
	waitWake(t, wk, 1, 3*time.Second)
	assertWakeCount(t, wk, 1, "the scheduled fire must land exactly once")
}

// TestResetNotifyEarlyFireOnDrop: the wall clears early (fresh pct back under
// the line while the timer is armed) → fire immediately, cancel the timer,
// close the episode — exactly one notify. The original schedule sits an hour
// out, so a notify inside the window can only have come from the early-fire
// path (a schedule-only implementation stays silent here), and the settle
// window proves no extra fire follows.
func TestResetNotifyEarlyFireOnDrop(t *testing.T) {
	reset := base.Add(time.Hour) // original schedule far outside every test window
	mon := &fakeMonitor{a: hotA(reset), b: fresh("B", f(5), f(12), base)}
	wk := newResetWaker()
	l := newResetLoop(mon, wk, &fakeFlipper{})
	l.UpdateConfig(cfgFor(99, 0, 30))

	l.Tick(context.Background(), base) // detection arms the timer
	mon.a = fresh("A", f(30), f(10), base)
	l.Tick(context.Background(), base.Add(time.Second)) // fresh pct 30 → early fire now
	waitWake(t, wk, 1, 3*time.Second)
	assertWakeCount(t, wk, 1, "early fire must land once — no second fire from any schedule")
}

// TestResetNotifyCancelOnLeaveA: Current leaving A mid-episode cancels the
// armed schedule — zero notifies even past the original fire instant (the
// flip notify already covered the moved sessions). The cancel is not gated
// on sample freshness: it applies on stale rounds too (decision 3 carries no
// freshness qualifier — Current is app state, not sample data).
func TestResetNotifyCancelOnLeaveA(t *testing.T) {
	t.Run("fresh round", func(t *testing.T) {
		reset := base.Add(500 * time.Millisecond)
		mon := &fakeMonitor{a: hotA(reset), b: fresh("B", f(5), f(12), base)}
		wk := newResetWaker()
		l := newResetLoop(mon, wk, &fakeFlipper{})
		l.UpdateConfig(cfgFor(99, 0, 30))

		l.Tick(context.Background(), base) // armed
		l.Current = "B"                    // Current left A
		l.Tick(context.Background(), base.Add(time.Second))
		assertWakeCount(t, wk, 0, "Current leaving A must cancel the episode — zero notifies")
	})
	t.Run("stale round", func(t *testing.T) {
		reset := base.Add(500 * time.Millisecond)
		mon := &fakeMonitor{a: hotA(reset), b: fresh("B", f(5), f(12), base)}
		wk := newResetWaker()
		l := newResetLoop(mon, wk, &fakeFlipper{})
		l.UpdateConfig(cfgFor(99, 0, 30))

		l.Tick(context.Background(), base) // armed
		l.Current = "B"
		mon.failAfter = 2 // the cancel round goes stale — decision 3 still applies
		l.Tick(context.Background(), base.Add(11*time.Minute))
		assertWakeCount(t, wk, 0, "the leave-A cancel must apply on stale rounds too")
	})
}

// TestResetNotifyMinGapBlocksSecondFire: two episodes inside the min-gap
// window must produce ONE notify; with the gap set to 0 the very same
// scenario fires twice — proving the suppression comes from the gap config,
// not from any sticky once-only latch.
func TestResetNotifyMinGapBlocksSecondFire(t *testing.T) {
	run := func(gapMin int) int {
		mon := &fakeMonitor{a: hotA(base.Add(500 * time.Millisecond)), b: fresh("B", f(5), f(12), base)}
		wk := newResetWaker()
		l := newResetLoop(mon, wk, &fakeFlipper{})
		l.UpdateConfig(cfgFor(99, 0, gapMin))
		l.Tick(context.Background(), base)                  // ep1 armed
		mon.a = fresh("A", f(30), f(10), base)              // wall clears early
		l.Tick(context.Background(), base.Add(time.Second)) // ep1 fires
		mon.a = hotA(base.Add(-time.Hour))                  // re-cross: ep2 with a past reset → immediate attempt
		l.Tick(context.Background(), base.Add(2*time.Second))
		waitWake(t, wk, 1, 3*time.Second)
		time.Sleep(700 * time.Millisecond) // settle past any mis-scheduled second fire
		return wk.count()
	}
	if got := run(30); got != 1 {
		t.Fatalf("two episodes inside the 30-min gap must notify once, got %d", got)
	}
	if got := run(0); got != 2 {
		t.Fatalf("gap 0 must let the second episode fire, got %d", got)
	}
}

// TestResetNotifyFiveHourResetChangedMidWall: the API re-reporting a
// different FiveHourReset inside one episode re-anchors the remaining time —
// the notify arrives per the NEW instant, exactly once (decision 2, idempotent).
func TestResetNotifyFiveHourResetChangedMidWall(t *testing.T) {
	mon := &fakeMonitor{a: hotA(base.Add(5 * time.Second)), b: fresh("B", f(5), f(12), base)}
	wk := newResetWaker()
	l := newResetLoop(mon, wk, &fakeFlipper{})
	l.UpdateConfig(cfgFor(99, 0, 30))

	l.Tick(context.Background(), base) // armed for +5s
	mon.a = hotA(base.Add(150 * time.Millisecond))
	l.Tick(context.Background(), base.Add(time.Second)) // re-report → re-anchor (fires ≈ immediately)
	waitWake(t, wk, 1, 3*time.Second)
	assertWakeCount(t, wk, 1, "re-anchoring must stay once-per-episode")
}

// TestResetNotifyStaleRoundChangesNothing: stale rounds (aged-out reused
// sample) must not open an episode, and must not close or cancel an open
// one — the machine only eats FRESH samples (decision 1).
func TestResetNotifyStaleRoundChangesNothing(t *testing.T) {
	t.Run("stale round must not open an episode", func(t *testing.T) {
		hot := hotA(base.Add(-2 * time.Hour)) // an OPEN would fire immediately (past fireAt)
		mon := &fakeMonitor{a: hot, b: fresh("B", f(5), f(12), base)}
		wk := newResetWaker()
		l := newResetLoop(mon, wk, &fakeFlipper{})
		cfg := cfgFor(99, 1, 30)
		cfg.ResetNotify = false // round 1: master off → nothing opens, but the sample caches
		l.UpdateConfig(cfg)
		l.Tick(context.Background(), base)
		cfg.ResetNotify = true
		l.UpdateConfig(cfg)
		mon.failAfter = 2 // round 2: queries fail → the aged cached sample turns the round stale
		l.Tick(context.Background(), base.Add(11*time.Minute))
		assertWakeCount(t, wk, 0, "a stale round must not open an episode even with a walled cached sample")
	})
	t.Run("stale round must not close or cancel an open episode", func(t *testing.T) {
		reset := base.Add(500 * time.Millisecond)
		mon := &fakeMonitor{a: hotA(reset), b: fresh("B", f(5), f(12), base)}
		wk := newResetWaker()
		l := newResetLoop(mon, wk, &fakeFlipper{})
		l.UpdateConfig(cfgFor(99, 0, 30))
		l.Tick(context.Background(), base) // armed, fires at +500ms real
		mon.failAfter = 2                  // round 2 goes stale — the machine must freeze
		l.Tick(context.Background(), base.Add(11*time.Minute))
		waitWake(t, wk, 1, 3*time.Second) // the armed schedule survives the stale round untouched
	})
}

// TestResetNotifyEpisodeClosesAfterScheduledFire: after the scheduled fire
// the episode must still close when the fresh pct drops back under the wall
// (episode = crossing until drop-back, doc contract line), so a LATER wall
// crossing can open a fresh episode and notify again — an implementation
// that never closes a fired episode would swallow the next wall's notify.
func TestResetNotifyEpisodeClosesAfterScheduledFire(t *testing.T) {
	reset := base.Add(500 * time.Millisecond)
	mon := &fakeMonitor{a: hotA(reset), b: fresh("B", f(5), f(12), base)}
	wk := newResetWaker()
	l := newResetLoop(mon, wk, &fakeFlipper{})
	l.UpdateConfig(cfgFor(99, 0, 0)) // gap 0: the second episode's fire must be allowed

	l.Tick(context.Background(), base) // ep1 armed
	waitWake(t, wk, 1, 3*time.Second)  // scheduled fire lands at the reset moment
	mon.a = fresh("A", f(30), f(10), base)
	l.Tick(context.Background(), base.Add(time.Second)) // fresh drop → episode closes (already fired, no early fire)
	mon.a = hotA(base.Add(-time.Hour))
	l.Tick(context.Background(), base.Add(2*time.Second)) // re-cross → NEW episode → immediate fire
	waitWake(t, wk, 2, 3*time.Second)
	assertWakeCount(t, wk, 2, "a closed episode must let the next wall crossing notify again")
}

// TestResetNotifyNilResetFallsBackToAnchor: FiveHourReset missing → the
// episode still opens, anchored to fallback anchor+5h+buffer (decision 1) —
// nothing fires inside the window, and the early-drop path still fires
// (proving the episode truly opened rather than being skipped or panicking).
func TestResetNotifyNilResetFallsBackToAnchor(t *testing.T) {
	s := fresh("A", f(99.5), f(10), base) // FiveHourReset nil
	mon := &fakeMonitor{a: s, b: fresh("B", f(5), f(12), base)}
	wk := newResetWaker()
	l := newResetLoop(mon, wk, &fakeFlipper{})
	l.UpdateConfig(cfgFor(99, 0, 30))

	l.Tick(context.Background(), base) // detection opens the episode on the fallback anchor
	assertWakeCount(t, wk, 0, "nil FiveHourReset must schedule 5h out — nothing fires inside the window")
	mon.a = fresh("A", f(30), f(10), base)
	l.Tick(context.Background(), base.Add(time.Second)) // early drop → the fallback-anchored episode fires
	waitWake(t, wk, 1, 3*time.Second)
}

// TestResetNotifyMasterOffMidEpisodeCancels: turning ResetNotify off
// mid-episode cancels the armed schedule and closes the episode (decision 3) —
// zero notifies past the original fire instant.
func TestResetNotifyMasterOffMidEpisodeCancels(t *testing.T) {
	reset := base.Add(500 * time.Millisecond)
	mon := &fakeMonitor{a: hotA(reset), b: fresh("B", f(5), f(12), base)}
	wk := newResetWaker()
	l := newResetLoop(mon, wk, &fakeFlipper{})
	l.UpdateConfig(cfgFor(99, 0, 30))

	l.Tick(context.Background(), base) // armed
	cfg := cfgFor(99, 0, 30)
	cfg.ResetNotify = false
	l.UpdateConfig(cfg)
	l.Tick(context.Background(), base.Add(time.Second))
	assertWakeCount(t, wk, 0, "master off mid-episode must cancel the armed schedule")
}

// TestResetNotifyFireFailureLogOnly: a failed fire is log-only (decision 5) —
// the attempt counts as the episode's single fire (no retry storm on later
// ticks) and both ticks complete normally, polling and switching untouched.
func TestResetNotifyFireFailureLogOnly(t *testing.T) {
	reset := base.Add(-2 * time.Hour) // fireAt past → immediate attempt
	mon := &fakeMonitor{a: hotA(reset), b: fresh("B", f(5), f(12), base)}
	wk := newResetWaker()
	wk.fail = true
	l := newResetLoop(mon, wk, &fakeFlipper{})
	l.UpdateConfig(cfgFor(99, 1, 30))

	r1 := l.Tick(context.Background(), base)
	waitWake(t, wk, 1, 3*time.Second)
	r2 := l.Tick(context.Background(), base.Add(time.Second)) // in-wall re-detection after the failed fire
	if r1.Switched || r2.Switched {
		t.Fatalf("a failed fire must not affect switching: %+v %+v", r1, r2)
	}
	assertWakeCount(t, wk, 1, "a failed fire must not retry — the episode had its single attempt")
}

// TestResetNotifyStaleRoundDoesNotCloseEpisode: a stale round must neither
// cancel NOR close the armed episode (decision 1: stale changes nothing).
// After the stale round the schedule still fires; a later FRESH in-wall round
// must find the episode still open (already fired → no re-open, no second
// fire). An implementation that closed the episode on the stale round would
// re-open it on the fresh round and double-fire.
func TestResetNotifyStaleRoundDoesNotCloseEpisode(t *testing.T) {
	reset := base.Add(500 * time.Millisecond)
	mon := &fakeMonitor{a: hotA(reset), b: fresh("B", f(5), f(12), base)}
	wk := newResetWaker()
	l := newResetLoop(mon, wk, &fakeFlipper{})
	l.UpdateConfig(cfgFor(99, 0, 0)) // gap 0: a re-open would be allowed to fire

	l.Tick(context.Background(), base) // armed, fires at +500ms real
	mon.failAfter = 2                  // round 2 goes stale — the machine must freeze entirely
	l.Tick(context.Background(), base.Add(11*time.Minute))
	waitWake(t, wk, 1, 3*time.Second) // the armed schedule survived the stale round
	again := fresh("A", f(99.5), f(10), base.Add(12*time.Minute))
	r := base.Add(500 * time.Millisecond)
	again.FiveHourReset = &r
	mon.a = again
	mon.failAfter = 0 // round 3 is fresh again, still in-wall, same reset
	l.Tick(context.Background(), base.Add(12*time.Minute))
	assertWakeCount(t, wk, 1, "the stale round must not have closed the episode — no re-open, no second fire")
}

// TestResetNotifyMasterOffSkipsEverything: the master switch off means no
// detection at all — a walled A with a past fire instant never notifies.
func TestResetNotifyMasterOffSkipsEverything(t *testing.T) {
	mon := &fakeMonitor{a: hotA(base.Add(-2 * time.Hour)), b: fresh("B", f(5), f(12), base)}
	wk := newResetWaker()
	l := newResetLoop(mon, wk, &fakeFlipper{})
	cfg := cfgFor(99, 1, 30)
	cfg.ResetNotify = false
	l.UpdateConfig(cfg)

	l.Tick(context.Background(), base)
	assertWakeCount(t, wk, 0, "master switch off must disable detection entirely")
}

// TestResetNotifyWallLineFromConfig: the wall line is the config's
// ResetNotifyAfterPct, inclusive at the line (decision 1) — a sample at the
// line opens the episode, one below it does not.
func TestResetNotifyWallLineFromConfig(t *testing.T) {
	build := func(pct float64) *resetWaker {
		s := fresh("A", f(pct), f(10), base)
		r := base.Add(-2 * time.Hour)
		s.FiveHourReset = &r
		mon := &fakeMonitor{a: s, b: fresh("B", f(5), f(12), base)}
		wk := newResetWaker()
		l := newResetLoop(mon, wk, &fakeFlipper{})
		l.UpdateConfig(cfgFor(97, 1, 30)) // line deliberately off the natural 99
		l.Tick(context.Background(), base)
		return wk
	}
	atLine := build(97.0) // == line → inclusive open → immediate fire
	waitWake(t, atLine, 1, 3*time.Second)
	below := build(96.9) // < line → no episode, nothing ever fires
	assertWakeCount(t, below, 0, "below the configured wall line no episode may open")
}

// resetLog is a recording Logger (the Loop's decision-log seam): the
// episode machine announces every schedule with one line carrying the fire
// instant (decision 11), which makes the schedule ARITHMETIC observable at
// acceptance level without waiting out the real timers.
type resetLog struct {
	mu    sync.Mutex
	lines []string
}

// Printf implements app.Logger (recording fake).
func (r *resetLog) Printf(format string, args ...any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.lines = append(r.lines, fmt.Sprintf(format, args...))
}

// scheduleFireAts returns the fire instants announced by "RESET 通知排程"
// lines, in order — one entry per schedule arm.
func (r *resetLog) scheduleFireAts(t *testing.T) []time.Time {
	t.Helper()
	rfc := regexp.MustCompile(`\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(Z|[+-]\d{2}:\d{2})`)
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []time.Time
	for _, ln := range r.lines {
		if !strings.Contains(ln, "RESET 通知排程") {
			continue
		}
		tok := rfc.FindString(ln)
		if tok == "" {
			t.Fatalf("schedule log line carries no RFC3339 instant: %q", ln)
		}
		at, err := time.Parse(time.RFC3339, tok)
		if err != nil {
			t.Fatalf("parse schedule instant %q: %v", tok, err)
		}
		out = append(out, at)
	}
	return out
}

// TestResetNotifyFallbackArithmeticExact: with FiveHourReset missing, the
// schedule log must announce fireAt = sample capture time + EXACTLY 5h + the
// configured buffer minutes (decision 1's disclosed constant). A fallback of
// any other length, an anchor drift (tick instant instead of the sample's
// capture time), or a buffer read in any unit other than minutes cannot
// produce this instant.
func TestResetNotifyFallbackArithmeticExact(t *testing.T) {
	s := fresh("A", f(99.5), f(10), base.Add(-30*time.Second)) // QueriedAt anchors; FiveHourReset nil
	mon := &fakeMonitor{a: s, b: fresh("B", f(5), f(12), base)}
	wk := newResetWaker()
	rl := &resetLog{}
	l := newResetLoop(mon, wk, &fakeFlipper{})
	l.Log = rl
	l.UpdateConfig(cfgFor(99, 7, 30)) // buffer deliberately off-default: 7 minutes

	l.Tick(context.Background(), base)
	ats := rl.scheduleFireAts(t)
	if len(ats) != 1 {
		t.Fatalf("exactly one schedule announcement, got %d (%v)", len(ats), rl.lines)
	}
	want := base.Add(-30 * time.Second).Add(5 * time.Hour).Add(7 * time.Minute)
	if !ats[0].Equal(want) {
		t.Fatalf("fallback fireAt = %s, want sample-capture + exactly 5h + 7min = %s", ats[0], want)
	}
}

// TestResetNotifyBufferUnitIsMinutes: with FiveHourReset present, the
// announced fireAt = reset instant + buffer MINUTES, exactly — a buffer read
// as seconds, hours, or not applied at all cannot produce this instant.
func TestResetNotifyBufferUnitIsMinutes(t *testing.T) {
	reset := base.Add(10 * time.Second)
	mon := &fakeMonitor{a: hotA(reset), b: fresh("B", f(5), f(12), base)}
	wk := newResetWaker()
	rl := &resetLog{}
	l := newResetLoop(mon, wk, &fakeFlipper{})
	l.Log = rl
	l.UpdateConfig(cfgFor(99, 2, 30)) // buffer deliberately off-default: 2 minutes

	l.Tick(context.Background(), base)
	ats := rl.scheduleFireAts(t)
	if len(ats) != 1 {
		t.Fatalf("exactly one schedule announcement, got %d (%v)", len(ats), rl.lines)
	}
	want := reset.Add(2 * time.Minute)
	if !ats[0].Equal(want) {
		t.Fatalf("fireAt = %s, want reset + exactly 2 minutes = %s", ats[0], want)
	}
}

// TestResetNotifySameResetRedetectionDoesNotReschedule: inside ONE unfired
// episode, a repeated FRESH detection reporting the SAME FiveHourReset must
// leave the schedule untouched — one schedule announcement with the original
// fire instant, still zero fires (decision 2's idempotence half). An
// implementation that re-arms (or closes and reopens) on every in-wall tick
// announces a second schedule and is killed here.
func TestResetNotifySameResetRedetectionDoesNotReschedule(t *testing.T) {
	mon := &fakeMonitor{a: hotA(base.Add(time.Hour)), b: fresh("B", f(5), f(12), base)}
	wk := newResetWaker()
	rl := &resetLog{}
	l := newResetLoop(mon, wk, &fakeFlipper{})
	l.Log = rl
	l.UpdateConfig(cfgFor(99, 0, 30))

	l.Tick(context.Background(), base)                  // detection opens + anchors once
	l.Tick(context.Background(), base.Add(time.Second)) // same fresh sample, same reset, episode still unfired
	ats := rl.scheduleFireAts(t)
	if len(ats) != 1 {
		t.Fatalf("same-reset re-detection must not reschedule — one announcement expected, got %d (%v)", len(ats), rl.lines)
	}
	if !ats[0].Equal(base.Add(time.Hour)) {
		t.Fatalf("the anchor must stay the original reset instant, got %s", ats[0])
	}
	assertWakeCount(t, wk, 0, "the episode stays armed and unfired — nothing may fire in the window")
}

// TestResetNotifyFirePathNeverFlipsStructural: structural assertion (spec
// decision 6) — the RESET fire path must never reference the quota-mode
// flip. Scans the app package's production sources: every file that carries
// the "RESET" wake mode must be free of any .Flip( call, and at least one
// such file must exist (the machine itself), so a missing machine fails too.
func TestResetNotifyFirePathNeverFlipsStructural(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatalf("cannot locate the test file")
	}
	dir := filepath.Dir(thisFile)
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read app package dir: %v", err)
	}
	fireFiles := 0
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatalf("read %s: %v", e.Name(), err)
		}
		if !bytes.Contains(data, []byte(`"RESET"`)) {
			continue
		}
		fireFiles++
		if bytes.Contains(data, []byte(".Flip(")) {
			t.Fatalf("%s carries the RESET fire path AND a Flip call — the RESET notify must never touch the quota-mode file (spec decision 6)", e.Name())
		}
	}
	if fireFiles == 0 {
		t.Fatalf("no production file carries the RESET fire path — the episode machine is missing")
	}
}
