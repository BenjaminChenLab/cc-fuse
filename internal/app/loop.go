// Package app is cc-fuse's main loop (design §3 wiring): it polls both
// accounts, hands the numbers to the real policy engine, executes the
// decision (switch settings.json, flip the quota-mode line, notify) and
// persists the pacing-budget settlement. Everything that touches the
// outside world arrives as a small interface (Mon/Sw/Qm/Not), so the whole
// orchestration is testable with recording fakes; production wiring lives
// in cmd/cc-fuse/main.go.
package app

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"runtime/debug"
	"sync"
	"time"

	"cc-fuse/internal/config"
	"cc-fuse/internal/domain"
	"cc-fuse/internal/policy"
	"cc-fuse/internal/store"
	"cc-fuse/internal/switcher"
)

// Logger is the logging seam the Loop writes every decision/error through
// (*log.Logger satisfies it). nil = silent (tests).
type Logger interface {
	Printf(format string, args ...any)
}

// LoopResult is one Tick's outcome: whether settings.json moved (Switched +
// Target), the human-readable decision reason passed through verbatim,
// whether the user saw a notification, and whether this round's policy
// settlement reached the budget state (BudgetSaved — in memory when
// StatePath is empty, on disk otherwise).
type LoopResult struct {
	Switched    bool
	Target      string // "A"/"B" when Switched
	Reason      string // policy's reason text, never rewritten
	Notified    bool   // a toast went out this round
	BudgetSaved bool   // the round's budget settlement was stored
}

// Loop orchestrates one cc-fuse runtime: injected collaborators, live config,
// the profile table, paths and the mutable runtime knobs the tray flips
// (Current/Autopilot/Pending/PendingExt). The zero Budget means cold start —
// policy settles it on the first tick (§7: 以當下值當基準).
//
// Thread-safety: Tick runs on the loop goroutine while the tray and the
// settings watcher mutate knobs from theirs, so every access to the mutable
// cell goes through mu. Collaborator calls (network, file IO) always run
// outside the lock — only the in-memory cell is guarded.
type Loop struct {
	// Collaborators (interfaces, see adapters.go).
	Mon Monitor
	Sw  Switcher
	Qm  QuotaFlipper
	Not Notifier

	Cfg          config.Config    // live config cell (the settings window edits this)
	Profiles     []domain.Profile // the A/B table; ID is the cross-card handle
	StatePath    string           // state.json path; empty = memory-only budget
	SettingsPath string           // ~/.claude/settings.json the switcher rewrites
	Log          Logger           // decision/error log (rotating file in main); nil = silent

	mu sync.Mutex // guards every field below

	Current       string                           // who settings.json points at: "A"/"B"
	Autopilot     bool                             // false = flee-to-B blocked (directional gate; B→A returns still decide)
	Pending       policy.ManualCmd                 // tray 切A/切B command, consumed by the next Tick
	PendingExt    policy.ExtEvent                  // settings-watch event, consumed by the next Tick
	Budget        policy.BudgetState               // persisted pacing state, seeded from store at boot
	budgetDirty   bool                             // 存檔觸發的預算重算尚未成功(R4;mu 下讀寫,Tick 負責清)
	cache         map[string]*domain.QuotaSnapshot // last good sample per account (A4 reuse)
	failA, failB  int                              // consecutive fresh-query failure streaks (PD2)
	parseAlerted  bool                             // PD2 toast latched for the current failure streak
	staleNotified bool                             // stale toast latched for the current bad streak
	lastStale     bool                             // whether the most recent round ran on stale data

	wakeOnce sync.Once
	wakeCh   chan struct{} // 手動/外部事件到場 → 打斷 Run 的睡眠,指令即時生效
}

