package main

// 設定窗(設計 §8):127.0.0.1 臨時埠跑 ui.Server,視窗本體用 WebView2
// 載內嵌頁;WebView2 runtime 不在(少數精簡系統)就退回預設瀏覽器開啟,
// 使用者照樣能改設定——設定窗的契約是 JSON API,視窗載體有實作自由度。

import (
	"fmt"
	"log"
	"net"
	"net/http"
	"os/exec"
	"runtime"
	"runtime/debug"
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

// newSettingsOpener returns the tray 設定 handler: each click boots a
// loopback-only server on a fresh ephemeral port and shows the settings
// page; closing the window stops that server (設計 §8: 關窗即停服務).
// onSave persists via config.Save, applies the autostart registry value and
// hot-updates the running Loop.
func newSettingsOpener(loop *app.Loop, cfgPath string, logger *log.Logger) func() {
	return func() {
		addr, srv := startSettingsServer(loop, cfgPath, logger)
		if srv == nil {
			return
		}
		url := fmt.Sprintf("http://%s/", addr)
		logger.Printf("設定窗開啟:%s", url)
		stop := func() {
			_ = srv.Close()
			logger.Printf("設定窗關閉,設定服務停止(%s)", addr)
		}
		if openWebView(logger, url, stop) {
			return
		}
		// WebView2 runtime 缺席 → 預設瀏覽器(使用者仍可完整操作);
		// 瀏覽器分頁何時關不得而知,服務留到行程結束(僅 loopback,無風險)。
		if err := exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start(); err != nil {
			logger.Printf("無法開啟設定頁(%v);請手動瀏覽 %s", err, url)
			stop()
		}
	}
}

// startSettingsServer serves the settings API on 127.0.0.1 with an ephemeral
// port. The server edits its own config cell (never Loop's memory); a
// successful save then = config.Save + 登錄檔 Run 機碼 + Loop 熱更新。
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
			}
		},
		func(target string) { loop.SetManual(target) },
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
// Reports false when the WebView2 runtime is not installed so the caller can
// fall back to the default browser.
func openWebView(logger *log.Logger, url string, onClosed func()) bool {
	if _, err := webviewloader.GetInstalledVersion(); err != nil {
		return false
	}
	go func() {
		// WebView2 的視窗與 COM 初始化綁在「建立它的那條 OS 執行緒」上:goroutine
		// 一旦被 Go 調度器搬去別條執行緒,訊息幫浦就抽不到這個視窗的訊息,
		// 症狀正是白畫面+「沒有回應」。整段生命週期必須釘死在同一條執行緒
		// (與 main.go 主執行緒跑 tray 的 LockOSThread 同一套規矩)。
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
				Width:  760,
				Height: 820,
			},
		})
		if w == nil {
			onClosed() // runtime 說有但建窗失敗:同樣收掉伺服器,不留懸掛服務
			return
		}
		defer w.Destroy()
		w.Navigate(url)
		w.Run()
		onClosed()
	}()
	return true
}
