# T02 — 政策引擎(切換規則 P1–P8 + B 配速預算,純邏輯)

- depends-on:T01(共用型別 `internal/domain`)
- expected files:`internal/policy/`(含 `*_test.go`)
- 領域依據:設計文件 §6 §7(規則表與配速公式為權威)

## 目標

把「什麼時候切、切到誰、預算怎麼配速」寫成**無 IO 的純函式引擎**:吃(時刻、目前身分、A/B 額度快照、預算狀態、設定),吐決策(動作+原因)。時間一律由參數注入,不讀時鐘、不發網路、不碰檔案——TDD 的理想形狀,也是 T03 主迴圈的腦。

## 非目標

- 任何 IO(查詢、寫檔、通知)
- UI 或選單狀態

## 現況錨點

- T01 的 `internal/domain`(Profile、QuotaSnapshot、Config);若 `store.State` 需要欄位,在 T02 定義 `BudgetState` 形狀並以 Go 型別直接放進 T01 的 State(序列化由 store 泛用處理;此為唯一允許的跨卡既有檔案小改,寫進交付清單)。

## 介面契約(完整給定)

```go
package policy

// 入口(T03 迴圈消費):func Evaluate(in Input) Decision
// Cfg 型別=cc-fuse/internal/config 的 Config(T01 實際形狀;原稿 domain.Config 係筆誤,2026-11-15 修訂)
type Input struct {
    Now          time.Time
    Current      string          // "A"|"B"|""
    QuotaA, QuotaB *domain.QuotaSnapshot // nil = 無資料
    Budget       BudgetState     // 跨日持久化狀態(見下)
    Cfg          config.Config   // 全部閾值(修訂 2026-11-15)
    AutopilotOn  bool
    External     ExtEvent        // 檔案監看事件(空=無)
    Manual       ManualCmd       // 手動指令(空=無)
}
// 型別形狀補全(per audit A5):
//   ExtEvent  = { Kind string // "":無事件 | "provider_changed":外部切到認得的帳號 | "unknown_provider":外部切到未知帳號 }
//               (env 以外變動由 T01 Watch 分類為 UnrelatedChange,T03 過濾後不進政策)
//   ManualCmd = { Target string // "A" | "B" | "":無指令 }
//   調適歸屬:T01 switcher.Event → policy.ExtEvent、tray 手動指令 → policy.ManualCmd,均由 T03(app 迴圈)映射;T02 只認上述形狀。
type Decision struct {
    Action     Action  // Stay | SwitchTo(id) | Pause | Resume | None
    Target     string  // SwitchTo 時的 "A"/"B"
    Reason     string  // 人類可讀,含觸發數字(通知與 log 用)
    QuotaMode  string  // "RACING"|"SAVING"|""(非空=切換後要翻的 mode)
    Budget     *BudgetState // 非nil=預算狀態有變更(結算或 P7 預警旗標異動),呼叫端一律持久化(2026-11-15 釐清,T03 據此)
    Stale      bool    // 資料過期 → 呼叫端暫停決策
    WarnFired  bool    // P7 預警旗標(per audit A5):當前帳號 5h% 跨過 warnThreshold 時 true,呼叫端通知一次
}

type BudgetState struct {
    WeekAnchorTime  *time.Time // B 週窗口重置時刻(nextResetTime 偵測;於結算時採納最新值)
    DayBoundary     *time.Time // 當預算日起點(錨定時刻,例 15:00)
    DayStartWeekly  *float64   // 當日開始時的 B 週%
    CeilingToday    *float64
    WarnedFiveHour  bool       // P7 已預警旗標(每窗口一次;修訂 2026-11-15,承卡片原文「以 BudgetState 記已預警」選項)
}
// 結算規則細節(2026-11-15 定案):
//   觸發=Now ≥ DayBoundary;新 DayBoundary=舊值+24h;日界=錨定時刻的時鐘時間
//   daysLeft = [今日, 次週錨點日期) 區間內的工作日數(WorkdaysOnly)或日曆日數;最少 1
//   WeekAnchorTime 缺 → 兜底:日界 00:00、daysLeft=7(設計 §7)
//   結算需 B 週% 有效;無效 → 不結算(Budget=nil),Reason 標明
//   日中(未過界)B 週% 下降 → 天花板不變(Budget=nil)
//   WeekAnchorTime 更新隨結算一併進行
```

## 規則實作規格(P1–P8,優先序即編號序;設計 §6 為權威)

