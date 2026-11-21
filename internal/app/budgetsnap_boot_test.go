package app_test

// Acceptance tests (black box) — 2026-11-19 amendment 2 (D6b/D6c) plus the
// review-driven refinements: both the refresh landing and the boot load snap
// a stale boundary to the anchor clock, the budget-day start is seeded from
// the ORIGINAL boundary BEFORE the snap (order-hole: restart → first Save
// drifted the ceiling to 74.3), and an existing DayStartAt is never
// overwritten. The card's until-prefix must equal the calculation's cutting
// time. Spec: docs/blueprints/2026-11-19-budget-day-inclusive-fix.md,
// amendment 2 (A9–A11) and its 補強 block.

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"cc-fuse/internal/app"
	"cc-fuse/internal/config"
	"cc-fuse/internal/policy"
	"cc-fuse/internal/store"
)

var (
	snapNow      = time.Date(2026, 11, 19, 12, 21, 0, 0, time.Local) // Thu 12:21 (field-report moment)
	snapPast     = time.Date(2026, 11, 19, 16, 0, 0, 0, time.Local)  // Thu 16:00 (past the anchor clock)
	snapStaleFri = time.Date(2026, 11, 20, 0, 0, 0, 0, time.Local)   // the legacy midnight-chain boundary
	snapAnchor   = time.Date(2026, 11, 20, 14, 57, 0, 0, time.Local) // the observed Fri 14:57 weekly reset
)

// seedStaleChain builds the field-reported legacy envelope (baseline 64,
// boundary Fri 00:00, anchor 14:57, old ceiling 95) and sets the config
// weekly total to 95 (the field-report scenario).
func seedStaleChain(l *app.Loop) {
	cfg := config.Default()
	cfg.Budget.WeeklyTotalPct = 95
	l.UpdateConfig(cfg)
	l.SeedBudget(policy.BudgetState{
		DayStartWeekly: f(64), DayBoundary: &snapStaleFri,
		WeekAnchorTime: &snapAnchor, CeilingToday: f(95),
	})
}

// A9 behavioral: live replay — the legacy midnight chain (baseline 64,
// boundary Fri 00:00, anchor 14:57, old ceiling 95) + a save → ceiling 79.5
// and the envelope boundary (the until data source) snaps to today 14:57.
func TestRefreshBudgetLandsSnappedBoundary(t *testing.T) {
	l := newLoop(&fakeMonitor{}, swNoop(), flNoop(), ntNoop())
	seedStaleChain(l)
	l.RefreshBudget(snapNow)

	st := l.Status()
	if st.CeilingToday == nil || *st.CeilingToday != 79.5 {
		t.Fatalf("ceiling must be 64+(95−64)/2=79.5, got %v", st.CeilingToday)
	}
	want := time.Date(2026, 11, 19, 14, 57, 0, 0, time.Local)
	if st.Until == nil || !st.Until.Equal(want) {
		t.Fatalf("until must snap to today 14:57 (no longer the stale Fri 00:00), got %v", st.Until)
	}
}

// A10 behavioral: past the anchor clock → the boundary rolls to tomorrow
// 14:57; the ceiling still computes from the original budget-day start (79.5).
func TestRefreshBudgetSnapsPastClockToTomorrow(t *testing.T) {
	l := newLoop(&fakeMonitor{}, swNoop(), flNoop(), ntNoop())
	seedStaleChain(l)
	l.RefreshBudget(snapPast)

	st := l.Status()
	if st.CeilingToday == nil || *st.CeilingToday != 79.5 {
		t.Fatalf("ceiling computes from the original day start, must stay 79.5, got %v", st.CeilingToday)
	}
	want := time.Date(2026, 11, 20, 14, 57, 0, 0, time.Local)
	if st.Until == nil || !st.Until.Equal(want) {
		t.Fatalf("past 14:57 the until must roll to Fri 14:57, got %v", st.Until)
	}
}

// A12 behavioral: two saves within the same budget day → the ceiling stays
// 79.5 and the boundary stays at the snapped value (the snap must not push
// the boundary−24h day-start reconstruction onto the previous day).
func TestRefreshBudgetSecondSaveSameDayNoDrift(t *testing.T) {
	l := newLoop(&fakeMonitor{}, swNoop(), flNoop(), ntNoop())
	seedStaleChain(l)
	l.RefreshBudget(snapNow) // first save: 79.5 + boundary snapped to today 14:57

	l.RefreshBudget(snapNow.Add(30 * time.Minute)) // second save, same budget day
	st := l.Status()
	if st.CeilingToday == nil || *st.CeilingToday != 79.5 {
		t.Fatalf("the second save must not drift the ceiling, must stay 79.5, got %v", *st.CeilingToday)
	}
	want := time.Date(2026, 11, 19, 14, 57, 0, 0, time.Local)
	if st.Until == nil || !st.Until.Equal(want) {
		t.Fatalf("until must stay at today 14:57 after the second save, got %v", st.Until)
	}
}

