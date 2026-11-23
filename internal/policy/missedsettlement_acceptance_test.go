package policy_test

// Acceptance tests (black box) — the missed-settlement bug (2026-11-23 field
// incident): the app was OFF when the budget day boundary (Mon 14:57) passed;
// at boot the boundary was snapped FORWARD to the next anchor clock
// (Tue 14:57) without the missed settlement ever running, so the day-1 slice
// (baseline 0, ceiling 19.0, days=5) silently extended through Tuesday and
// the P5 guard forced B back to a dead A. Requirement:
// docs/blueprints/2026-11-23-budget-missed-settlement-fix.md.
//
// Settled semantics under test:
//   S1  SnapBoundary must never move an already-overdue boundary past now —
//       an overdue boundary is the settlement trigger and is consumed by
//       settling, never by snapping. Future boundaries keep the existing
//       snap-to-anchor-clock correction (contrast pins included).
//   S2  RefreshBudget's recompute path must refuse to recompute a dead day:
//       an overdue boundary means "wait for the settlement" (ok=false).
//   S3  Evaluate must settle on the first round when the boundary is overdue,
//       re-anchoring the baseline to the CURRENT weekly% and recounting the
//       days (the engine-level pin: settle-at-overdue already works and must
//       survive the fix untouched).

import (
	"math"
	"testing"
	"time"

	"cc-fuse/internal/config"
	"cc-fuse/internal/domain"
	"cc-fuse/internal/policy"
)

// freshSnapshot builds a usable (HTTP 200, no error) quota sample queried at
// queriedAt — fresh enough to pass the P2 staleness gate.
func freshSnapshot(id string, five, weekly *float64, queriedAt time.Time) *domain.QuotaSnapshot {
	return &domain.QuotaSnapshot{ProfileID: id, FiveHourPct: five, WeeklyPct: weekly, QueriedAt: queriedAt}
}

// The incident timeline (all local time):
//   Fri 11-20 14:57:23.998  weekly window reset; day-1 settlement minutes later
//                           (baseline 0, days=5 → ceiling 19.0, boundary Mon 14:57)
//   Mon 11-23 14:57         the budget day boundary — app was OFF, missed
//   Mon 11-23 16:37         the app boots; the incident's decision moment
var (
	incidentBoundary = time.Date(2026, 11, 23, 14, 57, 0, 0, time.Local)      // Mon, overdue at boot
	incidentAnchor   = time.Date(2026, 11, 27, 14, 57, 23, 998000000, time.Local) // Fri, the next weekly reset
	incidentDayStart = time.Date(2026, 11, 20, 14, 58, 24, 0, time.Local)     // Fri, day-1 real start
	incidentNow      = time.Date(2026, 11, 23, 16, 37, 0, 0, time.Local)      // Mon, boot moment
	incidentCeiling  = 19.0                                                   // the stale day-1 ceiling

	// After the overdue settlement: baseline = 19.2 (current weekly%),
	// days = 4 (Tue, Wed, Thu + the reset-day Friday slice), so
	// ceiling = 19.2 + (95 − 19.2)/4 = 38.15, boundary = Tue 14:57:23.998.
	wantSettledBoundary = time.Date(2026, 11, 24, 14, 57, 23, 998000000, time.Local)
	wantSettledCeiling  = 38.15
)

// incidentEnvelope builds the exact state.json payload the field incident
// fossilized: the day-1 envelope plus a boundary already past (as it was on
// disk before the boot snap made it durable).
func incidentEnvelope() policy.BudgetState {
	return policy.BudgetState{
		WeekAnchorTime: &incidentAnchor,
		DayBoundary:    &incidentBoundary,
		DayStartWeekly: ptr(0),
		DayStartAt:     &incidentDayStart,
		CeilingToday:   &incidentCeiling,
	}
}

func cfg95() config.Config {
	c := config.Default()
	c.Budget.WeeklyTotalPct = 95 // the user's configured weekly budget (the 19.0 = 95/5 slice)
	return c
}

