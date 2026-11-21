# 需求文件:B 配速預算天數語意修正(含重置日)+ 顯示如實化

- 日期:2026-11-19
- 狀態:settled(與使用者討論定案,使用者已核准開工)
- 單元名:`budget-day-inclusive`

## 背景(現況與實證)

cc-fuse 的 B 帳號「配速預算」把週預算總量(例 95%)按每日結算均分:每天結算時
`天花板 = 日始週%基準 + max((週預算總量 − 基準) ÷ 剩餘預算日, 每日保底)`。
程式在 `internal/policy/policy.go`:`daysLeftInWindow`(剩餘天數)、
`settleBudget`(每日結算)、`nextBoundary`(下一次結算時刻)、`RefreshBudget`(存檔即時重算)。

2026-11-19(週四)實測(來源 `%APPDATA%\cc-fuse\state.json`,已驗證):
`DayStartWeekly=64`、`WeekAnchorTime=2026-11-20T14:57(週五)`、`CeilingToday=95`、
`DayBoundary=2026-11-20T00:00`。B 週已用 71%。

兩個問題:

1. **重置日被排除,最後一個預算日吃掉全部剩餘額度。**`daysLeftInWindow` 數
   「今天(含)到重置日(不含)」,週五(重置日)整日不算 → 週四結算得
   days=1 → 天花板 = 64 + (95−64)÷1 = 95。若週四真燒到 95,週五早上只剩
   「每日保底 3%」的殘量,B 失去避難所資格,要乾等到 14:57 週重置。
2. **每日結算時刻卡死在午夜 00:00。**`nextBoundary` 給「舊日界+24h」較高優先權,
   錨點鐘點只在第一次冷啟動(無日界紀錄)時用一次;這條鏈當初在查不到重置時間時
   以 00:00 開跑,之後永久沿用,即使錨點(14:57)已知。設計文件
   `docs/blueprints/2026-11-15-cc-fuse-design.md` §「預算週錨定」明寫:結算時刻
   跟週重置鐘點(例:週五 15:00 重置 → 每天 15:00 結算,**不用 00:00**),
   00:00 只是查不到重置時間的備案。

## 需求原文(使用者,2026-11-19)

> 「這東西的目的是不讓重置前沒 quota 用, 所以要剩餘日平均分配. 照 95 今天用掉,
> 豈不明天沒得用?? 這不對」

## 定案(使用者核准「ok go」,全批執行)

語意模型(使用者 2026-11-19 追加澄清,逐字):
「比如, 週四今天早上, 說到15:00 前能用到多少, 用的是 web 1500 to thu 1500 quota.
過了 thu 1500, 說 到fri 1500 能用到多少」(補正:時刻以實際偵測的重置瞬間 14:57 為準,
不取整)— 即:額度陳述永遠綁定「上一個 cut-off → 下一個 cut-off」的切片,
每日額度 = 該切片的配額,最後一切片到週重置瞬間為止。此語意與 D1+D2 的引擎行為一致,
D3/D3a 把顯示講法對齊(不用「今日/today」,用「到下一個 cut-off 前」)。

- **D1 天數含重置日(含頭含尾)**:`daysLeftInWindow` 改數
  `[today, anchorDate]` 含 anchor 當日(重置日是一個預算日,它的額度涵蓋重置時刻前的早上)。
  workdaysOnly 與「永不小於 1」的夾限(clamp)語意不變。
- **D2 結算時刻貼回錨點鐘點**:`nextBoundary` 改為錨點優先 — 只要週重置錨點已知,
  下一次結算 = 今天錨點鐘點(已過則 +24h 一次),不再讓「舊日界+24h」的鏈把
  00:00 永久沿用;錨點未知時維持現狀(有日界 → 舊+24h,無 → 當日午夜)。
