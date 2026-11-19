// Package policy is the cc-fuse decision brain (design §6 rules P1–P8 and
// §7 pacing budget): a pure, IO-free function that turns (time, current
// identity, quota snapshots, persisted budget state, config, external
// events) into one switching decision. All time and data arrive via Input —
// the package never reads the clock, the network or the filesystem — so
// every rule is table-testable and T03's main loop stays a thin shell that
// persists whatever Decision.Budget carries back.
package policy

import (
	"fmt"
	"time"

	"cc-fuse/internal/config"
	"cc-fuse/internal/domain"
)

// Action is the decision verb produced by one Evaluate round.
type Action int

// Decision verbs (enum pin 2026-11-15, audit O1):
// ActionNone skips this round's decision only (stale/missing data, dead key,
// external adoption) — it is not a mode pause; ActionStay
// positively keeps the current account; ActionSwitch moves to Decision.Target;
// ActionPause stops autopilot (the only producer is the unknown-provider
// event). ActionResume is never produced here — resuming is a user action
// owned by T03, which flips AutopilotOn directly.
// 2026-11-17 directional flee gate: a paused autopilot (or 六日) is NOT a
// None producer anymore — it blocks only the A→B paths (P3, P6's switch-to-B)
// and surfaces as an explicit ActionStay; B→A returns (P4/P5) keep deciding.
const (
	ActionNone Action = iota
	ActionStay
	ActionSwitch
	ActionPause
	ActionResume
)

// ExtEvent is a provider-level change detected by T01's settings watcher and
// mapped into policy by T03 (policy only knows this shape).
type ExtEvent struct {
	// Kind: "" = no event; "provider_changed" = the outside world switched
	// onto an account cc-fuse recognizes (adopt silently); "unknown_provider"
	// = switched onto an unknown account (the only Pause path).
	Kind string
}

// ManualCmd is a user-issued immediate switch request from the tray.
type ManualCmd struct {
	// Target: "A" or "B" to force a switch this round; "" = no command.
	Target string
}

// BudgetState is the cross-day pacing bookkeeping for account B that T03
// persists between Evaluate rounds. Pointer fields are nil until the first
// settlement (cold start) fills them.
type BudgetState struct {
	// WeekAnchorTime is B's weekly reset instant (from nextResetTime); the
	// newest observed value is adopted at settlement. It anchors both the
	// daily settlement clock time and the remaining-days window.
	WeekAnchorTime *time.Time
	// DayBoundary is the instant the current budget day settles (same clock
	// time as the weekly anchor); Now >= DayBoundary triggers the next
	// settlement.
	DayBoundary *time.Time
	// DayStartWeekly is B's weekly% when the current budget day began — the
	// baseline the day's allowance is computed from. It is never relaxed
	// mid-day even if the weekly% drops.
	DayStartWeekly *float64
	// DayStartAt is the instant the current budget day began (2026-11-19
	// amendment 2 D6d). The recompute path used to reconstruct it as
	// DayBoundary−24h — an identity that breaks once the boundary is snapped
	// to the anchor clock (the snapped boundary's previous day is no longer
	// this budget day's start, so a second save would drift the ceiling).
	// Settlements and recompute landings persist the real start; legacy
	// state without the field falls back to the old reconstruction and
	// adopts it on the first landing.
	DayStartAt *time.Time
	// CeilingToday is the weekly% line B may burn up to today; reaching it
	// invokes the strict guard (P5) and removes B as a refuge candidate.
	CeilingToday *float64
	// WarnedFiveHour is the P7 one-shot warning latch: set when the current
	// account's 5h% crosses the warn line, cleared when it falls back below
	// (a re-crossing after a clear warns again).
	WarnedFiveHour bool
}