// A13 behavioral: a different anchor clock (18:40, later than now) snaps by
// the same rule — guards against a hardcoded 14:57 slipping through.
func TestRefreshBudgetSnapsToAnchorClockNotLiteral(t *testing.T) {
	anchor1840 := time.Date(2026, 11, 20, 18, 40, 0, 0, time.Local)
	l := newLoop(&fakeMonitor{}, swNoop(), flNoop(), ntNoop())
	cfg := config.Default()
	cfg.Budget.WeeklyTotalPct = 95
	l.UpdateConfig(cfg)
	l.SeedBudget(policy.BudgetState{
		DayStartWeekly: f(64), DayBoundary: &snapStaleFri,
		WeekAnchorTime: &anchor1840, CeilingToday: f(95),
	})
	l.RefreshBudget(snapNow)

	st := l.Status()
	want := time.Date(2026, 11, 19, 18, 40, 0, 0, time.Local)
	if st.Until == nil || !st.Until.Equal(want) {
		t.Fatalf("until must snap to the anchor clock today 18:40 (not a literal 14:57), got %v", st.Until)
	}
}

// A11 boot level (D6c): a state.json with no anchor → the boundary unchanged
// (the fallback chain stays intact).
func TestLoadBudgetKeepsBoundaryWithoutAnchor(t *testing.T) {
	raw, err := json.Marshal(policy.BudgetState{DayStartWeekly: f(64), DayBoundary: &snapStaleFri})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "state.json")
	if err := store.SaveState(path, store.State{Budget: raw}); err != nil {
		t.Fatal(err)
	}

	b, err := app.LoadBudget(path, true)
	if err != nil {
		t.Fatal(err)
	}
	if b.DayBoundary == nil || !b.DayBoundary.Equal(snapStaleFri) {
		t.Fatalf("no anchor must leave the boundary untouched, got %v", b.DayBoundary)
	}
}

// A11 boot level: anchor present, boundary missing → the boundary stays nil
// (never guessed; K1(d)).
func TestLoadBudgetKeepsNilBoundaryWithAnchor(t *testing.T) {
	raw, err := json.Marshal(policy.BudgetState{DayStartWeekly: f(64), WeekAnchorTime: &snapAnchor})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "state.json")
	if err := store.SaveState(path, store.State{Budget: raw}); err != nil {
		t.Fatal(err)
	}

	b, err := app.LoadBudget(path, true)
	if err != nil {
		t.Fatal(err)
	}
	if b.DayBoundary != nil {
		t.Fatalf("a missing boundary must never be minted at load, got %v", b.DayBoundary)
	}
	if b.DayStartAt != nil {
		t.Fatalf("with the boundary missing, DayStartAt must stay nil too (never guessed), got %v", b.DayStartAt)
	}
}

// A14 boot level (review refinement): a file that already carries DayStartAt
// (the normal shape after a save + restart) must load it unchanged —
// overwriting it with boundary−24h would swap the real start for the previous
// day and re-introduce the 74.3 drift on the next restart's first save.
// wantSnappedBoundary mirrors the full snap rule independently (review
// refinement: the mirrored derivation must include the weekend skip — a bare
// atClock+roll would go red on weekend CI runs). Disclosed mirror: a clock
// seam would be the durable non-mirrored alternative.
func wantSnappedBoundary(now time.Time) time.Time {
	want := time.Date(now.Year(), now.Month(), now.Day(), 14, 57, 0, 0, time.Local)
	for want.Weekday() == time.Saturday || want.Weekday() == time.Sunday {
		want = want.Add(24 * time.Hour)
	}
	if !want.After(now) {
		want = want.Add(24 * time.Hour)
		for want.Weekday() == time.Saturday || want.Weekday() == time.Sunday {
			want = want.Add(24 * time.Hour)
		}
	}
	return want
}

func TestLoadBudgetKeepsExistingDayStart(t *testing.T) {
	realStart := time.Date(2026, 11, 19, 0, 0, 0, 0, time.Local) // the landed real day start
	snapped := time.Date(2026, 11, 19, 14, 57, 0, 0, time.Local) // the already-snapped boundary
	raw, err := json.Marshal(policy.BudgetState{
		DayStartWeekly: f(64), DayBoundary: &snapped,
		DayStartAt: &realStart, WeekAnchorTime: &snapAnchor,
	})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "state.json")
	if err := store.SaveState(path, store.State{Budget: raw}); err != nil {
		t.Fatal(err)
	}

	b, err := app.LoadBudget(path, true)
	if err != nil {
		t.Fatal(err)
	}
	if b.DayStartAt == nil || !b.DayStartAt.Equal(realStart) {
		t.Fatalf("an existing DayStartAt must never be overwritten, got %v", b.DayStartAt)
	}
	// The snap rule re-applied independently (idempotent within the same day;
	// rolls once the day's anchor clock has passed, weekends skipped —
	// rule-derived, not the input echoed back).
	wantBoundary := wantSnappedBoundary(time.Now())
	if b.DayBoundary == nil || !b.DayBoundary.Equal(wantBoundary) {
		t.Fatalf("a boundary already on the anchor clock must snap idempotently, want %v got %v", wantBoundary, b.DayBoundary)
	}
}

