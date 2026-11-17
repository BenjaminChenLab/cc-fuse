package policy_test

// 驗收測試(黑箱)——方向性避難閘(2026-11-17):
// 暫停與「六日不自動切 B」(noFleeOnWeekend)都只封鎖 A→B(P3 避難、P6 仲裁的切 B),
// 不擋 B→A(P4 恢復切回、P5 天花板強制切回);暫停期間 §7 結算與 P2 過期閘照常運作。
// 規格:docs/blueprints/2026-11-17-cc-fuse-flee-gate.md(需求逐字 + 詮釋決定)。
// v2(Phase-3 審計補強):P6 封鎖改用鑑別配對(預算 OK 但 5h 不可用的 B——
// 舊仲裁真的會切 B,封鎖才真的攔了它);補「封鎖不得發明切回 A」、預警/結算/
// RACING/成因標籤斷言、注入時間 vs 快照時間戳、精確午夜邊界、週線觸發、金鑰失效。

import (
	"strings"
	"testing"
	"time"

	"cc-fuse/internal/config"
	"cc-fuse/internal/domain"
	"cc-fuse/internal/policy"
)

// 避難封鎖測試宇宙:既有宇宙外補六日邊界(2026-11-20=五、21=六、22=日、23=一)。
var (
	fri23h59 = time.Date(2026, 11, 20, 23, 59, 0, 0, time.Local)
	sat00h00 = time.Date(2026, 11, 21, 0, 0, 30, 0, time.Local) // 週六第一分鐘「內」(精確午夜邊界)
	sat00h01 = time.Date(2026, 11, 21, 0, 1, 0, 0, time.Local)
	sat00h03 = time.Date(2026, 11, 21, 0, 3, 0, 0, time.Local) // 快照時間戳對照案例用
	sun23h59 = time.Date(2026, 11, 22, 23, 59, 0, 0, time.Local)
	mon00h00 = time.Date(2026, 11, 23, 0, 0, 30, 0, time.Local) // 週一第一分鐘「內」
	mon00h01 = time.Date(2026, 11, 23, 0, 1, 0, 0, time.Local)
)

// evalWithCfg:同 evaluate,但允許改設定(六日 checkbox 等自訂欄位)。
func evalWithCfg(t *testing.T, in evalIn, mutate func(*config.Config)) policy.Decision {
	t.Helper()
	cfg := config.Default()
	if mutate != nil {
		mutate(&cfg)
	}
	return policy.Evaluate(policy.Input{
		Now: in.now, Current: in.current, QuotaA: in.a, QuotaB: in.b,
		Budget: in.budget, Cfg: cfg, AutopilotOn: in.autoplt,
		External: in.ext, Manual: in.manual,
	})
}

// fleeWeekendCfg 打開「六日不自動切 B」。
func fleeWeekendCfg(c *config.Config) { c.NoFleeOnWeekend = true }

// hotA 造出過熱的 A(5h 95),可用於 P3/P6 情境。
func hotA(at time.Time) *domain.QuotaSnapshot {
	return snap("A", f(95), f(10), 0, at)
}

// recoveredA 造出已恢復的 A(5h 25 ≤ 30、週 50 ≤ 60)。
func recoveredA(at time.Time) *domain.QuotaSnapshot {
	return snap("A", f(25), f(50), 0, at)
}

// exhaustedB 造出預算耗盡的 B(週 30 ≥ 天花板 20)→ P5 觸發、P6 喪失候選。
func exhaustedB(at time.Time) *domain.QuotaSnapshot {
	return snap("B", f(40), f(30), 0, at)
}

// fiveBlockedB 造出「預算 OK 但 5h 不可用」的 B:週 10 < 天花板 20(預算 OK),
// 5h 95 ≥ 切換線 90(不可用)→ bAvailable=false 且 budgetBlocked=false,
// P6 仲裁才會真的走到「B 先恢復 → 切 B」。這是 P6 封鎖案例的鑑別前提:
// 若改用預算耗盡的 B,舊邏輯本來就留守 A(喪失候選),封鎖案例變空彈。
func fiveBlockedB(at time.Time) *domain.QuotaSnapshot {
	return snap("B", f(95), f(10), 0, at)
}

