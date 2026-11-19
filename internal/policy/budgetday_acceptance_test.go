package policy

// 驗收測試(內部白箱)——2026-11-19 需求:B 配速預算天數語意修正(含重置日)。
// 規格:docs/blueprints/2026-11-19-budget-day-inclusive-fix.md(定案 D1/D2,
// 驗收語意 A1–A6)。本檔直接釘在文件點名的函式上(daysLeftInWindow /
// nextBoundary / settleBudget / RefreshBudget),使紅燈訊號與文件條目一一對應。
//
// D1 天數含重置日(含頭含尾):daysLeftInWindow 改數 [today, anchorDate];
//    workdaysOnly 排除與「永不小於 1」夾限語意不變(A3 釘住不變式)。
// D2 結算時刻貼回錨點鐘點:nextBoundary 改錨點優先 —— 週錨已知 → 今天錨點
//    鐘點(已過則 +24h 一次);錨點未知維持現狀(有日界 → 舊+24h;無 → 當日
//    午夜)(A5 三態)。

import (
	"math"
	"testing"
	"time"

	"cc-fuse/internal/config"
	"cc-fuse/internal/domain"
)

// 需求文件的實證宇宙(2026-11;機器驗證:15=日、16=一、19=四、20=五、21=六、22=日):
// B 週錨(重置)= 週五 2026-11-20 14:57;實證日 11-19(週四)的舊午夜日界鏈卡在 11-20 00:00。
var (
	bdMon16   = time.Date(2026, 11, 16, 9, 0, 0, 0, time.Local)  // 週一
	bdThu19   = time.Date(2026, 11, 19, 11, 0, 0, 0, time.Local) // 週四(實證日)
	bdThu19E  = time.Date(2026, 11, 19, 16, 0, 0, 0, time.Local) // 週四 16:00(錨點鐘點已過)
	bdFri20   = time.Date(2026, 11, 20, 10, 0, 0, 0, time.Local) // 週五(重置日)10:00,仍在 14:57 重置前
	bdSat21   = time.Date(2026, 11, 21, 10, 0, 0, 0, time.Local) // 週六
	bdSun22   = time.Date(2026, 11, 22, 10, 0, 0, 0, time.Local) // 週日
	bdAnchor  = time.Date(2026, 11, 20, 14, 57, 0, 0, time.Local) // 週錨(重置時刻)14:57
	bdThu1457 = time.Date(2026, 11, 19, 14, 57, 0, 0, time.Local) // 週四當日錨點鐘點 14:57
	bdFriMid  = time.Date(2026, 11, 20, 0, 0, 0, 0, time.Local)   // 舊午夜鏈的日界 11-20 00:00
	bdThu15   = time.Date(2026, 11, 19, 15, 0, 0, 0, time.Local)  // 15:00 鐘點日界(舊鏈 +24h 用)
	bdFri1500 = time.Date(2026, 11, 20, 15, 0, 0, 0, time.Local)  // 週五 15:00(舊+24h 的產物)
	// (A5③ 的期望=bdFriMid:當日午夜已過 → 翌日 00:00,與 ① 的日界夾具同值。)
)

func bdF(v float64) *float64 { return &v }

func bdP(v *float64) float64 {
	if v == nil {
		return math.NaN()
	}
	return *v
}

func bdNear(a, b float64) bool { return math.Abs(a-b) <= 1e-9 }

func bdCfg(total float64) config.Config {
	c := config.Default()
	c.Budget.WeeklyTotalPct = total
	return c
}

// bdAnchorP:共用錨點指標(14:57)。
func bdAnchorP() *time.Time { t := bdAnchor; return &t }

// --- A1–A3:daysLeftInWindow(D1 含重置日;A3 釘住週末排除與夾限不變式) ---

func TestBudgetDayInclusiveDays(t *testing.T) {
	cases := []struct {
		name    string
		now     time.Time
		anchor  time.Time
		wkonly  bool
		want    int
		wantOld int // 舊語意值(文件標注「舊=N」的對照;0 = 舊新相同的不變式)
	}{
		// A1:週四含週五(重置日)→ 2(舊=1)。
		{"A1_thu_incl_reset_day", bdThu19, bdAnchor, true, 2, 1},
		// A2:同一週週一 → 一~五共 5(舊=4)。
		{"A2_mon_full_week", bdMon16, bdAnchor, true, 5, 4},
		// A3a:錨點週六、now 週五、workdays → 週六被排除 → 1(不變式,舊新相同)。
		{"A3a_weekend_excluded_unchanged", bdFri20, bdSat21, true, 1, 0},
		// A3b:now 週六、錨點週日(全週末)→ 0 → 夾成 1(不變式)。
		{"A3b_all_weekend_clamps_to_1", bdSat21, bdSun22, true, 1, 0},
		// A3c:workdays=false 含頭含尾照數:週四~週六 → 3(舊=2)。
		{"A3c_calendar_days_inclusive", bdThu19, bdSat21, false, 3, 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := tc.anchor
			got := daysLeftInWindow(tc.now, &a, tc.wkonly)
			if got != tc.want {
				t.Fatalf("daysLeftInWindow(now=%v, anchor=%v, workdays=%v) = %d, want %d (舊語意 %d)",
					tc.now, tc.anchor, tc.wkonly, got, tc.want, tc.wantOld)
			}
		})
	}
}

// --- A4:settleBudget 天花板隨新天數語意(基準 64、總量 95) ---

