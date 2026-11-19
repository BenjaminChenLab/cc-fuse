package app_test

// 單元測試(黑箱)——2026-11-19 D3a:Status 帶出下一個結算瞬間(來源
// persisted BudgetState.DayBoundary),供設定頁 B 卡天花板 chip 的
// 「Until …」前綴渲染;從未結算(無日界)→ nil,前端據此省略前綴。

import (
	"testing"
	"time"

	"cc-fuse/internal/policy"
)

// 有日界 → Status.Until 必須原樣帶出同一瞬間;無日界 → nil。
func TestStatusCarriesUntilFromDayBoundary(t *testing.T) {
	at := time.Date(2026, 11, 20, 14, 57, 0, 0, time.Local)
	l := newLoop(&fakeMonitor{}, swNoop(), flNoop(), ntNoop())

	l.SeedBudget(policy.BudgetState{DayBoundary: &at, CeilingToday: f(79.5)})
	st := l.Status()
	if st.Until == nil || !st.Until.Equal(at) {
		t.Fatalf("Status 必須帶 Until(=DayBoundary):%+v", st.Until)
	}

	l.SeedBudget(policy.BudgetState{})
	if st := l.Status(); st.Until != nil {
		t.Fatalf("無日界(未結算)→ Until 必須為 nil:%+v", st.Until)
	}
}
