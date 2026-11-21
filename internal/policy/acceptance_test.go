package policy_test

// 驗收測試(黑箱)——cc-fuse T02:政策引擎 P1–P8 + B 配速預算。
// 規格:docs/blueprints/tasks/T02-policy-engine.md(含 2026-11-15 修訂:Evaluate 入口、
// Cfg=config.Config、BudgetState.WarnedFiveHour、A1 夾牆 ceiling=min(x,100)、
// P6 候選資格仲裁(錢優於時間)、P7 旗標回落清除、動作枚舉釘死)+ 編碼審計補強
// (A1 配速七段鏈、A2/A3 P6 仲裁編碼、M1–M9、D2–D4)。

import (
	"math"
	"strings"
	"testing"
	"time"

	"cc-fuse/internal/config"
	"cc-fuse/internal/domain"
	"cc-fuse/internal/policy"
)

// --- 測試宇宙:2026-11(機器驗證:15=日、18=三、19=四、20=五、21=六、27=五),週錨=週五 15:00 ---

var (
	wed15   = time.Date(2026, 11, 18, 15, 0, 0, 0, time.Local)
	wed16   = wed15.Add(1 * time.Hour)
	wed10   = time.Date(2026, 11, 18, 10, 0, 0, 0, time.Local)
	thu15   = time.Date(2026, 11, 19, 15, 0, 0, 0, time.Local)
	fri14   = time.Date(2026, 11, 20, 14, 0, 0, 0, time.Local)
	fri16   = time.Date(2026, 11, 20, 16, 0, 0, 0, time.Local)
	sat15   = time.Date(2026, 11, 21, 15, 0, 0, 0, time.Local)
	nextFri = time.Date(2026, 11, 27, 15, 0, 0, 0, time.Local)
)

func anchorFri15() *time.Time { t := fri14.Add(1 * time.Hour); return &t } // 本週週五 15:00

func f(v float64) *float64 { return &v }

func derefP(v *float64) float64 {
	if v == nil {
		return -1
	}
	return *v
}

func snap(id string, five, weekly *float64, age time.Duration, at time.Time) *domain.QuotaSnapshot {
	return &domain.QuotaSnapshot{
		ProfileID: id, FiveHourPct: five, WeeklyPct: weekly,
		QueriedAt: at.Add(-age),
	}
}

type evalIn struct {
	now     time.Time
	current string
	a, b    *domain.QuotaSnapshot
	budget  policy.BudgetState
	autoplt bool
	ext     policy.ExtEvent
	manual  policy.ManualCmd
}

func evaluate(t *testing.T, in evalIn) policy.Decision {
	t.Helper()
	return policy.Evaluate(policy.Input{
		Now: in.now, Current: in.current, QuotaA: in.a, QuotaB: in.b,
		Budget: in.budget, Cfg: config.Default(), AutopilotOn: in.autoplt,
		External: in.ext, Manual: in.manual,
	})
}

func healthy(now time.Time) (a, b *domain.QuotaSnapshot) {
	return snap("A", f(10), f(10), 0, now), snap("B", f(5), f(12), 0, now)
}

const actionNone, actionStay, actionSwitch, actionPause = policy.ActionNone, policy.ActionStay, policy.ActionSwitch, policy.ActionPause

// --- P1:手動最優先(含 PD3:手動翻 mode;壓過 P2 過期閘 → 證明 P1>P2) ---

func TestP1ManualSwitchOverridesEverything(t *testing.T) {
	a, b := healthy(wed16)
	in := evalIn{now: wed16, current: "A", a: a, b: b, autoplt: true, manual: policy.ManualCmd{Target: "B"}}
	d := evaluate(t, in)
	if d.Action != actionSwitch || d.Target != "B" {
		t.Fatalf("manual B: %+v", d)
	}
	if !strings.Contains(d.Reason, "手動") {
		t.Fatalf("reason must mark manual: %q", d.Reason)
	}
	if d.QuotaMode != "SAVING" {
		t.Fatalf("manual→B must flip SAVING (PD3): %q", d.QuotaMode)
	}

	in2 := evalIn{now: wed16, current: "B", a: a, b: b, autoplt: true, manual: policy.ManualCmd{Target: "A"}}
	d2 := evaluate(t, in2)
	if d2.Action != actionSwitch || d2.Target != "A" || d2.QuotaMode != "RACING" {
		t.Fatalf("manual→A: %+v", d2)
	}

	// 無任何額度資料(過期閘情境)手動照切 → P1 嚴格高於 P2。
	in3 := evalIn{now: wed16, current: "A", autoplt: true, manual: policy.ManualCmd{Target: "B"}}
	d3 := evaluate(t, in3)
	if d3.Action != actionSwitch || d3.Target != "B" {
		t.Fatalf("manual must work without quota data: %+v", d3)
	}
}

