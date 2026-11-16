package app

// 白箱測試(Phase-3 審查補強):landRefresh 的信封同一性防護與分路落帳、
// 以及「dirty 只被真結算輪清」——先前這些防護是零覆蓋分支
// (審查突變 2/3:整段拆掉測試照綠)。與 acceptance_test.go(app_test)
// 不同套件,假件與輔助自備,直接摸 Loop 內部欄位以安排交錯情境。

import (
	"context"
	"errors"
	"testing"
	"time"

	"cc-fuse/internal/config"
	"cc-fuse/internal/domain"
	"cc-fuse/internal/policy"
)

var (
	mWen15 = time.Date(2026, 11, 18, 15, 0, 0, 0, time.Local)
	mThu15 = time.Date(2026, 11, 19, 15, 0, 0, 0, time.Local)
	mFri00 = time.Date(2026, 11, 20, 0, 0, 0, 0, time.Local)
	mBase  = time.Date(2026, 11, 18, 12, 0, 0, 0, time.Local)
)

func mf(v float64) *float64 { return &v }

func wbSnap(id string, five, weekly *float64, at time.Time) *domain.QuotaSnapshot {
	return &domain.QuotaSnapshot{ProfileID: id, FiveHourPct: five, WeeklyPct: weekly, QueriedAt: at}
}

type wbMonitor struct{ a, b *domain.QuotaSnapshot }

func (m *wbMonitor) Query(ctx context.Context, p domain.Profile) domain.QuotaSnapshot {
	if p.ID == "A" && m.a != nil {
		return *m.a
	}
	if p.ID == "B" && m.b != nil {
		return *m.b
	}
	return domain.QuotaSnapshot{ProfileID: p.ID, Err: errors.New("no data"), QueriedAt: time.Time{}}
}

type wbNotifier struct{}

func (wbNotifier) Notify(string, string) {}

func wbLoop(mon *wbMonitor) *Loop {
	return &Loop{
		Mon: mon, Not: wbNotifier{},
		Cfg:          config.Default(),
		Profiles:     []domain.Profile{{ID: "A", APIKey: "k-a"}, {ID: "B", APIKey: "k-b"}},
		SettingsPath: `C:\nonexistent\settings.json`,
		Current:      "A",
		Autopilot:    true,
	}
}

// 冷啟落帳:前一信封無日界 → 整顆落地。
func TestLandRefreshColdLandsWhole(t *testing.T) {
	nb := policy.BudgetState{DayBoundary: &mThu15, DayStartWeekly: mf(20), CeilingToday: mf(90)}
	merged, ok := landRefresh(policy.BudgetState{}, policy.BudgetState{}, nb)
	if !ok || merged.DayBoundary == nil || merged.CeilingToday == nil || merged.DayStartWeekly == nil {
		t.Fatalf("冷啟必須整顆落帳:%+v ok=%v", merged, ok)
	}
}

// 冷啟但 Tick 已搶先結算(prev 有日界)→ 丟棄本輪結果、維持重試。
func TestLandRefreshColdDiscardedWhenSettledMeanwhile(t *testing.T) {
	prev := policy.BudgetState{DayBoundary: &mThu15, DayStartWeekly: mf(20)}
	nb := policy.BudgetState{DayBoundary: &mFri00, DayStartWeekly: mf(25)}
	merged, ok := landRefresh(prev, policy.BudgetState{}, nb)
	if ok || merged.DayBoundary != prev.DayBoundary || *merged.DayStartWeekly != 20 {
		t.Fatalf("Tick 已結算的日界不得被冷啟結果覆寫:ok=%v merged=%+v", ok, merged)
	}
}

// 重算落帳:日界不變 → 只併入天花板,其餘欄位原封(含預警閂鎖)。
func TestLandRefreshRecomputeSplicesCeilingOnly(t *testing.T) {
	prev := policy.BudgetState{WeekAnchorTime: &mThu15, DayBoundary: &mThu15, DayStartWeekly: mf(20), WarnedFiveHour: true}
	nb := prev
	nb.CeilingToday = mf(77)
	merged, ok := landRefresh(prev, prev, nb)
	if !ok || merged.CeilingToday == nil || *merged.CeilingToday != 77 {
		t.Fatalf("重算必須併入新天花板:%v ok=%v", merged.CeilingToday, ok)
	}
	if !merged.WarnedFiveHour || merged.DayStartWeekly == nil || *merged.DayStartWeekly != 20 || merged.DayBoundary != prev.DayBoundary {
		t.Fatalf("其餘欄位必須原封:%+v", merged)
	}
}

// 重算但日界已被 Tick 換掉 → 丟棄、維持重試。
func TestLandRefreshRecomputeDiscardedWhenBoundaryMoved(t *testing.T) {
	prev := policy.BudgetState{WeekAnchorTime: &mThu15, DayBoundary: &mFri00, DayStartWeekly: mf(20)}
	snap := policy.BudgetState{WeekAnchorTime: &mThu15, DayBoundary: &mThu15, DayStartWeekly: mf(20)}
	merged, ok := landRefresh(prev, snap, prev)
	if ok || merged.CeilingToday != nil || merged.DayBoundary != prev.DayBoundary {
		t.Fatalf("日界不符必須丟棄重算結果:ok=%v merged=%+v", ok, merged)
	}
}

// 複審突變3:dirty 只被「真結算」輪清——僅改預警旗標的整顆覆寫輪
// (ensureEmitted 帶舊信封)不得清,否則自癒鏈在存檔與 Tick 交錯時斷線。
func TestTickWarnRoundKeepsDirty(t *testing.T) {
	mon := &wbMonitor{a: wbSnap("A", mf(85), mf(10), mBase), b: wbSnap("B", mf(5), mf(12), mBase)}
	l := wbLoop(mon)
	l.budgetDirty = true // 模擬:資料過期時存檔留下的待重算閂鎖

	// 預算日界設在未來 → 本輪不結算;A 5h 85 跨預警線 80 → 預警輪
	// (dec.Budget 非 nil 但 Settled=false)。
	future := mBase.Add(2 * time.Hour)
	l.SeedBudget(policy.BudgetState{DayBoundary: &future, DayStartWeekly: mf(10)})
	l.Tick(context.Background(), mBase)
	if !l.budgetDirty {
		t.Fatalf("預警輪不得清 dirty(清了自癒鏈就斷)")
	}

	// 緊接的結算輪(日界已過 → 冷啟重結算,Settled=true)→ 清。
	past := mBase.Add(-1 * time.Hour)
	l.SeedBudget(policy.BudgetState{DayBoundary: &past, DayStartWeekly: mf(10)})
	l.Tick(context.Background(), mBase.Add(time.Minute))
	if l.budgetDirty {
		t.Fatalf("真結算輪必須清 dirty")
	}
}
