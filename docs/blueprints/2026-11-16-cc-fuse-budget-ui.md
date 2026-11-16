# cc-fuse 預算顯示與開關計畫(B 卡今日天花板 / 強制切總開關 / A 卡恢復時間 / 存檔即時重算)

日期:2026-11-16。狀態:設計已與 Benjamin 對話談定(三個 AskUserQuestion 決策 + 一個補充需求),本文件把它落成可審計、可實作的計畫。上游事實來源:`docs/blueprints/2026-11-15-cc-fuse-design.md`(§6 政策、§7 配速預算、§8 UI、§9 設定全表)。

## 0. 背景與現況(可否證的程式碼事實)

cc-fuse 是 Go 寫的 Windows 系統匣常駐工具,監看兩個 z.ai 帳號(A=團隊共用優先、B=自購避難)的額度,自動改寫 Claude Code 的 settings.json。與本計畫相關的現況:

- **F1**:`internal/policy/policy.go` 的每日結算 `settleBudget` 只在跨過 `DayBoundary`(或冷啟動)時執行;`CeilingToday`(今日天花板,B 週% 的當日上限)一算出來就鎖到下次結算。中途改預算設定(週預算總量/每日保底/只數工作日),當日天花板**不會**跟著更新。
- **F2**:「天花板」目前寫死影響政策三處,全部無開關:①P5(`decideFromB`:在 B 上週% ≥ 天花板 → 強制切回 A,即使 A 未恢復);②P6 仲裁(`budgetBlocked`:B 燒到天花板 → 喪失避難候選資格,A 過熱時留 A 等結算);③`bAvailable`(B 可用定義含「週% < 天花板」)。
- **F3**:`BudgetState`(policy.go)持有 `CeilingToday *float64`,存在 Loop 的 `Budget` 欄位並落盤 state.json;但 `app.Status()`(loop.go)不帶它,`ui.StatusView`(server.go)也沒有對應欄位 — 前端拿不到天花板。
- **F4**:`internal/ui/page.go` 的切換卡片只顯示各帳號 5h/7d 百分比與各自的 ⏱ 重置倒數;B 卡無天花板行、A 卡無綜合「恢復時間」行。卡片動態字串一律 DOM textContent 建,禁 innerHTML(外部字串安全規矩)。
- **F5**:`config.Budget`(config.go)現有三欄:`WeeklyTotalPct`、`MinDailyAllowancePct`、`WorkdaysOnly`;`Load` 把部分設定檔疊在 `Default()` 上,缺的欄位吃預設值。
- **F6**:設定存檔鏈:`internal/ui/server.go` postConfig → `cmd/cc-fuse/settings.go` onSave(`config.Save` + applyAutoStart + `loop.UpdateConfig` + `loop.SetProfiles`)→ 下一輪 Tick 才看到新設定。存檔成功後前端只顯示「已存檔」,不重抓 /api/status(卡片靠每 5 秒自刷)。

## 1. 已定案的需求(使用者決策錄案)

| # | 需求 | 決策 |
|---|---|---|
| R2 | 「天花板強制切回 A」總開關 | 新增布林設定,**預設開**;關掉 = 天花板**完全不影響切換**(F2 三處全部跳過),記帳照跑 |
| R1 | B 卡顯示今日天花板 | 顯示「今日天花板 週 X.X%(到點強制切回 A)」;開關關時照顯示但標註「(不強制)」;無結算資料則整行不顯示 |
| R3 | A 卡顯示恢復時間 | **只看 5h 重置時刻**(使用者明確選擇;週窗口也超標時此估計過度樂觀,已知悉) |
| R4 | 改預算設定存檔後當日額度立刻重算 | 存檔 → 立刻用新公式重算當日天花板;基準(當日起始週%)不放寬;B 資料暫不可算 → 記 dirty,資料回來自動補算 |

## 2. 設計

### 2.1 R2 — `EnforceDailyCeiling` 開關

