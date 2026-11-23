package app_test

// Boot-level acceptance tests (black box) — the missed-settlement incident
// (2026-11-23): the app was OFF when the Mon 14:57 budget boundary passed;
// LoadBudget's snap pushed the boundary to Tue 14:57 and the boot dirty
// recompute re-minted the DEAD day-1 ceiling (19.0) from the stale Friday
// baseline — so B showed 尚可 0.0pt with 81% of the weekly pool left, and the
// P5 guard forced B back to a dead (100% 5h) A. Requirement:
// docs/blueprints/2026-11-23-budget-missed-settlement-fix.md.
//
// Boot semantics under test:
//   B1  LoadBudget must NOT move an already-overdue boundary forward (it is
//       the settlement trigger); future boundaries keep the D6c snap.
//   B2  The full boot wiring (load → seed → MarkBudgetDirty → first Tick)
//       must settle on the first poll: ceiling 19.2+(95−19.2)/4=38.15, the
//       until chip at Tue 14:57:23.998, the settlement persisted to
//       state.json, and NO forced return to A.
//
// LoadBudget takes an injected now (deterministic boot tests; main.go passes
// time.Now()) — the signature change is part of this requirement.

import (
	"context"
	"encoding/json"
	"math"
	"path/filepath"
	"testing"
	"time"

	"cc-fuse/internal/app"
	"cc-fuse/internal/config"
	"cc-fuse/internal/domain"
	"cc-fuse/internal/policy"
	"cc-fuse/internal/store"
)

// The incident timeline (local time) — mirrors the policy-level file.
var (
	bootBoundary = time.Date(2026, 11, 23, 14, 57, 0, 0, time.Local)          // Mon, overdue at boot
	bootAnchor   = time.Date(2026, 11, 27, 14, 57, 23, 998000000, time.Local) // Fri weekly reset
	bootDayStart = time.Date(2026, 11, 20, 14, 58, 24, 0, time.Local)         // Fri, day-1 real start
	bootNow      = time.Date(2026, 11, 23, 16, 37, 0, 0, time.Local)          // Mon, boot moment
)

// writeIncidentState persists the exact fossilized state.json of the
// incident: day-1 envelope (baseline 0, ceiling 19) + overdue boundary.
func writeIncidentState(t *testing.T) string {
	t.Helper()
	raw, err := json.Marshal(policy.BudgetState{
		WeekAnchorTime: &bootAnchor,
		DayBoundary:    &bootBoundary,
		DayStartWeekly: f(0),
		DayStartAt:     &bootDayStart,
		CeilingToday:   f(19),
	})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "state.json")
	if err := store.SaveState(path, store.State{Budget: raw}); err != nil {
		t.Fatal(err)
	}
	return path
}

// --- B1: the load keeps an overdue boundary ---

func TestLoadBudgetKeepsOverdueBoundary(t *testing.T) {
	path := writeIncidentState(t)

	b, err := app.LoadBudget(path, bootNow, true)
	if err != nil {
		t.Fatal(err)
	}
	if b.DayBoundary == nil || !b.DayBoundary.Equal(bootBoundary) {
		t.Fatalf("an overdue boundary must stay Mon 14:57 (the settlement trigger), got %v", b.DayBoundary)
	}
	if b.DayStartAt == nil || !b.DayStartAt.Equal(bootDayStart) {
		t.Fatalf("an existing DayStartAt must never be overwritten, got %v", b.DayStartAt)
	}
	if b.DayStartWeekly == nil || *b.DayStartWeekly != 0 {
		t.Fatalf("the day-1 baseline must pass through untouched, got %v", b.DayStartWeekly)
	}
	if b.CeilingToday == nil || *b.CeilingToday != 19 {
		t.Fatalf("the stale ceiling passes through unchanged at load, got %v", b.CeilingToday)
	}
}