func TestBudgetDayInclusiveSettlement(t *testing.T) {
	mk := func(now time.Time) Input {
		return Input{
			Now: now,
			QuotaB: &domain.QuotaSnapshot{
				ProfileID: "B", FiveHourPct: bdF(5), WeeklyPct: bdF(64), QueriedAt: now,
			},
			Budget: BudgetState{WeekAnchorTime: bdAnchorP()},
			Cfg:    bdCfg(95),
		}
	}
	// days=2(週四+週五重置日)→ 64+(95−64)/2 = 79.5(舊語意 days=1 → 95)。
	st, ok := settleBudget(mk(bdThu19))
	if !ok {
		t.Fatalf("A4:結算必須可算(B 週百分比在場)")
	}
	if !bdNear(bdP(st.CeilingToday), 79.5) {
		t.Fatalf("A4:ceiling = %v, want 79.5(=64+31/2,含重置日 days=2)", bdP(st.CeilingToday))
	}
	if !bdNear(bdP(st.DayStartWeekly), 64) {
		t.Fatalf("A4:基準 = %v, want 64", bdP(st.DayStartWeekly))
	}
	// days=1(週五重置日當天、錨點鐘點前)→ 64+31 = 95(與舊語意相同的不變式;
	// 夾限:95 < hardWall 100,每日保底路徑不在此例)。
	st1, ok1 := settleBudget(mk(bdFri20))
	if !ok1 {
		t.Fatalf("A4:重置日晨間結算必須可算")
	}
	if !bdNear(bdP(st1.CeilingToday), 95) {
		t.Fatalf("A4:重置日 days=1 ceiling = %v, want 95", bdP(st1.CeilingToday))
	}
}

// --- A5:nextBoundary 三態(D2 錨點優先) ---

func TestBudgetDayInclusiveNextBoundary(t *testing.T) {
	// ① 既有午夜日界 + 已知錨點 → 貼錨點鐘點(舊語意會給 sat00 的舊+24h 鏈)。
	// ①a now=週四 11:00(14:57 未過)→ 當日(週四)14:57。
	b1 := nextBoundary(Input{Now: bdThu19, Budget: BudgetState{WeekAnchorTime: bdAnchorP(), DayBoundary: &bdFriMid}})
	if b1 == nil || !b1.Equal(bdThu1457) {
		t.Fatalf("A5①a:next = %v, want 週四 14:57(貼錨點鐘點)", b1)
	}
	// ①b now=週四 16:00(14:57 已過)→ 當日 14:57 +24h 一次 → 週五 14:57(=錨點本身)。
	b2 := nextBoundary(Input{Now: bdThu19E, Budget: BudgetState{WeekAnchorTime: bdAnchorP(), DayBoundary: &bdFriMid}})
	if b2 == nil || !b2.Equal(bdAnchor) {
		t.Fatalf("A5①b:next = %v, want 週五 14:57(已過 +24h 一次)", b2)
	}
	// ② 錨點 nil + 有日界 → 舊+24h(維持現狀;日界 15:00 +24h = 週五 15:00)。
	b3 := nextBoundary(Input{Now: bdThu19E, Budget: BudgetState{DayBoundary: &bdThu15}})
	if b3 == nil || !b3.Equal(bdFri1500) {
		t.Fatalf("A5②:next = %v, want 週五 15:00(無錨點維持舊+24h)", b3)
	}
	// ③ 兩者皆 nil → 當日午夜(週四 00:00 已過 → 翌日=週五 00:00;維持現狀的不變式)。
	b4 := nextBoundary(Input{Now: bdThu19, Budget: BudgetState{}})
	if b4 == nil || !b4.Equal(bdFriMid) {
		t.Fatalf("A5③:next = %v, want 週五 00:00(兜底午夜鏈,維持現狀)", b4)
	}
}

// --- A6:RefreshBudget 重算路徑走同一個 daysLeftInWindow(實證場景) ---

func TestBudgetDayInclusiveRefreshRecompute(t *testing.T) {
	mk := func(now time.Time) Input {
		return Input{
			Now: now,
			Budget: BudgetState{
				DayStartWeekly: bdF(64),
				DayBoundary:    &bdFriMid, // 舊午夜鏈 11-20 00:00(部署當下的存量)
				WeekAnchorTime: bdAnchorP(),
			},
			Cfg: bdCfg(95),
		}
	}
	// ref = 日界−24h = 週四 00:00 → days=2(含週五重置日)→ 79.5(收緊現狀 95;
	// 「日中不放寬」針對同公式重算,公式改版造成的收緊是本需求要的效果)。
	for _, now := range []time.Time{bdThu19, bdThu19E} {
		got, ok := RefreshBudget(mk(now))
		if !ok {
			t.Fatalf("A6:now=%v 重算必須可算", now)
		}
		if !bdNear(bdP(got.CeilingToday), 79.5) {
			t.Fatalf("A6:now=%v ceiling = %v, want 79.5(ref=週四,days=2 含重置日)", now, bdP(got.CeilingToday))
		}
		// 日中重算穩定性:基準/日界/錨點原封,只有天花板換新。
		if !bdNear(bdP(got.DayStartWeekly), 64) || got.DayBoundary == nil || !got.DayBoundary.Equal(bdFriMid) {
			t.Fatalf("A6:now=%v 基準與日界不得動:%+v", now, got)
		}
		if got.WeekAnchorTime == nil || !got.WeekAnchorTime.Equal(bdAnchor) {
			t.Fatalf("A6:now=%v 錨點不得動:%+v", now, got)
		}
	}
}
