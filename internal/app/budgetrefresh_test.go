package app_test

// 驗收測試(黑箱)——2026-11-16 計畫 R4:存檔即時重算 + Status 帶出天花板。
// 規格:docs/blueprints/2026-11-16-cc-fuse-budget-ui.md §2.4(定稿:分路落帳、
// dirty 只在 Tick 清、冷啟新鮮門檻)。決策錄案:存檔後 Status 立刻反映新天花板;
// 資料過期時存檔 → 記 dirty,資料恢復後補算。

import (
	"context"
	"testing"
	"time"

	"cc-fuse/internal/config"
	"cc-fuse/internal/domain"
	"cc-fuse/internal/policy"
)

var (
	settleAt = time.Date(2026, 11, 19, 15, 0, 0, 0, time.Local) // 週四 15:00(結算時刻)
	resetFri = time.Date(2026, 11, 20, 15, 0, 0, 0, time.Local) // 本週週五 15:00(B 的下個重置)
)

// Status 必須帶出 persisted 的今日天花板(卡片資料來源)。
func TestStatusCarriesCeilingToday(t *testing.T) {
	mon, sw, fl, nt := &fakeMonitor{}, &fakeSwitcher{}, &fakeFlipper{}, &fakeNotifier{}
	l := newLoop(mon, sw, fl, nt)
	l.SeedBudget(policy.BudgetState{CeilingToday: f(42.5)})
	st := l.Status()
	if st.CeilingToday == nil || *st.CeilingToday != 42.5 {
		t.Fatalf("Status 必須帶 CeilingToday:%+v", st.CeilingToday)
	}
}

// 存檔(UpdateConfig)→ RefreshBudget() → Status 立刻反映新天花板的數學鏈:
// 結算於週四 15:00(基準 20、日界週五 00:00、錨週五 15:00;2026-11-19 D1
// 含頭含尾:天數 = [週四, 週五重置日] = 2 → 天花板 20+(90−20)/2 = 55)
// → 總量改 50 → 重算 = 20+(50−20)/2 = 35,日界與基準不動。
func TestRefreshBudgetImmediate(t *testing.T) {
	b := &domain.QuotaSnapshot{
		ProfileID: "B", FiveHourPct: f(5), WeeklyPct: f(20),
		WeeklyReset: &resetFri, QueriedAt: settleAt,
	}
	mon := &fakeMonitor{a: fresh("A", f(10), f(10), settleAt), b: b}
	l := newLoop(mon, swNoop(), flNoop(), ntNoop())
	if r := l.Tick(context.Background(), settleAt); !r.BudgetSaved {
		t.Fatalf("冷啟結算必須落帳:%+v", r)
	}
	if st := l.Status(); st.CeilingToday == nil || *st.CeilingToday != 55 {
		t.Fatalf("結算後天花板=20+(90−20)/2=55(週四+週五重置日):%+v", st.CeilingToday)
	}

	newCfg := config.Default()
	newCfg.Budget.WeeklyTotalPct = 50
	l.UpdateConfig(newCfg)
	l.RefreshBudget(settleAt.Add(30 * time.Minute)) // 同預算日內存檔

	if st := l.Status(); st.CeilingToday == nil || *st.CeilingToday != 35 {
		t.Fatalf("存檔後 Status 必須立刻反映 20+(50−20)/2=35:%+v", st.CeilingToday)
	}
}