- `config.Budget` 加欄位 `EnforceDailyCeiling bool json:"enforceDailyCeiling"`;`Default()` 給 `true`(現有使用者行為完全不變;部分設定檔缺欄位 → 依 F5 疊在預設上仍為 true)。
- `policy.Evaluate` 及其子函數經 `in.Cfg.Budget.EnforceDailyCeiling` 讀取,三處行為:
  1. **P5**(`decideFromB` 的天花板分支):開關關 → 跳過,B 留在 B(即使週% 已過天花板)。
  2. **P6**(`budgetBlocked`):開關關 → 恆回 false,B 不因預算喪失候選資格,仲裁照常比較兩邊 5h 重置時刻。
  3. **`bAvailable`**:開關關 → 只檢查 B 的 5h% < 切換線,天花板比較跳過。
- 結算(`settleBudget`)不受開關影響,照跑 — 天花板數字仍要算出來供 R1 顯示與記帳。
- UI(page.go「B 配速預算」fieldset)加勾選框「天花板強制切回 A」,預設勾選;tooltip:「勾選:B 燒到當日天花板時強制切回 A(寧停等不超支),燒過線的 B 也不當避難所;不勾:B 額度只顯示與記帳,不影響自動切換。」load()/collect() 帶此欄位。

### 2.2 R1 — B 卡今日天花板

- `app.Status()`(`internal/app/loop.go`)多帶出 persisted `l.Budget.CeilingToday`(`Status` struct 加 `CeilingToday *float64`)。Status 已在鎖下取值,零額外同步。
- `ui.StatusView`(`internal/ui/server.go`)加 `CeilingToday *float64 json:"ceilingToday"` 與 `EnforceDailyCeiling bool json:"enforceDailyCeiling"`(前端要開關狀態才能決定標註)。
- `cmd/cc-fuse/settings.go` 的 status 閉包把兩欄接進去。
- 前端(page.go buildCard 的 B 卡):`CeilingToday` 非 nil → 加一行「今日天花板 週 X.X%」+ 標註:開(「到點強制切回 A」)/關(「(不強制)」);nil → 整行不建。**per Benjamin 2026-11-16(Q4 定案:不加行內過期標註)** — 結算做不成(B 資料過期跨過日界)時照顯示昨日天花板,由既有「額度資料過期」提示列承擔告知責任。DOM textContent。

### 2.3 R3 — A 卡恢復時間(只看 5h)

- 純前端(page.go buildCard 的 A 卡):A snapshot 的 `fiveHourReset` 非 nil → 加一行「恢復預計 HH:mm ⏱倒數」(倒數沿用既有 fmtCD);nil → 不建。**per Benjamin 2026-11-16(Q3 定案:有資料就顯示,不依 A 健康與否增減)**。後端零改動(資料已在 /api/status)。

### 2.4 R4 — 存檔即時重算

- `policy` 新增公開函數 `RefreshBudget(in Input) (BudgetState, bool ok)`(純函數,表格可測;ok=false = 不可算,呼叫端據此設 dirty):
  - **冷啟動**(`in.Budget.DayStartWeekly == nil`,今天還沒結算過):**per Benjamin 2026-11-16(Q2 定案:要求新鮮)** — 需 B 快照存在、`WeeklyPct != nil`、且查詢時間在 `StaleAfterMin` 內(與引擎 staleGate 同門檻);不足 → 回 `(in.Budget, false)`(過期數字不得錨成整天基準);足 → `(settleBudget(in), true)`。
  - **重算**(已有當日起始值):**per Benjamin 2026-11-16(Q1 定案:一日 = 重置時刻到隔天同時刻,天數照這個錨定日數)** — 天數基準 = 本預算日起點 `ref = *DayBoundary − 24h`(結算恆設 DayBoundary;有錨點時其時鐘時刻 = 週重置時刻,即「15:00→15:00」語意,無錨點盲鏈以午夜為界 — ref 公式照抄結算的既有約定,兩者皆確定性;重置時刻維持 z.ai API 自動偵測,Benjamin 同日定案不加手動欄位);`days := daysLeftInWindow(ref, WeekAnchorTime, WorkdaysOnly)`。整個預算日內 DayBoundary 不變 → 同一份設定任何時刻存檔結果相同(稽核 C7 的午夜漂移消除)。`allowance = max((WeeklyTotalPct − *DayStartWeekly)/days, MinDailyAllowancePct)`,`ceiling = min(*DayStartWeekly + allowance, hardWallPct)`;只換 `CeilingToday`,`WeekAnchorTime`/`DayBoundary`/`DayStartWeekly`/`WarnedFiveHour` 全不動。此路徑**不需要** B 快照(公式只吃持久化基準+設定+錨點)。改「只數工作日」→ 天數以新設定按 ref 重算,當天生效。Benjamin 例語(2026-11-16):剩 3 天、已用 70% → 一日 10%,燒到即切 A、B 停用至下個重置時刻 — 即既有 P5/P6 行為,本計畫不改變它。