- **D3 B 卡天花板 chip 改用 cut-off 講法並加顯示尚可燒量**:chip 前綴下一個
  cut-off 時刻,不再用「今日/today」字眼;並加一段 `ceiling − 週已用`
  (pt,一位小數,下限 0)。顯示格式(時刻 = 下一個結算/cut-off 瞬間,見 D3a;
  星期用短寫、HH:MM 本機時間去秒,實際錨點例如 14:57,不取整):
  EN:`Until Fri 14:57: ceiling wk 79.5% · 8.5pt left (forces switch back to A)`;
  zh:`到週五 14:57 前:天花板 週79.5% · 尚可 8.5pt(到點強制切回 A)`。
  cut-off 時刻未知時省略前綴(呈現 `ceiling wk X% · Ypt left` 無 today 字眼的形態)。
  只改顯示,不改切換邏輯;`ceilingToday` 或週% 缺任一時不加「尚可」段(維持現狀);
  chip 整行在 `ceilingToday==null` 時不建(既有規則不變)。
- **D3a status payload 帶下一個 cut-off 時刻**:/api/status 的 B 卡資料新增
  下一個結算瞬間(來源 `BudgetState.DayBoundary`;nil → 不帶此欄),
  前端據此渲染 D3 的「Until …」前綴。
- **D4 UI 標籤欄對齊**:`internal/ui/page.go` CSS `label{min-width:170px}` 改
  `label{width:190px}`(固定寬;最長英文標籤 "No auto-flee on weekends" 約 175px,
  放得下;所有輸入框/開關同一條 x 起點)。
- **D5 文件回寫**:設計文件 `2026-11-15-cc-fuse-design.md` 的公式/天數/結算時刻
  段落由 supervisor 層回寫(舊文刪線保留),本檔為需求事實來源。

## 驗收語意(驗收測試須釘死;例子用 2026-11-19 真實日期)

- A1 daysLeftInWindow(now=週四 11-19, anchor=週五 11-20 14:57, workdays=true)
  **= 2**(舊=1;含週五)。
- A2 同週週一(now=11-16, 同 anchor)= **5**(一~五;舊=4)。
- A3 週末排除與夾限不變:anchor=週六、now=週五、workdays=true → 1;
  now=週六、anchor=週日(全週末)→ 0 → 夾成 1;workdays=false 時含頭含尾照數
  (now=週四、anchor=週六 → 3)。
- A4 settleBudget:基準 64、總量 95、days=2 → 天花板 **79.5**;days=1 → 95
  (每日保底、hardWall 100 夾限邏輯不變)。
- A5 nextBoundary 三態:①既有午夜日界 + 已知錨點 → 貼錨點鐘點
  (now=週四 00:00 後、錨點 14:57 → 下一界 = 當日 14:57;已過 14:57 → +24h);
  ②錨點 nil + 有日界 → 舊+24h(維持);③兩者皆 nil → 當日午夜(維持)。
- A6 RefreshBudget 重算路徑走同一個 daysLeftInWindow:ref=日界−24h,
  本例重算今日天花板 = 79.5(收緊現狀 95;「日中不放寬」針對同公式重算,
  公式改版造成的收緊是本需求要的效果)。
- A7 UI chip:ceiling=79.5、週已用 71、DayBoundary=週五 14:57 → 顯示
  EN `Until Fri 14:57: ceiling wk 79.5% · 8.5pt left …` /
  zh「到週五 14:57 前:天花板 週79.5% · 尚可 8.5pt …」;
  週已用 ≥ 天花板 → 尚可顯示 0.0;DayBoundary 缺 → 無前綴;
  缺資料時不加「尚可」段、ceilingToday 缺時整行不建(既有)。
- A8 CSS:label 固定 190px(以字串標記檢查嵌入式 HTML 為準)。

## 範圍與約束

- 產品碼:`internal/policy/policy.go`(D1、D2)、`internal/ui/page.go`(D3、D4)、`internal/ui/server.go`、`internal/app/loop.go`、`cmd/cc-fuse/settings.go`(D3a;~~未列~~ 2026-11-19 實作期補列 — 修訂版 D3a 實際觸及)。
- 不新增設定欄位(所有數字皆已可設定);不碰 DB;不碰切換決策 P3–P6 的語意
  (bAvailable/天花板比較邏輯不變,只有天花板「數值」隨天數語意變化)。
