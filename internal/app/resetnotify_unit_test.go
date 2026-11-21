package app

// White-box unit tests — 2026-11-21 RESET wall-episode machine internals the
// black-box acceptance suite (resetnotify_test.go, package app_test) cannot
// pin: exact anchor arithmetic, the real-clock min-gap rule, the suppressed
// paths' state (armed vs consumed), callback idempotence against the
// early-fire race, the nil wake seam, and the same-round flee ordering.
// Self-contained fakes (rn* prefix) — wbMonitor/wbNotifier/wbSnap come from
// budgetmerge_internal_test.go (same package); the recording waker is
// mutex-guarded because the armed schedule fires on its own goroutine.

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"cc-fuse/internal/config"
	"cc-fuse/internal/domain"
)

// rnWaker records Wake calls; fail turns it into a failing waker.
type rnWaker struct {
	mu    sync.Mutex
	calls [][2]string
	fail  bool
}

// Wake implements WakeNotifier (recording fake).
func (w *rnWaker) Wake(watchersFile, mode string) error {
	w.mu.Lock()
	w.calls = append(w.calls, [2]string{watchersFile, mode})
	w.mu.Unlock()
	if w.fail {
		return errors.New("headless spawn failed")
	}
	return nil
}

// count returns how many wake calls landed so far.
func (w *rnWaker) count() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return len(w.calls)
}

// rnSwitcher accepts every Apply (records nothing — the reset unit tests only
// need a non-nil seam so a flee round completes).
type rnSwitcher struct{}

// Apply implements Switcher.
func (rnSwitcher) Apply(p domain.Profile, settingsPath string) error { return nil }

// rnFlipper records the quota-mode writes: the machine must never add one.
type rnFlipper struct {
	mu    sync.Mutex
	modes []string
}

// Flip implements QuotaFlipper.
func (f *rnFlipper) Flip(path, mode string) error {
	f.mu.Lock()
	f.modes = append(f.modes, mode)
	f.mu.Unlock()
	return nil
}

// rnLoop builds a Loop parked on A with the flee gate closed (autopilot off),
// the machine's config override applied, and the waker wired. A nil wk leaves
// the Wake INTERFACE nil (the unwired seam) — never a typed-nil pointer.
func rnLoop(mon *wbMonitor, wk *rnWaker, tune func(*config.Config)) *Loop {
	cfg := config.Default()
	if tune != nil {
		tune(&cfg)
	}
	var wake WakeNotifier
	if wk != nil {
		wake = wk
	}
	return &Loop{
		Mon: mon, Sw: rnSwitcher{}, Qm: &rnFlipper{}, Not: wbNotifier{},
		Cfg:          cfg,
		Profiles:     []domain.Profile{{ID: "A", APIKey: "k-a"}, {ID: "B", APIKey: "k-b"}},
		SettingsPath: `C:\nonexistent\settings.json`,
		Current:      "A",
		Wake:         wake,
	}
}

// rnWalled builds A's walled fresh sample with the given reset instant.
func rnWalled(pct float64, reset time.Time) *domain.QuotaSnapshot {
	s := wbSnap("A", mf(pct), mf(10), mBase)
	if !reset.IsZero() {
		s.FiveHourReset = &reset
	}
	return s
}

// rnWaitWake polls until the waker records n calls (bounded).
func rnWaitWake(t *testing.T, w *rnWaker, n int) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if w.count() >= n {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %d wake calls, got %d", n, w.count())
}

// rnStampLastFire sets the min-gap stamp relative to the REAL clock (the
// machine measures time.Since against it — decision 6).
func rnStampLastFire(l *Loop, d time.Duration) {
	l.mu.Lock()
	l.rnLastFire = time.Now().Add(d)
	l.mu.Unlock()
}

// rnEpisode copies the machine state under the lock.
func rnEpisode(l *Loop) resetEpisode {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.rnEp
}