- `app.Loop` 加公開方法 `RefreshBudget()`。**per audit C8/A2 + 複審 K2(已摺入,分路落帳)**:`budgetDirty` 一律在 `l.mu` 下讀寫(HTTP goroutine 寫、Tick 讀)。流程:鎖下取 cfg/budget/cache["B"] 快照 → 呼叫 policy.RefreshBudget:
  - **冷啟路徑**(ok=true,回傳整顆新信封):鎖下檢查信封同一性(落帳時 l.Budget 的 DayBoundary 仍與計算時相同)→ 成立則**整顆落帳**(saveBudget);中途已被 Tick 結算覆寫 → 丟棄本輪結果、維持 dirty(Tick 的結算已是新信封)。
  - **重算路徑**(ok=true,只換天花板):鎖下重讀當下 l.Budget,**信封同一性檢查**(計算時的 DayBoundary == 落帳時的 DayBoundary,不符則重算一次或維持 dirty)→ 只把新 `CeilingToday` 併入最新信封後落帳(雙向 lost-update 都消除)。
  - **per 複審 K2-Residual1(should,已摺入)**:**dirty 只在 Tick 清** — 重算落帳成功後不清 dirty;Tick 尾端整顆覆寫(結算或 applyWarn 的 ensureEmitted)若回退了剛併入的天花板,下一輪 Tick 的重算(無 Now 輸入、冪等、只併入天花板)自動自癒;Tick 例行結算成功 → 清 dirty(結算是權威信封)。
  - `Tick` 每輪結尾在鎖下檢查:dirty → 重呼叫 RefreshBudget。
- **per 複審 K1(d)(blocking,已摺入)**:重算路徑 nil 防護 — `DayStartWeekly != nil` 但 `DayBoundary == nil`(hand-edited state.json 經 LoadBudget 可達;正常寫入者恆兩者同設)→ 回 `(in.Budget, false)`,不 panic、不猜 ref。
- `cmd/cc-fuse/settings.go` onSave:在 `loop.UpdateConfig(c)` 後呼叫 `loop.RefreshBudget()`(新設定即時反映到 Status)。
- page.go 存檔成功 handler 加 `fetchStatus()`(存檔當下立刻重抓,不等 5 秒自刷)。

### 2.5 設計文件同步

`docs/blueprints/2026-11-15-cc-fuse-design.md`:§6 P5/P6 列加開關語意、§7 加「存檔即重算」與「基準不放寬」、§8 加卡片新行與勾選框、§9 設定全表加 `budget.enforceDailyCeiling`。**per audit A5(已摺入)**:同步時一併調和稽核點出的既有漂移 — §6 P4 補「無週窗口視為達標」(commit c17ea18 已入碼、文件未跟)、§7 補 hardWallPct=100 封頂(稽核 A1 已入碼、文件未記)、§9 補 `parseFailAlertLimit`/`proxyUrl` 兩列、§8 的「顯示偵測到的重置錨點」標明未實作(或移除)。

