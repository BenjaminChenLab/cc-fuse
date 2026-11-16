package main

// 設定窗(設計 §8):127.0.0.1 臨時埠跑 ui.Server,視窗本體用 WebView2
// 載內嵌頁;WebView2 runtime 不在(少數精簡系統)就退回預設瀏覽器開啟,
// 使用者照樣能改設定——設定窗的契約是 JSON API,視窗載體有實作自由度。
// R6(2026-11-16):主視窗單例——重複點系統匣圖示不開新窗,只把已開的
// 視窗帶到前景(windowKeeper);瀏覽器後備同樣單例,再點重開同一網址。

import (
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os/exec"
	"runtime"
	"runtime/debug"
	"sync"
	"time"

	"github.com/jchv/go-webview2"
	"github.com/jchv/go-webview2/webviewloader"

	"cc-fuse/internal/app"
	"cc-fuse/internal/config"
	"cc-fuse/internal/ui"
)

// settingsWindowName is the WebView2 window title (the window is the app's
// main screen: switcher cards on top, settings below).
const settingsWindowName = "cc-fuse"

// windowKeeper keeps the settings window a singleton (R6, Benjamin's bug
// report: double-clicking the tray icon used to spawn several WebView2
// windows whose predecessors froze). One live window OR one fallback
// browser session, ever; a repeated click raises the existing one instead.
// Raise checks IsWindow first (re-audit K4: HWNDs get recycled, and a stale
// raise could foreground an unrelated window) and falls back to opening a
// fresh window; unregistering is idempotent because onClosed has three
// entry points (normal close, panic, window-creation failure).
// Benjamin's fallback ruling (2026-11-16): browser mode is a singleton too —
// the loopback server starts once and lives to process exit, later clicks
// merely re-open the same URL.
type windowKeeper struct {
	mu             sync.Mutex
	hwnd           uintptr // live window handle; 0 = none (or still materializing)
	stop           func()  // stops the window's loopback server; nil = none
	fallbackURL    string  // non-empty = browser-fallback session is up
	isWindow       func(uintptr) bool
	raise          func(uintptr) bool
	openWebview    func() (uintptr, func(), error)
	startFallback  func() string
	reopenFallback func()
}

// requestOpen handles one tray 設定 click: raise / reopen / first-open,
// fully serialized.
func (g *windowKeeper) requestOpen() {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.openWebview == nil { // 瀏覽器後備模式(WebView2 runtime 不在)
		if g.fallbackURL == "" {
			g.fallbackURL = g.startFallback() // "" = 綁定失敗,下次點擊再試
			return
		}
		g.reopenFallback()
		return
	}
	if g.hwnd != 0 {
		if g.isWindow(g.hwnd) {
			g.raise(g.hwnd)
			return
		}
		// 死窗殘留(onClosed 尚未跑到):收舊伺服器,照無視窗流程重開。
		if g.stop != nil {
			g.stop()
		}
		g.hwnd, g.stop = 0, nil
	}
	if g.stop != nil { // 開窗進行中(hwnd 尚未回報)→ 防重入,忽略本次點擊
		return
	}
	hwnd, stop, err := g.openWebview()
	if err != nil {
		return // 開窗失敗不登記,下次點擊再試
	}
	g.hwnd, g.stop = hwnd, stop
}

// closed unregisters (and stops) the current window; the ONLY onClosed
// target, so all three close paths funnel here — idempotent under the mutex.
func (g *windowKeeper) closed() {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.stop != nil {
		g.stop()
		g.stop = nil
	}
	g.hwnd = 0
}

// newSettingsOpener returns the tray 設定 handler: each click goes through
// the singleton keeper (raise the open window / open the first one). The
// WebView2-vs-browser mode is picked once at construction. onSave persists
// via config.Save, applies the autostart registry value and hot-updates the
// running Loop (with an immediate budget recompute, plan R4).
func newSettingsOpener(loop *app.Loop, cfgPath string, logger *log.Logger) func() {
	g := &windowKeeper{isWindow: hwndAlive, raise: raiseWindow}
	if _, err := webviewloader.GetInstalledVersion(); err == nil {
		g.openWebview = func() (uintptr, func(), error) {
			addr, srv := startSettingsServer(loop, cfgPath, logger)
			if srv == nil {
				return 0, nil, errors.New("settings server bind failed")
			}
			url := fmt.Sprintf("http://%s/", addr)
			logger.Printf("設定窗開啟:%s", url)
			hwndCh := make(chan uintptr, 1)
			var stopOnce sync.Once
			stop := func() {
				stopOnce.Do(func() {
					_ = srv.Close()
					logger.Printf("設定窗關閉,設定服務停止(%s)", addr)
				})
			}
			if !openWebView(logger, url, g.closed, func(h uintptr) { hwndCh <- h }) {
				stop()
				return 0, nil, errors.New("webview2 unavailable")
			}
			select {
			case h := <-hwndCh:
				return h, stop, nil
			case <-time.After(2 * time.Second):
				// 視窗還在建立(hwnd 未回報):以「進行中」登記(hwnd=0、
				// stop 已設)→ 重複點擊防重入;之後的關閉照常走 g.closed 清理。
				return 0, stop, nil
			}
		}
	} else {
		var fallbackURL string
		openBrowser := func(u string) {
			if err := exec.Command("rundll32", "url.dll,FileProtocolHandler", u).Start(); err != nil {
				logger.Printf("無法開啟設定頁(%v);請手動瀏覽 %s", err, u)
			}
		}
		g.startFallback = func() string {
			addr, srv := startSettingsServer(loop, cfgPath, logger)
			if srv == nil {
				return ""
			}
			fallbackURL = fmt.Sprintf("http://%s/", addr)
			logger.Printf("設定頁以瀏覽器開啟(伺服器存活至程式結束):%s", fallbackURL)
			openBrowser(fallbackURL)
			return fallbackURL
		}
		g.reopenFallback = func() {
			if fallbackURL != "" {
				openBrowser(fallbackURL) // 同一網址:伺服器單一,絕不多開(Benjamin 2026-11-16 定案)
			}
		}
	}
	return g.requestOpen
}

