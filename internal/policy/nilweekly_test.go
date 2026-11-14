package policy_test

// 白箱補釘(實測回饋 2026-11-15):A 無週窗口(GLM default 方案的額度 API
// 不回傳週 bucket,WeeklyPct=nil)時,P4 恢復判定不得被「週 ≤ 恢復線」卡死
// ——沒有限制=條件自動滿足,恢復只看 5 小時線。T02 驗收測試(已凍結)未釘
// 此語意,由此單元測試補上,四個案例把行為邊界全部釘死。

import (
	"strings"
	"testing"

	"cc-fuse/internal/config"
	"cc-fuse/internal/policy"
)

func TestP4RecoveryIgnoresMissingWeekly(t *testing.T) {
	cfg := config.Default()
	b := snap("B", f(10), f(20), 0, wed16)
	run := func(aFive, aWeekly *float64) policy.Decision {
		return policy.Evaluate(policy.Input{
			Now: wed16, Current: "B",
			QuotaA: snap("A", aFive, aWeekly, 0, wed16), QuotaB: b,
			Cfg: cfg, AutopilotOn: true,
		})
	}
	// nil 週 + 5h 已恢復(30 ≤ 30,含界)→ 切回 A,理由標明「無週窗口」。
	if d := run(f(30), nil); d.Action != policy.ActionSwitch || d.Target != "A" || !strings.Contains(d.Reason, "無週窗口") {
		t.Fatalf("nil weekly must not block recovery: %+v (reason %q)", d, d.Reason)
	}
	// nil 週但 5h 未恢復 → 留在 B(恢復仍須 5h 達線,不是無條件切回)。
	if d := run(f(31), nil); d.Action == policy.ActionSwitch || d.Action == policy.ActionPause {
		t.Fatalf("hot 5h must stay on B: %+v", d)
	}
	// 有週窗口且超線(61 > 60)→ 原語意不變,照樣卡住。
	if d := run(f(30), f(61)); d.Action == policy.ActionSwitch {
		t.Fatalf("weekly above recovery line must still block: %+v", d)
	}
	// 有週窗口且恰好達線(60 = 60)→ 照舊切回(含界語意不變)。
	if d := run(f(30), f(60)); d.Action != policy.ActionSwitch || d.Target != "A" {
		t.Fatalf("weekly at line must recover (inclusive): %+v", d)
	}
}
