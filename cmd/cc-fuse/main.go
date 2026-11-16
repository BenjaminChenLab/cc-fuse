// Command cc-fuse is the resident tray app that watches two z.ai account
// quotas and auto-switches Claude Code providers (design §3).
//
// Boot order: single-instance mutex → rotating log → config → state →
// Loop + tray + settings window → external settings.json watcher. Every
// collaborator is standard T01/T02 production code; this file only wires.
package main

import (
	"context"
	"flag"
	"runtime"
	"runtime/debug"
	"time"

	"cc-fuse/internal/app"
	"cc-fuse/internal/config"
	"cc-fuse/internal/notify"
	"cc-fuse/internal/quotamode"
	"cc-fuse/internal/switcher"
	"cc-fuse/internal/tray"
	"cc-fuse/internal/version"
)

// quotaQueryTimeout is the per-request z.ai quota HTTP timeout (T01: 15s).
const quotaQueryTimeout = 15 * time.Second

func main() {
	// tray 訊息迴圈必須待在主執行緒;其餘工作全部走 goroutine。
	runtime.LockOSThread()
	flag.Parse() // -settings 覆寫(測試用),不給旗標就是正常路徑

	// 1. 單例鎖:第二實例彈通知即退出(§11.6),不進主流程。
	si, first, err := acquireSingleInstance()
	if err != nil {
		failBox("cc-fuse 無法確認單一實例:" + err.Error())
		return
	}
	if !first {
		notify.Windows{}.Notify("cc-fuse 已在執行中", "另一份 cc-fuse 正在跑,本實例結束。")
		return
	}
	defer si.release()

	// 2. log:旋轉檔 %APPDATA%\cc-fuse\logs\cc-fuse.log(§10)。
	logger := bootLogger()

	// 3. 設定與跨重啟狀態:壞設定檔退回預設(記 log),照樣開張。
	cfgPath := defaultConfigPath()
	cfg, err := config.Load(cfgPath)
	if err != nil {
		logger.Printf("config %s 無法解析,改用預設值:%v", cfgPath, err)
		cfg = config.Default()
	}
	statePath := defaultStatePath()
	budget, err := app.LoadBudget(statePath)
	if err != nil {
		logger.Printf("state %s 無法解析,預算重記:%v", statePath, err)
	}

	// 4. 主迴圈:所有協作者都是真件(監看器、切換器、翻 mode、Toast)。
	profiles := normalizeProfiles(cfg)
	loop := &app.Loop{
		Mon:          app.NewMonitorPool(quotaQueryTimeout),
		Sw:           app.SwitcherFunc(switcher.Apply),
		Qm:           app.QuotaFlipperFunc(quotamode.Flip),
		Not:          notify.Windows{Log: logger},
		Cfg:          cfg,
		Profiles:     profiles,
		StatePath:    statePath,
		SettingsPath: settingsPath(),
		Log:          logger,
		Autopilot:    true,
		Current:      currentFromSettings(profiles, logger),
	}
	loop.SeedBudget(budget)

	// 5. 設定窗(127.0.0.1 臨時埠;存檔 = config.Save + 套用自啟 + 熱更新設定)。
	openSettings := newSettingsOpener(loop, cfgPath, logger)

	// 6. 外部變動監看(D7/D8):settings.json 被 cc-switch 改 → 餵事件給 Loop。
	// 7. 主迴圈與匣:tray 佔主執行緒訊息迴圈,Loop 走 goroutine。
	// 兩條背景 goroutine 都包 recover+堆疊進 log:常駐程式崩了必須留遺言,
	// 否則 Start-Process 啟動下 stderr 沒人接,死因永遠成謎(實測踩過)。
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		defer func() {
			if r := recover(); r != nil {
				logger.Printf("watcher panic: %v\n%s", r, debug.Stack())
			}
		}()
		watchExternal(ctx, loop, logger)
	}()
	go func() {
		defer func() {
			if r := recover(); r != nil {
				logger.Printf("loop panic: %v\n%s", r, debug.Stack())
			}
		}()
		loop.Run(ctx)
	}()
	logger.Printf("cc-fuse v%s 啟動(A=%s B=%s settings=%s)",
		version.Version, profileSummary(profiles, "A"), profileSummary(profiles, "B"), loop.SettingsPath)
	tray.Run(ctx, tray.Handles{
		Loop:         loop,
		OpenSettings: openSettings,
		OnQuit:       cancel,
		Log:          logger,
	})
	// tray 退出(結束選單)→ 收掉迴圈與背景工作。
	logger.Printf("cc-fuse 結束")
}