// Tick runs one polling round at decision instant now and returns what
// happened. Orchestration order is pinned by the card contract:
// query both accounts → policy.Evaluate → Apply → Flip quota-mode → notify.
//
// Policies this implements (design §4 + §11, per audit A4):
//  1. each account is queried once per tick; a FAILED query falls back to the
//     last good sample regardless of its age, so a momentary network blip
//     changes nothing; policy's staleness gate then ages the reused sample
//     out after StaleAfterMin and the round goes stale;
//  2. never-successful accounts carry the failed sample itself (zero
//     QueriedAt = infinitely old = stale immediately);
//  3. stale data switches nothing and notifies exactly once per bad streak
//     (通知一次不洗版), re-arming once fresh data returns;
//  4. manual commands beat everything and are consumed by the round that
//     executes them; external events are consumed the same way, with a
//     recognized provider change adopted silently before evaluating;
//  5. a failing Apply notifies the error, leaves Current untouched and never
//     flips the quota-mode line;
//  6. a missing quota-mode file only logs (§11.5: 跳過,其餘照常);
//  7. the round's budget settlement (if any) is persisted to StatePath, or
//     kept in memory when StatePath is empty — either way BudgetSaved says so.
func (l *Loop) Tick(ctx context.Context, now time.Time) LoopResult {
	var res LoopResult

	// 1. 查詢兩帳號(A 先 B 後;編排序釘死:查詢→決策→Apply→Flip→通知)。
	freshA, snapA := l.query(ctx, "A")
	freshB, snapB := l.query(ctx, "B")

	// 2. 連續解析失敗計數(PD2):吃「新鮮」結果,與沿用舊值無關。
	l.trackParseFails(freshA, freshB, &res)

	// 3. 外部事件預處理:認得的帳號 → 重讀 settings.json 靜默採納(更新 Current)。
	l.adoptExternal()

	// 4. 政策決策:直接驅動真 policy.Evaluate(無 policy seam,審計 F10 釘死);
	// Evaluate 只吃副本,網路與決策全程不佔鎖。
	current, autopilot, manual, ext, cfg := l.knobs()
	dec := policy.Evaluate(policy.Input{
		Now:         now,
		Current:     current,
		QuotaA:      snapA,
		QuotaB:      snapB,
		Budget:      l.budget(),
		Cfg:         cfg,
		AutopilotOn: autopilot,
		External:    ext,
		Manual:      manual,
	})
	// 手動指令與外部事件都是一次性:本輪已進入決策,清空避免下一輪重放;
	// 只清「本輪真的消費的那一筆」——睡眠期間新到場的指令不可誤刪;
	// 資料恢復時順手把過期通知鎖重新上膛。
	l.consumeEvents(!dec.Stale, manual, ext)

	// 5. 執行決策(Switch/Pause 各自處理通知;Stay/None 只處理 stale 與 P7 預警)。
	res.Reason = dec.Reason
	switch dec.Action {
	case policy.ActionSwitch:
		res = l.doSwitch(dec, cfg)
	case policy.ActionPause:
		l.SetAutopilot(false)
		l.Not.Notify("cc-fuse 已暫停自動切 B", dec.Reason)
		res.Notified = true
		l.logf("已暫停自動切 B(A→B 封鎖,切回 A 照常):%s", dec.Reason)
	default:
		// stale 閂鎖只在 Tick 執行緒讀寫(唯一寫者;Status 不看它),免鎖即可。
		if dec.Stale && !l.staleNotified {
			l.staleNotified = true
			l.Not.Notify("cc-fuse 額度資料過期", dec.Reason)
			res.Notified = true
			l.logf("額度資料過期:%s", dec.Reason)
		}
		if dec.WarnFired {
			// P7 一次性預警:政策引擎的閂鎖保證每輪跨越只提示一次。
			l.Not.Notify("cc-fuse 額度預警", dec.Reason)
			res.Notified = true
			l.logf("預警:%s", dec.Reason)
		}
	}

	// 6. 預算結算落帳(§7:記帳與動作解耦;冷啟結算也必須抵達 state)。
	res.BudgetSaved = l.saveBudget(dec.Budget)

	// 6b. 存檔觸發的預算重算閂鎖(R4):只有「真結算」輪(dec.Settled,信封
	// 權威更新)清 dirty —— 僅改預警旗標的整顆覆寫輪(ensureEmitted)帶的是
	// 舊信封,不清、不終結自癒(Phase-3 審查指出漂移);其餘 dirty 輪 →
	// 重試 RefreshBudget,重算冪等、只併入天花板,Tick 尾端整顆覆寫的回退
	// 下一輪自動自癒(複審 K2-Residual1)。
	if dec.Settled {
		l.mu.Lock()
		l.budgetDirty = false
		l.mu.Unlock()
	} else if l.budgetDirtyPeek() {
		l.RefreshBudget(now)
	}
	return res
}

