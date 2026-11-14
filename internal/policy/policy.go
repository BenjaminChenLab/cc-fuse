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
// autopilot off, external adoption) — it is not a mode pause; ActionStay
// positively keeps the current account; ActionSwitch moves to Decision.Target;
// ActionPause stops autopilot (the only producer is the unknown-provider
// event). ActionResume is never produced here — resuming is a user action
// owned by T03, which flips AutopilotOn directly.
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
	AutopilotOn    bool                  // false = no automatic switching (manual still works)
	External       ExtEvent              // settings-watch event (empty = none)
	Manual         ManualCmd             // tray command (empty = none)
}

// Decision is one round's outcome. Action/Target/Reason/QuotaMode drive the
// switcher and the notifier; Budget, when non-nil, is the state the caller
// must persist (a settlement and/or a P7 flag change); Stale suspends
// decisions; WarnFired asks for the one-shot warning toast.
type Decision struct {
	Action    Action       // Stay | SwitchTo(Target) | Pause | None
	Target    string       // "A"/"B" when Action == ActionSwitch
	Reason    string       // human-readable, carries the triggering numbers
	QuotaMode string       // "RACING"|"SAVING"|"" — the mode line to flip on switch
	Budget    *BudgetState // non-nil = persist this budget state
	Stale     bool         // data too old/missing → caller suspends deciding
	WarnFired bool         // P7 one-shot warning for the current account
}

// Evaluate decides one round. Priority follows design §6 numbering with the
// 2026-11-15 pins: P1 manual beats everything (including the staleness gate,
// and it flips QuotaMode symmetrically with automatic switches, PD3); the
// external-event adoption/pause is environment truth about who is running;
// autopilot off blocks all automatic action; P2 gates on data quality; the
// §7 settlement is bookkeeping independent of the action; P3–P6 pick the
// action (money-before-time: a budget-exhausted B loses refuge candidacy);
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
	// 自駕閘(枚舉釘死):關閉時本輪不做任何自動決策,非暫停模式。
	if !in.AutopilotOn {
		return Decision{Action: ActionNone, Reason: "自駕關閉,本輪不自動決策"}
	}
	// P2 資料品質閘:缺失/過期/當前帳號金鑰失效 → 本輪不決策。
	if d, gated := staleGate(in); gated {
		return d
	}
	// §7 結算記帳:與動作解耦,先行完成;當日規則評估吃(可能已更新的)天花板。
	budget := in.Budget
	var budgetOut *BudgetState
	settleSkipped := false
	if settlementDue(in) {
		if nb, ok := settleBudget(in); ok {
			budget = nb
			budgetOut = &budget
		} else {
			settleSkipped = true
		}
	}
	// P3–P6:動作決策;P7:預警旗標(僅疊在非切換輪)。
	d := decideAction(in, budget)
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
		return Decision{Action: ActionPause, Reason: "外部切到未知供應商,暫停自動切換以待人工確認"}, true
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
// Rules: the next boundary is old+24h (cold start: today at the anchor clock
// time, rolled to tomorrow when already past); DayStartWeekly re-anchors to
// the CURRENT B weekly% (a mid-day drop never relaxes today's ceiling — the
// boundary has not moved); allowance = max((weeklyTotal−dayStart)/daysLeft,
// minDaily); ceiling = min(dayStart+allowance, hardWallPct); the newest
// observed weekly reset is adopted as the anchor; the P7 latch carries over.
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
	return BudgetState{
		WeekAnchorTime: anchor,
		DayBoundary:    next,
		DayStartWeekly: &dayStart,
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

// nextBoundary computes the instant the NEXT settlement falls due: old
// boundary +24h, or on cold start today at the anchor clock time (midnight
// without an anchor), rolled forward once when that instant is already past.
func nextBoundary(in Input) *time.Time {
	var b time.Time
	if in.Budget.DayBoundary != nil {
		b = in.Budget.DayBoundary.Add(24 * time.Hour)
	} else if in.Budget.WeekAnchorTime != nil {
		b = atClock(in.Now, *in.Budget.WeekAnchorTime)
	} else {
		b = midnight(in.Now)
	}
	if !b.After(in.Now) {
		b = b.Add(24 * time.Hour)
	}
	return &b
}

// daysLeftInWindow counts the budget days between today and the next weekly
// anchor date ([today, anchorDate), calendar-day loop), keeping only
// workdays when WorkdaysOnly is set, and never returning less than 1. An
// anchor at or before Now rolls forward in 7-day weeks to the NEXT reset
// (settlement after the weekly reset still paces the remaining days); a
// missing anchor falls back to the design's 7-day blind split.
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
	for d := midnight(now); d.Before(end); d = d.AddDate(0, 0, 1) {
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
// refuge; otherwise P6 arbitration when A is hot but B is not usable.
func decideFromA(in Input, budget BudgetState) Decision {
	hot, why := aHot(in)
	if !hot {
		return idleStay()
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
// even when A has not recovered — 寧停等不超支.
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
	if budget.CeilingToday != nil && b.WeeklyPct != nil && *b.WeeklyPct >= *budget.CeilingToday {
		return Decision{
			Action: ActionSwitch, Target: "A", QuotaMode: modeFor("A"),
			Reason: fmt.Sprintf("B 今日額度用完(週 %.1f%% ≥ 天花板 %.1f%%),寧停等不超支 → 切回 A", *b.WeeklyPct, *budget.CeilingToday),
		}
	}
	return idleStay()
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
func bAvailable(in Input, budget BudgetState) bool {
	b := in.QuotaB
	if b.FiveHourPct != nil && *b.FiveHourPct >= in.Cfg.SwitchThreshold5h {
		return false
	}
	if budget.CeilingToday != nil && b.WeeklyPct != nil && *b.WeeklyPct >= *budget.CeilingToday {
		return false
	}
	return true
}

// budgetBlocked reports whether B has burned through today's ceiling and so
// lost refuge candidacy until the next settlement (arbitration pin
// 2026-11-15: switching in early would burn money past the line — money
// before time).
func budgetBlocked(budget BudgetState, b *domain.QuotaSnapshot) bool {
	return budget.CeilingToday != nil && b.WeeklyPct != nil && *b.WeeklyPct >= *budget.CeilingToday
}

// arbitrateBothDown is P6: A is hot and B is not a usable refuge. A
// budget-exhausted B loses candidacy outright (stay on A until the next
// settlement); otherwise the earlier 5h reset wins, and a tie or any missing
// reset info keeps A.
func arbitrateBothDown(in Input, budget BudgetState, why string) Decision {
	b := in.QuotaB
	if budgetBlocked(budget, b) {
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