- **P1 手動最優先**:Manual 切換 → 直接 SwitchTo,Reason 註記「手動」;不因自動規則取消;**手動切換同樣設 QuotaMode**(Benjamin 核可 2026-11-15,PD3):手動→B 即 SAVING、手動→A 即 RACING,與自動規則對稱。
- **P2 資料品質闸門**:任一側 Quota 為 nil、或 `Now−QueriedAt > staleAfterMin`、或當前帳號 HTTPStatus∈{401,403} → Stale=true 且 Action=None/Pause(金鑰失效另以 Reason 標明)。
- **P3 A→B**:Current=A 且(A.5h% ≥ switch5h 或 A.週% ≥ switchWeekly)且 **B 可用** → SwitchTo B,QuotaMode=SAVING。
- **P4 B→A**:Current=B 且 A.5h% ≤ recovery5h 且 A.週% ≤ recoveryWeekly → SwitchTo A,QuotaMode=RACING。
- **P5 B 預算嚴格護欄**:Current=B 且 B 週% ≥ 今日天花板 → SwitchTo A(縱使 A 未恢復),Reason 載明「B 今日額度用完,寧停等不超支」。
- **P6 兩邊不可用**:Current=A 且 A 超閾 且 B 不可用 → 目標=較早恢復者。**B 候選資格(2026-11-15 仲裁,per audit A2/A3 + D5 錢優於時間)**:B 預算耗盡(週%≥天花板)→ 喪失候選資格直至下次結算,絕不切入(提前切入=結算前燒錢越線);B 僅 5h 受阻而預算可用 → 仍為候選,恢復時間=自身 5h 重置(結算解不開 5h 之鎖)。候選比較:較早恢復者勝;平手或任一缺重置資訊 → 留 A。
- **P7 預警**:當前帳號 5h% 跨過 warnThreshold(每窗口一次,以 BudgetState 或回傳欄位記「已預警」)→ Decision 帶預警旗標(Action 可疊 Stay)。**旗標清除語意(2026-11-15 釘死)**:5h% 回落到 warnThreshold 以下 → 清除 WarnedFiveHour(回落後再跨越=新的高點推進,值得再警);僅「當前帳號」觸發警示。
- **動作枚舉釘死(2026-11-15,審計 O1)**:健康閒置→Stay;資料過期/缺失/金鑰失效→ActionNone(僅停用本輪決策,非暫停模式);autopilot 關閉→ActionNone;provider_changed→ActionNone(靜默採納);unknown_provider→Pause(唯一產生 Pause 的路徑);ActionResume 不由任何規則產生(恢復是 T03 的使用者動作,直接翻 AutopilotOn)。
- **P8 外部未知供應商**:External=UnknownProvider → Pause。
- **B 可用定義**:B.週% < 今日天花板 且 B.5h% < switch5h(天花板未知時視為可用)。
- **天花板過期判定**:跨過 DayBoundary 時刻 → 結算新的一天(daysLeft、allowance、ceiling 見 §7 公式),回傳新 BudgetState 要求持久化。

## 配速數學(設計 §7 為權威,此處給可測恆等式)

1. `daysLeft = max(1, 預算週內剩餘工作日數)`;workdaysOnly=true 時排除週六日;工作日以 Now 起算到 WeekAnchor,逐日曆日判斷;WeekAnchor 缺 → 7。
2. `allowance = max((weeklyTotal − DayStartWeekly)/daysLeft, minDaily)`。
3. `ceiling = DayStartWeekly + allowance`。
4. **恆等式(必測)**:連續多日天天燒到天花板 → 每日 allowance 收斂於 weeklyTotal/剩餘工作日數(90/7≈12.86→…→90/1),週% 永不超過 weeklyTotal。
   **但書(per audit A1,2026-11-15 精確化)**:保底發動時除外——此時 ceiling 可越過 weeklyTotal(吃進預留餘裕),但一律夾在真牆 **100(含)以內**(ceiling=min(dayStart+allowance, 100));冷啟動週尾案例(週%已 88、daysLeft=1 → ceiling=91)與極端案例(98+3→夾為 100)以此讀法通過,保底測試與恆等式測試不再互斥。
5. 日中週% 下降(滾動過期)→ 當日 ceiling **不**放寬。
6. 錨定:WeekAnchor 由 B 的 WeeklyReset 逐次更新;DayBoundary 的時分 = WeekAnchor 的時分(例 15:00),日期=當天。
7. 無持久狀態冷啟 → DayStartWeekly=當下 B 週%,ceiling 立即以當時計算。

## 驗收標準(黑箱,全部表格驅動)

1. P1–P8 每條:觸發案例 + 不觸發案例 + 邊界(恰好等於閾值)至少各一;P3/P4 的 5h 與週窗口獨立觸發;遲滯(90 切/30 回)不抖動案例。
2. 配速恆等式(上節 4–7)逐條測,含週五 15:00 錨定跨日結算、週末排除下 daysLeft 計算、保底發動、雙雙撞牆時 P6 的 B 端取 min(額度重置, 明日結算)。
3. 預算狀態往返(BudgetState → store 序列化 → 回讀)不失真。
4. 決策輸出之 Reason 含觸發數字(可讀性驗收:字串含實際百分比)。
5. `go test ./...` 全綠;零 IO(測試不需網路/檔案系統/時鐘——時間全注入)。

## 風險與回退

- 風險:天花板判定與結算時序在邊界(恰於錨定時刻查詢)雙觸發 → 結算為冪等設計(同一天重結算同結果),測試覆蓋。
- 回退:純新增套件,刪除即回退;唯一跨卡改動=BudgetState 型別放進 T01 的 store State(序列化泛用,不改 T01 行為)。