// --- P2:資料品質閘(過期/缺失/金鑰失效 → 零自動決策;審計 M1/M3) ---

func TestP2StalenessGate(t *testing.T) {
	// 缺失 → Stale、ActionNone(枚舉釘死:過期=本輪不決策,非 Pause)。
	d := evaluate(t, evalIn{now: wed16, current: "A", autoplt: true})
	if !d.Stale || d.Action != actionNone {
		t.Fatalf("nil quota: %+v", d)
	}

	// 11 分鐘(>10)→ 過期。
	oldA := snap("A", f(10), f(10), 11*time.Minute, wed16)
	d2 := evaluate(t, evalIn{now: wed16, current: "A", a: oldA, autoplt: true})
	if !d2.Stale || d2.Action != actionNone {
		t.Fatalf("11min-old must be stale: %+v", d2)
	}

	// 恰好 10 分鐘(卡片:> 嚴格)→ 仍新鮮(審計 M3 邊界)。
	exact := snap("A", f(10), f(10), 10*time.Minute, wed16)
	bFresh := snap("B", f(5), f(12), 0, wed16)
	d3 := evaluate(t, evalIn{now: wed16, current: "A", a: exact, b: bFresh, autoplt: true})
	if d3.Stale {
		t.Fatalf("exactly 10min must still be fresh (strict >): %+v", d3)
	}

	// 金鑰失效 401(當前帳號)→ 暫停決策+Reason 標明。
	authDead := snap("A", f(10), f(10), 0, wed16)
	authDead.HTTPStatus = 401
	d4 := evaluate(t, evalIn{now: wed16, current: "A", a: authDead, b: bFresh, autoplt: true})
	if d4.Action != actionNone || !strings.Contains(d4.Reason, "金鑰") {
		t.Fatalf("401 on current must pause with key reason: %+v", d4)
	}

	// 403(審計 M3)同屬金鑰失效。
	authDead2 := snap("A", f(10), f(10), 0, wed16)
	authDead2.HTTPStatus = 403
	d5 := evaluate(t, evalIn{now: wed16, current: "A", a: authDead2, b: bFresh, autoplt: true})
	if d5.Action != actionNone {
		t.Fatalf("403 on current must gate: %+v", d5)
	}

	// 非當前帳號(B)金鑰失效 → 不閘(卡片:僅「當前帳號」;審計 M3)。
	bDead := snap("B", f(5), f(12), 0, wed16)
	bDead.HTTPStatus = 401
	aOk := snap("A", f(10), f(10), 0, wed16)
	d6 := evaluate(t, evalIn{now: wed16, current: "A", a: aOk, b: bDead, autoplt: true})
	if d6.Stale || d6.Action == actionNone {
		t.Fatalf("B's 401 must not gate A's decisions: %+v", d6)
	}

	// 過期閘壓過 P3(審計 M1):資料舊 + A 超閾 → 仍不切。
	staleHot := snap("A", f(95), f(10), 11*time.Minute, wed16)
	d7 := evaluate(t, evalIn{now: wed16, current: "A", a: staleHot, b: bFresh, autoplt: true})
	if !d7.Stale || d7.Action != actionNone {
		t.Fatalf("stale must gate P3 even over threshold: %+v", d7)
	}
}

// --- P3:A→B 避難(≥ 邊界;B 可用=週<天花板 **且** 5h<閾值,審計 M2) ---

