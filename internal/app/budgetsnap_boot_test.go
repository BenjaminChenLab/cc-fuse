package app_test

// 驗收測試(黑箱)——2026-11-19 修訂 2(D6b/D6c):存檔重算與程式啟動都把
// 停滯的舊日界貼回錨點鐘點,讓卡片 until 前綴等於計算真正的切截時刻。
// 使用者實測(規格:docs/blueprints/2026-11-19-budget-day-inclusive-fix.md
// 修訂 2,A9–A11):存檔後天花板已正確 79.5%,但 until 仍顯示週五 00:00。

import (
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"cc-fuse/internal/app"
	"cc-fuse/internal/config"
	"cc-fuse/internal/policy"
	"cc-fuse/internal/store"
)

// seedStaleChain 架出使用者實測的舊午夜鏈信封(基準 64、日界週五 00:00、
// 錨 14:57、舊天花板 95),並把 config 總量設為 95(實測情境)。
func seedStaleChain(l *app.Loop) {
	cfg := config.Default()
	cfg.Budget.WeeklyTotalPct = 95
	l.UpdateConfig(cfg)
	l.SeedBudget(policy.BudgetState{
		DayStartWeekly: f(64), DayBoundary: &snapStaleFri,
		WeekAnchorTime: &snapAnchor, CeilingToday: f(95),
	})
}

var (
	snapNow      = time.Date(2026, 11, 19, 12, 21, 0, 0, time.Local) // 週四 12:21(實測當下)
	snapPast     = time.Date(2026, 11, 19, 16, 0, 0, 0, time.Local)  // 週四 16:00(已過 14:57)
	snapStaleFri = time.Date(2026, 11, 20, 0, 0, 0, 0, time.Local)   // 舊午夜鏈日界
	snapAnchor   = time.Date(2026, 11, 20, 14, 57, 0, 0, time.Local) // 實測週五 14:57 重置
)

// A9 行為級:實況重播 — 舊午夜鏈(基準 64、日界週五 00:00、錨 14:57、舊天花板 95)
// 存檔重算 → 天花板 79.5,且 envelope 日界(Status 的 until 資料源)貼回當日 14:57。
func TestRefreshBudgetLandsSnappedBoundary(t *testing.T) {
	l := newLoop(&fakeMonitor{}, swNoop(), flNoop(), ntNoop())
	seedStaleChain(l)
	l.RefreshBudget(snapNow)

	st := l.Status()
	if st.CeilingToday == nil || *st.CeilingToday != 79.5 {
		t.Fatalf("天花板必須是 64+(95−64)/2=79.5,got %v", st.CeilingToday)
	}
	want := time.Date(2026, 11, 19, 14, 57, 0, 0, time.Local)
	if st.Until == nil || !st.Until.Equal(want) {
		t.Fatalf("until 必須貼回當日 14:57(不再是停滯的週五 00:00),got %v", st.Until)
	}
}

// A10 行為級:已過當日錨點鐘點 → 日界滾到明天 14:57;天花板仍由舊預算日起點算(79.5)。
func TestRefreshBudgetSnapsPastClockToTomorrow(t *testing.T) {
	l := newLoop(&fakeMonitor{}, swNoop(), flNoop(), ntNoop())
	seedStaleChain(l)
	l.RefreshBudget(snapPast)

	st := l.Status()
	if st.CeilingToday == nil || *st.CeilingToday != 79.5 {
		t.Fatalf("天花板由舊預算日起點計算,必須仍為 79.5,got %v", st.CeilingToday)
	}
	want := time.Date(2026, 11, 20, 14, 57, 0, 0, time.Local)
	if st.Until == nil || !st.Until.Equal(want) {
		t.Fatalf("已過 14:57 時 until 必須滾到週五 14:57,got %v", st.Until)
	}
}