func ptr(v float64) *float64 { return &v }

// --- S1: SnapBoundary and the overdue boundary ---

// S1 core: a boundary already past must come back UNCHANGED — snapping it
// forward converts an overdue settlement into a future one and the day slice
// never rotates (the incident's exact mechanism).
func TestSnapBoundaryKeepsOverdueBoundary(t *testing.T) {
	got := policy.SnapBoundary(incidentNow, &incidentBoundary, &incidentAnchor, true)
	if got == nil {
		t.Fatalf("overdue boundary must be returned, not nil")
	}
	if !got.Equal(incidentBoundary) {
		t.Fatalf("overdue boundary must stay Mon 14:57 for the settlement to fire, got %v", got)
	}
}

// S1 property: for ANY (boundary ≤ now) shape the result is the input
// boundary itself — the guard is unconditional, not a special case for the
// incident's dates.
func TestSnapBoundaryNeverSwallowsOverdue(t *testing.T) {
	anchorMon10 := time.Date(2026, 11, 23, 10, 0, 0, 0, time.Local)
	cases := []struct {
		name        string
		boundary    time.Time
		now         time.Time
		anchor      *time.Time
		workdays    bool
		description string
	}{
		{"minutes late", incidentBoundary, incidentNow, &incidentAnchor, true, "Mon 14:57 at Mon 16:37"},
		{"days late", time.Date(2026, 11, 20, 14, 57, 0, 0, time.Local), incidentNow, &incidentAnchor, true, "Fri 14:57 at Mon 16:37 (3 missed days)"},
		{"exact instant", incidentBoundary, incidentBoundary, &incidentAnchor, true, "boundary == now (inclusive M7) must not roll"},
		{"weekend overdue", time.Date(2026, 11, 20, 14, 57, 0, 0, time.Local), time.Date(2026, 11, 21, 10, 0, 0, 0, time.Local), &incidentAnchor, true, "Fri boundary at Sat 10:00"},
		{"no anchor", incidentBoundary, incidentNow, nil, true, "anchorless fallback chain, overdue"},
		{"workdays off", incidentBoundary, incidentNow, &anchorMon10, false, "seven-day blind split, overdue"},
	}
	for _, tc := range cases {
		b := tc.boundary
		got := policy.SnapBoundary(tc.now, &b, tc.anchor, tc.workdays)
		if got == nil || !got.Equal(tc.boundary) {
			t.Fatalf("%s (%s): overdue boundary must stay %v, got %v", tc.name, tc.description, tc.boundary, got)
		}
	}
}

// S1 contrast pin: the ORIGINAL snap purpose survives — a stale future
// boundary (the legacy midnight chain) still snaps to the anchor clock.
func TestSnapBoundaryFutureBoundaryStillSnaps(t *testing.T) {
	staleMidnight := time.Date(2026, 11, 20, 0, 0, 0, 0, time.Local) // Fri 00:00 legacy chain
	now := time.Date(2026, 11, 19, 12, 21, 0, 0, time.Local)         // Thu 12:21, boundary still future
	got := policy.SnapBoundary(now, &staleMidnight, &incidentAnchor, true)
	if got == nil {
		t.Fatalf("future boundary must still snap, got nil")
	}
	want := time.Date(2026, 11, 19, 14, 57, 23, 998000000, time.Local)
	if !got.Equal(want) {
		t.Fatalf("future boundary must snap to Thu 14:57:23.998, got %v", got)
	}
}

// --- S2: RefreshBudget refuses to recompute a dead day ---

