package policy_test

// 驗收測試(黑箱)——2026-11-16 計畫:R2 天花板強制切總開關 + R4 存檔即時重算。
// 規格:docs/blueprints/2026-11-16-cc-fuse-budget-ui.md §2.1/§2.4(定稿含複審修正:
// 分路落帳、nil 防護、ref=DayBoundary−24h、冷啟新鮮門檻)。使用者決策錄案:
// 開關關=天花板完全不影響切換(P5/P6/bAvailable 三處);重算天數照錨定預算日數;
// 冷啟結算要求 B 資料新鮮(≤StaleAfterMin)。

import (
	"math"
	"testing"
	"time"

	"cc-fuse/internal/config"
	"cc-fuse/internal/domain"
	"cc-fuse/internal/policy"
)

// --- 本檔專用宇宙補充(沿用 acceptance_test.go 的 2026-11 固定宇宙) ---

var (
	wed23_50 = time.Date(2026, 11, 18, 23, 50, 0, 0, time.Local) // 預算日 [wed15,thu15) 跨午夜前
	thu00_10 = time.Date(2026, 11, 19, 0, 10, 0, 0, time.Local)  // 同一預算日跨午夜後
	thu14_59 = time.Date(2026, 11, 19, 14, 59, 0, 0, time.Local) // 同一預算日尾端
	thu10    = time.Date(2026, 11, 19, 10, 0, 0, 0, time.Local)
	fri15    = fri14.Add(1 * time.Hour) // 2026-11-20 15:00(=週錨)
	fri00    = time.Date(2026, 11, 20, 0, 0, 0, 0, time.Local)
)

// evaluateCfg:同 evaluate 但吃自訂 Cfg(開關/預算參數測試用)。
func evaluateCfg(t *testing.T, in evalIn, cfg config.Config) policy.Decision {
	t.Helper()
	return policy.Evaluate(policy.Input{
		Now: in.now, Current: in.current, QuotaA: in.a, QuotaB: in.b,
		Budget: in.budget, Cfg: cfg, AutopilotOn: in.autoplt,
		External: in.ext, Manual: in.manual,
	})
}

// settledBudget:已結算的信封(週三 15:00 結算:基準 20%、日界 thu15、錨本週五 15:00)。
func settledBudget(ceiling float64) policy.BudgetState {
	return policy.BudgetState{
		WeekAnchorTime: anchorFri15(),
		DayBoundary:    &thu15,
		DayStartWeekly: f(20),
		CeilingToday:   f(ceiling),
	}
}

// cfgWith 給出可調參數的預設設定檔。
func cfgWith(total float64, enforce bool) config.Config {
	c := config.Default()
	c.Budget.WeeklyTotalPct = total
	c.Budget.EnforceDailyCeiling = enforce
	return c
}

// --- R2:開關關 = 天花板完全不影響切換(P5/P6/bAvailable 三處) ---

// P5:在 B 上燒過天花板,開=切回 A(既有行為釘住);關=留在 B。
func TestToggleOff_P5Skipped(t *testing.T) {
	in := evalIn{
		now: wed16, current: "B", autoplt: true,
		a:      snap("A", f(80), f(40), 0, wed16),
		b:      snap("B", f(50), f(31), 0, wed16), // 週31% ≥ 天花板30%
		budget: settledBudget(30),
	}
	if d := evaluate(t, in); d.Action != policy.ActionSwitch || d.Target != "A" {
		t.Fatalf("開關開(預設):燒過天花板必切回 A,got %+v", d)
	}
	if d := evaluateCfg(t, in, cfgWith(90, false)); d.Action != policy.ActionStay {
		t.Fatalf("開關關:P5 必須跳過,留在 B,got %+v", d)
	}
}

// bAvailable:開關關時 A 過熱、B 週%過天花板 → B 仍是可用避難所(P3 直切 B)。
func TestToggleOff_BStillRefuge(t *testing.T) {
	in := evalIn{
		now: wed16, current: "A", autoplt: true,
		a:      snap("A", f(95), f(40), 0, wed16),
		b:      snap("B", f(10), f(95), 0, wed16), // 5h 可用、週% 遠過天花板
		budget: settledBudget(30),
	}
	if d := evaluateCfg(t, in, cfgWith(90, false)); d.Action != policy.ActionSwitch || d.Target != "B" {
		t.Fatalf("開關關:B 仍是避難所(A 過熱必切 B),got %+v", d)
	}
}

