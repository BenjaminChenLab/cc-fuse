package policy

// Unit tests — 2026-11-19 amendment 2 (D6a): SnapBoundary re-derives a stale
// boundary to the anchor clock (today at the anchor's clock, rolled +24h once
// past), so the card's until-prefix and the settlement clock equal the calc's
// cutting time. Spec: docs/blueprints/2026-11-19-budget-day-inclusive-fix.md,
// amendment 2 + its review-driven refinement (A9–A11, A13). Universe from the
// 2026-11-19 (Thursday) user field report.

import (
	"testing"
	"time"

	"cc-fuse/internal/config"
	"cc-fuse/internal/domain"
)

var (
	snapThuMorning  = time.Date(2026, 11, 19, 12, 21, 0, 0, time.Local) // Thu 12:21 (field-report moment, before the anchor clock)
	snapThuEvening  = time.Date(2026, 11, 19, 16, 0, 0, 0, time.Local)  // Thu 16:00 (past the anchor clock)
	snapFri1600     = time.Date(2026, 11, 20, 16, 0, 0, 0, time.Local)  // Fri 16:00 (evening, weekend ahead)
	snapSat1000     = time.Date(2026, 11, 21, 10, 0, 0, 0, time.Local)  // Sat 10:00 (weekend save)
	snapFriMidnight = time.Date(2026, 11, 20, 0, 0, 0, 0, time.Local)   // the legacy midnight-chain boundary
	snapSatMidnight = time.Date(2026, 11, 21, 0, 0, 0, 0, time.Local)   // Sat 00:00 (weekend-chain legacy boundary)
	snapSunMidnight = time.Date(2026, 11, 22, 0, 0, 0, 0, time.Local)   // Sun 00:00 (weekend-chain legacy boundary)
	snapAnchor      = time.Date(2026, 11, 20, 14, 57, 0, 0, time.Local) // the observed Fri 14:57 weekly reset
)

// f returns a pointer to v — the snapshot builders take pointer fields.
func f(v float64) *float64 { return &v }

// A9 helper level: the anchor clock has not passed today → snap to today 14:57
// (no longer the stale Fri 00:00).
func TestSnapBoundarySameDay(t *testing.T) {
	got := SnapBoundary(snapThuMorning, &snapFriMidnight, &snapAnchor, true)
	want := time.Date(2026, 11, 19, 14, 57, 0, 0, time.Local)
	if got == nil || !got.Equal(want) {
		t.Fatalf("boundary must snap to today's anchor clock 14:57, got %v", got)
	}
}

// A10 helper level: the anchor clock has passed → roll +24h to tomorrow 14:57.
func TestSnapBoundaryRollsToTomorrow(t *testing.T) {
	got := SnapBoundary(snapThuEvening, &snapFriMidnight, &snapAnchor, true)
	want := time.Date(2026, 11, 20, 14, 57, 0, 0, time.Local)
	if got == nil || !got.Equal(want) {
		t.Fatalf("past the anchor clock must roll to tomorrow 14:57, got %v", got)
	}
}

// A11 helper level: no anchor → the boundary is returned unchanged (the
// fallback chain stays intact).
func TestSnapBoundaryNilAnchorKeepsBoundary(t *testing.T) {
	got := SnapBoundary(snapThuMorning, &snapFriMidnight, nil, true)
	if got == nil || !got.Equal(snapFriMidnight) {
		t.Fatalf("no anchor must return the boundary unchanged, got %v", got)
	}
}

// A13 helper level (review refinement): a missing boundary → return nil,
// never mint one from nothing (K1(d) spirit; the first draft minted an
// instant — corrected).
func TestSnapBoundaryNilBoundaryReturnsNil(t *testing.T) {
	if got := SnapBoundary(snapThuMorning, nil, &snapAnchor, true); got != nil {
		t.Fatalf("missing boundary must return nil (never mint), got %v", got)
	}
}