// Run drives Tick on the adaptive polling rhythm until ctx is cancelled:
// each round sleeps NextDelay for the account currently active — fast while
// it runs hot (任一窗口 > FastPollAbovePct), normal otherwise. A queued
// manual command or external event cuts the sleep short so P1 switches are
// immediate (驗收 6: 手動切換即時生效).
func (l *Loop) Run(ctx context.Context) {
	wake := l.wakeChannel()
	for {
		l.runTick(ctx)
		st := l.Status()
		delay := l.NextDelay(st.Current, st.A, st.B)
		l.logf("下一輪 %s 後", delay)
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-wake:
			timer.Stop()
			l.logf("收到手動/外部事件,提前進入下一輪")
		case <-timer.C:
		}
	}
}

// runTick runs one Tick, recovering panics into the log: a single bad round
// must never take the whole resident app down — a skipped round is strictly
// better than a silently dead tray.
func (l *Loop) runTick(ctx context.Context) {
	defer func() {
		if r := recover(); r != nil {
			l.logf("tick panic(已復原,輪詢繼續):%v\n%s", r, debug.Stack())
		}
	}()
	l.Tick(ctx, time.Now())
}

// wakeChannel lazily creates the wake channel (Run and the tray setters are
// the only users).
func (l *Loop) wakeChannel() chan struct{} {
	l.wakeOnce.Do(func() { l.wakeCh = make(chan struct{}, 1) })
	return l.wakeCh
}

// nudge wakes the sleeping Run loop, best effort (a queued nudge is enough —
// never block the tray).
func (l *Loop) nudge() {
	select {
	case l.wakeChannel() <- struct{}{}:
	default:
	}
}

// NextDelay returns how long to wait after this round (design §4): fast
// poll while the CURRENT account's either window sits above
// FastPollAbovePct (strictly above — the line itself is still normal),
// normal poll otherwise. Missing/absent data never speeds polling up.
// Reads a locked Cfg copy: the settings window may replace the cell
// concurrently.
func (l *Loop) NextDelay(current string, a, b *domain.QuotaSnapshot) time.Duration {
	l.mu.Lock()
	cfg := l.Cfg
	l.mu.Unlock()
	poll := time.Duration(cfg.PollIntervalSec) * time.Second
	fast := time.Duration(cfg.FastPollSec) * time.Second
	// 手改設定檔可能出 0/負值:退回 §9 預設,不讓 ticker 變成空轉。
	if poll <= 0 {
		poll = 60 * time.Second
	}
	if fast <= 0 {
		fast = 20 * time.Second
	}
	cur := a
	if current == "B" {
		cur = b
	}
	if cur == nil {
		return poll
	}
	if hot := (cur.FiveHourPct != nil && *cur.FiveHourPct > cfg.FastPollAbovePct) ||
		(cur.WeeklyPct != nil && *cur.WeeklyPct > cfg.FastPollAbovePct); hot {
		return fast
	}
	return poll
}