func TestP3FleeToB(t *testing.T) {
	a, b := healthy(wed16)
	a.FiveHourPct = f(90)
	d := evaluate(t, evalIn{now: wed16, current: "A", a: a, b: b, autoplt: true})
	if d.Action != actionSwitch || d.Target != "B" || d.QuotaMode != "SAVING" {
		t.Fatalf("5h=90 must flee: %+v", d)
	}
	if !strings.Contains(d.Reason, "90") {
		t.Fatalf("reason must carry the number: %q", d.Reason)
	}

	a2, b2 := healthy(wed16)
	a2.WeeklyPct = f(90)
	d2 := evaluate(t, evalIn{now: wed16, current: "A", a: a2, b: b2, autoplt: true})
	if d2.Action != actionSwitch || d2.Target != "B" {
		t.Fatalf("weekly=90 must flee: %+v", d2)
	}

	a3, b3 := healthy(wed16)
	a3.FiveHourPct = f(89.9)
	d3 := evaluate(t, evalIn{now: wed16, current: "A", a: a3, b: b3, autoplt: true})
	if d3.Action != actionStay {
		t.Fatalf("5h=89.9 must stay: %+v", d3)
	}

	// B 不可用(週%=天花板)→ 不切 B(審計 M2 的天花板維度)。
	a4, _ := healthy(wed16)
	a4.FiveHourPct = f(95)
	budget := policy.BudgetState{WeekAnchorTime: anchorFri15(), DayBoundary: &thu15, DayStartWeekly: f(45), CeilingToday: f(45)}
	b4 := snap("B", f(5), f(45), 0, wed16)
	d4 := evaluate(t, evalIn{now: wed16, current: "A", a: a4, b: b4, budget: budget, autoplt: true})
	if d4.Action == actionSwitch && d4.Target == "B" {
		t.Fatalf("B at ceiling must not be a refuge: %+v", d4)
	}

	// B 不可用(5h=95 ≥ 90,預算 OK)→ 也不切(審計 M2 的 5h 維度)。
	b5 := snap("B", f(95), f(20), 0, wed16)
	d5 := evaluate(t, evalIn{now: wed16, current: "A", a: a4, b: b5, autoplt: true})
	if d5.Action == actionSwitch && d5.Target == "B" {
		t.Fatalf("B with 5h over threshold must not be a refuge: %+v", d5)
	}
}

// --- P4:B→A 回歸(兩窗都 ≤ 安全區;邊界含等於;遲滯) ---

func TestP4ReturnToA(t *testing.T) {
	a, b := healthy(wed16)
	a.FiveHourPct, a.WeeklyPct = f(30), f(60)
	d := evaluate(t, evalIn{now: wed16, current: "B", a: a, b: b, autoplt: true})
	if d.Action != actionSwitch || d.Target != "A" || d.QuotaMode != "RACING" {
		t.Fatalf("recovered A must recall: %+v", d)
	}

	a2, b2 := healthy(wed16)
	a2.FiveHourPct, a2.WeeklyPct = f(31), f(60)
	d2 := evaluate(t, evalIn{now: wed16, current: "B", a: a2, b: b2, autoplt: true})
	if d2.Action != actionStay {
		t.Fatalf("5h=31 must stay on B: %+v", d2)
	}

	a3, b3 := healthy(wed16)
	a3.FiveHourPct, a3.WeeklyPct = f(30), f(61)
	d3 := evaluate(t, evalIn{now: wed16, current: "B", a: a3, b: b3, autoplt: true})
	if d3.Action != actionStay {
		t.Fatalf("weekly=61 must stay on B: %+v", d3)
	}
}

// --- P5:B 預算嚴格護欄(≥ 天花板 → 切回 A,縱使 A 未恢復) ---

func TestP5BudgetCeilingStrict(t *testing.T) {
	a, b := healthy(wed16)
	a.FiveHourPct = f(95)
	b.WeeklyPct = f(45)
	budget := policy.BudgetState{WeekAnchorTime: anchorFri15(), DayBoundary: &thu15, DayStartWeekly: f(25.5), CeilingToday: f(45)}
	d := evaluate(t, evalIn{now: wed16, current: "B", a: a, b: b, budget: budget, autoplt: true})
	if d.Action != actionSwitch || d.Target != "A" {
		t.Fatalf("B at ceiling must return to A even if A is down: %+v", d)
	}
	if !strings.Contains(d.Reason, "45") {
		t.Fatalf("reason must carry budget numbers: %q", d.Reason)
	}

	// 44.9 → 未達天花板:正向釘 Stay(審計 D2:反向複合斷言抓不到錯誤實作)。
	b2 := snap("B", f(5), f(44.9), 0, wed16)
	d2 := evaluate(t, evalIn{now: wed16, current: "B", a: a, b: b2, budget: budget, autoplt: true})
	if d2.Action != actionStay {
		t.Fatalf("B below ceiling must stay on B: %+v", d2)
	}
}

// --- P6:兩邊不可用(2026-11-15 仲裁:B 預算耗盡=喪失候選;5h 受阻=候選,恢復=自身 5h 重置) ---