// Boot level (review refinement 2a): no baseline (never settled) → no
// day-start seeding (the cold path is written whole by the settlement).
func TestLoadBudgetNoBaselineNoDayStart(t *testing.T) {
	raw, err := json.Marshal(policy.BudgetState{DayBoundary: &snapStaleFri, WeekAnchorTime: &snapAnchor})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "state.json")
	if err := store.SaveState(path, store.State{Budget: raw}); err != nil {
		t.Fatal(err)
	}

	b, err := app.LoadBudget(path, true)
	if err != nil {
		t.Fatal(err)
	}
	if b.DayStartAt != nil {
		t.Fatalf("no baseline must mean no DayStartAt seeding, got %v", b.DayStartAt)
	}
}

// Order-hole (2026-11-19 user field report: the first Save after a restart
// drifted the ceiling to 74.3): a legacy file without DayStartAt must be
// seeded from the ORIGINAL boundary (Thu 00:00) BEFORE the boundary is
// snapped (Thu 14:57) — the reversed order reconstructs the start from the
// snapped boundary (Wed 14:57), over-counts the days, and the first save
// lands 74.3 (64+31/3) instead of 79.5. Spec: requirement doc amendment 2,
// D6c refinement. Chain tail pins the first-save math end to end.
func TestLoadBudgetSeedsDayStartBeforeSnap(t *testing.T) {
	raw, err := json.Marshal(policy.BudgetState{
		DayStartWeekly: f(64), DayBoundary: &snapStaleFri, WeekAnchorTime: &snapAnchor,
	})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "state.json")
	if err := store.SaveState(path, store.State{Budget: raw}); err != nil {
		t.Fatal(err)
	}

	b, err := app.LoadBudget(path, true)
	if err != nil {
		t.Fatal(err)
	}
	wantStart := time.Date(2026, 11, 19, 0, 0, 0, 0, time.Local) // Thu 00:00 (original boundary − 24h)
	if b.DayStartAt == nil || !b.DayStartAt.Equal(wantStart) {
		t.Fatalf("load must seed the day start Thu 00:00 BEFORE snapping, got %v", b.DayStartAt)
	}
	now := time.Now()
	wantBoundary := wantSnappedBoundary(now)
	if b.DayBoundary == nil || !b.DayBoundary.Equal(wantBoundary) {
		t.Fatalf("the boundary must still snap to the anchor clock, want %v got %v", wantBoundary, b.DayBoundary)
	}

	// Chain tail: the first save recomputed from this loaded state must land
	// 79.5 (64+31/2), never the Wednesday-derived 74.3.
	nb, ok := policy.RefreshBudget(policy.Input{
		Now: time.Now(), Budget: b,
		Cfg: func() config.Config {
			cfg := config.Default()
			cfg.Budget.WeeklyTotalPct = 95
			return cfg
		}(),
	})
	if !ok || nb.CeilingToday == nil || *nb.CeilingToday != 79.5 {
		t.Fatalf("the first save must recompute 79.5 (no drift), ok=%v got %v", ok, nb.CeilingToday)
	}
}

// Boot recompute (2026-11-19 user field report: every restart showed the
// stale settled ceiling 83.5 until a manual Save bumped it to 95 — the boot
// load fixed the envelope's boundary/day-start but never recomputed the
// ceiling; the Save path was the only recompute trigger). The fix: main.go
// marks the budget dirty right after seeding a settled envelope, so the
// FIRST poll lands the freshly computed ceiling with no Save. The seeded
// envelope mirrors the field state: settled Thu 14:58 (baseline 78, stale
// ceiling 83.5), boundary/anchor Fri 14:57; polling at Thu 15:28 → the last
// slice before the reset takes the full remainder → 78+(95−78)/1 = 95.
func TestBootDirtyLatchRecomputesCeilingOnFirstTick(t *testing.T) {
	l := newLoop(&fakeMonitor{}, swNoop(), flNoop(), ntNoop())
	cfg := config.Default()
	cfg.Budget.WeeklyTotalPct = 95
	l.UpdateConfig(cfg)
	staleStart := time.Date(2026, 11, 19, 14, 58, 0, 0, time.Local) // the stale pre-fix settlement instant
	l.SeedBudget(policy.BudgetState{
		DayStartWeekly: f(78), DayStartAt: &staleStart,
		DayBoundary: &snapAnchor, WeekAnchorTime: &snapAnchor,
		CeilingToday: f(83.5),
	})
	l.MarkBudgetDirty() // the boot wiring under test (compile-RED until implemented)

	bootPoll := time.Date(2026, 11, 19, 15, 28, 0, 0, time.Local) // Thu 15:28, the field-report poll moment
	l.Tick(context.Background(), bootPoll)

	st := l.Status()
	if st.CeilingToday == nil || *st.CeilingToday != 95 {
		t.Fatalf("the first poll after boot must recompute 78+(95−78)/1=95 without a Save, got %v", st.CeilingToday)
	}
	if st.Until == nil || !st.Until.Equal(snapAnchor) {
		t.Fatalf("the until chip must stay at the Fri 14:57 cut-off, got %v", st.Until)
	}
}
