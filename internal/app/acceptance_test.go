package app_test

// 驗收測試(黑箱)——cc-fuse T03:主迴圈編排與輪詢節奏(可自動測部分)。
// 契約:docs/blueprints/tasks/T03-app-shell.md(Loop.Tick 編排序、LoopResult、
// NextDelay 快慢輪詢、通知一次不洗版)。所有協作者以注入假件替換。

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"cc-fuse/internal/app"
	"cc-fuse/internal/config"
	"cc-fuse/internal/domain"
	"cc-fuse/internal/policy"
	"cc-fuse/internal/store"
)

// --- 注入假件(記錄呼叫) ---

type fakeMonitor struct {
	a, b      *domain.QuotaSnapshot
	calls     int
	failAfter int       // 呼叫數超過此值後一律查詢失敗(0=永不失敗)
	log       *[]string // 跨假件共用呼叫序(驗編排順序;nil=不記錄)
}

func (m *fakeMonitor) Query(ctx context.Context, p domain.Profile) domain.QuotaSnapshot {
	m.calls++
	mark(m.log, "query")
	if m.failAfter > 0 && m.calls > m.failAfter {
		// 失敗樣本用零值時戳:相對固定決策時刻恆為「過期」,不讀牆鐘(決定論)。
		return domain.QuotaSnapshot{ProfileID: p.ID, Err: errors.New("query failed"), QueriedAt: time.Time{}}
	}
	if p.ID == "A" && m.a != nil {
		return *m.a
	}
	if p.ID == "B" && m.b != nil {
		return *m.b
	}
	return domain.QuotaSnapshot{ProfileID: p.ID, Err: errors.New("no data"), QueriedAt: time.Time{}}
}

// mark 記錄一筆呼叫到共用序;nil 序=不記錄。
func mark(l *[]string, e string) {
	if l != nil {
		*l = append(*l, e)
	}
}

type fakeSwitcher struct {
	applied []string // 被切的 profile ID 序列
	failErr error
	log     *[]string
}

func (s *fakeSwitcher) Apply(p domain.Profile, settingsPath string) error {
	if s.failErr != nil {
		return s.failErr
	}
	s.applied = append(s.applied, p.ID)
	mark(s.log, "apply")
	return nil
}

type fakeFlipper struct {
	modes []string
	log   *[]string
}

func (f *fakeFlipper) Flip(path, mode string) error {
	f.modes = append(f.modes, mode)
	mark(f.log, "flip")
	return nil
}

type fakeNotifier struct {
	msgs []string
	log  *[]string
}

func (n *fakeNotifier) Notify(title, body string) {
	n.msgs = append(n.msgs, title+" | "+body)
	mark(n.log, "notify")
}

// --- 組裝工具 ---

func profiles() []domain.Profile {
	return []domain.Profile{
		{ID: "A", Name: "team", APIKey: "key-a", BaseURL: "https://api.z.ai/api/anthropic",
			ModelEnv: map[string]string{"ANTHROPIC_DEFAULT_OPUS_MODEL": "glm-5.3"}},
		{ID: "B", Name: "mine", APIKey: "key-b", BaseURL: "https://api.z.ai/api/anthropic"},
	}
}

func newLoop(mon *fakeMonitor, sw *fakeSwitcher, fl *fakeFlipper, nt *fakeNotifier) *app.Loop {
	return &app.Loop{
		Mon: mon, Sw: sw, Qm: fl, Not: nt,
		Cfg:          config.Default(),
		Profiles:     profiles(),
		StatePath:    "",                               // 測試:記憶體態即可(BudgetSaved 語義仍驗)
		SettingsPath: "C:\\nonexistent\\settings.json", // 假 Switcher 不真寫檔
		Autopilot:    true,
		Current:      "A",
	}
}

func f(v float64) *float64 { return &v }
func fresh(id string, five, weekly *float64, now time.Time) *domain.QuotaSnapshot {
	return &domain.QuotaSnapshot{ProfileID: id, FiveHourPct: five, WeeklyPct: weekly, QueriedAt: now}
}

var base = time.Date(2026, 11, 18, 12, 0, 0, 0, time.Local)

// --- Tick 編排:健康閒置 → 查詢 2 次、零動作 ---

func TestTickHealthyIdle(t *testing.T) {
	mon := &fakeMonitor{a: fresh("A", f(10), f(10), base), b: fresh("B", f(5), f(12), base)}
	sw, fl, nt := &fakeSwitcher{}, &fakeFlipper{}, &fakeNotifier{}
	l := newLoop(mon, sw, fl, nt)

	r := l.Tick(context.Background(), base)
	if r.Switched || r.Notified {
		t.Fatalf("healthy idle must be quiet: %+v", r)
	}
	if !r.BudgetSaved {
		// 冷啟結算是 §7 的正常記帳(中途啟動以當下週%當基準),安靜輪也要落帳,
		// 否則長時間閒置期間的結算永遠到不了 state.json(重開機即失憶)。
		t.Fatalf("cold-start settlement must persist even on a quiet round: %+v", r)
	}
	if l.Current != "A" {
		t.Fatalf("current unchanged: %q", l.Current)
	}
	if mon.calls != 2 {
		t.Fatalf("must query both accounts: %d", mon.calls)
	}
	if len(sw.applied) != 0 || len(fl.modes) != 0 {
		t.Fatalf("no side effects allowed: %v %v", sw.applied, fl.modes)
	}
}

