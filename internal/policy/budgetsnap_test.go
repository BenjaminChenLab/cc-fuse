package policy

// 單元測試——2026-11-19 修訂 2(D6a):SnapBoundary 把停滯在歷史鏈的日界貼回
// 錨點鐘點(今天的錨點鐘點,已過則 +24h),讓「until 前綴/下次結算時刻」等於
// 計算真正的切截時刻。規格:docs/blueprints/2026-11-19-budget-day-inclusive-fix.md
// 修訂 2(A9–A11)。宇宙沿用 2026-11-19(週四)使用者實測情境。

import (
	"testing"
	"time"
)

var (
	snapThuMorning  = time.Date(2026, 11, 19, 12, 21, 0, 0, time.Local) // 週四 12:21(實測當下,錨點鐘點未過)
	snapThuEvening  = time.Date(2026, 11, 19, 16, 0, 0, 0, time.Local)  // 週四 16:00(已過錨點鐘點)
	snapFriMidnight = time.Date(2026, 11, 20, 0, 0, 0, 0, time.Local)   // 舊午夜鏈留下的日界
	snapAnchor      = time.Date(2026, 11, 20, 14, 57, 0, 0, time.Local) // 實測週五 14:57 週重置
)

// A9 helper 級:當日錨點鐘點未過 → 貼今天 14:57(不再是停滯的週五 00:00)。
func TestSnapBoundarySameDay(t *testing.T) {
	got := SnapBoundary(snapThuMorning, &snapFriMidnight, &snapAnchor)
	want := time.Date(2026, 11, 19, 14, 57, 0, 0, time.Local)
	if got == nil || !got.Equal(want) {
		t.Fatalf("日界必須貼回當日錨點鐘點 14:57,got %v", got)
	}
}

// A10 helper 級:當日錨點鐘點已過 → +24h 貼明天 14:57。
func TestSnapBoundaryRollsToTomorrow(t *testing.T) {
	got := SnapBoundary(snapThuEvening, &snapFriMidnight, &snapAnchor)
	want := time.Date(2026, 11, 20, 14, 57, 0, 0, time.Local)
	if got == nil || !got.Equal(want) {
		t.Fatalf("已過錨點鐘點必須滾到明天 14:57,got %v", got)
	}
}

// A11 helper 級:無錨點 → 日界原樣回傳(備案鏈不變)。
func TestSnapBoundaryNilAnchorKeepsBoundary(t *testing.T) {
	got := SnapBoundary(snapThuMorning, &snapFriMidnight, nil)
	if got == nil || !got.Equal(snapFriMidnight) {
		t.Fatalf("無錨點必須原樣回傳日界,got %v", got)
	}
}

// A13 helper 級:日界缺失 → 回傳 nil,不憑空造日界(K1(d) 精神;修訂 2 補強
// 把初版的「造瞬間」行為改正)。
func TestSnapBoundaryNilBoundaryReturnsNil(t *testing.T) {
	if got := SnapBoundary(snapThuMorning, nil, &snapAnchor); got != nil {
		t.Fatalf("日界缺失必須回傳 nil(不得造出日界),got %v", got)
	}
}

// A13 helper 級:now 恰等於當日錨點鐘點 → 含端點滾 +24h(audit M7 慣例)。
func TestSnapBoundaryAtExactInstantRolls(t *testing.T) {
	now := time.Date(2026, 11, 19, 14, 57, 0, 0, time.Local)
	got := SnapBoundary(now, &snapFriMidnight, &snapAnchor)
	want := time.Date(2026, 11, 20, 14, 57, 0, 0, time.Local)
	if got == nil || !got.Equal(want) {
		t.Fatalf("恰在錨點鐘點必須滾到明天 14:57,got %v", got)
	}
}

// A13 helper 級:不同錨點鐘點(09:13)同規則 — 防止對 14:57 的硬編碼蒙混。
// now 取 08:00(早於 09:13),釘「貼當日鐘點」而非「已過滾明天」的分支。
func TestSnapBoundaryDifferentClock(t *testing.T) {
	anchor0913 := time.Date(2026, 11, 20, 9, 13, 0, 0, time.Local)
	now := time.Date(2026, 11, 19, 8, 0, 0, 0, time.Local)
	got := SnapBoundary(now, &snapFriMidnight, &anchor0913)
	want := time.Date(2026, 11, 19, 9, 13, 0, 0, time.Local)
	if got == nil || !got.Equal(want) {
		t.Fatalf("日界必須貼回當日錨點鐘點 09:13,got %v", got)
	}
}