func TestP6BothDown(t *testing.T) {
	budgetOK := policy.BudgetState{WeekAnchorTime: anchorFri15(), DayBoundary: &thu15, DayStartWeekly: f(25.5), CeilingToday: f(45)}
	mk := func(aReset, bReset *time.Time, weekly float64) evalIn {
		a := snap("A", f(95), f(10), 0, wed16)
		a.FiveHourReset = aReset
		b := snap("B", f(95), f(weekly), 0, wed16)
		b.FiveHourReset = bReset
		return evalIn{now: wed16, current: "A", a: a, b: b, budget: budgetOK, autoplt: true}
	}
	fri18 := time.Date(2026, 11, 20, 18, 0, 0, 0, time.Local)
	sat10 := time.Date(2026, 11, 21, 10, 0, 0, 0, time.Local)
	fri20 := time.Date(2026, 11, 20, 20, 0, 0, 0, time.Local)
	sat18 := time.Date(2026, 11, 21, 18, 0, 0, 0, time.Local)

	// 1) B 僅 5h 受阻(預算 OK):A 較早恢復(Fri18 < Sat10)→ 留 A。
	d := evaluate(t, mk(&fri18, &sat10, 30))
	if d.Action != actionStay {
		t.Fatalf("A recovers earlier → stay: %+v", d)
	}

	// 2) B 較早恢復(Fri20 < Sat18)→ 切 B(預算 OK,錢安全)。
	d2 := evaluate(t, mk(&sat18, &fri20, 30))
	if d2.Action != actionSwitch || d2.Target != "B" {
		t.Fatalf("B recovers earlier → flee to B: %+v", d2)
	}

	// 3) 平手 → 留 A(審計 M4)。
	d3 := evaluate(t, mk(&fri18, &fri18, 30))
	if d3.Action != actionStay {
		t.Fatalf("tie → stay A: %+v", d3)
	}

	// 4) B 缺重置資訊 → 留 A(審計 M4)。
	d4 := evaluate(t, mk(&fri18, nil, 30))
	if d4.Action != actionStay {
		t.Fatalf("B missing reset → stay A: %+v", d4)
	}

	// 5) B 預算耗盡(週%=天花板)→ 喪失候選,縱使 B 的 5h 較早恢復也不切
	//    (仲裁:提前切入預算耗盡的 B = 結算前燒錢越線,違反 D5)。
	budgetDead := policy.BudgetState{WeekAnchorTime: anchorFri15(), DayBoundary: &thu15, DayStartWeekly: f(45), CeilingToday: f(45)}
	a := snap("A", f(95), f(10), 0, wed16)
	a.FiveHourReset = &sat18
	b := snap("B", f(95), f(45), 0, wed16)
	b.FiveHourReset = &fri20
	d5 := evaluate(t, evalIn{now: wed16, current: "A", a: a, b: b, budget: budgetDead, autoplt: true})
	if d5.Action != actionStay {
		t.Fatalf("budget-exhausted B loses candidacy → must positively stay on A: %+v", d5)
	}
	if !strings.Contains(d5.Reason, "結算") {
		t.Fatalf("reason should point to B's next settlement: %q", d5.Reason)
	}
}

// --- P7:預警(≥80 觸發;鏈式:已警不重複→回落清除→再跨再警;僅當前帳號;審計 D3/M5) ---

func TestP7WarnChain(t *testing.T) {
	_, b := healthy(wed16)

	// 1) 81 跨過 80 → 警+旗標持久化。
	hot := snap("A", f(81), f(10), 0, wed16)
	d1 := evaluate(t, evalIn{now: wed16, current: "A", a: hot, b: b, autoplt: true})
	if !d1.WarnFired || d1.Budget == nil || !d1.Budget.WarnedFiveHour {
		t.Fatalf("crossing 80 must warn and persist flag: %+v", d1)
	}

	// 2) 已警、還在 81 → 不重複(吃 d1 的狀態=真鏈)。
	d2 := evaluate(t, evalIn{now: wed16, current: "A", a: hot, b: b, autoplt: true, budget: *d1.Budget})
	if d2.WarnFired {
		t.Fatalf("already-warned must not re-warn: %+v", d2)
	}

	// 3) 回落 70 → 旗標必須清除且可持久化(釘「回落清除」讀法)。
	below := snap("A", f(70), f(10), 0, wed16)
	d3 := evaluate(t, evalIn{now: wed16, current: "A", a: below, b: b, autoplt: true, budget: *d1.Budget})
	if d3.Budget == nil || d3.Budget.WarnedFiveHour {
		t.Fatalf("drop below warn line must emit cleared flag: %+v", d3.Budget)
	}

	// 4) 吃 d3 的清除狀態,再跨 82 → 再警(證明鏈真的接上)。
	hot2 := snap("A", f(82), f(10), 0, wed16)
	d4 := evaluate(t, evalIn{now: wed16, current: "A", a: hot2, b: b, autoplt: true, budget: *d3.Budget})
	if !d4.WarnFired {
		t.Fatalf("re-crossing with cleared flag must warn again: %+v", d4)
	}

	// 5) 邊界:恰好 80(≥)→ 警(審計 M5)。
	exact := snap("A", f(80), f(10), 0, wed16)
	d5 := evaluate(t, evalIn{now: wed16, current: "A", a: exact, b: b, autoplt: true})
	if !d5.WarnFired {
		t.Fatalf("exactly 80 must warn (>=): %+v", d5)
	}

	// 6) 僅當前帳號:B 81 但當前是 A → 不警(審計 M5)。
	bHot := snap("B", f(81), f(12), 0, wed16)
	d6 := evaluate(t, evalIn{now: wed16, current: "A", a: snap("A", f(10), f(10), 0, wed16), b: bHot, autoplt: true})
	if d6.WarnFired {
		t.Fatalf("non-current account must not trigger warn: %+v", d6)
	}
}