// Input is everything one decision round may look at. No IO: callers inject
// the current time, the latest snapshots and the persisted budget state.
type Input struct {
	Now            time.Time             // decision instant (injected, never read from the clock)
	Current        string                // "A" | "B" | "" — who settings.json points at right now
	QuotaA, QuotaB *domain.QuotaSnapshot // nil = no data for that side
	Budget         BudgetState           // persisted pacing state (read)
	Cfg            config.Config         // every threshold is user-settable
	AutopilotOn    bool                  // false = flee-to-B blocked (directional gate); B→A returns still decide; manual always works
	External       ExtEvent              // settings-watch event (empty = none)
	Manual         ManualCmd             // tray command (empty = none)
}

// Decision is one round's outcome. Action/Target/Reason/QuotaMode drive the
// switcher and the notifier; Budget, when non-nil, is the state the caller
// must persist (a settlement and/or a P7 flag change); Stale suspends
// decisions; WarnFired asks for the one-shot warning toast. Settled marks
// that THIS round actually ran a §7 settlement (not merely a warn-latch
// carry) — the app clears its budget-recompute latch only on settled rounds
// (a settlement is the authoritative envelope; a warn-only copy may be stale
// and must not end the self-heal retry).
type Decision struct {
	Action    Action       // Stay | SwitchTo(Target) | Pause | None
	Target    string       // "A"/"B" when Action == ActionSwitch
	Reason    string       // human-readable, carries the triggering numbers
	QuotaMode string       // "RACING"|"SAVING"|"" — the mode line to flip on switch
	Budget    *BudgetState // non-nil = persist this budget state
	Stale     bool         // data too old/missing → caller suspends deciding
	WarnFired bool         // P7 one-shot warning for the current account
	Settled   bool         // a §7 settlement ran this round (Budget is authoritative)
}

// Evaluate decides one round. Priority follows design §6 numbering with the
// 2026-11-15 pins: P1 manual beats everything (including the staleness gate,
// and it flips QuotaMode symmetrically with automatic switches, PD3); the
// external-event adoption/pause is environment truth about who is running;
// P2 gates on data quality (also while the flee gate holds); the §7
// settlement is bookkeeping independent of the action and keeps running while
// paused (2026-11-17: P5 needs the ceiling); P3–P6 pick the action
// (money-before-time: a budget-exhausted B loses refuge candidacy; the
// directional flee gate blocks every switch-to-B path while B→A decides on);
// P7 latches the one-shot warning onto Stay rounds only.
func Evaluate(in Input) Decision {
	// P1 手動最優先:不因任何自動規則或資料狀態被取消。
	if d, ok := manualDecision(in); ok {
		return d
	}
	// P8 外部事件:未知供應商 → Pause;認得的帳號 → 靜默採納(None)。
	if d, ok := externalDecision(in.External); ok {
		return d
	}
	// P2 資料品質閘:缺失/過期/當前帳號金鑰失效 → 本輪不決策(避難封鎖下照常)。
	if d, gated := staleGate(in); gated {
		return d
	}
	// §7 結算記帳:與動作解耦,先行完成;當日規則評估吃(可能已更新的)天花板。
	budget := in.Budget
	var budgetOut *BudgetState
	settleSkipped := false
	settled := false
	if settlementDue(in) {
		if nb, ok := settleBudget(in); ok {
			budget = nb
			budgetOut = &budget
			settled = true
		} else {
			settleSkipped = true
		}
	}
	// P3–P6:動作決策;P7:預警旗標(僅疊在非切換輪)。
	d := decideAction(in, budget)
	d.Settled = settled
	budgetOut = applyWarn(in, budget, budgetOut, &d)
	if budgetOut != nil {
		d.Budget = budgetOut
	}
	// 結算到期但 B 資料無效 → 明確告訴呼叫端這輪沒結算(審計 M9)。
	if settleSkipped {
		d.Reason += ";已到預算日界但 B 週% 無效,無法結算"
	}
	return d
}