// TestResetUnitFallbackAnchorArithmetic pins the nil-FiveHourReset fallback
// EXACTLY: first-crossing anchor (the sample's capture time) + the disclosed
// 5h constant + the configured buffer, with resetAt left nil (case 7).
func TestResetUnitFallbackAnchorArithmetic(t *testing.T) {
	l := rnLoop(&wbMonitor{a: rnWalled(99.5, time.Time{})}, &rnWaker{},
		func(c *config.Config) { c.ResetNotifyBufferMin = 7 })
	l.resetNotifyTick(mBase, l.Cfg, l.cacheA(t), true)
	ep := rnEpisode(l)
	if !ep.open {
		t.Fatalf("the fallback episode must open")
	}
	want := mBase.Add(5 * time.Hour).Add(7 * time.Minute)
	if !ep.fireAt.Equal(want) {
		t.Fatalf("fallback fireAt = %v, want %v (anchor+5h+buffer, exact)", ep.fireAt, want)
	}
	if ep.resetAt != nil {
		t.Fatalf("fallback episode must carry resetAt == nil, got %v", ep.resetAt)
	}
}

// TestResetUnitResetAnchorArithmetic pins the real-reset arithmetic: fireAt =
// FiveHourReset + buffer and the anchored instant stored (case 8).
func TestResetUnitResetAnchorArithmetic(t *testing.T) {
	reset := mBase.Add(3 * time.Hour)
	l := rnLoop(&wbMonitor{a: rnWalled(99.5, reset)}, &rnWaker{},
		func(c *config.Config) { c.ResetNotifyBufferMin = 3 })
	l.resetNotifyTick(mBase, l.Cfg, l.cacheA(t), true)
	ep := rnEpisode(l)
	if !ep.fireAt.Equal(reset.Add(3 * time.Minute)) {
		t.Fatalf("fireAt = %v, want reset+3min = %v", ep.fireAt, reset.Add(3*time.Minute))
	}
	if ep.resetAt == nil || !ep.resetAt.Equal(reset) {
		t.Fatalf("resetAt = %v, want the anchored reset %v", ep.resetAt, reset)
	}
}

// TestResetUnitFallbackReanchorsOnRealReset: a fallback-anchored episode must
// re-anchor when the API starts reporting a real FiveHourReset (nil→set
// counts as a changed anchor, case 9) — remaining time per the new instant,
// still one fire.
func TestResetUnitFallbackReanchorsOnRealReset(t *testing.T) {
	mon := &wbMonitor{a: rnWalled(99.5, time.Time{})} // FiveHourReset nil
	wk := &rnWaker{}
	l := rnLoop(mon, wk, func(c *config.Config) { c.ResetNotifyBufferMin = 0 })
	l.resetNotifyTick(mBase, l.Cfg, l.cacheA(t), true)
	if ep := rnEpisode(l); ep.resetAt != nil {
		t.Fatalf("precondition: the first episode must be fallback-anchored")
	}
	reset := mBase.Add(300 * time.Millisecond)
	mon.a = rnWalled(99.5, reset)
	l.resetNotifyTick(mBase.Add(time.Second), l.Cfg, l.cacheA(t), true)
	ep := rnEpisode(l)
	if ep.resetAt == nil || !ep.resetAt.Equal(reset) {
		t.Fatalf("the episode must have re-anchored to the real reset, resetAt=%v", ep.resetAt)
	}
	if !ep.fireAt.Equal(reset) {
		t.Fatalf("re-anchored fireAt = %v, want %v (buffer 0)", ep.fireAt, reset)
	}
	rnWaitWake(t, wk, 1)
	time.Sleep(300 * time.Millisecond)
	if n := wk.count(); n != 1 {
		t.Fatalf("re-anchor must stay once-per-episode, got %d fires", n)
	}
}

// TestResetUnitSampleGuardsNoOpen: nil sample, failed sample, and a sample
// without a 5h bucket must all leave the machine closed (case 13); so must a
// below-line sample with no episode (case 22).
func TestResetUnitSampleGuardsNoOpen(t *testing.T) {
	cases := []struct {
		name string
		snap *domain.QuotaSnapshot
	}{
		{"nil sample", nil},
		{"failed sample", &domain.QuotaSnapshot{ProfileID: "A", Err: errors.New("boom")}},
		{"no 5h bucket", wbSnap("A", nil, mf(10), mBase)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			l := rnLoop(&wbMonitor{}, &rnWaker{}, nil)
			l.resetNotifyTick(mBase, l.Cfg, tc.snap, true)
			if ep := rnEpisode(l); ep.open {
				t.Fatalf("no episode may open on %s", tc.name)
			}
		})
	}
	t.Run("below line with no episode", func(t *testing.T) {
		l := rnLoop(&wbMonitor{}, &rnWaker{}, nil)
		l.resetNotifyTick(mBase, l.Cfg, wbSnap("A", mf(30), mf(10), mBase), true)
		if ep := rnEpisode(l); ep.open {
			t.Fatalf("a below-line round with no episode must stay closed")
		}
	})
}