// --- Tick 編排:避難切換的完整因果鏈(查詢→決策→Apply→Flip→通知) ---

func TestTickFleeChain(t *testing.T) {
	var seq []string // 共用呼叫序:釘編排順序
	mon := &fakeMonitor{a: fresh("A", f(95), f(10), base), b: fresh("B", f(5), f(12), base), log: &seq}
	sw, fl, nt := &fakeSwitcher{log: &seq}, &fakeFlipper{log: &seq}, &fakeNotifier{log: &seq}
	l := newLoop(mon, sw, fl, nt)

	r := l.Tick(context.Background(), base)
	if !r.Switched || r.Target != "B" {
		t.Fatalf("must flee to B: %+v", r)
	}
	if l.Current != "B" {
		t.Fatalf("current must become B: %q", l.Current)
	}
	if len(sw.applied) != 1 || sw.applied[0] != "B" {
		t.Fatalf("Apply must target B: %v", sw.applied)
	}
	if len(fl.modes) != 1 || fl.modes[0] != "SAVING" {
		t.Fatalf("quota-mode must flip SAVING: %v", fl.modes)
	}
	// 編排順序(卡片契約):查詢→決策→Apply→Flip→通知。
	want := []string{"query", "query", "apply", "flip", "notify"}
	if len(seq) != len(want) {
		t.Fatalf("call sequence = %v, want %v", seq, want)
	}
	for i := range want {
		if seq[i] != want[i] {
			t.Fatalf("call order broken at %d: %v, want %v", i, seq, want)
		}
	}
	if !r.Notified || len(nt.msgs) == 0 || !strings.Contains(nt.msgs[0], "95") {
		t.Fatalf("notification must carry the reason numbers: %+v %v", r, nt.msgs)
	}
	// Reason 原樣傳遞:回傳值要帶政策引擎的觸發數字與語義,不得自造或清空。
	if !strings.Contains(r.Reason, "切換線") || !strings.Contains(r.Reason, "90") {
		t.Fatalf("LoopResult.Reason must pass through policy reason: %q", r.Reason)
	}
}

// --- Tick:Stale → 零切換、通知一次不洗版(第二次 stale tick 不再通知) ---

func TestTickStaleNotifiesOnce(t *testing.T) {
	mon := &fakeMonitor{} // 全 nil → stale
	sw, fl, nt := &fakeSwitcher{}, &fakeFlipper{}, &fakeNotifier{}
	l := newLoop(mon, sw, fl, nt)

	r1 := l.Tick(context.Background(), base)
	if r1.Switched {
		t.Fatalf("stale must not switch: %+v", r1)
	}
	if !r1.Notified {
		t.Fatalf("first stale tick notifies: %+v", r1)
	}
	r2 := l.Tick(context.Background(), base.Add(time.Minute))
	if r2.Switched || r2.Notified {
		t.Fatalf("second stale tick must stay silent (通知一次不洗版): %+v", r2)
	}
}

// --- Tick:A4 查詢暫時性失敗 → 退避重試、沿用上次數字;老化超過 staleAfterMin 才視同過期 ---

func TestTickQueryFailureReusesLastNumbers(t *testing.T) {
	mon := &fakeMonitor{a: fresh("A", f(10), f(30), base), b: fresh("B", f(5), f(12), base), failAfter: 2}
	sw, fl, nt := &fakeSwitcher{}, &fakeFlipper{}, &fakeNotifier{}
	l := newLoop(mon, sw, fl, nt)

	l.Tick(context.Background(), base) // 首輪兩筆查詢成功,留下好數字
	r := l.Tick(context.Background(), base.Add(time.Minute))
	if r.Switched || r.Notified {
		t.Fatalf("recent failure must reuse last numbers (1min < staleAfterMin): %+v", r)
	}
	r = l.Tick(context.Background(), base.Add(11*time.Minute))
	if r.Switched {
		t.Fatalf("aged-out data must not switch: %+v", r)
	}
	if !r.Notified {
		t.Fatalf("aged-out data must turn stale and notify: %+v", r)
	}
}

// --- Tick:手動指令直通(消費後清空) ---

func TestTickManualPassthrough(t *testing.T) {
	mon := &fakeMonitor{a: fresh("A", f(10), f(10), base), b: fresh("B", f(5), f(12), base)}
	sw, fl, nt := &fakeSwitcher{}, &fakeFlipper{}, &fakeNotifier{}
	l := newLoop(mon, sw, fl, nt)
	l.Pending = policy.ManualCmd{Target: "B"}

	r := l.Tick(context.Background(), base)
	if !r.Switched || r.Target != "B" {
		t.Fatalf("manual must switch: %+v", r)
	}
	if l.Pending.Target != "" {
		t.Fatalf("manual must be consumed: %+v", l.Pending)
	}
	if len(fl.modes) != 1 || fl.modes[0] != "SAVING" {
		t.Fatalf("manual→B flips SAVING (PD3): %v", fl.modes)
	}
}