// The recompute path derives days from the persisted DayStartAt (Friday) —
// with an overdue boundary that re-mints the DEAD day's ceiling. It must
// report not-computable and leave the envelope untouched; the first Tick's
// settlement is the authoritative update. QuotaB is FRESH on purpose: the
// refusal must be attributable to the overdue boundary alone, never to a
// missing/stale B sample (a fix that merely extends the cold path's data
// gate would otherwise pass vacuously). The envelope passthrough is pinned
// field by field — a fix that nils the baseline (silently turning the next
// round into a cold settle) must go red here.
func TestRefreshBudgetOverdueBoundaryWaitsForSettlement(t *testing.T) {
	envelope := incidentEnvelope()
	envelope.WarnedFiveHour = true // non-default on purpose: passthrough must keep it
	got, ok := policy.RefreshBudget(policy.Input{
		Now:    incidentNow,
		QuotaB: freshSnapshot("B", ptr(1), ptr(19.2), incidentNow),
		Budget: envelope,
		Cfg:    cfg95(),
	})
	if ok {
		t.Fatalf("overdue boundary must not recompute the dead day (ok=false expected)")
	}
	if got.CeilingToday == nil || *got.CeilingToday != incidentCeiling {
		t.Fatalf("the envelope must pass through untouched, ceiling %v", got.CeilingToday)
	}
	if got.DayBoundary == nil || !got.DayBoundary.Equal(incidentBoundary) {
		t.Fatalf("the boundary must stay the overdue Mon 14:57, got %v", got.DayBoundary)
	}
	if got.DayStartWeekly == nil || *got.DayStartWeekly != 0 {
		t.Fatalf("the baseline must pass through untouched, got %v", got.DayStartWeekly)
	}
	if got.DayStartAt == nil || !got.DayStartAt.Equal(incidentDayStart) {
		t.Fatalf("the day start must pass through untouched, got %v", got.DayStartAt)
	}
	if got.WeekAnchorTime == nil || !got.WeekAnchorTime.Equal(incidentAnchor) {
		t.Fatalf("the anchor must pass through untouched, got %v", got.WeekAnchorTime)
	}
	if !got.WarnedFiveHour {
		t.Fatalf("the warn latch must pass through untouched, got %v", got.WarnedFiveHour)
	}
}

// --- S3: Evaluate settles an overdue boundary on the first round ---

// Engine-level pin (green before the fix on purpose): with the boundary left
// overdue, the very first Evaluate must run the settlement — new baseline =
// current weekly%, days recounted — and P5 must NOT force B back to A (the
// refreshed ceiling 38.15 is far above the current weekly 19.2).
func TestEvaluateSettlesOverdueBoundaryFirstTick(t *testing.T) {
	in := policy.Input{
		Now:     incidentNow,
		Current: "B",
		QuotaA:  freshSnapshot("A", ptr(100), nil, incidentNow),
		QuotaB: func() *domain.QuotaSnapshot {
			s := freshSnapshot("B", ptr(1), ptr(19.2), incidentNow)
			s.WeeklyReset = &incidentAnchor
			return s
		}(),
		Budget:      incidentEnvelope(),
		Cfg:         cfg95(),
		AutopilotOn: true,
	}
	d := policy.Evaluate(in)

	if !d.Settled || d.Budget == nil {
		t.Fatalf("an overdue boundary must settle on the first round, settled=%v budget=%v", d.Settled, d.Budget)
	}
	if d.Budget.CeilingToday == nil || math.Abs(*d.Budget.CeilingToday-wantSettledCeiling) > 1e-9 {
		t.Fatalf("ceiling must re-anchor to 19.2+(95−19.2)/4=38.15, got %v", d.Budget.CeilingToday)
	}
	if d.Budget.DayBoundary == nil || !d.Budget.DayBoundary.Equal(wantSettledBoundary) {
		t.Fatalf("boundary must land on Tue 14:57:23.998, got %v", d.Budget.DayBoundary)
	}
	if d.Budget.DayStartWeekly == nil || *d.Budget.DayStartWeekly != 19.2 {
		t.Fatalf("the baseline must re-anchor to the current weekly 19.2, got %v", d.Budget.DayStartWeekly)
	}
	if d.Action != policy.ActionStay {
		t.Fatalf("B still has budget under the refreshed ceiling — must stay on B, got action %v (%s)", d.Action, d.Reason)
	}
}