// TestResetUnitGapUsesRealClock pins the min-gap measurement to the REAL
// clock (decision 6): stamps 31 minutes ago satisfy a 30-minute gap, stamps
// 29 minutes ago do not — injected tick times are never consulted (cases 27,
// 29); and the gap-suppressed scheduled fire leaves the episode OPEN but
// consumed so in-wall re-detection stays inert (case 26).
func TestResetUnitGapUsesRealClock(t *testing.T) {
	l := rnLoop(&wbMonitor{}, &rnWaker{}, nil)
	// Zero stamp (first fire ever) and a non-positive gap (the anti-spam
	// window disabled) must both short-circuit to OK regardless of any stamp.
	l.mu.Lock()
	l.rnLastFire = time.Time{}
	zeroStampOK := l.resetNotifyGapOKLocked(30)
	l.rnLastFire = time.Now().Add(-time.Minute)
	gapZeroOK := l.resetNotifyGapOKLocked(0)
	gapNegativeOK := l.resetNotifyGapOKLocked(-1)
	l.mu.Unlock()
	if !zeroStampOK || !gapZeroOK || !gapNegativeOK {
		t.Fatalf("zero stamp / gap 0 / gap -1 must all satisfy the gap (zero=%v gap0=%v gap-1=%v)", zeroStampOK, gapZeroOK, gapNegativeOK)
	}
	l.mu.Lock()
	l.rnLastFire = time.Now().Add(-31 * time.Minute)
	l.mu.Unlock()
	if !l.resetNotifyGapOKLocked(30) {
		t.Fatalf("31 min since the last fire must satisfy a 30-min gap")
	}
	l.mu.Lock()
	l.rnLastFire = time.Now().Add(-29 * time.Minute)
	l.mu.Unlock()
	if l.resetNotifyGapOKLocked(30) {
		t.Fatalf("29 min since the last fire must NOT satisfy a 30-min gap")
	}

	// Suppression path: a past-fireAt episode inside the gap suppresses and
	// consumes WITHOUT delivering, and the episode stays open.
	mon := &wbMonitor{a: rnWalled(99.5, mBase.Add(-2*time.Hour))}
	wk := &rnWaker{}
	l2 := rnLoop(mon, wk, func(c *config.Config) { c.ResetNotifyBufferMin = 0 })
	rnStampLastFire(l2, -29*time.Minute)
	l2.resetNotifyTick(mBase, l2.Cfg, l2.cacheA(t), true)
	rnWaitWakeNever(t, wk)
	ep := rnEpisode(l2)
	if !ep.open || !ep.fired {
		t.Fatalf("the gap-suppressed episode must stay open AND be consumed (open=%v fired=%v)", ep.open, ep.fired)
	}
	if wk.count() != 0 {
		t.Fatalf("the suppressed fire must not deliver, got %d wakes", wk.count())
	}
}

// TestResetUnitEarlyFireGapUnsatisfiedStaysArmed: below-wall rounds inside
// the min-gap fire NOTHING and keep the episode armed (fired=false, timer
// alive); once the real clock passes the gap, the next below-wall round
// fires early and closes the episode (case 20).
func TestResetUnitEarlyFireGapUnsatisfiedStaysArmed(t *testing.T) {
	mon := &wbMonitor{a: rnWalled(99.5, mBase.Add(time.Hour))}
	wk := &rnWaker{}
	l := rnLoop(mon, wk, func(c *config.Config) { c.ResetNotifyBufferMin = 0 })
	l.resetNotifyTick(mBase, l.Cfg, l.cacheA(t), true) // armed for +1h
	rnStampLastFire(l, -29*time.Minute)

	mon.a = wbSnap("A", mf(30), mf(10), mBase.Add(time.Second))
	l.resetNotifyTick(mBase.Add(time.Second), l.Cfg, l.cacheA(t), true)
	ep := rnEpisode(l)
	if !ep.open || ep.fired || ep.timer == nil {
		t.Fatalf("gap-unsatisfied early moment: episode must stay armed (open=%v fired=%v timer=%v)", ep.open, ep.fired, ep.timer != nil)
	}
	if wk.count() != 0 {
		t.Fatalf("no fire inside the gap, got %d wakes", wk.count())
	}

	rnStampLastFire(l, -31*time.Minute)
	l.resetNotifyTick(mBase.Add(2*time.Second), l.Cfg, l.cacheA(t), true)
	rnWaitWake(t, wk, 1)
	if ep := rnEpisode(l); ep.open {
		t.Fatalf("the early fire must close the episode")
	}
}

