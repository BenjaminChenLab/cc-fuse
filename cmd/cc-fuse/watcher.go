package main

// 外部變動監看(設計 §5,驗收 10):settings.json 被外界(cc-switch、
// Claude Code 本身)改動時,switcher.Watch 已做好分類,這裡只把事件種類
// 翻譯成 policy 的 ExtEvent 語彙餵給 Loop 的下一輪 Tick。

import (
	"context"
	"log"
	"os"

	"cc-fuse/internal/app"
	"cc-fuse/internal/switcher"
)

// watchExternal runs the settings watcher until ctx is done. Classification
// mapping: ProviderChanged → policy "provider_changed"(Loop 靜默採納)、
// UnknownProvider → "unknown_provider"(policy 暫停+通知一次);
// UnrelatedChange(env 內非供應商欄位或 env 以外變動)不產生事件。
// 限制:profile 比對清單在啟動時交給 Watch 固定,設定窗改 A/B 定義後
// 外部變動的身分判定要到下次啟動才跟上(切換本身用 Loop 的最新表,不受影響)。
func watchExternal(ctx context.Context, loop *app.Loop, logger *log.Logger) {
	events, err := switcher.Watch(ctx, loop.SettingsPath, loop.Profiles)
	if err != nil {
		logger.Printf("settings 監看啟動失敗(外部變動將不會被偵測):%v", err)
		return
	}
	for ev := range events {
		switch ev.Kind {
		case switcher.ProviderChanged:
			logger.Printf("外部切換到認得的帳號 %s,採納", ev.ProfileID)
			loop.ApplyExternal("provider_changed")
		case switcher.UnknownProvider:
			// Watch 的身分分類用「啟動時」抓的帳號表快照:設定窗存檔新增/改過
			// 帳號後快照過期,新帳號會被誤判成未知 → 假暫停(實測踩過)。
			// 用 Loop 當下的帳號表重判一次:認得 → 當 provider_changed 靜默採納。
			if id, ok := reidentify(loop); ok {
				logger.Printf("外部切換到認得的帳號 %s(帳號表重判),採納", id)
				loop.ApplyExternal("provider_changed")
				break
			}
			logger.Printf("外部切到未知供應商,送暫停事件")
			loop.ApplyExternal("unknown_provider")
		case switcher.UnrelatedChange:
			// Claude Code 自己改 model 等供應商以外欄位:刻意不處理(§11.4)。
		}
	}
}

// reidentify reads settings.json NOW and classifies it against the Loop's
// current profile table — the Watch snapshot may be stale after a settings
// save added or redefined profiles.
func reidentify(loop *app.Loop) (string, bool) {
	data, err := os.ReadFile(loop.SettingsPath)
	if err != nil {
		return "", false
	}
	return switcher.Identify(data, loop.ProfilesCopy())
}