// A13 helper level: now exactly AT the anchor clock → inclusive +24h roll
// (audit M7 convention).
func TestSnapBoundaryAtExactInstantRolls(t *testing.T) {
	now := time.Date(2026, 11, 19, 14, 57, 0, 0, time.Local)
	got := SnapBoundary(now, &snapFriMidnight, &snapAnchor, true)
	want := time.Date(2026, 11, 20, 14, 57, 0, 0, time.Local)
	if got == nil || !got.Equal(want) {
		t.Fatalf("exactly at the anchor clock must roll to tomorrow 14:57, got %v", got)
	}
}

// A13 helper level: a different anchor clock (09:13) follows the same rule —
// guards against a hardcoded 14:57 slipping through. now is 08:00 (before
// 09:13) to pin the same-day branch, not the roll branch.
func TestSnapBoundaryDifferentClock(t *testing.T) {
	anchor0913 := time.Date(2026, 11, 20, 9, 13, 0, 0, time.Local)
	now := time.Date(2026, 11, 19, 8, 0, 0, 0, time.Local)
	got := SnapBoundary(now, &snapFriMidnight, &anchor0913, true)
	want := time.Date(2026, 11, 19, 9, 13, 0, 0, time.Local)
	if got == nil || !got.Equal(want) {
		t.Fatalf("boundary must snap to today's anchor clock 09:13, got %v", got)
	}
}

// Amendment 3 (review finding F2): the snap NEVER mints a weekend cut-off —
// a Fri-evening candidate rolls past Sat/Sun to Mon 14:57 (the Fri→Mon slice
// merges), and a Saturday save lands Monday too. workdaysOnly=false keeps the
// calendar behavior (weekend candidates are legal there).
// Missed-settlement amendment (2026-11-23 spec D-E): the property is pinned
// on FUTURE boundaries only — an overdue boundary is returned unchanged by
// the D-A guard and consumed by the settlement, never snapped (the incident's
// exact mechanism: an overdue Fri boundary swallowed by the weekend roll).
func TestSnapBoundaryWeekendMerge(t *testing.T) {
	// Fri 16:00 (after the anchor clock; boundary Sat 00:00 still future) →
	// Sat would be skipped → Mon 14:57.
	got := SnapBoundary(snapFri1600, &snapSatMidnight, &snapAnchor, true)
	want := time.Date(2026, 11, 23, 14, 57, 0, 0, time.Local)
	if got == nil || !got.Equal(want) {
		t.Fatalf("Fri evening must snap past the weekend to Mon 14:57, got %v", got)
	}
	// Sat 10:00 (boundary Sun 00:00 still future) → Mon 14:57.
	got = SnapBoundary(snapSat1000, &snapSunMidnight, &snapAnchor, true)
	if got == nil || !got.Equal(want) {
		t.Fatalf("Saturday save must snap to Mon 14:57, got %v", got)
	}
	// workdaysOnly=false → the weekend candidate is legal (Sat 14:57).
	got = SnapBoundary(snapSat1000, &snapSunMidnight, &snapAnchor, false)
	wantSat := time.Date(2026, 11, 21, 14, 57, 0, 0, time.Local)
	if got == nil || !got.Equal(wantSat) {
		t.Fatalf("calendar mode must keep the Sat candidate, got %v", got)
	}
}

// Self-heal pin (review finding): every settlement envelope must carry
// DayStartAt = the settlement instant — this is what makes the requirement's
// "permanent self-heal after the 14:57 settlement" claim real.
func TestSettleBudgetCarriesDayStartAt(t *testing.T) {
	in := Input{
		Now:    snapThuMorning,
		QuotaB: &domain.QuotaSnapshot{WeeklyPct: f(20), WeeklyReset: &snapAnchor, QueriedAt: snapThuMorning},
		Budget: BudgetState{},
		Cfg:    config.Default(),
	}
	nb, ok := settleBudget(in)
	if !ok {
		t.Fatal("settlement must run with a valid weekly%")
	}
	if nb.DayStartAt == nil || !nb.DayStartAt.Equal(snapThuMorning) {
		t.Fatalf("settlement envelope must carry DayStartAt = the settlement instant, got %v", nb.DayStartAt)
	}
}