// P1 manual switch: always honored, Reason marks 手動, and the QuotaMode
// flips symmetrically with automatic rules (→B SAVING, →A RACING, PD3).
func manualDecision(in Input) (Decision, bool) {
	t := in.Manual.Target
	if t != "A" && t != "B" {
		return Decision{}, false
	}
	mode := modeFor(t)
	return Decision{
		Action: ActionSwitch, Target: t, QuotaMode: mode,
		Reason: fmt.Sprintf("手動指令:切換到 %s(%s)", t, mode),
	}, true
}

// P8 external events: an unknown provider means settings.json points
// somewhere cc-fuse cannot vouch for → pause autopilot; a recognized
// provider change is adopted silently (T03 re-reads Current; this round
// takes no automatic action).
func externalDecision(e ExtEvent) (Decision, bool) {
	switch e.Kind {
	case "unknown_provider":
		return Decision{Action: ActionPause, Reason: "外部切到未知供應商,暫停自動切 B 以待人工確認"}, true
	case "provider_changed":
		return Decision{Action: ActionNone, Reason: "外部已切到認得的帳號,靜默採納,本輪不自動決策"}, true
	}
	return Decision{}, false
}

// modeFor maps a target account to the quota-mode line it implies:
// B (self-paid refuge) = SAVING, A (team shared, always preferred) = RACING.
func modeFor(target string) string {
	if target == "B" {
		return "SAVING"
	}
	return "RACING"
}

// P2 data-quality gate: any side missing or older than StaleAfterMin, or the
// CURRENT account answering 401/403 (dead key), suspends this round's
// decision. A dead key on the non-current side does not gate (card: 僅當前帳號).
func staleGate(in Input) (Decision, bool) {
	if in.QuotaA == nil || in.QuotaB == nil {
		return gateDecision(in, "額度資料缺失,暫停本輪自動決策"), true
	}
	limit := time.Duration(in.Cfg.StaleAfterMin) * time.Minute
	if in.Now.Sub(in.QuotaA.QueriedAt) > limit || in.Now.Sub(in.QuotaB.QueriedAt) > limit {
		return gateDecision(in, "額度資料過期,暫停本輪自動決策"), true
	}
	if cur := currentSnap(in); cur != nil && (cur.HTTPStatus == 401 || cur.HTTPStatus == 403) {
		return gateDecision(in, fmt.Sprintf("當前帳號(%s)金鑰失效(HTTP %d),暫停自動決策", in.Current, cur.HTTPStatus)), true
	}
	return Decision{}, false
}

// gateDecision builds the P2 outcome; when a budget settlement falls due on
// this same round it says so explicitly, so the caller knows bookkeeping is
// being skipped along with the decision (審計 M9).
func gateDecision(in Input, why string) Decision {
	d := Decision{Action: ActionNone, Stale: true, Reason: why}
	if settlementDue(in) {
		d.Reason += ";已到預算日界但資料無效,無法結算"
	}
	return d
}

// currentSnap returns the snapshot of whoever settings.json points at now.
func currentSnap(in Input) *domain.QuotaSnapshot {
	switch in.Current {
	case "A":
		return in.QuotaA
	case "B":
		return in.QuotaB
	}
	return nil
}

// settlementDue reports whether the §7 daily settlement must run this round:
// a cold start (no persisted boundary) settles immediately; afterwards the
// trigger is Now >= DayBoundary, inclusive at the exact instant (審計 M7).
func settlementDue(in Input) bool {
	if in.Budget.DayBoundary == nil {
		return true
	}
	return !in.Now.Before(*in.Budget.DayBoundary)
}

// hardWallPct is the absolute weekly wall (used%) the ceiling may never
// cross, even when the daily floor pushes the pace past the weekly budget
// total (audit A1: the floor may eat into headroom, but never past 100).
const hardWallPct = 100.0