// --- P8:外部事件(未知=Pause;provider_changed=靜默採納) ---

func TestP8ExternalEvents(t *testing.T) {
	a, b := healthy(wed16)
	d := evaluate(t, evalIn{now: wed16, current: "A", a: a, b: b, autoplt: true, ext: policy.ExtEvent{Kind: "unknown_provider"}})
	if d.Action != actionPause {
		t.Fatalf("unknown provider must pause: %+v", d)
	}
	d2 := evaluate(t, evalIn{now: wed16, current: "B", a: a, b: b, autoplt: true, ext: policy.ExtEvent{Kind: "provider_changed"}})
	if d2.Action != actionNone {
		t.Fatalf("provider_changed adopted silently: %+v", d2)
	}
}

// --- 自駕開關(2026-11-17 方向性:暫停只封鎖 A→B;完整案例見 fleegate_test.go) ---

func TestAutopilotOff(t *testing.T) {
	a, b := healthy(wed16)
	a.FiveHourPct = f(95)
	d := evaluate(t, evalIn{now: wed16, current: "A", a: a, b: b, autoplt: false})
	if d.Action != actionStay {
		t.Fatalf("autopilot off must block flee as an explicit stay (directional gate): %+v", d)
	}
	d2 := evaluate(t, evalIn{now: wed16, current: "A", a: a, b: b, autoplt: false, manual: policy.ManualCmd{Target: "B"}})
	if d2.Action != actionSwitch || d2.Target != "B" {
		t.Fatalf("manual must bypass autopilot: %+v", d2)
	}
}