### 2.6 R5 — 設定頁「自動切換」開關按鈕(Benjamin 2026-11-16 追加需求)

- 現況:自動切換的開/暫停只有系統匣選單(`Loop.SetAutopilot`),設定頁只在提示列顯示狀態文字,不能操作。
- `internal/ui/server.go`:加路由 `POST /api/autopilot`(body `{"on":bool}`)→ 呼叫新接線閉包;`Server` 結構加 `autopilot func(bool)` 欄位,`NewServerWithStatus` 加一個參數(既有測試呼叫點同步更新);未接線 → 501(與 status/switch 同規矩);壞 JSON → 400;非 POST → 405(與其他路由同)。`StatusView` 既有 `Autopilot` 欄位回報現況,零新增欄位。
- page.go:提示列加一顆按鈕 — 自動切換開:「自動切換:開」+ 按鈕「暫停」;已暫停:「自動切換:已暫停」+ 按鈕「恢復」;點擊 POST 後立刻 `fetchStatus()`。DOM textContent。
- 與系統匣共用同一個 Loop 旗標(`SetAutopilot` 恢復時本就會喚醒 Run),兩處狀態一致(5 秒自刷+操作後即抓)。

### 2.7 R6 — 主視窗單例(Benjamin 2026-11-16 bug 回報:重複點圖示開多個主視窗、舊窗卡死)

- 現況:`cmd/cc-fuse/settings.go` 的 `newSettingsOpener` 每次點系統匣「設定」都開新的 loopback 服務+新的 WebView2 視窗;重複點 = 多個視窗,且舊窗會卡死(根因未深究,疑與多個 WebView2/訊息幫浦並存有關;單例化後觸發條件消失,若單例後仍卡死再另開票)。
- 設計:`cmd/cc-fuse` 加一個小型視窗管理器(`sync.Mutex` + handle{server, hwnd, raise}),可注入 `raise`/`open` 函數以便單元測試:
  - 點「設定」時若已有開著的視窗 → **不開新窗**,呼叫 raise 把既有視窗帶到前景(win32:`ShowWindow(SW_RESTORE)` + `SetForegroundWindow`,user32 syscall 薄包裝);
  - 沒有 → 照現行流程開服務+視窗,把 hwnd(WebView2 的 `w.Window()`)登記進 handle;
  - 視窗關閉(onClosed)→ 解除登記 + 停服務(現行行為不變)。
- 期望行為:任何時刻至多一個主視窗;重複點圖示 = 已開的那個浮到最前面。
- **per 複審 K4(should,已摺入)**:raise 前先 `IsWindow(hwnd)` 驗證(視窗已毀但 onClosed 尚未解除登記的縫隙;HWND 會被系統回收,盲 raise 可能帶起不相干視窗)— 失敗 → 註銷舊 handle、照「無視窗」流程開新窗;解除登記冪等(onClosed 有正常關窗/panic/建窗失敗三種進入點,mutex 下重入安全)。
- **per Benjamin 2026-11-16(後備路徑定案)**:後備也單例 — 無 WebView2 runtime 的機器上,第一次點 = 開單一 loopback 服務 + rundll32 開網址,登記「後備已開」;再點 = 重跑 rundll32 開**同一**網址,**不再新開伺服器**(旗標把關);伺服器存活至行程結束(現行行為不變)。多分頁可接受(同一網址無害),絕不多伺服器互踩設定副本。

## 3. 測試計畫