// withCeiling 造出「今日結算已完成」的預算狀態:日界設在未來(nextFri)讓本輪不再
// 觸發結算,注入的天花板 20 才不會被先行結算覆寫(§7:結算先行、決策吃新天花板)。
func withCeiling(b policy.BudgetState) policy.BudgetState {
	b.DayBoundary = &nextFri
	b.DayStartWeekly = f(10)
	b.CeilingToday = f(20)
	return b
}

// 六日邊界日期健全性:先釘死測試宇宙的 weekday,防止時間算術錯位讓整組案例失義。
func TestFleeGateUniverseWeekdays(t *testing.T) {
	for _, tc := range []struct {
		at   time.Time
		want time.Weekday
		name string
	}{
		{fri16, time.Friday, "fri16"},
		{fri23h59, time.Friday, "fri23h59"},
		{sat00h00, time.Saturday, "sat00h00"},
		{sat00h01, time.Saturday, "sat00h01"},
		{sat00h03, time.Saturday, "sat00h03"},
		{sat15, time.Saturday, "sat15"},
		{sun23h59, time.Sunday, "sun23h59"},
		{mon00h00, time.Monday, "mon00h00"},
		{mon00h01, time.Monday, "mon00h01"},
	} {
		if got := tc.at.Weekday(); got != tc.want {
			t.Fatalf("%s: weekday = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// R1 方向性核心:暫停只封鎖 A→B;B→A(P4/P5)照常;封鎖不得反向發明切回 A。
func TestPausedBlocksFleeOnly(t *testing.T) {
	// 暫停 + 在 A + A 過熱 + B 可用:明確留守 A(封鎖),絕不切 B;
	// 理由必須寫明成因「暫停」;P7 預警(95 ≥ 80)照響;冷啟結算照跑。
	a, b := healthy(wed16)
	a.FiveHourPct = f(95)
	d := evaluate(t, evalIn{now: wed16, current: "A", a: a, b: b, autoplt: false})
	if d.Action != actionStay {
		t.Fatalf("paused flee must be blocked as Stay, got %+v", d)
	}
	if !strings.Contains(d.Reason, "封鎖") || !strings.Contains(d.Reason, "暫停自動切 B") {
		t.Fatalf("blocked stay reason must name the cause 暫停: %q", d.Reason)
	}
	if !d.WarnFired {
		t.Fatalf("P7 warning must still fire on a blocked stay round: %+v", d)
	}
	if !d.Settled || d.Budget == nil {
		t.Fatalf("settlement must keep running on a blocked stay round: %+v", d)
	}

	// 暫停 + 在 B + A 已恢復:P4 照切回 A、翻 RACING(暫停不擋 B→A)。
	d2 := evaluate(t, evalIn{now: wed16, current: "B",
		a: recoveredA(wed16), b: snap("B", f(40), f(30), 0, wed16), autoplt: false})
	if d2.Action != actionSwitch || d2.Target != "A" || d2.QuotaMode != "RACING" {
		t.Fatalf("paused must NOT block P4 return: %+v", d2)
	}

	// 暫停 + 在 B + B 燒到當日天花板:P5 強制切回 A 照常,且翻 RACING(B→A 不受暫停影響)。
	d3 := evaluate(t, evalIn{now: wed16, current: "B",
		a: hotA(wed16), b: exhaustedB(wed16), budget: withCeiling(policy.BudgetState{}), autoplt: false})
	if d3.Action != actionSwitch || d3.Target != "A" || d3.QuotaMode != "RACING" {
		t.Fatalf("paused must NOT block P5 ceiling return: %+v", d3)
	}

	// 暫停 + 在 B + A 未恢復 + B 預算 OK:封鎖只擋切 B,絕不反向「發明」切回 A——
	// 暫停正是為了繼續用自購 B;被迫切回 A 等於改燒團隊額度。
	d6 := evaluate(t, evalIn{now: wed16, current: "B",
		a: hotA(wed16), b: snap("B", f(40), f(10), 0, wed16),
		budget: withCeiling(policy.BudgetState{}), autoplt: false})
	if d6.Action != actionStay {
		t.Fatalf("block must never invent a forced return to A: %+v", d6)
	}

	// 暫停 + 在 A + A 過熱 + B 預算 OK 但 5h 不可用 + B 的 5h 重置較早:
	// P6 仲裁會切 B 等重置,那也是 A→B → 封鎖下留守 A。
	aHot := hotA(wed16)
	aHot.FiveHourReset = &sat15
	bNo := fiveBlockedB(wed16)
	bNo.FiveHourReset = &fri16
	d4 := evaluate(t, evalIn{now: wed16, current: "A",
		a: aHot, b: bNo, budget: withCeiling(policy.BudgetState{}), autoplt: false})
	if d4.Action != actionStay {
		t.Fatalf("paused must block P6 switch-to-B: %+v", d4)
	}
	// 鑑別配對(審計 F1):同一輸入解除封鎖,P6 照舊邏輯切 B——證明上面的留守
	// 真是封鎖造成的,而非輸入本來就會留守(否則本案例對「閘門只蓋 P3」的
	// 錯誤實作沒有鑑別力)。
	d4b := evaluate(t, evalIn{now: wed16, current: "A",
		a: aHot, b: bNo, budget: withCeiling(policy.BudgetState{}), autoplt: true})
	if d4b.Action != actionSwitch || d4b.Target != "B" {
		t.Fatalf("same input unblocked must arbitrate to B (discriminating pair): %+v", d4b)
	}

	// 暫停 + 週線觸發的過熱(5h 健康、週 95 ≥ 切換線):封鎖同樣蓋住週線路徑。
	aW := snap("A", f(10), f(95), 0, wed16)
	d7 := evaluate(t, evalIn{now: wed16, current: "A",
		a: aW, b: snap("B", f(5), f(12), 0, wed16), autoplt: false})
	if d7.Action != actionStay || !strings.Contains(d7.Reason, "封鎖") {
		t.Fatalf("weekly-triggered flee must be blocked too: %+v", d7)
	}

	// 暫停 + 手動切 B:P1 永遠穿透封鎖。
	a, b = healthy(wed16)
	a.FiveHourPct = f(95)
	d5 := evaluate(t, evalIn{now: wed16, current: "A", a: a, b: b, autoplt: false,
		manual: policy.ManualCmd{Target: "B"}})
	if d5.Action != actionSwitch || d5.Target != "B" {
		t.Fatalf("manual must bypass flee block: %+v", d5)
	}

	// 暫停 + 健康閒置:明確留守(ActionStay)——方向性語意下暫停不再是無聲的 None。
	aH, bH := healthy(wed16)
	d8 := evaluate(t, evalIn{now: wed16, current: "A", a: aH, b: bH, autoplt: false})
	if d8.Action != actionStay {
		t.Fatalf("paused idle round must be an explicit stay: %+v", d8)
	}
}

// R2 暫停期間書記照跑:§7 結算照落帳、P2 過期閘與金鑰失效閘照常
// (舊行為暫停=整輪早退,兩者都凍結)。
func TestPausedKeepsBookkeeping(t *testing.T) {
	// 冷啟(無 DayBoundary)+ B 資料新鮮:暫停中結算照樣跑、天花板落帳。
	b := snap("B", f(5), f(10), 0, wed16)
	b.WeeklyReset = &nextFri
	d := evaluate(t, evalIn{now: wed16, current: "B",
		a: recoveredA(wed16), b: b, budget: policy.BudgetState{}, autoplt: false})
	if !d.Settled || d.Budget == nil {
		t.Fatalf("settlement must keep running while paused: %+v", d)
	}

	// 過期資料:暫停中照樣判過期(Stale),不是「自駕關閉」的無聲 None。
	oldA := snap("A", f(10), f(10), 11*time.Minute, wed16)
	d2 := evaluate(t, evalIn{now: wed16, current: "A", a: oldA, autoplt: false})
	if !d2.Stale {
		t.Fatalf("stale gate must stay active while paused: %+v", d2)
	}

	// 當前帳號金鑰失效(401):暫停中 P2 全項照常,不只過期項。
	deadA := snap("A", f(10), f(10), 0, wed16)
	deadA.HTTPStatus = 401
	d3 := evaluate(t, evalIn{now: wed16, current: "A",
		a: deadA, b: snap("B", f(5), f(12), 0, wed16), autoplt: false})
	if !d3.Stale {
		t.Fatalf("key-dead gate must stay active while paused: %+v", d3)
	}
}

// R3 六日 checkbox:勾選時週六/週日封鎖 A→B,B→A 與手動照常;不勾=現狀不變。
func TestWeekendGate(t *testing.T) {
	hot := func(at time.Time) (x, y *domain.QuotaSnapshot) {
		x, y = healthy(at)
		x.FiveHourPct = f(95)
		return x, y
	}

	t.Run("不勾=現狀:週六照切 B", func(t *testing.T) {
		x, y := hot(sat15)
		d := evalWithCfg(t, evalIn{now: sat15, current: "A", a: x, b: y, autoplt: true}, nil)
		if d.Action != actionSwitch || d.Target != "B" {
			t.Fatalf("unchecked weekend must keep P3 flee: %+v", d)
		}
	})

	t.Run("勾選+週六+過熱:留守 A 封鎖,成因標籤=六日", func(t *testing.T) {
		x, y := hot(sat15)
		d := evalWithCfg(t, evalIn{now: sat15, current: "A", a: x, b: y, autoplt: true}, fleeWeekendCfg)
		if d.Action != actionStay || !strings.Contains(d.Reason, "封鎖") ||
			!strings.Contains(d.Reason, "六日不自動切 B") {
			t.Fatalf("weekend flee must be blocked with its cause: %+v", d)
		}
	})

	t.Run("勾選+週日深夜:仍封鎖", func(t *testing.T) {
		x, y := hot(sun23h59)
		d := evalWithCfg(t, evalIn{now: sun23h59, current: "A", a: x, b: y, autoplt: true}, fleeWeekendCfg)
		if d.Action != actionStay {
			t.Fatalf("sunday must stay blocked: %+v", d)
		}
	})

	t.Run("勾選+週六00:00:30:封鎖(精確午夜邊界)", func(t *testing.T) {
		x, y := hot(sat00h00)
		d := evalWithCfg(t, evalIn{now: sat00h00, current: "A", a: x, b: y, autoplt: true}, fleeWeekendCfg)
		if d.Action != actionStay {
			t.Fatalf("saturday 00:00 must be blocked: %+v", d)
		}
	})

	t.Run("勾選+週五 23:59:放行(邊界)", func(t *testing.T) {
		x, y := hot(fri23h59)
		d := evalWithCfg(t, evalIn{now: fri23h59, current: "A", a: x, b: y, autoplt: true}, fleeWeekendCfg)
		if d.Action != actionSwitch || d.Target != "B" {
			t.Fatalf("friday night must still flee: %+v", d)
		}
	})

	t.Run("勾選+週六 00:01:封鎖(邊界)", func(t *testing.T) {
		x, y := hot(sat00h01)
		d := evalWithCfg(t, evalIn{now: sat00h01, current: "A", a: x, b: y, autoplt: true}, fleeWeekendCfg)
		if d.Action != actionStay {
			t.Fatalf("saturday 00:01 must be blocked: %+v", d)
		}
	})

	t.Run("勾選+週一00:00:30:放行(精確午夜邊界)", func(t *testing.T) {
		x, y := hot(mon00h00)
		d := evalWithCfg(t, evalIn{now: mon00h00, current: "A", a: x, b: y, autoplt: true}, fleeWeekendCfg)
		if d.Action != actionSwitch || d.Target != "B" {
			t.Fatalf("monday 00:00 must flee again: %+v", d)
		}
	})

	t.Run("勾選+週一 00:01:放行(邊界)", func(t *testing.T) {
		x, y := hot(mon00h01)
		d := evalWithCfg(t, evalIn{now: mon00h01, current: "A", a: x, b: y, autoplt: true}, fleeWeekendCfg)
		if d.Action != actionSwitch || d.Target != "B" {
			t.Fatalf("monday 00:01 must flee again: %+v", d)
		}
	})

	// weekday 必須吃「決策當下注入的 Now」,不是額度快照的查詢時間戳:
	// 資料是週五深夜查的(5 分鐘前,仍新鮮),決策當下已是週六 → 封鎖。
	t.Run("勾選+週六00:03+快照是週五查的:仍封鎖", func(t *testing.T) {
		aFri := snap("A", f(95), f(10), 5*time.Minute, sat00h03)
		bFri := snap("B", f(5), f(12), 5*time.Minute, sat00h03)
		d := evalWithCfg(t, evalIn{now: sat00h03, current: "A", a: aFri, b: bFri, autoplt: true}, fleeWeekendCfg)
		if d.Action != actionStay {
			t.Fatalf("weekday must come from the injected Now, not snapshot timestamps: %+v", d)
		}
	})

	t.Run("勾選+週六+在 B+A 恢復:P4 照切回", func(t *testing.T) {
		d := evalWithCfg(t, evalIn{now: sat15, current: "B",
			a: recoveredA(sat15), b: snap("B", f(40), f(30), 0, sat15), autoplt: true}, fleeWeekendCfg)
		if d.Action != actionSwitch || d.Target != "A" || d.QuotaMode != "RACING" {
			t.Fatalf("weekend must NOT block P4 return: %+v", d)
		}
	})

	t.Run("勾選+週六+在 B+天花板:P5 照切回且翻 RACING", func(t *testing.T) {
		d := evalWithCfg(t, evalIn{now: sat15, current: "B",
			a: hotA(sat15), b: exhaustedB(sat15), budget: withCeiling(policy.BudgetState{}),
			autoplt: true}, fleeWeekendCfg)
		if d.Action != actionSwitch || d.Target != "A" || d.QuotaMode != "RACING" {
			t.Fatalf("weekend must NOT block P5 return: %+v", d)
		}
	})

	t.Run("勾選+週六+在 B+A 未恢復+預算OK:留守 B(不發明切回)", func(t *testing.T) {
		d := evalWithCfg(t, evalIn{now: sat15, current: "B",
			a: hotA(sat15), b: snap("B", f(40), f(10), 0, sat15),
			budget: withCeiling(policy.BudgetState{}), autoplt: true}, fleeWeekendCfg)
		if d.Action != actionStay {
			t.Fatalf("weekend block must never invent a return to A: %+v", d)
		}
	})

	t.Run("勾選+週六+手動切 B:P1 穿透", func(t *testing.T) {
		x, y := hot(sat15)
		d := evalWithCfg(t, evalIn{now: sat15, current: "A", a: x, b: y, autoplt: true,
			manual: policy.ManualCmd{Target: "B"}}, fleeWeekendCfg)
		if d.Action != actionSwitch || d.Target != "B" {
			t.Fatalf("manual must bypass weekend gate: %+v", d)
		}
	})
}

// R4 不變量:任何封鎖輪(暫停/六日)若產生 Switch,目標必為 A——封鎖只許回家、不許出走。
func TestFleeBlockSwitchTargetsOnlyA(t *testing.T) {
	type fleeScen struct {
		name  string
		build func(at time.Time) (string, *domain.QuotaSnapshot, *domain.QuotaSnapshot, policy.BudgetState)
	}
	scenarios := []fleeScen{
		{name: "在A過熱B可用", build: func(at time.Time) (string, *domain.QuotaSnapshot, *domain.QuotaSnapshot, policy.BudgetState) {
			x, y := healthy(at)
			x.FiveHourPct = f(95)
			return "A", x, y, policy.BudgetState{}
		}},
		{name: "在A過熱B僅5h不可用先恢復", build: func(at time.Time) (string, *domain.QuotaSnapshot, *domain.QuotaSnapshot, policy.BudgetState) {
			x := hotA(at)
			x.FiveHourReset = &sat15
			y := fiveBlockedB(at)
			y.FiveHourReset = &fri16
			return "A", x, y, withCeiling(policy.BudgetState{})
		}},
		{name: "在B已恢復", build: func(at time.Time) (string, *domain.QuotaSnapshot, *domain.QuotaSnapshot, policy.BudgetState) {
			return "B", recoveredA(at), snap("B", f(40), f(30), 0, at), policy.BudgetState{}
		}},
		{name: "在B天花板", build: func(at time.Time) (string, *domain.QuotaSnapshot, *domain.QuotaSnapshot, policy.BudgetState) {
			return "B", hotA(at), exhaustedB(at), withCeiling(policy.BudgetState{})
		}},
	}
	blocks := []struct {
		name  string
		input func(in evalIn) evalIn
		cfg   func(*config.Config)
	}{
		{name: "暫停", input: func(in evalIn) evalIn { in.autoplt = false; return in }, cfg: nil},
		{name: "六日", input: func(in evalIn) evalIn { return in }, cfg: fleeWeekendCfg},
	}
	for _, block := range blocks {
		for _, s := range scenarios {
			at := sat15 // 六日案例鎖週六;暫停案例週六也成立(方向性與日期正交)
			current, x, y, budget := s.build(at)
			in := block.input(evalIn{now: at, current: current, a: x, b: y, budget: budget, autoplt: true})
			d := evalWithCfg(t, in, block.cfg)
			if d.Action == actionSwitch && d.Target != "A" {
				t.Fatalf("%s+%s: blocked round must never switch to %s: %+v", block.name, s.name, d.Target, d)
			}
		}
	}
}