// A11 啟動級(D6c):state.json 載入即貼回當日錨點鐘點(期望值按規則獨立推導,
// 不綁死測試執行的真實時刻)。
func TestLoadBudgetSnapsAtBoot(t *testing.T) {
	raw, err := json.Marshal(policy.BudgetState{
		DayStartWeekly: f(64), DayBoundary: &snapStaleFri, WeekAnchorTime: &snapAnchor,
	})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "state.json")
	if err := store.SaveState(path, store.State{Budget: raw}); err != nil {
		t.Fatal(err)
	}

	b, err := app.LoadBudget(path)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	want := time.Date(now.Year(), now.Month(), now.Day(), 14, 57, 0, 0, time.Local)
	if !want.After(now) {
		want = want.Add(24 * time.Hour)
	}
	if b.DayBoundary == nil || !b.DayBoundary.Equal(want) {
		t.Fatalf("啟動載入必須把日界貼回錨點鐘點 %v,got %v", want, b.DayBoundary)
	}
}

// A11 啟動級:無錨點的 payload → 日界原樣(備案鏈不變)。
func TestLoadBudgetKeepsBoundaryWithoutAnchor(t *testing.T) {
	raw, err := json.Marshal(policy.BudgetState{DayStartWeekly: f(64), DayBoundary: &snapStaleFri})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "state.json")
	if err := store.SaveState(path, store.State{Budget: raw}); err != nil {
		t.Fatal(err)
	}

	b, err := app.LoadBudget(path)
	if err != nil {
		t.Fatal(err)
	}
	if b.DayBoundary == nil || !b.DayBoundary.Equal(snapStaleFri) {
		t.Fatalf("無錨點不得動日界,got %v", b.DayBoundary)
	}
}

// A14 啟動級:錨點在、日界缺 → 維持缺失(不憑空造日界,K1(d) 精神)。
func TestLoadBudgetKeepsNilBoundaryWithAnchor(t *testing.T) {
	raw, err := json.Marshal(policy.BudgetState{DayStartWeekly: f(64), WeekAnchorTime: &snapAnchor})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "state.json")
	if err := store.SaveState(path, store.State{Budget: raw}); err != nil {
		t.Fatal(err)
	}

	b, err := app.LoadBudget(path)
	if err != nil {
		t.Fatal(err)
	}
	if b.DayBoundary != nil {
		t.Fatalf("日界缺失時載入不得造出日界,got %v", b.DayBoundary)
	}
}

// A12 行為級:同一預算日內連續兩次存檔 → 天花板恆定 79.5、日界穩定在貼回值
// (貼回不得讓「日界−24h」的預算日起點漂移到前一天)。
func TestRefreshBudgetSecondSaveSameDayNoDrift(t *testing.T) {
	l := newLoop(&fakeMonitor{}, swNoop(), flNoop(), ntNoop())
	seedStaleChain(l)
	l.RefreshBudget(snapNow) // 第一次存檔:79.5 + 日界貼回當日 14:57

	l.RefreshBudget(snapNow.Add(30 * time.Minute)) // 同預算日內第二次存檔
	st := l.Status()
	if st.CeilingToday == nil || *st.CeilingToday != 79.5 {
		t.Fatalf("第二次存檔天花板不得漂移,必須仍為 79.5,got %v", *st.CeilingToday)
	}
	want := time.Date(2026, 11, 19, 14, 57, 0, 0, time.Local)
	if st.Until == nil || !st.Until.Equal(want) {
		t.Fatalf("第二次存檔後 until 必須穩定在當日 14:57,got %v", st.Until)
	}
}

// A13 行為級:不同錨點鐘點(18:40,晚於 now)同規則貼回 — 防止對 14:57 的硬編碼蒙混。
func TestRefreshBudgetSnapsToAnchorClockNotLiteral(t *testing.T) {
	anchor1840 := time.Date(2026, 11, 20, 18, 40, 0, 0, time.Local)
	l := newLoop(&fakeMonitor{}, swNoop(), flNoop(), ntNoop())
	cfg := config.Default()
	cfg.Budget.WeeklyTotalPct = 95
	l.UpdateConfig(cfg)
	l.SeedBudget(policy.BudgetState{
		DayStartWeekly: f(64), DayBoundary: &snapStaleFri,
		WeekAnchorTime: &anchor1840, CeilingToday: f(95),
	})
	l.RefreshBudget(snapNow)

	st := l.Status()
	want := time.Date(2026, 11, 19, 18, 40, 0, 0, time.Local)
	if st.Until == nil || !st.Until.Equal(want) {
		t.Fatalf("until 必須貼回錨點鐘點當日 18:40(非硬編碼 14:57),got %v", st.Until)
	}
}