// settleBudget runs the §7 daily settlement. It fires when Now crossed the
// persisted day boundary (or on cold start) and B carries a usable weekly%.
// Rules: the next boundary sticks to the weekly anchor's clock time (D2
// 2026-11-19; the DayBoundary+24h / midnight chains are only the no-anchor
// fallbacks); daysLeft counts [today, reset day] inclusive (D1); DayStartWeekly
// re-anchors to the CURRENT B weekly% (a mid-day drop never relaxes today's
// ceiling — the boundary has not moved); allowance =
// max((weeklyTotal−dayStart)/daysLeft, minDaily); ceiling =
// min(dayStart+allowance, hardWallPct); the newest observed weekly reset is
// adopted as the anchor; the P7 latch carries over.
// Returns ok=false when B's weekly% is unavailable (caller leaves Budget
// untouched and flags the skipped settlement).
func settleBudget(in Input) (BudgetState, bool) {
	b := in.QuotaB
	if b == nil || b.WeeklyPct == nil {
		return BudgetState{}, false
	}
	anchor := adoptAnchor(in, b)
	next := nextBoundary(in)
	days := daysLeftInWindow(in.Now, anchor, in.Cfg.Budget.WorkdaysOnly)
	dayStart := *b.WeeklyPct
	allowance := (in.Cfg.Budget.WeeklyTotalPct - dayStart) / float64(days)
	if allowance < in.Cfg.Budget.MinDailyAllowancePct {
		allowance = in.Cfg.Budget.MinDailyAllowancePct
	}
	ceiling := dayStart + allowance
	if ceiling > hardWallPct {
		ceiling = hardWallPct
	}
	dayStartAt := in.Now // D6d:預算日起點持久化(ref 與日界解耦,存檔不漂移)
	return BudgetState{
		WeekAnchorTime: anchor,
		DayBoundary:    next,
		DayStartWeekly: &dayStart,
		DayStartAt:     &dayStartAt,
		CeilingToday:   &ceiling,
		WarnedFiveHour: in.Budget.WarnedFiveHour,
	}, true
}

// adoptAnchor takes B's newest weekly reset as the budget-week anchor when it
// is fresher than the persisted one (anchor adoption happens at settlement).
func adoptAnchor(in Input, b *domain.QuotaSnapshot) *time.Time {
	cur := in.Budget.WeekAnchorTime
	if b.WeeklyReset != nil && (cur == nil || b.WeeklyReset.After(*cur)) {
		t := *b.WeeklyReset
		return &t
	}
	return cur
}

// nextBoundary computes the instant the NEXT settlement falls due (2026-11-19
// D2, anchor-first): when the weekly anchor is known the day chain sticks to
// the anchor's clock time — today at that clock, rolled to tomorrow once the
// instant is past — so a stale midnight chain can never pin the settlement
// clock at 00:00 forever. Anchor unknown keeps the old fallbacks: old
// boundary +24h (blind chain), else today's midnight. The shared tail rolls
// the candidate forward once when already past, inclusive at the exact
// instant (audit M7).
func nextBoundary(in Input) *time.Time {
	var b time.Time
	if in.Budget.WeekAnchorTime != nil {
		// D2 錨點優先:結算時刻貼回週重置鐘點;00:00 只是不知錨點的備案。
		b = atClock(in.Now, *in.Budget.WeekAnchorTime)
	} else if in.Budget.DayBoundary != nil {
		b = in.Budget.DayBoundary.Add(24 * time.Hour)
	} else {
		b = midnight(in.Now)
	}
	if !b.After(in.Now) {
		b = b.Add(24 * time.Hour)
	}
	return &b
}