// TestResetUnitScheduledFireNoopWhenClosed: the armed callback must be inert
// once the episode is closed, and must not fire twice on an already-consumed
// episode (the timer-vs-early-fire single-fire race, case 25).
func TestResetUnitScheduledFireNoopWhenClosed(t *testing.T) {
	t.Run("callback after close", func(t *testing.T) {
		wk := &rnWaker{}
		l := rnLoop(&wbMonitor{}, wk, nil)
		l.resetNotifyScheduledFire(`S:/wf`) // nothing open
		if wk.count() != 0 {
			t.Fatalf("a closed episode's callback must not deliver")
		}
	})
	t.Run("callback after the fire already happened", func(t *testing.T) {
		mon := &wbMonitor{a: rnWalled(99.5, mBase.Add(-2*time.Hour))}
		wk := &rnWaker{}
		l := rnLoop(mon, wk, func(c *config.Config) { c.ResetNotifyBufferMin = 0 })
		l.resetNotifyTick(mBase, l.Cfg, l.cacheA(t), true) // past fireAt → immediate fire
		rnWaitWake(t, wk, 1)
		l.resetNotifyScheduledFire(`S:/wf`) // late duplicate callback
		time.Sleep(300 * time.Millisecond)
		if n := wk.count(); n != 1 {
			t.Fatalf("an already-fired episode must stay at one fire, got %d", n)
		}
	})
}