// Status snapshots the runtime facts the tray needs to render icon/tooltip
// (design §8) without poking Loop internals directly. CeilingToday carries
// the persisted pacing ceiling so the switcher cards can show B's "今日
// 天花板" line (2026-11-16 plan R1).
type Status struct {
	Current      string                // "A"/"B"/""
	Autopilot    bool                  // false = paused
	Stale        bool                  // last round ran on missing/aged-out data
	KeyDead      bool                  // current account answered 401/403
	ParseFailing bool                  // consecutive parse failures reached the alert limit (PD2 red)
	A, B         *domain.QuotaSnapshot // last good sample per account (nil = none yet)
	CeilingToday *float64              // B's pacing ceiling for today (nil = not settled yet)
}

// Status returns the current render inputs; safe to call from the tray
// goroutine between ticks.
func (l *Loop) Status() Status {
	l.mu.Lock()
	defer l.mu.Unlock()
	st := Status{
		Current:      l.Current,
		Autopilot:    l.Autopilot,
		Stale:        l.lastStale,
		A:            l.cache["A"],
		B:            l.cache["B"],
		CeilingToday: l.Budget.CeilingToday,
	}
	cur := st.A
	if l.Current == "B" {
		cur = st.B
	}
	st.KeyDead = cur != nil && (cur.HTTPStatus == 401 || cur.HTTPStatus == 403)
	limit := l.Cfg.ParseFailAlertLimit
	st.ParseFailing = limit > 0 && (l.failA >= limit || l.failB >= limit)
	return st
}

// SetManual queues a tray 切A/切B command; the next Tick executes it
// whatever the policy rules say (P1 手動最優先), and Run wakes immediately.
func (l *Loop) SetManual(target string) {
	l.mu.Lock()
	l.Pending = policy.ManualCmd{Target: target}
	l.mu.Unlock()
	l.nudge()
}

// ApplyExternal queues a settings-watch event for the next Tick
// ("provider_changed" / "unknown_provider") and wakes Run immediately.
func (l *Loop) ApplyExternal(kind string) {
	l.mu.Lock()
	l.PendingExt = policy.ExtEvent{Kind: kind}
	l.mu.Unlock()
	l.nudge()
}

// SetAutopilot flips automatic flee-to-B (tray 暫停/恢復自動切 B). Resuming
// wakes Run so the re-enabled autopilot re-decides immediately; pausing
// needs no extra round (the next scheduled tick stays silent anyway).
func (l *Loop) SetAutopilot(on bool) {
	l.mu.Lock()
	l.Autopilot = on
	l.mu.Unlock()
	if on {
		l.nudge()
	}
}

// UpdateConfig replaces the live config cell after a settings-window save;
// the next Tick sees the new numbers.
func (l *Loop) UpdateConfig(c config.Config) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.Cfg = c
}

// RefreshBudget recomputes today's pacing ceiling immediately after the
// settings window saved budget changes (2026-11-16 plan R4) — the card
// shows the new number on the next status read instead of tomorrow's
// settlement. Landing discipline is per-path (folded §2.4, re-audit K2):
//   - cold path returns a full envelope → land it whole, but only if a Tick
//     settlement didn't land a fresh envelope meanwhile (else retry via the
//     dirty latch);
//   - recompute path returns only a ceiling → re-read the CURRENT envelope
//     under the lock and splice just CeilingToday into it, with an envelope
//     identity check (DayBoundary unchanged since compute) so neither this
//     save nor a concurrent Tick settlement can clobber the other's fields.
//
// ok=false (stale/missing B data, hand-mangled state) latches budgetDirty;
// the latch is cleared only by a Tick settlement or by the Tick retry path —
// a Tick tail that reverts a freshly spliced ceiling heals on the next poll
// because the recompute is Now-free and idempotent (re-audit K2-Residual1).
func (l *Loop) RefreshBudget(now time.Time) {
	l.mu.Lock()
	in := policy.Input{
		Now: now, Current: l.Current,
		QuotaA: l.cache["A"], QuotaB: l.cache["B"],
		Budget: l.Budget, Cfg: l.Cfg, AutopilotOn: l.Autopilot,
	}
	l.mu.Unlock()

	nb, ok := policy.RefreshBudget(in)
	if !ok {
		l.mu.Lock()
		l.budgetDirty = true
		l.mu.Unlock()
		l.logf("預算即時重算暫緩(資料不可用),已記待補算")
		return
	}

	l.mu.Lock()
	merged, landed := landRefresh(l.Budget, in.Budget, nb)
	if !landed {
		// 落帳前信封已被 Tick 換過(冷啟時日界出現/重算時日界不同)→
		// 本輪結果過期,維持 dirty 交給下一輪重試。
		l.budgetDirty = true
		l.mu.Unlock()
		return
	}
	l.Budget = merged
	dirty := l.budgetDirty
	l.mu.Unlock()
	if nb.CeilingToday != nil {
		l.logf("預算即時重算:今日天花板 %.1f%%(dirty=%v)", *nb.CeilingToday, dirty)
	}
}

