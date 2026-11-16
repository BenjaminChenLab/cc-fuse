package main

// 驗收測試(黑箱)——2026-11-16 計畫 R6:主視窗單例(Benjamin bug 回報:重複點
// 系統匣圖示開多個主視窗、舊窗卡死)。規格:docs/blueprints/2026-11-16-cc-fuse-budget-ui.md
// §2.7(含複審 K4:raise 前 IsWindow 驗證、失敗註銷重開、冪等解除登記;
// Benjamin 定案:瀏覽器後備也單例,再點重開同一網址、伺服器不重複)。
// win32 細節(ShowWindow/SetForegroundWindow)為薄包裝,人工驗收;此處測決策邏輯。

import (
	"testing"
)

func newKeeper(isWin func(uintptr) bool) *windowKeeper {
	return &windowKeeper{isWindow: isWin, raise: func(uintptr) bool { return true }}
}

// 沒有視窗 → 開新窗(開窗函式恰呼叫一次,handle 登記)。
func TestKeeperOpensWhenNone(t *testing.T) {
	opens := 0
	g := newKeeper(func(uintptr) bool { return true })
	g.openWebview = func() (uintptr, func(), error) {
		opens++
		return 101, func() {}, nil
	}
	g.requestOpen()
	g.requestOpen() // 第二次點:raise,不再開窗
	if opens != 1 {
		t.Fatalf("單例:只准開一次窗,實際 %d", opens)
	}
	if g.hwnd != 101 {
		t.Fatalf("handle 未登記:%v", g.hwnd)
	}
}

// 視窗還活著 → 走 raise 帶到前景,不開新窗。
func TestKeeperRaisesWhenAlive(t *testing.T) {
	g := newKeeper(func(uintptr) bool { return true })
	g.hwnd = 77
	raised := 0
	g.raise = func(uintptr) bool { raised++; return true }
	g.openWebview = func() (uintptr, func(), error) {
		t.Fatalf("活窗時不得再開窗")
		return 0, nil, nil
	}
	g.requestOpen()
	if raised != 1 {
		t.Fatalf("必須 raise 一次,實際 %d", raised)
	}
}

// 視窗已死(handle 殘留)→ 註銷舊伺服器、照無視窗流程重開。
func TestKeeperReopensWhenDead(t *testing.T) {
	stopped := 0
	opens := 0
	g := newKeeper(func(uintptr) bool { return false }) // IsWindow=false
	g.hwnd = 55
	g.stop = func() { stopped++ }
	g.openWebview = func() (uintptr, func(), error) {
		opens++
		return 56, func() {}, nil
	}
	g.requestOpen()
	if stopped != 1 || opens != 1 || g.hwnd != 56 {
		t.Fatalf("死窗必須收舊開新:stopped=%d opens=%d hwnd=%d", stopped, opens, g.hwnd)
	}
}

// 瀏覽器後備:已開過 → 再點重開同一網址(伺服器單一,不重跑啟動)。
func TestKeeperFallbackReopensURL(t *testing.T) {
	starts, reopens := 0, 0
	g := &windowKeeper{isWindow: func(uintptr) bool { return false }, raise: func(uintptr) bool { return true }}
	g.startFallback = func() string { starts++; return "http://127.0.0.1:9/" }
	g.reopenFallback = func() { reopens++ }
	g.requestOpen()
	g.requestOpen()
	g.requestOpen()
	if starts != 1 || reopens != 2 {
		t.Fatalf("後備單例:啟動 1 次、重開網址 2 次,實際 start=%d reopen=%d", starts, reopens)
	}
}

// 解除登記冪等(onClosed 有多種進入點);之後可再正常開新窗。
func TestKeeperClosedIdempotent(t *testing.T) {
	stopped := 0
	opens := 0
	g := newKeeper(func(uintptr) bool { return true })
	g.openWebview = func() (uintptr, func(), error) {
		opens++
		return uintptr(100 + opens), func() { stopped++ }, nil
	}
	g.requestOpen()
	g.closed()
	g.closed() // 冪等:第二次必須無害
	if stopped != 1 {
		t.Fatalf("伺服器恰停一次,實際 %d", stopped)
	}
	g.requestOpen() // 解除後照常開新窗
	if opens != 2 || g.hwnd != 102 {
		t.Fatalf("關窗後再點 = 開新窗:opens=%d hwnd=%d", opens, g.hwnd)
	}
}

// 開窗進行中(hwnd 尚未回報、stop 已設)→ 防重入,不再開窗(審查突變未覆蓋臂)。
func TestKeeperInFlightIgnoresClicks(t *testing.T) {
	opens := 0
	g := newKeeper(func(uintptr) bool { return true })
	g.stop = func() {} // 「進行中」標記:stop 已設而 hwnd 為 0
	g.openWebview = func() (uintptr, func(), error) {
		opens++
		return 1, func() {}, nil
	}
	g.requestOpen()
	if opens != 0 {
		t.Fatalf("開窗進行中不得再開窗,實際 %d", opens)
	}
}

// 後備綁定失敗(回 "")→ 不視為已開,下次點擊重試啟動。
func TestKeeperFallbackBindFailureRetries(t *testing.T) {
	starts, reopens := 0, 0
	g := &windowKeeper{isWindow: func(uintptr) bool { return false }, raise: func(uintptr) bool { return true }}
	g.startFallback = func() string {
		starts++
		if starts == 1 {
			return ""
		}
		return "http://127.0.0.1:9/"
	}
	g.reopenFallback = func() { reopens++ }
	g.requestOpen() // 綁定失敗
	g.requestOpen() // 重試成功
	g.requestOpen() // 之後走重開網址
	if starts != 2 || reopens != 1 {
		t.Fatalf("綁定失敗要重試:start=%d reopen=%d", starts, reopens)
	}
}

// raise 被前景鎖拒絕(false)→ 屬外觀層瑕疵:不重開、不炸(審查未覆蓋臂)。
func TestKeeperRaiseDeniedIsCosmetic(t *testing.T) {
	g := newKeeper(func(uintptr) bool { return true })
	g.hwnd = 9
	denied := false
	g.raise = func(uintptr) bool { denied = true; return false }
	g.openWebview = func() (uintptr, func(), error) {
		t.Fatalf("raise 被拒不得重開窗")
		return 0, nil, nil
	}
	g.requestOpen()
	if !denied {
		t.Fatalf("必須嘗試過 raise")
	}
}
