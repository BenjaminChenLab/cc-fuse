# T03 — 外殼與接線(系統匣、通知、設定窗、匯入、主迴圈、單例、自啟)

- depends-on:T01、T02
- expected files:`cmd/cc-fuse/`、`internal/app/`、`internal/tray/`、`internal/notify/`、`internal/ui/`、`internal/importer/`(含可測部分的 `*_test.go`)
- 領域依據:設計文件 §3 §4 §8 §9 §10 §11(§4 之退避重試/沿用上次數字歸本卡的 Tick 契約,per audit A4)

## 目標

把 T01 的地基與 T02 的腦接成活的常駐 app:定時查詢→政策→切換→通知→quota-mode 翻轉的完整迴圈;系統匣呈現與手動操作;設定窗讓所有數字可改;單例與開機自啟。

## 非目標

- 政策或解析邏輯的任何修改(只消費 T01/T02 介面)
- 用量統計/報表、多工具管理(設計 §13 明確不做)

## 現況錨點

- `internal/domain`、`internal/policy`(T01/T02 交付)。
- UI 技術:匣=energye/systray(或同等維護中之 systray 套件);設定窗=WebView2 載入內嵌 HTML、經 `127.0.0.1` 臨時埠以 JSON API 交換(實作自由度在契約內);通知=systray 氣泡或 go-toast(擇一,契約=使用者看得到 Windows 通知)。

## 介面契約

```go
// internal/app — 主迴圈(可測部分)
type Loop struct { /* monitor、switcher、policy、quotamode、config、store 注入 */ }
func (l *Loop) Tick(ctx, now time.Time) LoopResult
// LoopResult{Switched bool, Target string, Reason string, Notified bool, BudgetSaved bool}
// Tick 內部:查 A/B → policy.Evaluate → 執行 SwitchTo(經 switcher)+ Flip quota-mode + 回通知事件
//   查詢失敗處理(設計 §4,per audit A4):暫時性失敗退避重試、沿用上次數字;資料年齡過期 → 走 policy 的 Stale 路徑
// 輪詢節奏:Tick 由 ticker 驅動(間隔=cfg.pollIntervalSec;當前帳號任一窗口>fastPollAbovePct → fastPollSec)

// internal/tray    — 圖示狀態(綠A/橘B/灰暫停或過期/紅金鑰失效)、tooltip(A:xx% · B:xx%)、選單[狀態|切A|切B|暫停/恢復|設定|結束]
// internal/notify  — Toast(標題, 內文);切換/預警/暫停/錯誤四類事件
// internal/ui      — 設定窗:profiles(A/B 名稱/key/端點/模型env)、閾值全表、預算(週總量/保底/假日勾選/顯示偵測錨點)、quotaModeFile 路徑、autoStart;存檔=config.Save
// internal/importer— ReadCcSwitch(dbPath) ([]domain.Profile, error):唯讀開啟 ~/.cc-switch/cc-switch.db(modernc.org/sqlite),盡力讀取 Claude Code 供應商
```

## 設計注意

1. **單例**:Windows 具名 mutex `Global\cc-fuse-single`;第二實例偵測到已存在 → 通知一聲即退出。
2. **開機自啟**:登錄檔 `HKCU\...\Run` 寫/移 `cc-fuse`(由 autoStart 設定驅動)。
3. **匯入為 best-effort**:cc-switch 資料庫是第三方內部結構(會改版),讀取失敗=明確錯誤訊息+手動輸入路徑永遠可用;**手動輸入是保底路徑,驗收以它為準**。
4. 設定窗以 127.0.0.1 繫結臨時埠,僅 loopback;關窗即停服務。
5. log:旋轉檔 `%APPDATA%\cc-fuse\logs\`(每自動切換、每決策 Reason、每錯誤皆落 log)。
6. 主迴圈對 policy 的 Stale 決策:暫停切換+圖示灰+通知一次(不洗版)。
7. **git 前置(per audit A6)**:repo 現無 `.git`,實作第 0 步 `git init`;此後全程只 `git add`(stage),**零 commit、零 push**——commit 權屬 Benjamin。
8. monitor 的 HTTP client 走 Go 標準庫預設(認 `HTTPS_PROXY`/`HTTP_PROXY` 環境變數);config 增選配 `proxyURL`(空=直連;per audit PD1 事實判定,見計畫文件稽核記錄)。
9. 有聲失效(PD2,Benjamin 核可 2026-11-15):**連續解析失敗**(預設 5 次,可設定)→ 圖示紅+Toast+log;v1 無自動更新機制。

## 驗收標準

**可自動測(表格驅動):**
1. `Loop.Tick` 以假 monitor/switcher/policy 注入:觸發切換時依序呼叫(查詢→決策→Apply→Flip→通知事件),Reason 傳遞不變;Stale 時零切換;手動指令直通。
2. 輪詢節奏:fast-poll 進出條件(當前帳號任一窗口>80 → 20s;回落 → 60s)。
3. importer:以組裝的 fixture SQLite(仿 providers 表最小欄位)讀出兩筆 profile;壞檔/缺表 → 明確錯誤非 panic。
4. 設定值往返:設定窗的 JSON API 存讀 = config 套件往返(用 api 層測試,不打 WebView)。

**手動驗收清單(回報文件列出,由使用者回來執行):**
5. 真實環境:以真 A/B key 跑 `cc-fuse.exe`,匣圖示顏色隨切換變化、tooltip 顯示雙邊百分比、額度查詢成功。
6. 手動切 A/B 即時生效;Claude Code session 不重啟、下一請求用新帳號(熱切換)。
7. 第二實例啟動 → 通知並退出。
8. 設定窗:改閾值→存檔→重啟後仍生效;假日勾選、錨點顯示正確。
9. `go build ./...` 產出單一 `cc-fuse.exe`;`go vet`、`go test ./...` 全綠。
10. **外部變動端到端(per audit A4)**:cc-switch(仍使用中)手動切換 `settings.json` → cc-fuse 數秒內採納(認得的 key)或暫停+通知(未知 key),D7/D8 情境走一輪。

## 風險與回退

- 風險:WebView2/systray 套件在無頭 CI 環境不可渲染 → 全部 UI 行為走契約介面注入測試,GUI 本體留手動清單。
- 風險:cc-switch 資料庫改版 → importer best-effort+保底手動輸入(上節 3)。
- 回退:新增套件+main;移除即回退。不觸碰使用者真實 settings.json 之測試(僅手動驗收時才碰,且切換器有備份)。
