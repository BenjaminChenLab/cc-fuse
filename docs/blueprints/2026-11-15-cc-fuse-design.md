# cc-fuse 設計文件

- 日期:2026-11-15
- 狀態:**已與使用者談定,等待開工指令**
- 專案位置:`E:\@sourceCode\cc-fuse`

## 1. 背景與目標

使用者有兩個 z.ai GLM Coding Plan 帳號:**A(團隊共用)**與 **B(自購)**。現行痛點:

1. A 撞 429(5 小時限額)時人在外面,工作停擺數小時直到手動切 B。
2. 手動切 B 後常忘記切回 A,白燒自己的錢。

cc-fuse 是 Windows 系統匣常駐小工具(Go,單一 exe),像保險絲一樣在過載前搶先熔斷:A 快滿自動切 B、A 恢復自動切回 A、B 有配速預算護欄。**同時完全取代 cc-switch 在本機的用途**(使用者只用它:切 Claude Code 供應商 + 看額度數字),cc-switch 退休,不存在狀態同步問題。

成功判準:上班時間 A 撞牆到切 B 的延遲 ≤ 1 分鐘輪詢週期;A 恢復後切回延遲同上;B 永遠不會一天燒超配速額度。

## 2. 已驗證的外部事實(設計地基)

以下皆於 2026-11-15 從 cc-switch 開源碼(`github.com/farion1231/cc-switch`,淺複製驗證)確認:

| # | 事實 | 證據位置 |
|---|---|---|
| V1 | 額度端點 `GET https://api.z.ai/api/monitor/usage/quota/limit`,header `Authorization: <apiKey>`(不加 Bearer),15 秒逾時 | `src-tauri/src/services/coding_plan.rs` `query_zhipu_at()` |
| V2 | 回傳 `data.limits[]`:type=`TOKENS_LIMIT`,`unit=3`=5 小時窗口、`unit=6`=週窗口;每條 `percentage`=已用%、`nextResetTime`=ms(5h 桶 0% 時可能缺 reset);`level`=套餐等級 | 同上 `parse_zhipu_token_tiers()` |
| V3 | 帳號級數字(A 含全隊隊友用量);監控端點不耗對話額度 | 端點語意 |
| V4 | 按量帳號(無 coding plan)回 HTTP 200 + `{"success":false,"msg":"…coding plan…"}` 業務錯 | `zhipu_body_has_no_plan()` |
| V5 | Claude Code 支援供應商熱切換,改 settings.json 即生效、進行中 session 不需重啟 | cc-switch 官方 FAQ |
| V6 | cc-switch 無檔案監看、無外部切換介面(deeplink 僅匯入)→ 外部切換無法同步進它的顯示 → 採「替代」而非「共存同步」 | Rust 原始碼 grep 零匹配 + `deeplink/mod.rs` |
| V7 | cc-switch 退出時只回寫「接在本機代理上的客戶端」(Routing 模式);Direct 模式退出不碰 settings.json | `lib.rs` `cleanup_before_exit` + `mode/controller.rs` `detach_all` |

## 3. 架構

```
[Monitor 額度監看器] → [Policy 政策引擎] → [Switcher 切換器] → ~/.claude/settings.json
       │                    │
       │                    ├→ [QuotaModeWriter] → S:\claude-cache\quota-mode.md(mode: 行)
       │                    └→ [Notifier] → Windows Toast
       └→ [TrayUI 匣選單+圖示] ←使用者手動操作
              └→ [SettingsUI 設定窗(WebView2)]
```

四個單元各司其職:Monitor 只負責拿數字、Policy 只負責決策(純函式、可表格測試)、Switcher 只負責安全地改檔、UI 只負責呈現與手動指令。單向依賴,Policy 不做 IO(時間由 Monitor 餵入)。

## 4. Monitor(額度監看器)

- 每週期對 A、B **各發一次** V1 端點請求(平行)。週期預設 60 秒;**目前使用中帳號**任一窗口 > `fastPollAbovePct`(預設 80)時縮為 `fastPollSec`(預設 20 秒)。
- 解析輸出結構:`{fiveHourPct, fiveHourReset, weeklyPct, weeklyReset, planLevel, queriedAt, err}`;遇 V4 業務錯或 HTTP 失敗 → 該帳號標記查詢失敗。
- 暫時性失敗(網路/逾時):退避重試,沿用上次數字。**資料年齡 > `staleAfterMin`(預設 10 分鐘)→ 政策引擎暫停一切切換決策、圖示變灰、Toast 通知一次**,絕不拿舊數字切換。
- 確定性失敗(401/403):該帳號金鑰失效 → 通知 + 圖示變紅,不切換。