// --- Tick:外部未知供應商 → 暫停一次通知,之後安靜 ---

func TestTickExternalUnknownPauses(t *testing.T) {
	mon := &fakeMonitor{a: fresh("A", f(10), f(10), base), b: fresh("B", f(5), f(12), base)}
	sw, fl, nt := &fakeSwitcher{}, &fakeFlipper{}, &fakeNotifier{}
	l := newLoop(mon, sw, fl, nt)
	l.PendingExt = policy.ExtEvent{Kind: "unknown_provider"}

	r1 := l.Tick(context.Background(), base)
	if r1.Switched || !r1.Notified {
		t.Fatalf("unknown provider pauses with notice: %+v", r1)
	}
	if l.Autopilot {
		t.Fatalf("pause must flip autopilot off: %+v", l)
	}
	r2 := l.Tick(context.Background(), base.Add(time.Minute))
	if r2.Notified {
		t.Fatalf("pause notification fires once: %+v", r2)
	}
}

// --- Tick:預算結算 → BudgetSaved=true;同刻重跑 → 不重複存 ---

func TestTickBudgetPersistence(t *testing.T) {
	// 結算情境:冷啟+當前 B,任何 tick 都會立即建立預算狀態。
	// StatePath 用真實暫存檔:§7「重開機不歸零」=預算真的落盤,不只是回傳旗標。
	mon := &fakeMonitor{a: fresh("A", f(10), f(10), base), b: fresh("B", f(5), f(10), base)}
	sw, fl, nt := &fakeSwitcher{}, &fakeFlipper{}, &fakeNotifier{}
	l := newLoop(mon, sw, fl, nt)
	l.Current = "B"
	l.StatePath = filepath.Join(t.TempDir(), "state.json")

	r1 := l.Tick(context.Background(), base)
	if !r1.BudgetSaved {
		t.Fatalf("cold-start settlement must persist budget: %+v", r1)
	}
	st, err := store.LoadState(l.StatePath)
	if err != nil || len(st.Budget) == 0 {
		t.Fatalf("budget must be on disk after settlement: err=%v budget=%q", err, st.Budget)
	}
	r2 := l.Tick(context.Background(), base.Add(time.Second))
	if r2.BudgetSaved {
		t.Fatalf("same-day re-tick must not re-save budget: %+v", r2)
	}
}

// --- NextDelay:快慢輪詢(當前帳號任一窗口 > 80 → 20s;否則 60s;資料缺失 → 60s) ---

func TestNextDelay(t *testing.T) {
	sw, fl, nt := &fakeSwitcher{}, &fakeFlipper{}, &fakeNotifier{}
	l := newLoop(&fakeMonitor{}, sw, fl, nt)

	cases := []struct {
		name    string
		current string
		a, b    *domain.QuotaSnapshot
		want    time.Duration
	}{
		{"A healthy", "A", fresh("A", f(60), f(30), base), fresh("B", f(5), f(12), base), 60 * time.Second},
		{"A 5h hot", "A", fresh("A", f(85), f(30), base), fresh("B", f(5), f(12), base), 20 * time.Second},
		{"A weekly hot", "A", fresh("A", f(60), f(85), base), fresh("B", f(5), f(12), base), 20 * time.Second},
		{"B hot while on B", "B", fresh("A", f(10), f(10), base), fresh("B", f(90), f(12), base), 20 * time.Second},
		{"B hot while on A", "A", fresh("A", f(10), f(10), base), fresh("B", f(90), f(12), base), 60 * time.Second},
		{"exactly 80 not fast", "A", fresh("A", f(80), f(30), base), fresh("B", f(5), f(12), base), 60 * time.Second},
		{"nil data", "A", nil, nil, 60 * time.Second},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := l.NextDelay(tc.current, tc.a, tc.b)
			if got != tc.want {
				t.Fatalf("got %v want %v", got, tc.want)
			}
		})
	}
}

// --- Tick:切換失敗(Apply 出錯)→ 通知錯誤、不翻 mode、Current 不變 ---

func TestTickApplyFailure(t *testing.T) {
	mon := &fakeMonitor{a: fresh("A", f(95), f(10), base), b: fresh("B", f(5), f(12), base)}
	sw, fl, nt := &fakeSwitcher{failErr: errors.New("disk on fire")}, &fakeFlipper{}, &fakeNotifier{}
	l := newLoop(mon, sw, fl, nt)

	r := l.Tick(context.Background(), base)
	if r.Switched {
		t.Fatalf("failed apply must not report switched: %+v", r)
	}
	if l.Current != "A" {
		t.Fatalf("current must stay on failure: %q", l.Current)
	}
	if len(fl.modes) != 0 {
		t.Fatalf("mode must not flip on failed apply: %v", fl.modes)
	}
	if !r.Notified || !strings.Contains(nt.msgs[0], "disk on fire") {
		t.Fatalf("failure must be notified: %+v %v", r, nt.msgs)
	}
}