// landRefresh merges one RefreshBudget result into the CURRENT envelope
// (re-read at landing time), per the folded per-path landing rule:
//   - cold path (snapshot had no day-start baseline): land the whole new
//     envelope, but only while no Tick settlement has landed one meanwhile
//     (prev grew a boundary) — a settlement's envelope is fresher;
//   - recompute path: envelope identity (prev and snapshot share the same
//     DayBoundary) → splice only the new CeilingToday; otherwise discard.
//
// ok=false means "discard, keep retrying via the latch". Pure function —
// the guards are unit-testable without concurrency (Phase-3 review finding:
// the guards were previously zero-coverage branches).
func landRefresh(prev, snap, nb policy.BudgetState) (policy.BudgetState, bool) {
	if snap.DayStartWeekly == nil {
		if prev.DayBoundary != nil {
			return prev, false
		}
		return nb, true
	}
	if prev.DayBoundary == nil || snap.DayBoundary == nil || !prev.DayBoundary.Equal(*snap.DayBoundary) {
		return prev, false
	}
	merged := prev
	merged.CeilingToday = nb.CeilingToday
	return merged, true
}

// budgetDirtyPeek reads the recompute latch (Tick retry path).
func (l *Loop) budgetDirtyPeek() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.budgetDirty
}

// ConfigCopy returns the current config as a value, for UI surfaces that
// keep their own editable cell (they must not share Loop's memory).
func (l *Loop) ConfigCopy() config.Config {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.Cfg
}

// SetProfiles replaces the profile table after a settings-window save; the
// next Tick and the switch seam see the new A/B definitions.
func (l *Loop) SetProfiles(ps []domain.Profile) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.Profiles = ps
}

// ProfilesCopy returns a snapshot of the current profile table for callers
// outside the loop goroutine (the settings watcher re-classifies with it).
func (l *Loop) ProfilesCopy() []domain.Profile {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]domain.Profile, len(l.Profiles))
	copy(out, l.Profiles)
	return out
}

// SeedBudget installs the persisted budget state at boot (cold start: zero
// value, policy settles from current numbers).
func (l *Loop) SeedBudget(b policy.BudgetState) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.Budget = b
}

// knobs copies the mutable inputs one decision round needs.
func (l *Loop) knobs() (current string, autopilot bool, manual policy.ManualCmd, ext policy.ExtEvent, cfg config.Config) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.Current, l.Autopilot, l.Pending, l.PendingExt, l.Cfg
}

// budget copies the current pacing state for Evaluate.
func (l *Loop) budget() policy.BudgetState {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.Budget
}