## 5. Switcher(切換器)

- 切換 = 讀 `C:\Users\Benjamin\.claude\settings.json` → 只替換 `env` 區塊內屬於供應商的欄位 → 原子寫回(同目錄暫存檔 + rename)。
- 供應商欄位集合 = `ANTHROPIC_API_KEY`、`ANTHROPIC_BASE_URL` + 該 profile 自帶的 `ANTHROPIC_DEFAULT_*` 模型對應鍵值(清單存在 profile 裡,不寫死)。**其他鍵與頂層所有區塊零碰觸**。
- 已知化妝品級副作用:JSON 重新序列化後鍵序會正規化(功能零影響,接受)。
- 首次改動前備份原檔到 `%APPDATA%\cc-fuse\backups\`(保留最近 10 份)。
- 寫入前後防衝:寫入流程為「讀 → 改 → 比對讀取時 mtime → 原子寫」;若期間檔案被他人改過,重讀再試(最多 3 次)。
- **身分判定**:A、B 端點相同,一律以 `env.ANTHROPIC_API_KEY` 比對 profile 決定「目前在用誰」。
- 檔案監看(外部變動偵測):watch `settings.json`;變動時比對 API key——認得(A/B)→ 默默採納為新現況;認不得(未知 key)→ 政策暫停 + Toast 詢問;**env 以外的變動(Claude Code 自己改 model 等)一律忽略**。

## 6. Policy(政策引擎)

立場:**永遠優先 A,B 是避難所;所有決策基於百分比,不猜重置語意**。規則按優先順序:

| # | 規則 | 動作 |
|---|---|---|
| P1 | 手動指令最優先:匣選單切 A/B 立即執行;自動政策不阻擋、下個週期照常接管;**手動切換同樣翻 quota-mode**(2026-11-15 Benjamin 核可,PD3):手動切 B→SAVING、切回 A→RACING | 切換+通知+翻 mode |
| P2 | 資料過期或查詢失效 → 不做任何自動決策 | 圖示灰/紅 |
| P3 | 目前在 A:5h% ≥ 切換閾值 **或** 週% ≥ 切換閾值,且 B 可用(見 §7)→ 切 B | 切換+Toast+quota-mode→SAVING |
| P4 | 目前在 B:A 的 5h% ≤ 恢復閾值 **且** 週% ≤ 週恢復閾值 → 切回 A;A 無週窗口(週% 缺失)時視為自動達標(2026-11-15 入碼 commit c17ea18,文件補記 per audit A5) | 切換+Toast+quota-mode→RACING |
| P5 | 目前在 B:B 預算天花板已到(見 §7)→ 嚴格執行:立刻切回 A,即使 A 尚未恢復(工作停等 A 重置,可接受——使用者明確選擇寧停等不超支自己的錢)。**總開關 `budget.enforceDailyCeiling`(2026-11-16,預設開)**:關閉時本條不生效,B 燒過天花板也留在 B | 切回 A+Toast 說明 |
| P6 | 兩邊都不可用(A 未恢復 且 B 不可用)→ 目標=較早恢復者。**B 候選資格(2026-11-15 仲裁,per audit A2/A3 + D5 錢優於時間)**:B 預算耗盡(週%≥天花板)→ **喪失候選資格**直至下次結算——P6 絕不切進預算耗盡的 B(提前切入會在結算前燒錢越線);B 僅 5h 受阻而預算可用 → 仍為候選,其恢復時間=自身 5h 重置(結算解不開 5h 之鎖)。候選比較:較早恢復者勝;平手或任一缺重置資訊 → 留 A。**總開關關閉時(2026-11-16):預算耗盡不影響候選資格**,照常比較 5h 重置 | 切換+Toast「兩邊都滿」 |
| P7 | 預警:目前帳號 5h% 跨過 `warnThreshold`(預設 80)時 Toast 一次(每窗口一次,不洗版) | 僅通知 |
| P8 | 外部未知變動 → 政策暫停,等使用者處理 | 圖示灰+Toast |

遲滯設計:恢復閾值(30)遠低於切換閾值(90),臨界點不來回抖動。

## 7. B 配速預算(核心護欄)

**動機**(使用者原話精神):B 的週額度絕不能週一就燒完,否則後面幾天全完;且 B 是自己的錢,上限到了寧可停等 A 重置也不超支。

**公式(每預算日結算一次):**

```
剩餘天數 daysLeft = 預算週內剩餘的工作日數(預設排除週六日,「假日要不要算」為設定勾選;最少 1)
今日額度 allowance = max( (weeklyBudgetTotal − 週%已用_當日開始) ÷ daysLeft, minDailyAllowance )
今日天花板 ceiling = 週%已用_當日開始 + allowance
```

- `weeklyBudgetTotal` 預設 90(真牆 100% 前留餘裕);`minDailyAllowance` 預設 3(設 0 = 嚴格到底)。全部可設定。
- **預算週錨定**:以 B 週窗口 `nextResetTime` 自動偵測(例:週五 15:00 重置 → 預算日每天 15:00 結算,不用 00:00);查不到重置時間時 fallback:結算時刻 00:00、daysLeft=7。
- **配速性質**:天天燒滿,明日額度恆約 12.9%(90÷7),週額度永不提前見底;省著用,後面額度自動變大。不需手動調數字。
- 狀態持久化(當日基準週%、天花板、結算時刻)存 `%APPDATA%\cc-fuse\state.json`,重開機不歸零;app 中途啟動且無紀錄 → 以當下週% 當基準。
- 保守原則:日中週% 因滾動過期而下降時,天花板**不**跟著放寬(當日結算時鎖定)。
- **天花板封頂**(2026-11-15 audit A1 入碼、文件補記 per audit A5):ceiling 最後夾 `min(…, 100)` — 每日保底吃掉 headroom 時也不得跨真牆。
- **總開關 `enforceDailyCeiling`(2026-11-16,預設開)**:關閉時今日天花板**完全不影響切換**(P5/P6/「B 可用」三處全跳過),只留顯示與記帳。
- **存檔即時重算(2026-11-16)**:改預算設定存檔 → 立即以新公式重算今日天花板;天數照「本預算日起點(結算時刻−24h)」數(整天結果一致、跨午夜存檔不漂移),基準/日界/錨點不動(日中不放寬);冷啟(今天未結算過)需 B 資料新鮮(≤ `staleAfterMin`)才錨定,過期記待補算、資料恢復(下一輪輪詢)自動補;落帳分路:冷啟整顆、重算只併入天花板+信封同一性檢查。
- 「B 可用」定義(P3/P5/P6 用):B 週% < 今日天花板 **且** B 5h% < 切換閾值(總開關關閉時天花板項跳過)。

## 8. UI(精簡版)

- **匣圖示狀態**:綠=使用 A、橘=使用 B、灰=資料過期/政策暫停、紅=金鑰失效。tooltip:`A: 42%(5h) · B: 12%(5h)`。
- **右鍵選單**:狀態行(在用誰+兩邊%)| 切到 A / 切到 B | 暫停自動 / 恢復自動 | 設定 | 結束。
- **設定窗**(WebView2 開內嵌本機頁,127.0.0.1 繫結,無外部依賴):
  - 切換卡片(2026-11-16):B 卡加「今日天花板 週 X%(到點強制切回 A/不強制)」行;A 卡加「恢復預計 HH:mm(⏱ 倒數)」行(只看 5h 重置);提示列加「自動切換 暫停/恢復」按鈕(`POST /api/autopilot`,與系統匣同一開關)。
  - **設定窗單例(2026-11-16 R6)**:重複點系統匣「設定」不開新窗,只把已開視窗帶到前景(raise 前 `IsWindow` 驗證、失敗收舊開新);瀏覽器後備(無 WebView2 runtime)同樣單例,再點重開同一網址、伺服器不重複。
  - 供應商 A/B:名稱、API key、端點、`ANTHROPIC_DEFAULT_*` 模型鍵值組;**「從 cc-switch 匯入」按鈕**(唯讀開啟 `~/.cc-switch/cc-switch.db`,純 Go SQLite 驅動,抓現有 profile 一次匯入)。
  - 閾值區:切換(5h/週)、恢復(5h/週)、預警線、輪詢間隔+快輪詢。
  - 預算區:**天花板強制切回 A(總開關,勾選,預設開)**、週預算總量、每日保底、**假日要不要算(勾選)**;原規劃的「顯示偵測到的重置錨點」未實作(2026-11-16 註記,per audit A5)。
  - 其他:quota-mode.md 路徑、開機自動啟動(登錄檔 Run 機碼)。
- 每次自動切換、預警、暫停都發 Windows Toast。

## 9. 設定檔全表(`%APPDATA%\cc-fuse\config.json`)

| 鍵 | 預設 | 說明 |
|---|---|---|
| `profiles` | – | A/B 兩組:{id, name, apiKey, baseUrl, modelEnv{…}} |
| `pollIntervalSec` / `fastPollSec` / `fastPollAbovePct` | 60 / 20 / 80 | 輪詢節奏 |
| `switchThreshold5h` / `switchThresholdWeekly` | 90 / 90 | P3 切換線 |
| `recoveryThreshold5h` / `recoveryThresholdWeekly` | 30 / 60 | P4 恢復線 |
| `warnThreshold` | 80 | P7 預警線 |
| `budget.weeklyTotalPct` / `budget.minDailyAllowancePct` / `budget.workdaysOnly` | 90 / 3 / true | §7 配速 |
| `budget.enforceDailyCeiling` | true | 天花板強制切總開關(關=天花板不影響任何切換,只顯示與記帳;2026-11-16) |
| `parseFailAlertLimit` | 5 | 連續查詢/解析失敗 N 次 → 紅色圖示+通知(0=停用;2026-11-16 補記,已入碼 per audit A5) |
| `proxyUrl` | ""(空) | 額度查詢用的 HTTP proxy(空=直連;2026-11-16 補記,已入碼 per audit A5) |
| `staleAfterMin` | 10 | 資料過期線 |
| `quotaModeFile` | `S:/claude-cache/quota-mode.md` | 只改 `mode:` 行 |
| `autoStart` | true | 開機自啟 |

API key 明文存放(v1 接受,個人機器;v2 可升級 Windows DPAPI)。

## 10. 技術棧與專案結構

- **Go**(使用者指定,機器已有環境)+ `energye/systray`(匣)+ WebView2 函式庫(設定窗,內嵌 HTML/JS,表單以 127.0.0.1 小型 HTTP 服務橋接)+ `modernc.org/sqlite`(純 Go、免 CGO,僅匯入用)。
- 單一 exe(`cc-fuse.exe`);單例鎖(具名 mutex);log 旋轉檔於 `%APPDATA%\cc-fuse\logs\`。

```
cc-fuse/
├─ CLAUDE.md
├─ docs/blueprints/2026-11-15-cc-fuse-design.md   ← 本文件
├─ cmd/cc-fuse/main.go
└─ internal/
   ├─ monitor/    # 額度 HTTP client + 解析(V1/V2/V4)
   ├─ policy/     # 規則引擎 P1–P8 + §7 配速(純邏輯,無 IO)
   ├─ switcher/   # settings.json 讀改寫、原子性、身分判定、檔案監看
   ├─ quotamode/  # quota-mode.md mode: 行翻轉
   ├─ notify/     # Windows Toast
   ├─ config/     # 設定載入/儲存/預設值
   ├─ store/      # state.json(預算基準等)
   └─ tray/       # 匣選單、圖示狀態、設定窗
