package policy

// 單元測試(內部白箱)——2026-11-19 D1/D2 的分支與邊界補釘:驗收檔
// (budgetday_acceptance_test.go)釘文件條目 A1–A6,本檔補上驗收未直接釘住的
// daysLeftInWindow / nextBoundary 分支:無錨點兜底、錨點前滾(恰等於瞬間 /
// 已過瞬間)、邊界鏈貼錨點的「恰在瞬間」+24h。宇宙沿用 2026-11
// (20=五、21=六、22=日、27=五)。

import (
	"testing"
	"time"
)

var (
	uThu19_1457 = time.Date(2026, 11, 19, 14, 57, 0, 0, time.Local) // 週四,恰在錨點鐘點
	uFri20_1000 = time.Date(2026, 11, 20, 10, 0, 0, 0, time.Local)  // 週五(重置日)上午
	uFri20_1457 = time.Date(2026, 11, 20, 14, 57, 0, 0, time.Local) // 本週週錨瞬間
	uSat21_1000 = time.Date(2026, 11, 21, 10, 0, 0, 0, time.Local)  // 週六(錨點已過)
)

func uAnchor() *time.Time { t := uFri20_1457; return &t }

// U1(D1 分支 anchor==nil):無錨點 → 設計的 7 天盲分;workdays 開關不影響此兜底。
func TestDaysLeftNilAnchorBlindSplit(t *testing.T) {
	for _, wk := range []bool{true, false} {
		if got := daysLeftInWindow(uFri20_1000, nil, wk); got != 7 {
			t.Fatalf("anchor nil: daysLeftInWindow = %d, want 7 (workdays=%v)", got, wk)
		}
	}
}

// U2(修訂 3 切片語意,now 恰在錨點瞬間 = 新週 day1 起點):錨點瞬間 == now
// → 前滾一整週;切片 [五→六](併入 day1 起點)、[六→日]、[日→一] 於
// workdays 模式併入/跳過 → 工作日切片 5 片(day1 Fri→Mon、day2 Mon→Tue、
// day3 Tue→Wed、day4 Wed→Thu、day5 Thu→Fri;使用者實例逐字)、日曆切片 7 片。
func TestDaysLeftRollsAtExactAnchorInstant(t *testing.T) {
	if got := daysLeftInWindow(uFri20_1457, uAnchor(), true); got != 5 {
		t.Fatalf("roll at exact instant: daysLeft = %d, want 5 (切片語意 5 個預算日)", got)
	}
	if got := daysLeftInWindow(uFri20_1457, uAnchor(), false); got != 7 {
		t.Fatalf("roll at exact instant: daysLeft = %d, want 7 (日曆切片)", got)
	}
}

// U3(D1 前滾迴圈,錨點瞬間已過):now 週六、錨點週五 → 前滾一週,
// 視窗 [六21, 五27] 含頭尾 = 日曆 7 天、工作日 5 天。
func TestDaysLeftRollsPastAnchorInstant(t *testing.T) {
	if got := daysLeftInWindow(uSat21_1000, uAnchor(), true); got != 5 {
		t.Fatalf("roll past instant: daysLeft = %d, want 5 ([六21,五27] 工作日)", got)
	}
	if got := daysLeftInWindow(uSat21_1000, uAnchor(), false); got != 7 {
		t.Fatalf("roll past instant: daysLeft = %d, want 7 ([六21,五27] 日曆天)", got)
	}
}

// U5(D1 含尾邊界 !d.After(end),單元素視窗):now 與錨點同日(重置日晨間)
// → [五20, 五20] 恰 1 日;workdays=false 路徑照數(週末排除分支不在此例)。
func TestDaysLeftSingleDayWindowInclusive(t *testing.T) {
	if got := daysLeftInWindow(uFri20_1000, uAnchor(), false); got != 1 {
		t.Fatalf("single-day window: daysLeft = %d, want 1 ([五20,五20] 含尾)", got)
	}
}

// U4(D2 錨點優先 + 共用尾巴 !b.After(now) 恰在瞬間):now 恰在錨點鐘點、
// 舊日界也在場 → 錨點分支勝出(不走日界+24h),當日鐘點恰等於 now →
// 尾巴 +24h 一次 → 翌日同一鐘點(本週週五 14:57 = 錨點瞬間本身)。
func TestNextBoundaryAnchorWinsAtExactInstant(t *testing.T) {
	friMid := time.Date(2026, 11, 20, 0, 0, 0, 0, time.Local) // 舊午夜鏈日界(誤導項:會給 11-21 00:00)
	b := nextBoundary(Input{
		Now:    uThu19_1457,
		Budget: BudgetState{WeekAnchorTime: uAnchor(), DayBoundary: &friMid},
	})
	if b == nil || !b.Equal(uFri20_1457) {
		t.Fatalf("next = %v, want 週五 11-20 14:57(錨點分支勝出,恰在瞬間 → +24h 一次)", b)
	}
}