// consumeEvents clears the one-shot events evaluated this round (only the
// exact values Evaluate saw — a command queued mid-round survives) and
// re-arms the stale-notification latch when data came back fresh.
func (l *Loop) consumeEvents(dataFresh bool, consumedManual policy.ManualCmd, consumedExt policy.ExtEvent) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.Pending == consumedManual {
		l.Pending = policy.ManualCmd{}
	}
	if l.PendingExt == consumedExt {
		l.PendingExt = policy.ExtEvent{}
	}
	l.lastStale = !dataFresh
	if dataFresh {
		l.staleNotified = false
	}
}

// query samples one account: returns (fresh, effective). fresh is what the
// monitor just answered (parse-fail streaks count on it); effective is what
// the policy engine sees — the fresh sample on success, the last good one on
// failure (A4), or the failed sample itself when nothing good ever arrived.
func (l *Loop) query(ctx context.Context, id string) (fresh, effective *domain.QuotaSnapshot) {
	p, ok := l.profile(id)
	if !ok {
		// 尚未設定的帳號不查詢,交給政策的資料缺失閘(stale)。
		return nil, nil
	}
	s := l.Mon.Query(ctx, p)
	if s.Err == nil {
		l.mu.Lock()
		if l.cache == nil {
			l.cache = make(map[string]*domain.QuotaSnapshot)
		}
		l.cache[id] = &s
		l.mu.Unlock()
		return &s, &s
	}
	// 暫時性失敗(設計 §4,審計 A4):沿用上次數字,老化交給政策 staleGate 判定;
	// 從未成功過 → 回失敗樣本(零時戳必過期 → 立即走 stale)。
	l.mu.Lock()
	last := l.cache[id]
	l.mu.Unlock()
	if last != nil {
		return &s, last
	}
	return &s, &s
}

// trackParseFails keeps the consecutive fresh-query failure streaks per
// account and fires the PD2 audible-failure alert once per streak: streak
// reaching ParseFailAlertLimit → toast + red tray state (Status.ParseFailing);
// any good sample resets its streak and re-arms the alert.
func (l *Loop) trackParseFails(freshA, freshB *domain.QuotaSnapshot, res *LoopResult) {
	l.mu.Lock()
	l.failA = bumpStreak(l.failA, freshA)
	l.failB = bumpStreak(l.failB, freshB)
	limit := l.Cfg.ParseFailAlertLimit
	failing := limit > 0 && (l.failA >= limit || l.failB >= limit)
	fire := failing && !l.parseAlerted
	switch {
	case failing && !l.parseAlerted:
		l.parseAlerted = true
	case !failing && l.parseAlerted:
		l.parseAlerted = false // 兩帳號恢復正常,警報重新上膛
	}
	streak := max(l.failA, l.failB)
	l.mu.Unlock()

	// 通知在鎖外發:每輪失敗只遞增一格,踩線那輪正好發一次。
	if !fire {
		return
	}
	l.Not.Notify("cc-fuse 額度解析持續失敗",
		fmt.Sprintf("連續 %d 次查詢/解析失敗(警報線 %d),z.ai 回應形態可能已改版;請更新程式或改用手動確認", streak, limit))
	res.Notified = true
	l.logf("PD2 警報:連續解析失敗 A=%d B=%d(警報線 %d)", l.failA, l.failB, limit)
}

// bumpStreak advances one account's failure streak: a fresh failed sample
// extends it, a fresh good sample resets it, and "no query happened"
// (unconfigured account) leaves it untouched.
func bumpStreak(cur int, fresh *domain.QuotaSnapshot) int {
	switch {
	case fresh == nil:
		return cur
	case fresh.Err != nil:
		return cur + 1
	default:
		return 0
	}
}