// --- 配速數學:切片結算鏈(2026-11-19 修訂 3 切片語意,使用者以實例定案) ---
//
// 乾淨週從週五 16:00 起(剛過週錨 15:00),切片 = 切截鐘點 → 切截鐘點,
// 週六日的切截跳過結算、其時間併入前一個工作日切片(Fri→Mon 為同一片)。
// 逐片推導(總量 90、只數工作日切片;錨點採納 B 的重置 = 週五 11-27 15:00):
//
//	day0 週五 16:00:剩餘切片 = day1..day5 = 5 → 90/5   = 18 → 18
//	    (day1 = 週五午後 → 週一 15:00,週末併入;邊界鏈:五 15:00 → 跳六日 → 一 15:00)
//	day1 週一 16:00:剩餘 4 片 → (90−18)/4 = 18 → 36
//	day2 週二 16:00:剩餘 3 片 → (90−36)/3 = 18 → 54
//	day3 週三 16:00:剩餘 2 片 → (90−54)/2 = 18 → 72
//	day4 週四 16:00:剩餘 1 片(最後一片領全額)→ (90−72)/1 = 18 → 90
//
// 收敛恆等:末天花板 == 總量 90,零死額度(舊曆日語意的「晨間留量 9.6」
// 隨切片語意退場——重置日晨間已併入最後一片)。18/36/54/72/90 皆二進制
// 精確,1e-9 容差為既有慣例。
func TestPacingIdentitySevenSettlements(t *testing.T) {
	want := []float64{18, 36, 54, 72, 90}

	// 五個結算瞬間(切片語意:週六日切截跳過,週末切片併入 day1 → 週末無結算):
	// Fri 16:00(day1 起點)、Mon/Tue/Wed/Thu 16:00(day2–day5)。
	nows := []time.Time{
		fri16,
		time.Date(2026, 11, 23, 16, 0, 0, 0, time.Local), // Mon
		time.Date(2026, 11, 24, 16, 0, 0, 0, time.Local), // Tue
		time.Date(2026, 11, 25, 16, 0, 0, 0, time.Local), // Wed
		time.Date(2026, 11, 26, 16, 0, 0, 0, time.Local), // Thu
	}

	budget := policy.BudgetState{
		WeekAnchorTime: anchorFri15(),
		DayBoundary:    &fri14, DayStartWeekly: f(0), CeilingToday: f(0),
	}
	for i, now := range nows {
		// B 的週%=前一片燒到的天花板(燒滿模擬);首片=0。
		burned := 0.0
		if i > 0 {
			burned = want[i-1]
		}
		b := snap("B", f(5), f(burned), 0, now)
		b.WeeklyReset = &nextFri
		d := policy.Evaluate(policy.Input{
			Now: now, Current: "B",
			QuotaA: snap("A", f(10), f(10), 0, now), QuotaB: b,
			Budget: budget, Cfg: config.Default(), AutopilotOn: true,
		})
		if d.Budget == nil {
			t.Fatalf("day %d: settlement must fire (now=%v boundary=%v)", i, now, *budget.DayBoundary)
		}
		if got := derefP(d.Budget.CeilingToday); math.Abs(got-want[i]) > 1e-9 {
			t.Fatalf("day %d: ceiling = %v, want %v (dayStart=%v)", i, got, want[i], derefP(d.Budget.DayStartWeekly))
		}
		if got := derefP(d.Budget.CeilingToday); got > 90+1e-9 {
			t.Fatalf("day %d: cumulative ceiling %v exceeds weekly total", i, got)
		}
		budget = *d.Budget
		if i == 0 {
			// Amendment 3 (review F1): the boundary chain must skip the weekend —
			// day0's next boundary is Mon 15:00, never a Sat/Sun cut-off.
			wantB := time.Date(2026, 11, 23, 15, 0, 0, 0, time.Local)
			if budget.DayBoundary == nil || !budget.DayBoundary.Equal(wantB) {
				t.Fatalf("day0 boundary must skip the weekend to Mon 15:00, got %v", budget.DayBoundary)
			}
		}
	}
	// 收敛恆等(修訂 3):最後一片領全額 → 末天花板 == 總量 90,零死額度。
	last := derefP(budget.CeilingToday)
	if math.Abs(last-90.0) > 1e-9 {
		t.Fatalf("identity: last ceiling %v must equal the full total 90 (no dead budget)", last)
	}
}

// 週三結算:基準更新、切片語意 days=[三午,四]+[五]=重置 → 2、邊界貼錨點鐘點
// (此例 +24h 同值 thu15);恰好卡在邊界時刻也觸發(≥ 含等於,審計 M7)。
func TestSettlementWednesday(t *testing.T) {
	b := snap("B", f(5), f(30), 0, wed15)
	budget := policy.BudgetState{WeekAnchorTime: anchorFri15(), DayBoundary: &wed15, DayStartWeekly: f(25.5), CeilingToday: f(50)}
	d := evaluate(t, evalIn{now: wed15, current: "A", a: snap("A", f(10), f(10), 0, wed15), b: b, budget: budget, autoplt: true})
	if d.Budget == nil {
		t.Fatalf("settlement fires at the exact boundary instant (>=): %+v", d)
	}
	if derefP(d.Budget.DayStartWeekly) != 30.0 {
		t.Fatalf("new day baseline = current weekly: %v", derefP(d.Budget.DayStartWeekly))
	}
	if derefP(d.Budget.CeilingToday) != 60.0 {
		t.Fatalf("ceiling = 30+(90-30)/2=60(切片語意 [三午,四]、[五]=重置): %v", derefP(d.Budget.CeilingToday))
	}
	if !d.Budget.DayBoundary.Equal(thu15) {
		t.Fatalf("next boundary = +24h: %v want %v", d.Budget.DayBoundary, thu15)
	}

	// 冪等(審計 M7):吃結算後狀態、同一 Now 再評 → 不重複結算。
	d2 := evaluate(t, evalIn{now: wed15, current: "A", a: snap("A", f(10), f(10), 0, wed15), b: b, budget: *d.Budget, autoplt: true})
	if d2.Budget != nil {
		t.Fatalf("same-instant re-evaluation must not re-settle: %+v", d2.Budget)
	}
}