// P6:開關關時 B 5h 也受阻 → 仲裁照比重置時刻(不因預算喪失候選資格)。
func TestToggleOff_P6ComparesResets(t *testing.T) {
	a := snap("A", f(95), f(40), 0, wed16)
	b := snap("B", f(95), f(95), 0, wed16)
	bRes := wed15.Add(-12 * time.Hour) // B 的 5h 重置較早(週三 03:00)
	aRes := thu15                      // A 的較晚
	a.FiveHourReset, b.FiveHourReset = &aRes, &bRes
	in := evalIn{now: wed16, current: "A", autoplt: true, a: a, b: b, budget: settledBudget(30)}
	if d := evaluate(t, in); d.Action != policy.ActionStay {
		t.Fatalf("開關開(預設):B 預算耗盡喪失候選資格 → 留 A,got %+v", d)
	}
	if d := evaluateCfg(t, in, cfgWith(90, false)); d.Action != policy.ActionSwitch || d.Target != "B" {
		t.Fatalf("開關關:B 照常比 5h 重置(B 較早 → 切 B 等重置),got %+v", d)
	}
}

// --- R4:RefreshBudget(定稿語意) ---

// 重算:改總量 90→60 → 天花板換新;基準/日界/錨點不動;不需 B 快照。
// 天數=2(週三、週四;daysLeftInWindow 數到 midnight(錨點),週五當天不算)。
func TestRefreshBudget_RecomputeNewTotal(t *testing.T) {
	in := policy.Input{
		Now: wed16, Budget: settledBudget(55), Cfg: cfgWith(60, true),
		QuotaB: nil, // 重算路徑不吃 B
	}
	got, ok := policy.RefreshBudget(in)
	if !ok {
		t.Fatalf("重算路徑必須可算(不需 B 快照)")
	}
	exp := 20.0 + (60.0-20.0)/2.0
	if math.Abs(derefP(got.CeilingToday)-exp) > 1e-9 {
		t.Fatalf("ceiling = %v, want %v", derefP(got.CeilingToday), exp)
	}
	if derefP(got.DayStartWeekly) != 20 || got.DayBoundary == nil || !got.DayBoundary.Equal(thu15) {
		t.Fatalf("基準與日界不得動:%+v", got)
	}
	if got.WeekAnchorTime == nil || !got.WeekAnchorTime.Equal(*anchorFri15()) {
		t.Fatalf("錨點不得動:%+v", got)
	}
}

// Q1 冪等性:同一預算日內跨午夜(23:50 vs 翌日 00:10)存檔 → 同天花板。
func TestRefreshBudget_IntraDayIdempotent(t *testing.T) {
	mk := func(now time.Time) policy.Input {
		return policy.Input{Now: now, Budget: settledBudget(55), Cfg: cfgWith(60, true)}
	}
	a, oka := policy.RefreshBudget(mk(wed23_50))
	b, okb := policy.RefreshBudget(mk(thu00_10))
	if !oka || !okb {
		t.Fatalf("both must be ok: %v %v", oka, okb)
	}
	if derefP(a.CeilingToday) != derefP(b.CeilingToday) {
		t.Fatalf("跨午夜存檔結果漂移:%v vs %v", derefP(a.CeilingToday), derefP(b.CeilingToday))
	}
}

// 日中不放寬性質:結算後同一預算日內任何時刻重存(設定不變)→ 天花板 ≤ 結算值(此例恆等)。
func TestRefreshBudget_NeverRelaxes(t *testing.T) {
	settled := 20.0 + (90.0-20.0)/2.0 // 結算於 wed15:剩 2 個預算日(週三、週四)
	for _, now := range []time.Time{wed16, wed23_50, thu00_10, thu14_59} {
		st, ok := policy.RefreshBudget(policy.Input{
			Now: now, Budget: settledBudget(settled), Cfg: cfgWith(90, true),
		})
		if !ok {
			t.Fatalf("now=%v 必須可算", now)
		}
		c := derefP(st.CeilingToday)
		if c > settled+1e-9 {
			t.Fatalf("now=%v 天花板被放寬:%v > 結算值 %v", now, c, settled)
		}
		if math.Abs(c-settled) > 1e-9 {
			t.Fatalf("now=%v 準點結算日內重存應恆等:%v vs %v", now, c, settled)
		}
	}
}