// adoptExternal handles a recognized external provider change before
// evaluation: re-reading settings.json and adopting its API key's profile as
// the new Current (design §5: 認得 → 默默採納為新現況). Unreadable file keeps
// the old Current — policy still treats the round as silent adoption.
func (l *Loop) adoptExternal() {
	l.mu.Lock()
	kind := l.PendingExt.Kind
	l.mu.Unlock()
	if kind != "provider_changed" {
		return
	}
	data, err := os.ReadFile(l.SettingsPath)
	if err != nil {
		l.logf("外部採納:讀 settings.json 失敗(%v),維持原判定", err)
		return
	}
	l.mu.Lock()
	profiles := l.Profiles
	l.mu.Unlock()
	if id, ok := switcher.Identify(data, profiles); ok {
		l.mu.Lock()
		changed := id != l.Current
		if changed {
			l.Current = id
		}
		l.mu.Unlock()
		if changed {
			l.logf("外部變動採納:Current 跟隨 settings.json 切到 %s", id)
		}
	}
}

// doSwitch executes a switch decision: Apply via the switcher, flip the
// quota-mode line, then notify. cfg is the config copy this round evaluated
// with (Tick's knobs() snapshot) — never the live cell. Apply failure →
// error toast, no flip, Current unchanged (the next round re-decides from
// reality). Flip failure → log only: the mode line is auxiliary bookkeeping
// and must never undo a completed settings.json switch (§11.5).
func (l *Loop) doSwitch(dec policy.Decision, cfg config.Config) LoopResult {
	target, ok := l.profile(dec.Target)
	if !ok {
		l.Not.Notify("cc-fuse 切換失敗", dec.Reason+";找不到目標 profile "+dec.Target)
		return LoopResult{Reason: dec.Reason, Notified: true}
	}
	if err := l.Sw.Apply(target, l.SettingsPath); err != nil {
		l.Not.Notify("cc-fuse 切換失敗", dec.Reason+";"+err.Error())
		l.logf("切換到 %s 失敗:%v", dec.Target, err)
		return LoopResult{Reason: dec.Reason + ";" + err.Error(), Notified: true}
	}
	l.mu.Lock()
	l.Current = dec.Target
	l.mu.Unlock()
	if dec.QuotaMode != "" && l.Qm != nil {
		if err := l.Qm.Flip(cfg.QuotaModeFile, dec.QuotaMode); err != nil {
			l.logf("quota-mode 翻轉失敗(切換照常生效):%v", err)
		}
	}
	l.Not.Notify("cc-fuse 已切換", dec.Reason)
	l.logf("已切換到 %s:%s", dec.Target, dec.Reason)
	return LoopResult{Switched: true, Target: dec.Target, Reason: dec.Reason, Notified: true}
}

// saveBudget persists the round's settlement: always into the in-memory
// Budget cell, and additionally into state.json when StatePath is set.
// Returns whether the settlement reached its store — a cold-start settlement
// on a quiet round counts too (§7: 閒置期間的結算必須抵達 state,重開機不歸零).
func (l *Loop) saveBudget(b *policy.BudgetState) bool {
	if b == nil {
		return false
	}
	l.mu.Lock()
	l.Budget = *b
	statePath := l.StatePath
	l.mu.Unlock()
	if statePath == "" {
		return true // 記憶體模式(測試/未設路徑):落在 Loop 內即為已記帳
	}
	data, err := json.Marshal(b)
	if err != nil {
		l.logf("budget 編碼失敗:%v", err)
		return false
	}
	if err := store.SaveState(statePath, store.State{Budget: data}); err != nil {
		l.logf("budget 落盤失敗:%v", err)
		return false
	}
	return true
}

// profile looks up a profile by its stable ID ("A"/"B"), on a locked
// snapshot of the table (the settings window may swap it concurrently).
func (l *Loop) profile(id string) (domain.Profile, bool) {
	l.mu.Lock()
	profiles := l.Profiles
	l.mu.Unlock()
	for _, p := range profiles {
		if p.ID == id {
			return p, true
		}
	}
	return domain.Profile{}, false
}

// logf routes through the injected logger; nil logger = silent.
func (l *Loop) logf(format string, args ...any) {
	if l.Log != nil {
		l.Log.Printf(format, args...)
	}
}