// startSettingsServer serves the settings API on 127.0.0.1 with an ephemeral
// port. The server edits its own config cell (never Loop's memory); a
// successful save then = config.Save + 登錄檔 Run 機碼 + Loop 熱更新 + 預算
// 即時重算(R4:改了預算算法存檔,當日天花板立刻重算,顯示馬上換新)。
func startSettingsServer(loop *app.Loop, cfgPath string, logger *log.Logger) (string, *http.Server) {
	onSave := func(c config.Config) error {
		if err := config.Save(cfgPath, c); err != nil {
			return err
		}
		if err := applyAutoStart(c.AutoStart, ""); err != nil {
			logger.Printf("autostart 套用失敗(設定已存檔):%v", err)
		}
		loop.UpdateConfig(c)
		loop.SetProfiles(normalizeProfiles(c))
		loop.RefreshBudget(time.Now())
		logger.Printf("設定已存檔並生效")
		return nil
	}
	uiCell := loop.ConfigCopy()                 // 設定窗自己的可編輯副本,與 Loop 的記憶體分離
	ln, err := net.Listen("tcp", "127.0.0.1:0") // 臨時埠、僅 loopback(卡片規矩)
	if err != nil {
		logger.Printf("設定服務綁定失敗:%v", err)
		return "", nil
	}
	srv := &http.Server{Handler: ui.NewServerWithStatus(&uiCell, onSave,
		func() ui.StatusView {
			st := loop.Status()
			return ui.StatusView{
				Current: st.Current, Autopilot: st.Autopilot, Stale: st.Stale,
				KeyDead: st.KeyDead, ParseFailing: st.ParseFailing,
				A: st.A, B: st.B, Profiles: loop.ProfilesCopy(),
				CeilingToday:        st.CeilingToday,
				EnforceDailyCeiling: loop.ConfigCopy().Budget.EnforceDailyCeiling,
			}
		},
		func(target string) { loop.SetManual(target) },
		func(on bool) { loop.SetAutopilot(on) },
	), ReadHeaderTimeout: 5 * time.Second}
	go func() {
		if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
			logger.Printf("設定服務結束:%v", err)
		}
	}()
	return ln.Addr().String(), srv
}

// openWebView shows url in a native WebView2 window on its own goroutine
// (the tray stays responsive; the window blocks its goroutine until closed,
// then invokes onClosed so the caller can stop the settings server).
// onReady reports the native window handle (used by windowKeeper to raise
// the window on repeated clicks). Reports false when the WebView2 runtime
// is not installed so the caller can fall back to the default browser.
func openWebView(logger *log.Logger, url string, onClosed func(), onReady func(uintptr)) bool {
	if _, err := webviewloader.GetInstalledVersion(); err != nil {
		return false
	}
	go func() {
		// WebView2 的視窗與 COM 初始化綁在「建立它的那條 OS 執緒」上:goroutine
		// 一旦被 Go 調度器搬去別條執行緒,訊息幫浦就抽不到這個視窗的訊息,
		// 症狀正是白畫面+「沒有回應」。整段生命週期必須釘死在同一條執行緒
		// (與 main.go 主執緒跑 tray 的 LockOSThread 同一套規矩)。
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		// 視窗生命週期內任何 panic 都要留堆疊進 log,並收掉伺服器不留懸掛。
		defer func() {
			if r := recover(); r != nil {
				logger.Printf("設定窗 panic: %v\n%s", r, debug.Stack())
				onClosed()
			}
		}()
		w := webview2.NewWithOptions(webview2.WebViewOptions{
			WindowOptions: webview2.WindowOptions{
				Title:  settingsWindowName,
				Width:  1000,
				Height: 860,
			},
		})
		if w == nil {
			onClosed() // runtime 說有但建窗失敗:同樣收掉伺服器,不留懸掛服務
			return
		}
		defer w.Destroy()
		if onReady != nil {
			onReady(uintptr(w.Window()))
		}
		w.Navigate(url)
		w.Run()
		onClosed()
	}()
	return true
}