// Q1:工作日開關改了 → 天數以新設定按 ref 重算,當天生效(此例:切到日曆天 → 更緊)。
func TestRefreshBudget_WorkdaysFlipSameDay(t *testing.T) {
	budget := policy.BudgetState{
		WeekAnchorTime: &nextFri, DayBoundary: &sat15, DayStartWeekly: f(10), CeilingToday: f(26),
	}
	c := config.Default() // WorkdaysOnly=true(5 天:五、一~四)
	c.Budget.EnforceDailyCeiling = true
	got, ok := policy.RefreshBudget(policy.Input{Now: fri16, Budget: budget, Cfg: c})
	if !ok || math.Abs(derefP(got.CeilingToday)-26) > 1e-9 {
		t.Fatalf("同設定重算應保持 10+80/5=26:%v ok=%v", derefP(got.CeilingToday), ok)
	}
	c.Budget.WorkdaysOnly = false // 改成日曆天(7 天)→ 當天生效
	got, ok = policy.RefreshBudget(policy.Input{Now: fri16, Budget: budget, Cfg: c})
	exp := 10.0 + (90.0-10.0)/7.0
	if !ok || math.Abs(derefP(got.CeilingToday)-exp) > 1e-9 {
		t.Fatalf("工作日開關當天生效:ceiling=%v want %v ok=%v", derefP(got.CeilingToday), exp, ok)
	}
}

// 冷啟+新鮮 → 等價完整結算(整顆信封:基準/日界/錨點/天花板齊備)。
func TestRefreshBudget_ColdStartFresh(t *testing.T) {
	b := &domain.QuotaSnapshot{
		ProfileID: "B", FiveHourPct: f(5), WeeklyPct: f(25),
		WeeklyReset: &fri15, QueriedAt: thu10.Add(-time.Minute),
	}
	got, ok := policy.RefreshBudget(policy.Input{
		Now: thu10, Budget: policy.BudgetState{}, QuotaB: b, Cfg: config.Default(),
	})
	if !ok {
		t.Fatalf("冷啟+新鮮必須結算")
	}
	if derefP(got.DayStartWeekly) != 25 {
		t.Fatalf("DayStartWeekly=25:%+v", got)
	}
	if got.DayBoundary == nil || !got.DayBoundary.Equal(fri00) {
		t.Fatalf("DayBoundary=翌日 00:00:%+v", got.DayBoundary)
	}
	if got.WeekAnchorTime == nil || !got.WeekAnchorTime.Equal(fri15) {
		t.Fatalf("錨點=B 的下個重置:%+v", got.WeekAnchorTime)
	}
	if math.Abs(derefP(got.CeilingToday)-90) > 1e-9 { // 25+(90−25)/1
		t.Fatalf("ceiling=90:%v", derefP(got.CeilingToday))
	}
}

// Q2:冷啟但 B 資料過期 → ok=false、信封原樣(過期數字不得錨成整天基準)。
func TestRefreshBudget_ColdStartStaleSkipped(t *testing.T) {
	b := &domain.QuotaSnapshot{
		ProfileID: "B", FiveHourPct: f(5), WeeklyPct: f(25),
		WeeklyReset: &fri15, QueriedAt: thu10.Add(-15 * time.Minute), // > StaleAfterMin(10)
	}
	seed := policy.BudgetState{WarnedFiveHour: true}
	got, ok := policy.RefreshBudget(policy.Input{
		Now: thu10, Budget: seed, QuotaB: b, Cfg: config.Default(),
	})
	if ok {
		t.Fatalf("過期資料不得錨定:%+v", got)
	}
	if got.WarnedFiveHour != true || got.DayStartWeekly != nil {
		t.Fatalf("ok=false 時信封必須原樣退回:%+v", got)
	}
}

// 冷啟但 B 缺/無週% → ok=false。
func TestRefreshBudget_ColdStartMissingB(t *testing.T) {
	if _, ok := policy.RefreshBudget(policy.Input{Now: thu10, Budget: policy.BudgetState{}, QuotaB: nil, Cfg: config.Default()}); ok {
		t.Fatalf("B 缺 → 不可算")
	}
	b := &domain.QuotaSnapshot{ProfileID: "B", QueriedAt: thu10} // 無週%
	if _, ok := policy.RefreshBudget(policy.Input{Now: thu10, Budget: policy.BudgetState{}, QuotaB: b, Cfg: config.Default()}); ok {
		t.Fatalf("B 無週百分比 → 不可算")
	}
}