// daysLeftInWindow counts the budget days between today and the next weekly
// anchor date INCLUSIVE ([today, anchorDate] calendar-day loop — 2026-11-19
// D1: the reset day is itself a budget day, its quota covers the morning up
// to the weekly reset instant, so the last pre-reset day no longer swallows
// the whole remainder), keeping only workdays when WorkdaysOnly is set, and
// never returning less than 1. An anchor at or before Now rolls forward in
// 7-day weeks to the NEXT reset (settlement at/after the weekly reset still
// paces the remaining days); a missing anchor falls back to the design's
// 7-day blind split.
func daysLeftInWindow(now time.Time, anchor *time.Time, workdaysOnly bool) int {
	if anchor == nil {
		return 7
	}
	a := *anchor
	for !a.After(now) {
		a = a.Add(7 * 24 * time.Hour)
	}
	end := midnight(a)
	count := 0
	for d := midnight(now); !d.After(end); d = d.AddDate(0, 0, 1) {
		if workdaysOnly && (d.Weekday() == time.Saturday || d.Weekday() == time.Sunday) {
			continue
		}
		count++
	}
	if count < 1 {
		count = 1
	}
	return count
}

// midnight returns the local midnight of t's calendar day.
func midnight(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
}

// atClock puts ref's clock time onto t's calendar date.
func atClock(t time.Time, ref time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(),
		ref.Hour(), ref.Minute(), ref.Second(), ref.Nanosecond(), t.Location())
}

// SnapBoundary re-derives the next settlement instant from the anchor clock
// (2026-11-19 D2 rule, amendment 2 D6a): persisted state can outlive the rule
// that produced it — a boundary minted by the old midnight chain keeps the
// card's until-prefix and the settlement clock pinned at 00:00 long after the
// anchor is known. Snap applies the anchor-first rule on demand (RefreshBudget
// landing, boot load): today at the anchor's clock, rolled to tomorrow once
// the instant is past (inclusive at the exact instant, audit M7). Anchor or
// boundary missing → the boundary unchanged (nil stays nil: the anchorless
// fallback chain and the baseline-without-boundary guard K1(d) stay as they
// are — a missing boundary is never minted from nothing).
func SnapBoundary(now time.Time, boundary, anchor *time.Time) *time.Time {
	if anchor == nil || boundary == nil {
		// 無錨點 → 備案鏈原樣;無日界 → 回傳 nil,不憑空造日界(K1(d) 精神)。
		return boundary
	}
	b := atClock(now, *anchor)
	if !b.After(now) {
		b = b.Add(24 * time.Hour)
	}
	return &b
}