- **policy**(表格驅動):開關關 — P5 不切(B 過天花板仍留 B)、bAvailable 不看天花板(A 過熱時 B 過天花板仍可避難)、P6 照比重置時刻;開關開 — 既有行為不變(既有測試全綠即證)。`RefreshBudget`(定稿語意):換總量/換保底/換工作日 → 天花板換;基準與日界不放寬(DayStartWeekly/DayBoundary 不變);**同預算日不同時刻存檔(含跨午夜,例 23:50 vs 翌日 00:10)→ 同天花板**(Q1 冪等性);冷啟動+資料新鮮 → 等價 settleBudget;**冷啟動+資料過期/缺失 → ok=false(Q2)**;重算路徑不吃 B 快照。
- **config**:`Default().Budget.EnforceDailyCeiling == true`;Save/Load 往返保留 false;手寫部分設定檔缺欄位 → Load 得 true。
- **app**:`Status()` 帶出 persisted CeilingToday;`RefreshBudget()` 存檔後 Status 立刻反映新天花板;B 資料無效時設 dirty、資料恢復後 Tick 補算。
- **ui**:StatusView 新欄位原樣回(status_test.go 既有樣式)。**per audit A4(已摺入;per 複審 K5 修正引用)**:ui/acceptance_test.go 實為純 API 測試、樹內並無 page 標記測試樣式 — page 面的 collect()/load() 防漏改用**白箱測試**(package ui 內直接斷言 indexHTML 的 collect()/load() 含 enforceDailyCeiling、B 卡/A 卡新行標記在場),再加 StatusView 的 API 往返。背景:POST /api/config 反序列化進零值 Config(server.go 不疊 Default),collect() 漏欄位會讓每次存檔都靜默寫成 false、關掉強制切。**R5**:POST /api/autopilot(on/off、壞 JSON 400、未接線 501、非 POST 405、合法呼叫抵達閉包)。
- **cmd(R6)**:視窗管理器邏輯以注入 fake 測試(已開 → 走 raise 不開新窗;未開 → open+登記;關窗 → 解除登記);win32 raise 薄包裝人工驗收。
- **驗證**:`go build ./...`、`go vet ./...`、`go test ./...` 全綠;掃測試樹引用被改類別的既有套件並實跑(policy/config/app/ui/cmd 既有套件測試即涵蓋)。零 commit、零 push,等 Benjamin。

## 4. 不做(範圍外)

- 不改 `store.go` 的 state.json schema(BudgetState 欄位不動,開關是 config 不是 state)。
- 不動系統匣 tooltip(需求只談切換畫面卡片)。
- 不為「只看 5h 恢復」加週窗口綜合判斷(使用者明確選擇只看 5h)。

## 5. 審計紀錄(spec-2nd-opinion,2026-11-16)

- 稽核員:唯讀 TOP,背景派工,九條主張 C1–C9 逐一裁決:C1–C6、C9 **confirmed**;C7(天數語意)、C8(併發寫入)**partially**(已摺入修正)。
- **AUDITOR POSITION:同意計畫整體形狀**;必要修正案 A1–A3(blocking)、A4–A5(should)。
- 已由 orchestrator 仲裁摺入(技術性,非使用者決策):**A2**(RefreshBudget 落帳改「鎖下重讀最新 l.Budget、只併入 CeilingToday」,消除與 Tick 的 lost-update;budgetDirty 在 mu 下讀寫,§2.4)、**A4**(page 層 load()/collect() 勾選框往返測試,§3)、**A5**(§2.5 調和既有文件漂移:§6 P4 無週窗口、§7 hardWall 封頂、§9 兩列缺漏、§8 未出貨項)。
- **待 Benjamin 裁決 → 已全部定案(2026-11-16,全數摺入)**:
  - **Q1 定案**:一日 = 重置時刻到隔天同時刻(重置時刻維持 z.ai API 自動偵測,不加手動欄位);天數照錨定預算日起點數(`ref = DayBoundary − 24h`)→ §2.4 重算路徑定稿;Benjamin 例語(30/3=10%、燒到切 A、停用至下個重置)確認 P5/P6 語意不變。
  - **Q2 定案**:冷啟結算要求 B 資料新鮮(≤ StaleAfterMin),過期/缺失 → ok=false 記 dirty → §2.4。
  - **Q3 定案**:A 卡恢復行有資料就顯示 → §2.3(與原計畫字面一致,歸檔為定案)。
  - **Q4 定案**:不加行內標註,靠既有「額度資料過期」提示列 → §2.2。
