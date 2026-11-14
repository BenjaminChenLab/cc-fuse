# T01 — 核心服務層(共用型別、設定、狀態、額度查詢解析、切換器、quota-mode 寫入)

- depends-on:無(首張卡)
- expected files:`go.mod`、`internal/domain/`、`internal/config/`、`internal/store/`、`internal/monitor/`、`internal/switcher/`、`internal/quotamode/`(含各自 `*_test.go`)
- 領域依據:設計文件 `E:\@sourceCode\cc-fuse\docs\blueprints\2026-11-15-cc-fuse-design.md` §4 §5 §9 §11(以路徑引用,此處不重複)

## 目標

建立 cc-fuse 全部「可測試的無 UI 地基」:共用型別、設定檔讀寫、跨重啟狀態、z.ai 額度查詢與解析、settings.json 安全切換器、quota-mode.md 寫入。完成後 T02(政策)與 T03(外殼)只消費本卡介面。

## 非目標

- 任何切換決策邏輯(P1–P8、配速預算)= T02
- 任何 UI、通知、主迴圈、單例、自啟 = T03
- 對 z.ai 端點的真連線測試(驗收用假 HTTP server;真 key 一次驗證屬 T03 手動清單)

## 現況錨點

全新專案(無既有程式碼)。外部錨點:

- 目標檔案結構:`C:\Users\Benjamin\.claude\settings.json`(測試用暫存副本;`env.ANTHROPIC_API_KEY`/`env.ANTHROPIC_BASE_URL`/`env.ANTHROPIC_DEFAULT_*` 為供應商欄位,其餘一律保留)
- `S:\claude-cache\quota-mode.md`(`mode: RACING|SAVING` 行 + 卡片內容)
- 參考實作:`S:\claude-cache\cc-switch-ref\src-tauri\src\services\coding_plan.rs`(z.ai 解析的 Rust 原始邏輯,行為照抄、陷阱照避)

## 介面契約(完整給定,跨卡邊界)

```go
// internal/domain
type Profile struct { ID, Name, APIKey, BaseURL string; ModelEnv map[string]string } // ID ∈ {"A","B"}
type QuotaSnapshot struct {
    ProfileID      string
    FiveHourPct    *float64   // nil = 該桶缺
    FiveHourReset  *time.Time // 5h 桶 0% 時可能缺
    WeeklyPct      *float64
    WeeklyReset    *time.Time
    PlanLevel      string
    QueriedAt      time.Time
    Err            error      // 查詢失敗(網路/業務錯/401)
    HTTPStatus     int        // 401/403 → 金鑰失效
}
// monitor(編碼審計 2026-11-15 定案之公開形狀):
//   New(baseURL string, timeout time.Duration) *Client     // base=profile.BaseURL;超時可注入供測試,正式呼叫端給 15s
//   (c) Query(ctx context.Context, p domain.Profile) domain.QuotaSnapshot
//       查詢路徑恆為 host 根的 /api/monitor/usage/quota/limit——base 帶 /api/anthropic 尾巴時須剝到 host 根
//       (決策 2026-11-15:quota 端點固定在 host 根,防雙路徑拼接)
//   Parse(data []byte) domain.QuotaSnapshot                // 純解析,可獨立測
//   哨兵錯誤:ErrNoPlan(按量帳號業務錯)、ErrAuthInvalid(401/403);其餘=瞬時/確定性一般錯,以 errors.Is 判
// switcher: Identify(settingsJSON []byte, profiles []domain.Profile) (ProfileID string, known bool)
//           // 以 env.ANTHROPIC_API_KEY 比對;bool=認得
//           ApplyTo(profile Profile, settingsPath string, backupDir string) error
//           Apply(profile Profile, settingsPath string) error        // = ApplyTo 以預設備份目錄(%APPDATA%\cc-fuse\backups)
//           // 差集語意(per audit A2):切換後 env 供應商欄位集合=目標 profile 完整集合
//           //   (ANTHROPIC_API_KEY + ANTHROPIC_BASE_URL + 目標 ModelEnv 全部鍵);目標沒有的 ANTHROPIC_DEFAULT_* 鍵移除
//           Watch(ctx context.Context, settingsPath string, profiles []domain.Profile) (<-chan WatchEvent, error)
//           // 事件:WatchEvent{Kind(ProviderChanged|UnrelatedChange|UnknownProvider), ProfileID}
//           // env 以內非供應商鍵變動(如 API_TIMEOUT_MS)→ UnrelatedChange(決策 2026-11-15:只看 key,design §5)
//           // 實作須 debounce/dedupe:一次變動至多一個事件;ctx 取消→通道關閉
// config:   Default() Config;Load(path) (Config, error)  // 缺檔→預設+nil;壞檔→明確錯誤(呼叫端決定走預設)
//           Save(path, Config) error
// store:    State{ Budget json.RawMessage }  // T02 的 BudgetState 以 JSON 藉此持久化
//           SaveState(path, State) error;LoadState(path) (State, error)
//           // 缺檔→(空 State, nil)冷啟;壞檔→(空 State, err)不 panic
// quotamode: ErrNotExist;Flip(path, mode string) error
//           // 只改 mode: 行,其餘 byte 不變(含 CRLF 行尾——Windows 活體檔禁被改寫成 LF)
//           // 輸入「mode:   RACING」多空白也命中;輸出統一正規化為「mode: <value>」單空白(決策 2026-11-15)
//           防衝(per audit A3):與 switcher 同款 mtime 防護——讀→改→mtime 比對→原子寫,衝突重讀重試(≤3 次);
//           此檔有第二個寫入者家族(quota 技能群整檔重寫),防的是交錯回寫造成卡片內容回滾
```