// RefreshBudget recomputes TODAY's ceiling immediately after the budget
// settings were saved (2026-11-16 plan R4) instead of waiting for the next
// daily settlement. Returns (state, ok); ok=false means "not computable now"
// — the caller keeps the incoming state untouched and should retry later
// (the app latches budgetDirty). Two paths, per the folded §2.4:
//
//   - Cold start (DayStartWeekly nil — no settlement yet today): requires the
//     B snapshot present, carrying a weekly%, and FRESH — QueriedAt within
//     StaleAfterMin, the same gate the engine itself applies before deciding
//     (user decision 2026-11-16 Q2: a stale number must never anchor the
//     whole day's baseline). Fresh → a full settleBudget envelope.
//   - Recompute (a day-start baseline exists): days counted from the CURRENT
//     budget day's start, ref = DayBoundary−24h (settlement always sets
//     DayBoundary; with an anchor its clock time equals the weekly-reset
//     clock — the 15:00→15:00 semantics — while an anchorless blind chain
//     falls back to midnight; ref reproduces whatever the settlement used).
//     DayBoundary is fixed within a budget day, so the same config saved at
//     any moment of the day yields the identical ceiling — the C7 midnight
//     drift is dead, and the recompute never relaxes the settled ceiling.
//     The formula reads only the persisted baseline + config + anchor; no B
//     snapshot needed. Only CeilingToday changes — baseline, boundary, anchor
//     and the warn latch all stay (日中不放寬).
//
// Guard: a hand-edited state.json can produce baseline-without-boundary
// (normal writers always set both; re-audit K1(d)) — treated as not
// computable rather than guessing a ref.
func RefreshBudget(in Input) (BudgetState, bool) {
	b := in.Budget
	if b.DayStartWeekly == nil {
		q := in.QuotaB
		stale := q == nil || q.WeeklyPct == nil || q.QueriedAt.IsZero() ||
			in.Now.Sub(q.QueriedAt) > time.Duration(in.Cfg.StaleAfterMin)*time.Minute
		if stale {
			return b, false
		}
		return settleBudget(in)
	}
	if b.DayBoundary == nil {
		return b, false
	}
	// D6d(2026-11-19):預算日起點優先取持久化的 DayStartAt(與日界解耦,
	// 日界貼回錨點鐘點後「日界−24h」不再恆等於本預算日起點);舊檔缺欄才
	// 用舊演算法重建,並在本次落帳補寫(下一次存檔起不再漂移)。
	ref := b.DayBoundary.Add(-24 * time.Hour)
	if b.DayStartAt != nil {
		ref = *b.DayStartAt
	}
	days := daysLeftInWindow(ref, b.WeekAnchorTime, in.Cfg.Budget.WorkdaysOnly)
	allowance := (in.Cfg.Budget.WeeklyTotalPct - *b.DayStartWeekly) / float64(days)
	if allowance < in.Cfg.Budget.MinDailyAllowancePct {
		allowance = in.Cfg.Budget.MinDailyAllowancePct
	}
	ceiling := *b.DayStartWeekly + allowance
	if ceiling > hardWallPct {
		ceiling = hardWallPct
	}
	b.CeilingToday = &ceiling
	if b.DayStartAt == nil {
		b.DayStartAt = &ref
	}
	return b, true
}

// decideAction applies the switching rules P3–P6 for the current account.
func decideAction(in Input, budget BudgetState) Decision {
	switch in.Current {
	case "A":
		return decideFromA(in, budget)
	case "B":
		return decideFromB(in, budget)
	}
	return idleStay()
}

// decideFromA: P3 flee to B when A crossed a switch line and B is a usable
// refuge; otherwise P6 arbitration when A is hot but B is not usable. The
// directional flee gate (2026-11-17) blocks both switch-to-B paths while it
// holds (paused autopilot, or 六日不自動切 B) and leaves an explicit blocked
// stay on A — B→A returns are decided in decideFromB regardless of the gate.
func decideFromA(in Input, budget BudgetState) Decision {
	hot, why := aHot(in)
	if !hot {
		return idleStay()
	}
	if block, whyBlock := fleeBlocked(in); block {
		return Decision{
			Action: ActionStay,
			Reason: fmt.Sprintf("%s;%s → 留守 A(封鎖)", why, whyBlock),
		}
	}
	if bAvailable(in, budget) {
		return Decision{
			Action: ActionSwitch, Target: "B", QuotaMode: modeFor("B"),
			Reason: why + ",B 可用 → 切到 B 避難(SAVING)",
		}
	}
	return arbitrateBothDown(in, budget, why)
}

// decideFromB: P4 return once A is back in the safe zone (both windows,
// inclusive), else P5 strict budget guard: B at today's ceiling returns to A
// even when A has not recovered — 寧停等不超支. P5 applies only while
// cfg.Budget.EnforceDailyCeiling is on (2026-11-16 plan R2: toggle off =
// the ceiling never affects switching; B simply stays).
func decideFromB(in Input, budget BudgetState) Decision {
	if aRecovered(in) {
		a := in.QuotaA
		reason := fmt.Sprintf("A 已回落到安全區(5h %.1f%% ≤ %.1f", *a.FiveHourPct, in.Cfg.RecoveryThreshold5h)
		if a.WeeklyPct != nil {
			reason += fmt.Sprintf("、週 %.1f%% ≤ %.1f", *a.WeeklyPct, in.Cfg.RecoveryThresholdWeekly)
		} else {
			reason += "、無週窗口(不受週限)"
		}
		reason += ")→ 切回 A(RACING)"
		return Decision{
			Action: ActionSwitch, Target: "A", QuotaMode: modeFor("A"),
			Reason: reason,
		}
	}
	b := in.QuotaB
	if in.Cfg.Budget.EnforceDailyCeiling &&
		budget.CeilingToday != nil && b.WeeklyPct != nil && *b.WeeklyPct >= *budget.CeilingToday {
		return Decision{
			Action: ActionSwitch, Target: "A", QuotaMode: modeFor("A"),
			Reason: fmt.Sprintf("B 今日額度用完(週 %.1f%% ≥ 天花板 %.1f%%),寧停等不超支 → 切回 A", *b.WeeklyPct, *budget.CeilingToday),
		}
	}
	return idleStay()
}

