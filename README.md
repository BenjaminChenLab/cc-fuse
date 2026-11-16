# cc-fuse

Claude Code 帳號自動切換器 — Windows 系統匣常駐小工具(Go)。

監看兩個 z.ai GLM Coding Plan 帳號的額度,在共用帳號快過載時搶先熔斷、自動切換 `~/.claude/settings.json` 的供應商,保護進行中的工作不被 429 燒死。**完全取代 cc-switch 在本機的用途**(切換 + 看額度),額度快過載時搶先走,而不是撞牆才反應。

## 功能

- **雙帳號自動切換**:A(共用帳號,永遠優先)的 5 小時% 或週% 過切換線 → 切到 B(自購帳號)避難;A 回落到安全區 → 自動切回。
- **B 配速預算**:每日額度 = (週預算總量 − 已用) ÷ 剩餘工作日,燒到「今日天花板」強制切回 A、寧停等不超支;總開關可整個關掉(關 = 天花板只顯示與記帳,不影響切換)。
- **存檔即時重算**:改預算設定存檔,當日天花板立刻更新,不用等隔天結算;資料暫時過期會記待補算、恢復後自動補。
- **quota-mode 同步**:切換時翻轉 `quota-mode.md` 的 `mode:` 行(RACING/SAVING),讓其他 Claude Code session 即時採用配速策略。
- **系統匣狀態圖示**:綠/橘實心 = 自動切換中(A/B)、空心環 = 暫停自動(保留帳號色)、灰 = 資料過期、紅 = 金鑰失效或解析持續失敗。
- **主畫面**:深色精緻化 UI — 額度進度條、今日天花板/恢復預計狀態膠囊、自動切換按鈕、撥動開關、聚焦光環;設定窗**單例**(重複點圖示只帶前景,不開新窗)。
- **熱切換**:進行中的 Claude Code session 下一個請求即生效;`settings.json` 原子寫入,permissions/statusLine 等其他欄位零損傷。
- **從 cc-switch 匯入**:唯讀開啟 `~/.cc-switch/cc-switch.db`,一鍵把既有 profile 填入 A/B。

## 建置

需求:Go 1.26+;WebView2 runtime(Win10/11 一般都有 — 缺的話設定頁自動退回預設瀏覽器開啟)。

```powershell
go build -ldflags "-H windowsgui" -o cc-fuse.exe ./cmd/cc-fuse
```

> `-H windowsgui` 必帶:漏了會建成主控台子系統的 exe,跑起來多一個黑色執行視窗,且關掉黑窗整個常駐程式跟著死。

測試:

```powershell
go test ./...
```

## 使用

- 啟動後常駐系統匣;**右鍵選單**:狀態行 | 切到 A / 切到 B | 暫停/恢復自動 | 設定 | 結束。
- **雙擊圖示**開主畫面:上半是 A/B 額度卡片(進度條 + 今日天花板 + 恢復預計),下半是全部設定。
- 所有數字皆可設定(切換線、恢復線、預警線、輪詢節奏、預算參數、資料過期門檻……),存檔即熱生效。
- 切換、預警、暫停都發 Windows Toast;log 旋轉檔在 `%APPDATA%\cc-fuse\logs\`。

## 設定與狀態

| 檔案 | 內容 |
|---|---|
| `%APPDATA%\cc-fuse\config.json` | 全部設定(A/B 定義、閾值、輪詢、預算、quota-mode 路徑、autostart、proxy)|
| `%APPDATA%\cc-fuse\state.json` | 配速記帳狀態(當日基準、天花板、結算時刻)——重開機不歸零 |
| `%APPDATA%\cc-fuse\logs\` | 旋轉日誌 |

欄位全表見 `docs/blueprints/2026-11-15-cc-fuse-design.md` §9;`budget.enforceDailyCeiling` 預設 `true`,舊設定檔缺欄位自動視為開,行為不變。

## 專案結構

```
cmd/cc-fuse/      接線層:main、tray 選單、設定窗(單例 keeper + WebView2)、監看器
internal/app/     主迴圈編排(查詢 → 決策 → 執行 → 落帳)
internal/policy/  決策引擎(P1–P8,純函數、表格可測)+ 配速預算數學
internal/config/  config.json 讀寫(原子寫入,缺欄位疊預設)
internal/domain/  跨套件值型別
internal/monitor/ z.ai 額度查詢與解析
internal/switcher/ settings.json 身分判定與原子改寫
internal/quotamode/ quota-mode.md 的 mode 行翻轉
internal/tray/    系統匣圖示/選單/tooltip(六態圖示,純函數可測)
internal/ui/      設定窗 JSON API + 內嵌頁(深色精緻化)
internal/version/ 版本號單一事實來源
```

## 版本

- 目前版本:**v0.1.0**(`internal/version/version.go`;git tag 對映)
- 發行紀錄:[CHANGELOG.md](CHANGELOG.md)
- 設計文件(單一事實來源):`docs/blueprints/2026-11-15-cc-fuse-design.md`