// 資料過期時存檔 → 記 dirty、不錨定過期數字;資料恢復 → RefreshBudget 直接補算。
func TestDirtyLatchRetriesWhenFresh(t *testing.T) {
	staleB := &domain.QuotaSnapshot{
		ProfileID: "B", FiveHourPct: f(5), WeeklyPct: f(20),
		WeeklyReset: &resetFri, QueriedAt: settleAt.Add(-15 * time.Minute), // > 過期門檻 10 分
	}
	mon := &fakeMonitor{a: fresh("A", f(10), f(10), settleAt), b: staleB}
	l := newLoop(mon, swNoop(), flNoop(), ntNoop())

	// 過期輪:引擎不做決策、不結算 → 天花板缺席。
	l.Tick(context.Background(), settleAt)
	if st := l.Status(); st.CeilingToday != nil {
		t.Fatalf("過期輪不得結算:%+v", st.CeilingToday)
	}

	// 使用者此時存檔 → 冷啟重算因資料過期跳過(記 dirty),天花板仍缺席。
	l.RefreshBudget(settleAt)
	if st := l.Status(); st.CeilingToday != nil {
		t.Fatalf("過期資料不得被存檔觸發的結算錨定:%+v", st.CeilingToday)
	}

	// 資料在監看器端恢復了,但 Loop 快取仍是最後一輪的過期樣本(額度快取
	// 只由 Tick 的查詢更新)→ 直接補算依然跳過、dirty 續存,不得用過期快取錨定。
	mon.b = &domain.QuotaSnapshot{
		ProfileID: "B", FiveHourPct: f(5), WeeklyPct: f(20),
		WeeklyReset: &resetFri, QueriedAt: settleAt,
	}
	l.RefreshBudget(settleAt)
	if st := l.Status(); st.CeilingToday != nil {
		t.Fatalf("快取仍過期,直接補算必須續跳:%+v", st.CeilingToday)
	}

	// 下一輪輪詢:查到新鮮資料 → 例行冷啟結算落帳並清 dirty → 天花板到場。
	if r := l.Tick(context.Background(), settleAt.Add(time.Minute)); !r.BudgetSaved {
		t.Fatalf("資料恢復後例行輪必須結算:%+v", r)
	}
	if st := l.Status(); st.CeilingToday == nil || *st.CeilingToday != 55 {
		t.Fatalf("輪詢補算:天花板 55(20+(90−20)/2,D1 含重置日):%+v", st.CeilingToday)
	}
}

// 資料恢復後走 Tick 也能補算(例行輪的冷啟結算),且不再重複觸發 dirty 補算。
func TestDirtyLatchTickAlsoHeals(t *testing.T) {
	staleB := &domain.QuotaSnapshot{
		ProfileID: "B", FiveHourPct: f(5), WeeklyPct: f(20),
		WeeklyReset: &resetFri, QueriedAt: settleAt.Add(-15 * time.Minute),
	}
	mon := &fakeMonitor{a: fresh("A", f(10), f(10), settleAt), b: staleB}
	l := newLoop(mon, swNoop(), flNoop(), ntNoop())
	l.Tick(context.Background(), settleAt)
	l.RefreshBudget(settleAt) // dirty 記上

	mon.b = &domain.QuotaSnapshot{
		ProfileID: "B", FiveHourPct: f(5), WeeklyPct: f(20),
		WeeklyReset: &resetFri, QueriedAt: settleAt,
	}
	if r := l.Tick(context.Background(), settleAt.Add(time.Minute)); !r.BudgetSaved {
		t.Fatalf("資料恢復後例行輪必須結算:%+v", r)
	}
	if st := l.Status(); st.CeilingToday == nil {
		t.Fatalf("Tick 補算後天花板必須在場")
	}
}

// Q4 定案(2026-11-16):結算過但資料隨後過期 → 昨日天花板照舊在 Status
// (過期告知交給既有「額度資料過期」提示列,卡片不加註、不隱藏)。
func TestStaleRoundKeepsYesterdayCeiling(t *testing.T) {
	b := &domain.QuotaSnapshot{
		ProfileID: "B", FiveHourPct: f(5), WeeklyPct: f(20),
		WeeklyReset: &resetFri, QueriedAt: settleAt,
	}
	mon := &fakeMonitor{a: fresh("A", f(10), f(10), settleAt), b: b}
	l := newLoop(mon, swNoop(), flNoop(), ntNoop())
	if r := l.Tick(context.Background(), settleAt); !r.BudgetSaved {
		t.Fatalf("首輪冷啟結算必須落帳:%+v", r)
	}

	// 資料老化過門檻 → 過期輪:不結算、信封不動。
	mon.a = fresh("A", f(10), f(10), settleAt.Add(-15*time.Minute))
	mon.b = &domain.QuotaSnapshot{
		ProfileID: "B", FiveHourPct: f(5), WeeklyPct: f(20),
		WeeklyReset: &resetFri, QueriedAt: settleAt.Add(-15 * time.Minute),
	}
	if r := l.Tick(context.Background(), settleAt.Add(time.Minute)); r.BudgetSaved {
		t.Fatalf("過期輪不得結算:%+v", r)
	}
	if st := l.Status(); st.CeilingToday == nil || *st.CeilingToday != 55 {
		t.Fatalf("過期時昨日天花板必須照舊在場(結算值 55,D1 含重置日):%+v", st.CeilingToday)
	}
}

// --- 既有假件的 no-op 變體(本檔用,與 acceptance_test.go 的記錄型假件無衝突) ---

func swNoop() *fakeSwitcher { return &fakeSwitcher{} }
func flNoop() *fakeFlipper  { return &fakeFlipper{} }
func ntNoop() *fakeNotifier { return &fakeNotifier{} }