// fleeBlocked is the directional A→B gate (2026-11-17 spec): it holds while
// the autopilot is paused, or while noFleeOnWeekend is set AND the injected
// Now (the loop's local machine time) falls on a Saturday/Sunday. It never
// blocks B→A — P4/P5 in decideFromB evaluate normally — and manual commands
// (P1) are decided before any gate.
func fleeBlocked(in Input) (bool, string) {
	if !in.AutopilotOn {
		return true, "暫停自動切 B"
	}
	if in.Cfg.NoFleeOnWeekend {
		if wd := in.Now.Weekday(); wd == time.Saturday || wd == time.Sunday {
			return true, "六日不自動切 B"
		}
	}
	return false, ""
}

// aHot reports whether A crossed either switch line (5h or weekly, inclusive
// at the threshold) and returns the human-readable trigger text.
func aHot(in Input) (bool, string) {
	a := in.QuotaA
	switch {
	case a.FiveHourPct != nil && *a.FiveHourPct >= in.Cfg.SwitchThreshold5h:
		return true, fmt.Sprintf("A 5h %.1f%% ≥ 切換線 %.1f%%", *a.FiveHourPct, in.Cfg.SwitchThreshold5h)
	case a.WeeklyPct != nil && *a.WeeklyPct >= in.Cfg.SwitchThresholdWeekly:
		return true, fmt.Sprintf("A 週 %.1f%% ≥ 切換線 %.1f%%", *a.WeeklyPct, in.Cfg.SwitchThresholdWeekly)
	}
	return false, ""
}

// aRecovered reports whether A is back in the safe zone: the 5h window at or
// below its recovery line, plus the weekly window at/below its line WHEN that
// window exists at all. A plan without a weekly bucket (WeeklyPct == nil,
// field-tested 2026-11-15 on default GLM plans) has no weekly limit — the
// weekly condition is then vacuously satisfied and recovery rides on 5h
// alone (P4 hysteresis: switch at 90, return at 30/60 — no flapping).
func aRecovered(in Input) bool {
	a := in.QuotaA
	return a.FiveHourPct != nil && *a.FiveHourPct <= in.Cfg.RecoveryThreshold5h &&
		(a.WeeklyPct == nil || *a.WeeklyPct <= in.Cfg.RecoveryThresholdWeekly)
}

// bAvailable is the refuge definition: B's weekly% below today's ceiling
// (unknown ceiling counts as available) AND B's 5h% below the switch line.
// The ceiling term applies only while budget enforcement is on (plan R2) —
// with the toggle off, an over-ceiling B still serves as a refuge.
func bAvailable(in Input, budget BudgetState) bool {
	b := in.QuotaB
	if b.FiveHourPct != nil && *b.FiveHourPct >= in.Cfg.SwitchThreshold5h {
		return false
	}
	if in.Cfg.Budget.EnforceDailyCeiling &&
		budget.CeilingToday != nil && b.WeeklyPct != nil && *b.WeeklyPct >= *budget.CeilingToday {
		return false
	}
	return true
}