- 既有測試是回歸牆:斷言舊天數語意的測試按新契約演進更新 — 一次一檔、
  斷言語意不變形,每處改動在報告列理由。
- 回歸範圍:internal/policy、internal/app(budgetrefresh/budgetmerge)、
  internal/ui(budgetview/status/page markers)相關套件測試全綠 +
  `go build ./...`、`go vet ./...` 乾淨。
- 全鏈零 git 寫入;使用者手動收尾 commit。

## 已接受的殘留風險(記錄用,非待辦)

- 佈署當下已存在的午夜日界:升級後第一次結算會把鏈貼回錨點鐘點;當天天花板
  要等那次結算或使用者按一次 Save(RefreshBudget)才會以新語意重算(使用者已知悉)。

## 修訂 2(2026-11-19 下午,使用者回報實測落差)

- 使用者原文:「image 21.png after saved . should not say until friday 00:00.
  should 14:57 matches to calculation cutting time」
- 觀察(實證):存檔重算**已正確**產生天花板 79.5%(基準 64 + 31÷2)、尚可 8.5pt;
  但 chip 前綴與下一次結算時刻仍停在舊午夜鏈的 `DayBoundary`(Fri 00:00)。
  原因:重算路徑照舊契約「日界不動」,導致顯示的 cut-off 與計算真正的切截時刻脫鉤。
- **D6 日界貼錨點規則延伸到「存檔重算」與「程式啟動」**(不只下次結算):
  - **D6a** policy 新增輸出 helper `SnapBoundary(now, boundary, anchor)`:
    日界與錨點皆非 nil → 回傳「今天的錨點鐘點,已過則 +24h」;
    任一為 nil → 原樣回傳(無錨點備案鏈與「基準無日界不可算」防禦皆不變)。
  - **D6b** app 落帳:`landRefresh` 的信封身分檢查維持在**原日界**上(不動、防並發真結算),
    落帳成功後於同一把鎖內將 envelope 的 `DayBoundary` 換成 `SnapBoundary` 結果
    (冪等;同值不重寫)。policy.RefreshBudget 本體**不改**(保持純重算,既有驗收不動)。
  - **D6c** app 啟動:`LoadBudget` 載入後,日界與錨點皆存在時就地貼回
    (用實際當下時間;只在記憶體生效,下次結算落盤時帶出)。
- 驗收語意:
  - **A9**:now=週四 12:21、日界=週五 00:00、錨點=週五 14:57、基準 64、總量 95 →
    RefreshBudget 落帳後 envelope:天花板 **79.5**、日界=**週四 14:57**(顯示 until 同步)。
  - **A10**:now 已過當日錨點鐘點(週四 16:00)→ 日界貼**週五 14:57**(明天的鐘點);
    天花板仍由舊日界推得的預算日起點計算(79.5,不受貼回影響)。
  - **A11**:無錨點 → 日界原樣(備案鏈行為不變);`LoadBudget` 同規則;日界缺失 → 不猜(K1(d) 不變)。

### 修訂 2 補強(同日新鮮眼審查發現,當日定案)

- **D6a 語意修正**:`SnapBoundary` 的 boundary 為 nil 時回傳 nil — **不憑空造日界**
  (K1(d) 精神;原文「任一為 nil → 原樣回傳」即此意,實作初版對 nil boundary 會
  造出瞬間,已改正)。恰在錨點鐘點的瞬間 → +24h(M7 含端點慣例)。
- **D6d 持久化預算日起點 `DayStartAt`**(state.json 新欄位,舊檔缺欄自動相容):
  結算與重算落帳都寫入「本預算日的起點瞬間」;RefreshBudget 的天數 ref 優先取它,
  缺欄才回退「日界−24h」舊演算法並在落帳時補寫。
  理由:D6b 的日界貼回讓「日界−24h」不再恆等於預算日起點(貼回後同日再存一次檔,
  ref 被推到前一天 → 天花板 79.5→74.33 漂移),違反既定恆等式「同預算日同設定
  存檔 → 同天花板、跨存檔不漂移」;DayStartAt 讓 ref 與日界徹底解耦,恆等式
  在貼回後仍成立。