// 複審 K1(d):基準在但日界 nil(hand-edited state.json 可達)→ ok=false、不 panic。
func TestRefreshBudget_NilGuard(t *testing.T) {
	seed := policy.BudgetState{WeekAnchorTime: anchorFri15(), DayStartWeekly: f(20)}
	got, ok := policy.RefreshBudget(policy.Input{
		Now: wed16, Budget: seed, QuotaB: snap("B", f(5), f(25), 0, wed16), Cfg: config.Default(),
	})
	if ok {
		t.Fatalf("有基準無日界 → 不可算,不得猜 ref")
	}
	if derefP(got.DayStartWeekly) != 20 || got.DayBoundary != nil {
		t.Fatalf("ok=false 信封原樣:%+v", got)
	}
}

// 複審突變1:重算路徑的每日保底與 100% 硬牆(公式複本,先前零覆蓋 —
// 同時刪掉兩者測試照綠)。
func TestRefreshBudget_RecomputeFloorAndWall(t *testing.T) {
	// 保底:基準 88、總量 90、剩 2 天 → 平均 1% < 保底 3% → 天花板 91。
	budget := policy.BudgetState{WeekAnchorTime: anchorFri15(), DayBoundary: &thu15, DayStartWeekly: f(88)}
	got, ok := policy.RefreshBudget(policy.Input{Now: wed16, Budget: budget, Cfg: cfgWith(90, true)})
	if !ok || math.Abs(derefP(got.CeilingToday)-91) > 1e-9 {
		t.Fatalf("每日保底應墊到 3(天花板 91):ceiling=%v ok=%v", derefP(got.CeilingToday), ok)
	}
	// 硬牆:基準 30、總量 200 → 平均 85 → 30+85=115 → 封 100。
	budget2 := policy.BudgetState{WeekAnchorTime: anchorFri15(), DayBoundary: &thu15, DayStartWeekly: f(30)}
	got2, ok2 := policy.RefreshBudget(policy.Input{Now: wed16, Budget: budget2, Cfg: cfgWith(200, true)})
	if !ok2 || derefP(got2.CeilingToday) != 100 {
		t.Fatalf("100 真牆必須封頂:ceiling=%v ok=%v", derefP(got2.CeilingToday), ok2)
	}
}

// 複審突變6:新鮮門檻的精確邊界(恰 10 分鐘=新鮮,嚴格大於才過期;與引擎 staleGate 同款)。
func TestRefreshBudget_ColdStartFreshBoundary(t *testing.T) {
	mk := func(age time.Duration) policy.Input {
		return policy.Input{Now: thu10, Budget: policy.BudgetState{}, Cfg: config.Default(),
			QuotaB: &domain.QuotaSnapshot{
				ProfileID: "B", FiveHourPct: f(5), WeeklyPct: f(25),
				WeeklyReset: &fri15, QueriedAt: thu10.Add(-age),
			}}
	}
	if _, ok := policy.RefreshBudget(mk(10 * time.Minute)); !ok {
		t.Fatalf("恰在門檻上的樣本必須視為新鮮(嚴格大於才過期)")
	}
	if _, ok := policy.RefreshBudget(mk(10*time.Minute + time.Millisecond)); ok {
		t.Fatalf("超過門檻必須拒絕")
	}
}

// 複審指出的未規範反向組合:有日界無基準(hand-mangled)→ 走冷啟路徑整顆重結算。
func TestRefreshBudget_BoundaryWithoutBaselineRunsCold(t *testing.T) {
	budget := policy.BudgetState{DayBoundary: &thu15}
	b := &domain.QuotaSnapshot{ProfileID: "B", FiveHourPct: f(5), WeeklyPct: f(25), WeeklyReset: &fri15, QueriedAt: thu10.Add(-time.Minute)}
	got, ok := policy.RefreshBudget(policy.Input{Now: thu10, Budget: budget, QuotaB: b, Cfg: config.Default()})
	if !ok || derefP(got.DayStartWeekly) != 25 || got.DayBoundary == nil {
		t.Fatalf("有日界無基準 → 冷啟重結算(整顆信封):%+v ok=%v", got, ok)
	}
}
