package tray

// systray 接線層:選單建置、圖示/tooltip 刷新、動作接回 Loop。
// 這檔不做決策——只把 PickIcon/PickTooltip/StatusText 的結果貼上匣。

import (
	"context"
	"runtime/debug"
	"sync"
	"time"

	"github.com/energye/systray"

	"cc-fuse/internal/app"
)

// Handles is what the tray is allowed to do to the rest of the app; main
// wires them. nil OpenSettings hides the 設定 item; nil OnQuit makes 結束
// only tear the tray down.
type Handles struct {
	Loop         *app.Loop
	OpenSettings func() // 開設定窗(WebView2/瀏覽器由 main 決定)
	OnQuit       func() // 結束時先取消主迴圈 ctx
	Log          app.Logger
}

// guard wraps a tray callback so a panic in one menu action logs a full
// stack instead of taking the whole resident process down silently.
func (h Handles) guard(name string, fn func()) func() {
	return func() {
		defer func() {
			if r := recover(); r != nil && h.Log != nil {
				h.Log.Printf("tray %s panic: %v\n%s", name, r, debug.Stack())
			}
		}()
		fn()
	}
}

// refreshEvery is the tray repaint cadence: quota numbers move at most once
// per poll round (20s+), so 2s keeps the icon snappy at trivial cost.
const refreshEvery = 2 * time.Second

// Run starts the tray and blocks until the user picks 結束 (or the app calls
// systray.Quit). Call from the main goroutine — the library owns the OS
// thread's message loop.
func Run(ctx context.Context, h Handles) {
	systray.Run(func() { onReady(ctx, h) }, func() {})
}

// onReady builds the menu [狀態 | 切到 A | 切到 B | 暫停/恢復自動 | 設定 | 結束]
// and starts the repaint goroutine (design §8 menu layout).
func onReady(ctx context.Context, h Handles) {
	if h.Loop == nil {
		return
	}
	st := h.Loop.Status()
	status := systray.AddMenuItem(StatusText(st), PickTooltip(st))
	status.Disable()
	var mainItem *systray.MenuItem
	if h.OpenSettings != nil {
		mainItem = systray.AddMenuItem("主畫面", "開啟切換畫面(額度卡片+設定)")
	}
	swA := systray.AddMenuItem("切到 A(共用)", "手動切到共用帳號 A(P1 手動最優先)")
	swB := systray.AddMenuItem("切到 B(自購)", "手動切到自購帳號 B(P1 手動最優先)")
	pause := systray.AddMenuItem(pauseTitle(st), "暫停/恢復自動切換")
	quit := systray.AddMenuItem("結束", "結束 cc-fuse")

	// 選單動作只改 Loop 的旋鈕,判定交給下一輪 Tick(P1 路徑)。
	swA.Click(h.guard("切A", func() { h.Loop.SetManual("A") }))
	swB.Click(h.guard("切B", func() { h.Loop.SetManual("B") }))
	pause.Click(h.guard("暫停切換", func() {
		cur := h.Loop.Status()
		h.Loop.SetAutopilot(!cur.Autopilot)
		pause.SetTitle(pauseTitle(h.Loop.Status()))
	}))
	if mainItem != nil {
		mainItem.Click(h.guard("主畫面", func() { h.OpenSettings() }))
	}
	quit.Click(h.guard("結束", func() {
		if h.OnQuit != nil {
			h.OnQuit()
		}
		systray.Quit()
	}))

	// 雙擊托盤圖示 = 開主畫面(左鍵單擊保持無動作,右鍵選單照舊,互不干擾)。
	systray.SetOnDClick(func(_ systray.IMenu) { h.guard("雙擊主畫面", h.OpenSettings)() })

	go repaint(ctx, h, status, pause)
	repaintOnce(h, status, pause) // onReady 當下先畫一次,不等第一個 tick
}

// repaintOnce applies the current render inputs to icon/tooltip/status line.
func repaintOnce(h Handles, status, pause *systray.MenuItem) {
	// One panicked repaint round must not kill the repaint loop or the app:
	// recover here, log the stack, let the next tick try again.
	defer func() {
		if r := recover(); r != nil && h.Log != nil {
			h.Log.Printf("tray repaint panic: %v\n%s", r, debug.Stack())
		}
	}()
	st := h.Loop.Status()
	if b, err := iconBytesCached(PickIcon(st)); err == nil {
		systray.SetIcon(b)
	}
	systray.SetTooltip(PickTooltip(st))
	status.SetTitle(StatusText(st))
	pause.SetTitle(pauseTitle(st))
}

// repaint refreshes the tray at refreshEvery until ctx is done.
func repaint(ctx context.Context, h Handles, status, pause *systray.MenuItem) {
	t := time.NewTicker(refreshEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			repaintOnce(h, status, pause)
		}
	}
}

// pauseTitle renders the 暫停/恢復 item's label from the autopilot flag.
func pauseTitle(st app.Status) string {
	if st.Autopilot {
		return "暫停自動切換"
	}
	return "恢復自動切換"
}

// iconCache memoizes rendered ICO bytes per state (repaint runs every 2s).
var iconCache = struct {
	mu sync.Mutex
	m  map[IconState][]byte
}{m: map[IconState][]byte{}}

// iconBytesCached returns the cached ICO for a state, rendering on first use.
func iconBytesCached(s IconState) ([]byte, error) {
	iconCache.mu.Lock()
	defer iconCache.mu.Unlock()
	if b, ok := iconCache.m[s]; ok {
		return b, nil
	}
	b, err := IconBytes(s)
	if err != nil {
		return nil, err
	}
	iconCache.m[s] = b
	return b, nil
}