- 驗收語意補:
  - **A12**:同一預算日內連續兩次存檔 → 兩次天花板相同(79.5 恆定)、日界穩定在
    貼回值;舊 state 檔(無 DayStartAt)第一次重算後即補寫,第二次起不漂移。
  - **A13**:`SnapBoundary(now, nil, anchor)` → nil;`now` 恰等於當日錨點鐘點 → +24h;
    不同錨點鐘點(例 09:13)同規則貼回,app 行為級同。
  - **A14**:boot 載入:錨點在、日界缺 → 日界維持缺失(不猜)。

## 修訂 3(2026-11-19 傍晚,使用者以實例定案天數語意)

- 使用者原文(逐字,勾選假日不算的情境):
  「day1 fri 1457 to mon 1457 / day2 mon 1457 to tue 1457 / day3 tue 1457 to wed 1457 /
  day4 wed 1457 to thu 1457 / day5 thu 1457 to fri 1457」
  「fri 1600 時. day 1. 5 days to reset, each day limit is max ceiling (let's say 95 here)
  divid 5 days」「thu 0900 時, 2 days left .( current percent to max ceiling ) divide 2 days」
  「thu 1600, 1 day left . ( current percent to max ceiling ) divide 1 days」
- **定案(切片語意)**:預算日 = 切片(切截鐘點 → 切截鐘點);`workdaysOnly` 勾選時,
  落在週六/週日的切截**跳過結算、其切片併入前一個工作日切片**(Fri 14:57 → Mon 14:57
  為同一個預算日 day1)。剩餘天數 = 含當前切片在內、到重置瞬間還剩幾片:
  `days = 1 + (日界之後、重置之前的工作日切截瞬間數)`。
  使用者三時刻驗證:Fri 16:00 → 5、Thu 09:00 → 2、Thu 16:00 → 1
  (最後一片領全額:天花板 = 當前週% + 剩餘 = 週預算總量)。
- 取代修訂 2 的「日曆日含重置日」計數:舊法把最後一片(切片語意下只剩一片)數成
  兩個曆日,尾片只領半份、整週收敛少 9.5 點(死額度);切片語意下整週收敛到
  完整的週預算總量。日中重算以 `DayStartAt`(切片起點)為 counting 基準,恆等式
  「同切片同設定存檔 → 同天花板」不變;切片起點在非工作日切截上的情境不存在
  (週末切截被跳過,切片起點恆為工作日)。
- 影響:全週每天的分配數字改變(例:總量 95、週一起算 → 4 片均分;最後一片領到
  95);既有測試的天數算術按新語意重推(推導寫在測試註解)。
- 既有接受:最後一片領全額 = 週四晚可燒到 95,週五早上 B 在線上(≥天花板)被擋至
  重置 — 使用者以本例明確選擇「用不完就作廢」的尾部行為。
- **D6c 語意補強(順序洞,使用者實測 restart 後第一次 Save 漂到 74.3)**:
  載入時的兩步有先後相依 — 必須**先用原始日界補出 `DayStartAt`(日界−24h)、
  再貼回日界**;順序反了,起點會從貼回後的日界倒推到前一天(週三 14:57),
  天數多算一天,第一次存檔即 74.3。有基準缺欄的舊檔在載入時補寫;
  日界已在錨點鐘點上、又無 `DayStartAt` 的狀態(僅今天過渡期會出現)無法重建
  真實起點,屬已知殘留,14:57 結算後 `DayStartAt` 由結算寫入即永久自癒。
- 結算時刻貼到 14:57 後,該筆結算貼著週重置瞬間:快照新鮮度閘(≤ staleAfterMin)
  決定它讀到重置前或重置後的值,單一片額度可能有一次過渡性雜訊,下次結算自癒。