// 新週結算:錨點採納 B 的新重置時間。
func TestSettlementNewWeekAfterAnchor(t *testing.T) {
	b := snap("B", f(5), f(0), 0, fri16)
	b.WeeklyReset = &nextFri
	budget := policy.BudgetState{WeekAnchorTime: anchorFri15(), DayBoundary: &fri14, DayStartWeekly: f(70), CeilingToday: f(85)}
	d := evaluate(t, evalIn{now: fri16, current: "B", a: snap("A", f(10), f(10), 0, fri16), b: b, budget: budget, autoplt: true})
	if d.Budget == nil {
		t.Fatalf("weekly reset crossing must settle: %+v", d)
	}
	if derefP(d.Budget.CeilingToday) != 18.0 {
		t.Fatalf("clean week: 90/5=18(切片語意 [五16→五27] 5 個切片,週末併入 day1): %v", derefP(d.Budget.CeilingToday))
	}
	if d.Budget.WeekAnchorTime == nil || !d.Budget.WeekAnchorTime.Equal(nextFri) {
		t.Fatalf("anchor must adopt B's new reset: %v", d.Budget.WeekAnchorTime)
	}
}

// 週六結算:假日排除 5 片(day1 週五午後→週一、day2 一~…~day5)vs 日曆 6 片
// (週六日各自成片;錨點已過 → 前滾一週至 11-27)。本測試注入的週六日界屬
// 過渡期遺留形狀(修訂 3 後邊界鏈不再鑄出週末切截);斷言聚焦天花板算術。
// 算術:allowance 14 / 31.67。
func TestSettlementWeekendWorkdaysOnly(t *testing.T) {
	run := func(workdaysOnly bool, now, boundary time.Time) policy.Decision {
		b := snap("B", f(5), f(20), 0, now)
		budget := policy.BudgetState{WeekAnchorTime: anchorFri15(), DayBoundary: &boundary, DayStartWeekly: f(20), CeilingToday: f(40)}
		cfg := config.Default()
		cfg.Budget.WorkdaysOnly = workdaysOnly
		return policy.Evaluate(policy.Input{
			Now: now, Current: "B", QuotaA: snap("A", f(10), f(10), 0, now), QuotaB: b,
			Budget: budget, Cfg: cfg, AutopilotOn: true,
		})
	}
	if d := run(true, sat15.Add(time.Minute), sat15); d.Budget == nil || derefP(d.Budget.CeilingToday) != 34.0 {
		t.Fatalf("workdays-only: ceiling=20+(70/5)=34(切片語意 5 片,週末併入 day1): %+v", d.Budget)
	}
	// 70/6 非二進制精確 → 容差比較(切片語意:日曆模式週六日各自成片 → 6 片)。
	if d := run(false, sat15.Add(time.Minute), sat15); d.Budget == nil || math.Abs(derefP(d.Budget.CeilingToday)-(20.0+70.0/6.0)) > 1e-9 {
		t.Fatalf("weekends counted: ceiling=20+(70/6)≈31.67: %+v", d.Budget)
	}
	// 審查 F1 殘餘 mutant 的殺手:週末晨間(錨點鐘點未過)處理逾期日界 →
	// 新日界必須由「錨點分支的週末跳過」鑄出週一 15:00;刪掉該跳過迴圈的
	// mutant 會鑄出同日 週六 15:00,在此斷言死亡。
	mon15 := time.Date(2026, 11, 23, 15, 0, 0, 0, time.Local)
	d := run(true, sat15.Add(-8*time.Hour), fri15) // 週六 07:00,逾期日界 週五 15:00
	if d.Budget == nil || d.Budget.DayBoundary == nil || !d.Budget.DayBoundary.Equal(mon15) {
		t.Fatalf("weekend-morning overdue settlement must mint Mon 15:00 (never a weekend cut-off): %+v", d.Budget)
	}
	if derefP(d.Budget.CeilingToday) != 34.0 {
		t.Fatalf("weekend-morning ceiling=20+(70/5)=34: %+v", d.Budget)
	}
}

// 保底+夾牆:daysLeft=1(結算點=週五 14:01,錨點日=當日 → D1 含頭含尾恰
// 1 日,真值而非夾底;斷言數值兩種語意相同)。
func TestBudgetFloorAndWallClamp(t *testing.T) {
	settle := func(dayStart, weekly float64) policy.Decision {
		now := fri14.Add(time.Minute)
		b := snap("B", f(5), f(weekly), 0, now)
		budget := policy.BudgetState{WeekAnchorTime: anchorFri15(), DayBoundary: &fri14, DayStartWeekly: f(dayStart), CeilingToday: f(dayStart)}
		return policy.Evaluate(policy.Input{
			Now: now, Current: "B", QuotaA: snap("A", f(10), f(10), 0, now), QuotaB: b,
			Budget: budget, Cfg: config.Default(), AutopilotOn: true,
		})
	}
	if d := settle(86, 86); d.Budget == nil || derefP(d.Budget.CeilingToday) != 90.0 {
		t.Fatalf("normal: (90-86)/1=4 ≥3 → ceiling=90: %+v", d.Budget)
	}
	if d := settle(88, 88); d.Budget == nil || derefP(d.Budget.CeilingToday) != 91.0 {
		t.Fatalf("floor fires: 88+3=91 (>90 legal, A1): %+v", d.Budget)
	}
	if d := settle(98, 98); d.Budget == nil || derefP(d.Budget.CeilingToday) != 100.0 {
		t.Fatalf("wall clamp: min(101,100)=100: %+v", d.Budget)
	}
}