// B1 contrast pin: the D6c snap survives for a FUTURE stale boundary (the
// legacy midnight chain) — the guard only ever protects overdue ones.
func TestLoadBudgetFutureBoundaryStillSnaps(t *testing.T) {
	staleMidnight := time.Date(2026, 11, 20, 0, 0, 0, 0, time.Local) // Fri 00:00 legacy chain
	anchorFri := time.Date(2026, 11, 20, 14, 57, 23, 998000000, time.Local)
	now := time.Date(2026, 11, 19, 12, 21, 0, 0, time.Local) // Thu 12:21, boundary future

	raw, err := json.Marshal(policy.BudgetState{
		DayStartWeekly: f(64), DayBoundary: &staleMidnight,
		WeekAnchorTime: &anchorFri, CeilingToday: f(95),
	})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "state.json")
	if err := store.SaveState(path, store.State{Budget: raw}); err != nil {
		t.Fatal(err)
	}

	b, err := app.LoadBudget(path, now, true)
	if err != nil {
		t.Fatal(err)
	}
	want := time.Date(2026, 11, 19, 14, 57, 23, 998000000, time.Local)
	if b.DayBoundary == nil || !b.DayBoundary.Equal(want) {
		t.Fatalf("a future stale boundary must still snap to Thu 14:57:23.998, got %v", b.DayBoundary)
	}
	wantStart := time.Date(2026, 11, 19, 0, 0, 0, 0, time.Local) // seeded from the ORIGINAL boundary − 24h
	if b.DayStartAt == nil || !b.DayStartAt.Equal(wantStart) {
		t.Fatalf("the day start must seed from the original boundary BEFORE the snap, got %v", b.DayStartAt)
	}
}

// --- B2: the full boot replay of the incident ---

// Boots on the incident's state.json at Mon 16:37 with B current (weekly
// 19.2, fresh) and A at 100% 5h (dead refuge). The first poll must settle
// (ceiling 38.15, until Tue 14:57:23.998), persist the settlement, and NOT
// switch back to A — under the bug this exact wiring switched to A (P5 saw
// the stale ceiling 19.0 ≤ weekly 19.2) and re-minted 19.0 via the dirty
// recompute.
func TestBootAfterMissedBoundarySettlesOnFirstTick(t *testing.T) {
	path := writeIncidentState(t)

	sw := &fakeSwitcher{}
	mon := &fakeMonitor{
		a: fresh("A", f(100), nil, bootNow),
		b: func() *domain.QuotaSnapshot {
			s := fresh("B", f(1), f(19.2), bootNow)
			s.WeeklyReset = &bootAnchor
			return s
		}(),
	}
	l := newLoop(mon, sw, flNoop(), ntNoop())
	cfg := cfg95Helper()
	l.UpdateConfig(cfg)
	l.StatePath = path // the loop persists to the SAME file it booted from

	seeded, err := app.LoadBudget(path, bootNow, true)
	if err != nil {
		t.Fatal(err)
	}
	l.SeedBudget(seeded)
	l.MarkBudgetDirty() // the real boot wiring (main.go)
	l.Current = "B"     // the user resumed on B over the weekend
	l.Autopilot = true

	r := l.Tick(context.Background(), bootNow)

	if !r.BudgetSaved {
		t.Fatalf("the catch-up settlement must reach state.json on this very round, BudgetSaved=false")
	}
	st := l.Status()
	if st.CeilingToday == nil || math.Abs(*st.CeilingToday-38.15) > 1e-9 {
		t.Fatalf("first poll must settle 19.2+(95−19.2)/4=38.15, got %v", st.CeilingToday)
	}
	wantUntil := time.Date(2026, 11, 24, 14, 57, 23, 998000000, time.Local)
	if st.Until == nil || !st.Until.Equal(wantUntil) {
		t.Fatalf("the until chip must move to Tue 14:57:23.998, got %v", st.Until)
	}
	if r.Switched || len(sw.applied) != 0 {
		t.Fatalf("B still has budget under the settled ceiling — must not return to dead A, switched=%v applied=%v", r.Switched, sw.applied)
	}

	// The settlement must reach state.json (§7: 重開機不歸零).
	b, err := app.LoadBudget(path, bootNow.Add(time.Minute), true)
	if err != nil {
		t.Fatal(err)
	}
	if b.CeilingToday == nil || math.Abs(*b.CeilingToday-38.15) > 1e-9 {
		t.Fatalf("the settled ceiling must persist to state.json, got %v", b.CeilingToday)
	}
	if b.DayBoundary == nil || !b.DayBoundary.Equal(wantUntil) {
		t.Fatalf("the persisted boundary must be Tue 14:57:23.998, got %v", b.DayBoundary)
	}
}

// cfg95Helper mirrors the user's budget config (weekly total 95; the default
// enforces the ceiling and counts workdays only).
func cfg95Helper() config.Config {
	c := config.Default()
	c.Budget.WeeklyTotalPct = 95
	return c
}