```

## 11. 錯誤處理與邊界彙總

1. z.ai 業務錯「無 coding plan」→ 該帳號查詢失敗處理(P2)。
2. `nextResetTime` 缺失(5h 桶 0%)→ 只影響顯示;決策不看它。
3. 預算日結算時 app 沒在跑 → 下次啟動以持久化的上個結算基準計算;無紀錄則以當下值當基準。
4. Claude Code 自身改寫 settings.json(如 /model)→ 監看只比對 API key,不受影響。
5. quota-mode.md 不存在 → 跳過(記 log),其餘功能照常。
6. 兩份 cc-fuse 同時跑 → 具名 mutex 擋下,第二份只彈通知退出。
7. 時鐘:全部用本機時間;週期/結算判斷容忍 ±1 分鐘偏移。
8. 有聲失效(2026-11-15 Benjamin 核可,PD2):z.ai 回應形態漂移導致**連續解析失敗**(預設連續 5 次,可設定)→ 圖示紅+Toast 通知+log;v1 不做自動更新機制。

## 12. 測試計畫

- **monitor 解析**:用 cc-switch 測試碼內的 zhipu 真實回傳樣本(含 unit=3/6、缺 reset、無 coding plan 業務錯)做 fixture。
- **policy**:表格驅動,逐條規則(P1–P8)+ 配速試算表(§7 的 12.9% 恆定性質、假日排除、保底、日中滾動下降不放寬)全部斷言。
- **switcher**:「不相關欄位零損傷」測試(改前後 diff 只允許供應商欄位)、原子寫、mtime 衝突重試、外部變動身分判定。
- **手動驗收**:真 key 查一次額度端點(經使用者同意)、A/B 實際切換一輪、熱切換生效確認。

## 13. 明確不做(Out of Scope)

- 不做 cc-switch 的其他工具管理(Codex/Gemini/…)、MCP/Prompts/Skills 管理。
- 不做用量統計/費用報表(使用者不用;需要時開 cc-switch 看)。
- 不經任何本地代理、不做請求層 failover(切換就是改設定檔,保持簡單)。
- 不支援 兩個以外的供應商輪替(v1 就是 A/B;資料結構預留 profile 陣列)。
