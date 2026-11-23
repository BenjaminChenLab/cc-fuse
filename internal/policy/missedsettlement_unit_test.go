package policy_test

// Unit tests (black box) for the missed-settlement guard branches the
// acceptance file does not pin (2026-11-23 spec D-A/D-B edge shapes):
// the D-A guard must also preempt the weekend roll at the exact-instant
// equality, the D-B guard must fire BEFORE the day-start reconstruction
// (a legacy envelope without DayStartAt) and at the inclusive equality,
// and the D-B guard must NOT leak into the cold branch — settling IS the
// settlement. Reuses the incident helpers from
// missedsettlement_acceptance_test.go (same package).
// Spec: docs/blueprints/2026-11-23-budget-missed-settlement-fix.md.

import (
	"testing"
	"time"

	"cc-fuse/internal/policy"
)

// D-A equality edge on a weekend: boundary == now on a Saturday with
// workdaysOnly — without the guard the snap would mint Mon 14:57 (the
// weekend roll), swallowing the settlement that must fire at this very
// instant. The guard must win over the weekend roll.
func TestSnapBoundaryOverdueEqualityWeekendNotRolled(t *testing.T) {
	satBoundary := time.Date(2026, 11, 21, 14, 57, 0, 0, time.Local) // Sat 14:57
	b := satBoundary
	got := policy.SnapBoundary(satBoundary, &b, &incidentAnchor, true)
	if got == nil || !got.Equal(satBoundary) {
		t.Fatalf("boundary == now on a weekend must be returned unchanged (never rolled to Mon), got %v", got)
	}
}

// D-B inclusive edge: the boundary is exactly now — the settlement fires at
// this instant (M7), so the recompute must refuse it too. The envelope is the
// LEGACY shape (no DayStartAt): the guard must precede the boundary−24h
// reconstruction, proving it does not depend on the day-start field.
func TestRefreshBudgetOverdueEqualityBoundaryRefuses(t *testing.T) {
	envelope := incidentEnvelope()
	envelope.DayStartAt = nil // legacy reconstruction shape
	got, ok := policy.RefreshBudget(policy.Input{
		Now:    incidentBoundary, // == the boundary, inclusive edge
		QuotaB: freshSnapshot("B", ptr(1), ptr(19.2), incidentBoundary),
		Budget: envelope,
		Cfg:    cfg95(),
	})
	if ok {
		t.Fatalf("boundary == now must refuse the recompute (ok=false expected)")
	}
	if got.CeilingToday == nil || *got.CeilingToday != incidentCeiling {
		t.Fatalf("the envelope must pass through untouched, ceiling %v", got.CeilingToday)
	}
	if got.DayStartAt != nil {
		t.Fatalf("ok=false must not seed a DayStartAt, got %v", got.DayStartAt)
	}
}

// D-B vs the cold branch: with NO baseline (DayStartWeekly nil) and an
// overdue boundary still persisted, RefreshBudget must NOT refuse — the cold
// branch calls the settlement, and settling IS the settlement (spec D-B keeps
// the cold path unguarded). The result equals the first-tick replay: baseline
// re-anchored to the fresh weekly 19.2, boundary moved to the next anchor
// instant, ceiling 38.15.
func TestRefreshBudgetColdBranchStillSettlesOverdueBoundary(t *testing.T) {
	envelope := incidentEnvelope()
	envelope.DayStartWeekly = nil // cold start: no settlement baseline yet
	got, ok := policy.RefreshBudget(policy.Input{
		Now:    incidentNow,
		QuotaB: freshSnapshot("B", ptr(1), ptr(19.2), incidentNow),
		Budget: envelope,
		Cfg:    cfg95(),
	})
	if !ok {
		t.Fatalf("the cold branch must settle despite the overdue boundary (ok=false means the D-B guard leaked)")
	}
	if got.DayStartWeekly == nil || *got.DayStartWeekly != 19.2 {
		t.Fatalf("the cold settlement must re-anchor the baseline to 19.2, got %v", got.DayStartWeekly)
	}
	if got.DayBoundary == nil || !got.DayBoundary.Equal(wantSettledBoundary) {
		t.Fatalf("the settled boundary must land on Tue 14:57:23.998, got %v", got.DayBoundary)
	}
	if got.CeilingToday == nil || *got.CeilingToday != wantSettledCeiling {
		t.Fatalf("the cold settlement must land 38.15, got %v", got.CeilingToday)
	}
}