- **追加需求(Benjamin 2026-11-16)**:**R5** 設定頁自動切換開關按鈕(§2.6)、**R6** 主視窗單例 — 重複點系統匣圖示不開新窗,只把已開視窗帶到前景(舊窗卡死問題隨單例化消除)(§2.7)。
- 稽核員點出、由修正案吸收的風險:第二寫入者 lost-update(C8)、午夜非冪等(C7)、過期快取冷啟結算(e)、R3 與既有 5h ⏱ 倒數資料重複(由 Q3 裁決呈現方式)。
- **close 戳:NON-CLEAN → 修正案與待決已解,待一輪針對性複審**(上限一輪,scope=摺入後定稿的 §2.4、新增 §2.6/§2.7 與相關風險)→ 最終計畫呈報 → Benjamin 明確說 go 才 fire(clean-fire 同意不適用非乾淨案)。
- **針對性複審收口(一輪,2026-11-16)**:K1/K2/K4 partially、K3 confirmed、K5 大致正確;**AUDITOR POSITION:AGREE 摺入後形狀**。已摺入:blocking×2(§2.4 分路落帳:冷啟整顆/重算只併入天花板+信封同一性檢查;§2.4 DayBoundary nil 防護)、should×2(dirty 只在 Tick 清,Tick 尾端整顆覆寫回退下一輪自癒;R6 IsWindow 驗證+失敗重開+冪等註銷)、doc×3(A3 內容=冷啟新鮮度門檻,已由 Q2 定案摺入 §2.4;§3 標記測試引用修正為白箱字串斷言;§2.4 邊界時鐘括註軟化:無錨點盲鏈以午夜為界)。§2.6 補「非 POST → 405」。**複審上限已用罄,不再有第三輪**。
- **待 Benjamin(複審唯一 PENDING)**:R6 瀏覽器後備路徑(無 WebView2 runtime 的機器)要不要把關 — 現狀每點必開新分頁+新 loopback 伺服器(存活到程式結束),「至多一個主視窗」在該類機器上不成立,多伺服器會互踩設定副本。
- **close 戳 2:NON-CLEAN → 修正案全摺;唯一 PENDING(R6 後備把關)與 go 同題呈 Benjamin:答完+說 go → 蓋 fire 戳 → spec-tdd-lite 實作。**
- **fire 戳:implementation fired (2026-11-16, lite): explicit go — non-clean resolution complete(複審 AGREE、修正全摺、唯一 PENDING 已定案:後備也單例再點重開網址)**。Benjamin 2026-11-16 明確說「開工(go)」;session tier 披露:本 session 非 opus-class(I21 按 spec-tdd-secretary invocation record 免問,兩個判斷密集派工依各站 skill 釘 TOP)。
- **close 戳:secretary pipeline closed (2026-11-16): audited spec-2nd-opinion(初審+針對性複審各一輪), implemented spec-tdd-lite**。Phase 1 驗收測試六檔(RED 親驗,全指新符號)→ Phase 2 實作六處全綠(build/vet/test/gofmt 乾淨)→ Phase 3 fresh TOP 測試審查:六個存活突變全數補強(重算保底/硬牆、landRefresh 信封同一性抽純函數、dirty 只在真結算輪清——Decision.Settled 已入碼修復規格漂移、StatusView 鍵在場斷言、collect 極性+存檔重抓子串釘死、新鮮門檻精確邊界)+ keeper 三情境;**聯合突變輪七發全中**(每個新斷言都抓到對應弄壞實作)後原樣還原,`go test -count=1 ./...` 11 套件全綠、vet/gofmt 乾淨。設計文件 §6/§7/§8/§9 已同步(含 A5 既有漂移調和)。零 commit 零 push,等 Benjamin 驗收。人工驗收清單(無法自動化):WebView2 實窗 raise 帶前景、頁面 JS 執行期行為(卡片行顯示/按鈕換標籤/存檔重抓)、瀏覽器後備路徑、舊窗卡死是否隨單例消失。