// 日中鎖定(審計 M8 判別器):結算鎖 ceiling=77.5;日中週%回落到 77——
// 「min 混合式」讀法會得 min(77.5, 65+(90−77)/2)=71.5(→P5 誤發切 A),鎖定式維持 77.5(→不發)。釘鎖定。
func TestMidDayLockedCeiling(t *testing.T) {
	budget := policy.BudgetState{WeekAnchorTime: anchorFri15(), DayBoundary: &thu15, DayStartWeekly: f(65), CeilingToday: f(77.5)}
	b := snap("B", f(5), f(77), 0, wed16)  // 77 < 77.5(鎖定)但 ≥ 76.5(重算)→ 兩讀法在此分歧
	a := snap("A", f(95), f(10), 0, wed16) // A 未恢復:若 P5 誤發就會切 A
	d := evaluate(t, evalIn{now: wed16, current: "B", a: a, b: b, budget: budget, autoplt: true})
	if d.Action != actionStay {
		t.Fatalf("locked ceiling must hold (no P5 fire): %+v", d)
	}
	if d.Budget != nil {
		t.Fatalf("mid-day must not settle: %+v", d.Budget)
	}
}

// 冷啟(審計 M6):錨點在,日界=當日錨定時刻;無錨點 → 兜底 00:00、daysLeft=7。
func TestColdStart(t *testing.T) {
	b := snap("B", f(5), f(10), 0, wed10)
	d := evaluate(t, evalIn{now: wed10, current: "B", a: snap("A", f(10), f(10), 0, wed10), b: b, autoplt: true,
		budget: policy.BudgetState{WeekAnchorTime: anchorFri15()}})
	if d.Budget == nil {
		t.Fatalf("cold start must settle immediately: %+v", d)
	}
	if derefP(d.Budget.DayStartWeekly) != 10.0 ||
		math.Abs(derefP(d.Budget.CeilingToday)-(10.0+80.0/3.0)) > 1e-9 {
		t.Fatalf("cold: baseline 10, ceiling 10+(90-10)/3≈36.6667(days=[三,四,五] 含重置日): %+v", d.Budget)
	}
	if d.Budget.DayBoundary == nil || !d.Budget.DayBoundary.Equal(wed15) {
		t.Fatalf("cold next boundary = today at anchor clock (Wed 15:00): %v", d.Budget.DayBoundary)
	}

	cfg := config.Default()
	cfg.Budget.WeeklyTotalPct = 84
	d2 := policy.Evaluate(policy.Input{
		Now: wed10, Current: "B",
		QuotaA: snap("A", f(10), f(10), 0, wed10), QuotaB: snap("B", f(5), f(0), 0, wed10),
		Budget: policy.BudgetState{}, Cfg: cfg, AutopilotOn: true,
	})
	if d2.Budget == nil || derefP(d2.Budget.CeilingToday) != 12.0 {
		t.Fatalf("no anchor: fallback daysLeft=7, ceiling=84/7=12: %+v", d2.Budget)
	}
	if d2.Budget.DayBoundary == nil || d2.Budget.DayBoundary.Format("15:04") != "00:00" {
		t.Fatalf("no anchor: fallback boundary at midnight: %v", d2.Budget.DayBoundary)
	}
}

// 結算需 B 資料有效;無效 → 不結算+Reason 標明(審計 M9)。
func TestSettlementNeedsValidB(t *testing.T) {
	budget := policy.BudgetState{WeekAnchorTime: anchorFri15(), DayBoundary: &wed15, DayStartWeekly: f(25.5), CeilingToday: f(50)}
	d := evaluate(t, evalIn{now: wed15, current: "B", a: snap("A", f(10), f(10), 0, wed15), b: nil, budget: budget, autoplt: true})
	if d.Budget != nil {
		t.Fatalf("cannot settle without B data: %+v", d.Budget)
	}
	if !strings.Contains(d.Reason, "結算") {
		t.Fatalf("reason must flag the skipped settlement: %q", d.Reason)
	}
}