// TestResetUnitNilWakeSeamConsumedQuietly: without the wake seam the episode
// still opens and consumes its single (suppressed) fire — no panic, no state
// wedge (case 28).
func TestResetUnitNilWakeSeamConsumedQuietly(t *testing.T) {
	mon := &wbMonitor{a: rnWalled(99.5, mBase.Add(-2*time.Hour))}
	l := rnLoop(mon, nil, func(c *config.Config) { c.ResetNotifyBufferMin = 0 })
	l.resetNotifyTick(mBase, l.Cfg, l.cacheA(t), true) // must not panic
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if ep := rnEpisode(l); ep.fired {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	ep := rnEpisode(l)
	if !ep.open || !ep.fired {
		t.Fatalf("the nil-seam fire must be consumed quietly (open=%v fired=%v)", ep.open, ep.fired)
	}
}

// TestResetUnitFleeSameRoundCancelsNotOpens: the hook runs AFTER the decision
// phase, so a flee-to-B switch in the same round must cancel (never open) the
// episode — the flip notify covers the moved sessions; the only wake carries
// the flip's SAVING order, never the reset order (cases 14, 32).
func TestResetUnitFleeSameRoundCancelsNotOpens(t *testing.T) {
	reset := mBase.Add(-2 * time.Hour) // past fireAt would fire immediately if wrongly opened
	mon := &wbMonitor{a: rnWalled(99.5, reset), b: wbSnap("B", mf(5), mf(12), mBase)}
	wk := &rnWaker{}
	fl := &rnFlipper{}
	l := rnLoop(mon, wk, nil)
	l.Qm = fl
	l.Autopilot = true // the flee gate is open: this round switches A→B

	r := l.Tick(context.Background(), mBase)
	if !r.Switched || r.Target != "B" {
		t.Fatalf("the walled-A round must flee to B: %+v", r)
	}
	if ep := rnEpisode(l); ep.open {
		t.Fatalf("the same-round flee must leave no episode open")
	}
	time.Sleep(300 * time.Millisecond) // settle any wrongly-armed schedule
	for _, c := range wk.snapshotAll() {
		if c[1] == "RESET" {
			t.Fatalf("the RESET order must never fire on a flee round, calls=%v", wk.snapshotAll())
		}
	}
	if n := len(fl.snapshot()); n != 1 || fl.snapshot()[0] != "SAVING" {
		t.Fatalf("the flee round flips SAVING exactly once, got %v", fl.snapshot())
	}
}

// TestResetUnitMachineNeverFlips: across a full episode lifecycle (open →
// scheduled fire → drop-back close → re-cross → early fire) the machine must
// add zero quota-mode writes — the flipper only ever sees what the policy
// engine itself ordered (case 31, behavioral half; the structural test is the
// source-scan half).
func TestResetUnitMachineNeverFlips(t *testing.T) {
	mon := &wbMonitor{a: rnWalled(99.5, mBase.Add(-2*time.Hour)), b: wbSnap("B", mf(5), mf(12), mBase)}
	wk := &rnWaker{}
	fl := &rnFlipper{}
	// Gap 0 is required: the second episode's fire lands milliseconds after
	// the first (both past-fireAt immediates), so the default 30-min min-gap
	// would suppress it and the lifecycle would never reach its second fire.
	l := rnLoop(mon, wk, func(c *config.Config) {
		c.ResetNotifyBufferMin = 0
		c.ResetNotifyMinGapMin = 0
	})
	l.Qm = fl

	l.resetNotifyTick(mBase, l.Cfg, l.cacheA(t), true) // open + immediate fire
	rnWaitWake(t, wk, 1)
	mon.a = wbSnap("A", mf(30), mf(10), mBase.Add(time.Second)) // drop-back → close
	l.resetNotifyTick(mBase.Add(time.Second), l.Cfg, l.cacheA(t), true)
	mon.a = rnWalled(99.5, mBase.Add(-3*time.Hour)) // re-cross → new episode → fire
	l.resetNotifyTick(mBase.Add(2*time.Second), l.Cfg, l.cacheA(t), true)
	rnWaitWake(t, wk, 2)
	if n := len(fl.snapshot()); n != 0 {
		t.Fatalf("the episode machine must never write the quota-mode file, flips=%v", fl.snapshot())
	}
}

// TestResetUnitScheduleMeasuresFromRoundNow pins the scheduling contract the
// 2026-11-21 acceptance run caught broken: the armed delay is measured from
// THIS round's decision instant (fireAt.Sub(now)), never from the real wall
// clock. The injected now (mBase, days behind real time) with a fireAt 400ms
// ahead must produce a real ~400ms delay — a time.Until-based schedule would
// read a hugely negative duration and fire at detection (case 44).
func TestResetUnitScheduleMeasuresFromRoundNow(t *testing.T) {
	mon := &wbMonitor{a: rnWalled(99.5, mBase.Add(400*time.Millisecond))}
	wk := &rnWaker{}
	l := rnLoop(mon, wk, func(c *config.Config) { c.ResetNotifyBufferMin = 0 })
	l.resetNotifyTick(mBase, l.Cfg, l.cacheA(t), true) // armed for +400ms from mBase
	time.Sleep(150 * time.Millisecond)
	if n := wk.count(); n != 0 {
		t.Fatalf("the schedule must wait out the 400ms round-instant delay, %d wake(s) already landed", n)
	}
	rnWaitWake(t, wk, 1)
}

// TestResetUnitFireAtFallbackAnchorsOnTickNow pins the fallback anchor's last
// link: a nil FiveHourReset AND a zero QueriedAt anchor on the TICK's decision
// instant (resetFireAt's anchor.IsZero branch — the sample alone cannot
// provide an anchor, case 21).
func TestResetUnitFireAtFallbackAnchorsOnTickNow(t *testing.T) {
	s := &domain.QuotaSnapshot{ProfileID: "A", FiveHourPct: mf(99.5)} // QueriedAt zero
	now := mBase.Add(time.Hour)
	fireAt, resetAt := resetFireAt(now, s, 2)
	want := now.Add(5 * time.Hour).Add(2 * time.Minute)
	if !fireAt.Equal(want) {
		t.Fatalf("fallback fireAt (zero QueriedAt) = %v, want tick-now+5h+buffer = %v", fireAt, want)
	}
	if resetAt != nil {
		t.Fatalf("fallback episode must carry resetAt == nil, got %v", resetAt)
	}
}

// TestResetUnitSameResetTable pins sameReset's anchor comparison, including
// the fallback-vs-fallback rule (both nil counts as the SAME anchor, so two
// fallback rounds never re-anchor), case 22.
func TestResetUnitSameResetTable(t *testing.T) {
	a := mBase.Add(time.Hour)
	b := mBase.Add(2 * time.Hour)
	cases := []struct {
		name     string
		stored   *time.Time
		reported *time.Time
		want     bool
	}{
		{"both nil (two fallback episodes)", nil, nil, true},
		{"stored nil, reported set", nil, &a, false},
		{"stored set, reported nil", &a, nil, false},
		{"equal instants", &a, &a, true},
		{"different instants", &a, &b, false},
	}
	for _, tc := range cases {
		if got := sameReset(tc.stored, tc.reported); got != tc.want {
			t.Fatalf("%s: sameReset = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// TestResetUnitInWallRedetectAfterFiredInert: after the single fire, a fresh
// in-wall round with the SAME reset must leave the episode open, consumed and
// unarmed — no re-fire, no re-arm, no new timer (decision 2's idempotence
// half, unit level; case 14).
func TestResetUnitInWallRedetectAfterFiredInert(t *testing.T) {
	reset := mBase.Add(-2 * time.Hour)
	mon := &wbMonitor{a: rnWalled(99.5, reset)}
	wk := &rnWaker{}
	l := rnLoop(mon, wk, func(c *config.Config) { c.ResetNotifyBufferMin = 0 })
	l.resetNotifyTick(mBase, l.Cfg, l.cacheA(t), true) // past fireAt → immediate fire
	rnWaitWake(t, wk, 1)
	mon.a = rnWalled(99.5, reset) // same reset re-reported, fresh round
	l.resetNotifyTick(mBase.Add(time.Second), l.Cfg, l.cacheA(t), true)
	ep := rnEpisode(l)
	if !ep.open || !ep.fired || ep.timer != nil {
		t.Fatalf("the fired episode must stay open, consumed and unarmed (open=%v fired=%v timer=%v)", ep.open, ep.fired, ep.timer != nil)
	}
	time.Sleep(400 * time.Millisecond) // settle: a wrongly re-armed schedule would fire here
	if n := wk.count(); n != 1 {
		t.Fatalf("in-wall re-detection after the fire must not re-fire, wakes = %d", n)
	}
}

// TestResetUnitClosedEpisodeRoundsStayClosed: with NO open episode neither
// cancel condition may fire — a master-off round ends at the static gate and
// a leave-A round's cancel is a no-op that still lets the round continue —
// and nothing opens (cases 4, 5, 6).
func TestResetUnitClosedEpisodeRoundsStayClosed(t *testing.T) {
	walled := rnWalled(99.5, mBase.Add(-2*time.Hour)) // past fireAt: an open would fire immediately
	t.Run("master off", func(t *testing.T) {
		l := rnLoop(&wbMonitor{a: walled}, &rnWaker{}, nil)
		cfg := l.Cfg
		cfg.ResetNotify = false
		l.resetNotifyTick(mBase, cfg, l.cacheA(t), true)
		if ep := rnEpisode(l); ep.open {
			t.Fatalf("a master-off round must not open an episode")
		}
	})
	t.Run("leave A with nothing to cancel", func(t *testing.T) {
		l := rnLoop(&wbMonitor{a: walled}, &rnWaker{}, nil)
		l.mu.Lock()
		l.Current = "B"
		l.mu.Unlock()
		l.resetNotifyTick(mBase, l.Cfg, l.cacheA(t), true)
		if ep := rnEpisode(l); ep.open {
			t.Fatalf("a leave-A round on a closed episode must stay closed")
		}
	})
}

// --- small helpers over the recording fakes ---

// rnWaitWakeNever asserts that no wake lands within a bounded settle window.
func rnWaitWakeNever(t *testing.T, w *rnWaker) {
	t.Helper()
	time.Sleep(400 * time.Millisecond)
	if n := w.count(); n != 0 {
		t.Fatalf("expected zero wakes, got %d", n)
	}
}

// snapshotAll copies the recorded calls.
func (w *rnWaker) snapshotAll() [][2]string {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := make([][2]string, len(w.calls))
	copy(out, w.calls)
	return out
}

// snapshot copies the recorded modes.
func (f *rnFlipper) snapshot() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.modes...)
}

// cacheA runs one fresh query round so the white-box tests can hand the
// machine the Loop's own effective A sample (the Tick-shaped input).
func (l *Loop) cacheA(t *testing.T) *domain.QuotaSnapshot {
	t.Helper()
	_, snapA := l.query(context.Background(), "A")
	return snapA
}