> **mtime 防衝的驗證歸屬(orchestrator 裁決 2026-11-15)**:switcher/quotamode 的 mtime 衝突重試屬內部機制,黑箱驗收測試**不**覆蓋(無公開測試縫;競寫 goroutine 測法不穩定)。由實作派工的**白箱單元測試**(內部 hook)覆蓋,orchestrator 於 Phase 3 對 diff 逐條核對此條款。config 壞檔=錯誤(審計 O1)為刻意釘死的契約,T03 據此編碼。

## z.ai 回應解析規格(真實樣本,全部抄自 cc-switch 測試碼)

端點:`GET {base}/api/monitor/usage/quota/limit`(base 預設 `https://api.z.ai`),header `Authorization: <apiKey>`(**不加 Bearer**)、`Content-Type: application/json`、`Accept-Language: en-US,en`,15s 逾時。

回應 `{"success":true,"data":{"limits":[…],"level":"…"}}`,`limits[]` 每條 `{type, unit?, number?, percentage, nextResetTime?}`(nextResetTime=毫秒)。分類規則:

1. 只認 `type` 大小寫不敏感等於 `TOKENS_LIMIT` 或 `CREDIT_LIMIT` 的條目;`TIME_LIMIT` 等忽略。
2. **`unit=3` → 5 小時桶;`unit=6` → 週桶(number 1 或 7 都是週)**;顯式分類壓過一切排序。
3. `unit` 缺失/不認得 → 兜底:無 `nextResetTime` 的優先填 5h 桶,其餘按 reset 升序填仍空缺的槽。
4. `percentage` 是**已用%**;字串/null → 視為 0;負數/超過 100 **不裁剪**照搬。
5. 5h 桶 0% 時可能沒有 `nextResetTime`(真實形態,reset=nil)。
6. 最多取兩條(5h+週);第三條丟棄;兩條同 unit 的異常形態,第一條歸位、第二條填剩槽。
7. `success:false` + msg 含「coding plan 不存在」語義(中英關鍵詞:不存在/沒有/未開通/not exist/not found/not have/no coding plan/not subscribed)→ 業務錯(按量帳號);其他 `success:false` → 一般業務錯。
8. HTTP 401/403 → 金鑰失效(與其他失敗區分)。
9. 參考陷阱實例(測試直接用):週期尾端**週桶會比 5h 桶早重置**(unit 必須壓過 reset 排序,cc-switch issue #3036);`unit:6,number:1` 也是週桶。

## 驗收標準(黑箱)

1. 解析:上述 9 條規則逐條有表格驅動測試(含每個真實樣本 JSON);fixture 至少覆蓋:雙桶正常、舊套餐單桶、全無 TOKENS_LIMIT、5h 缺 reset、大小寫變體、percentage 字串/null/-5/150、unit=6 壓過 reset 順序、unit 缺失兜底、雙 unit=3 異常、三條取二、無 coding plan 業務錯。
2. monitor:httptest 假 server 覆蓋 200/業務錯/401/逾時四路,QuotaSnapshot 欄位正確。
3. switcher:Apply 前後「不相關欄位語義零損傷」(深比對除供應商欄位外全等);**差集測試(per audit A2)**——目標 profile 缺的 DEFAULT_* 鍵被移除、profile 有的鍵全部就位(活體檔 `ANTHROPIC_DEFAULT_SONNET_MODEL_NAME` 即現成案例);原子性(實作為同目錄暫存檔+rename);mtime 被外部改動時重讀重試(≤3 次);備份保留最近 10 份輪替(per audit C7-v);Identify 以 key 比對;Watch 對「env 以外變動」回 UnrelatedChange、「key 換成認得的」回 ProviderChanged、「key 認不得」回 UnknownProvider;首次改動前備份。
4. config:預設值表完整(設計 §9 每欄都有)、讀寫往返一致。
5. store:狀態往返一致;壞檔回預設不 panic。
6. quotamode:翻 mode 行後其餘內容 byte 不變;無檔案回明確錯誤;mtime 衝突重讀重試(per audit A3)。
7. `go vet`、`go test ./...` 全綠;純標準庫+必要第三方(systray 等留給 T03)。

## 風險與回退

- 風險:settings.json 結構未来變化 → Identify/Apply 以「key 比對+僅替換已知欄位」保守設計,新增未知欄位自動保留。
- 回退:全新檔案,刪除即回退;不觸碰使用者真實 `settings.json`(測試全用 temp 目錄)。