// budgetBlocked reports whether B has burned through today's ceiling and so
// lost refuge candidacy until the next settlement (arbitration pin
// 2026-11-15: switching in early would burn money past the line — money
// before time). Inert while budget enforcement is off (plan R2: the toggle
// removes the ceiling from every switching decision; P6 then arbitrates on
// the 5h resets alone).
func budgetBlocked(cfg config.Config, budget BudgetState, b *domain.QuotaSnapshot) bool {
	if !cfg.Budget.EnforceDailyCeiling {
		return false
	}
	return budget.CeilingToday != nil && b.WeeklyPct != nil && *b.WeeklyPct >= *budget.CeilingToday
}

// arbitrateBothDown is P6: A is hot and B is not a usable refuge. A
// budget-exhausted B loses candidacy outright (stay on A until the next
// settlement); otherwise the earlier 5h reset wins, and a tie or any missing
// reset info keeps A.
func arbitrateBothDown(in Input, budget BudgetState, why string) Decision {
	b := in.QuotaB
	if budgetBlocked(in.Cfg, budget, b) {
		return Decision{
			Action: ActionStay,
			Reason: fmt.Sprintf("%s;B 今日額度用完(週 %.1f%% ≥ 天花板 %.1f%%),喪失候選資格,留 A 等下次結算",
				why, *b.WeeklyPct, *budget.CeilingToday),
		}
	}
	aReset, bReset := in.QuotaA.FiveHourReset, b.FiveHourReset
	if aReset != nil && bReset != nil && bReset.Before(*aReset) {
		return Decision{
			Action: ActionSwitch, Target: "B", QuotaMode: modeFor("B"),
			Reason: fmt.Sprintf("%s;B 不可用但先恢復(5h 重置 %s 早於 A %s)→ 切 B 等重置(SAVING)",
				why, bReset.Format("15:04"), aReset.Format("15:04")),
		}
	}
	return Decision{Action: ActionStay, Reason: why + ";兩邊都不可用,A 先恢復/平手或缺重置資訊 → 留 A 等恢復"}
}

// idleStay is the healthy-idle verdict (enum pin: 健康閒置 → Stay).
func idleStay() Decision {
	return Decision{Action: ActionStay, Reason: "額度健康,維持現狀"}
}

// applyWarn folds P7 into the round: the CURRENT account's 5h% crossing the
// warn line (inclusive) fires the one-shot warning and latches
// WarnedFiveHour into the persisted state; falling back below the line
// clears the latch so a later re-crossing warns again. It never rides on a
// switch or pause round (the card allows it to stack on Stay only). The
// returned pointer is the state to persist — the incoming emitted state when
// a settlement already produced one, else a fresh copy of the input state.
func applyWarn(in Input, budget BudgetState, emitted *BudgetState, d *Decision) *BudgetState {
	if d.Action == ActionSwitch || d.Action == ActionPause {
		return emitted
	}
	cur := currentSnap(in)
	if cur == nil || cur.FiveHourPct == nil {
		return emitted
	}
	five, warn := *cur.FiveHourPct, in.Cfg.WarnThreshold
	switch {
	case !budget.WarnedFiveHour && five >= warn:
		emitted = ensureEmitted(budget, emitted)
		emitted.WarnedFiveHour = true
		d.WarnFired = true
		d.Reason += fmt.Sprintf(";預警:當前帳號(%s)5h %.1f%% 跨過預警線 %.1f%%", in.Current, five, warn)
	case budget.WarnedFiveHour && five < warn:
		emitted = ensureEmitted(budget, emitted)
		emitted.WarnedFiveHour = false
	}
	return emitted
}

// ensureEmitted lazily materializes the budget state to persist when only
// the warning latch changed (no settlement this round).
func ensureEmitted(budget BudgetState, emitted *BudgetState) *BudgetState {
	if emitted != nil {
		return emitted
	}
	st := budget
	return &st
}
