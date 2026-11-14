# cc-fuse 實作計畫(權威三元組)

- 日期:2026-11-15
- 狀態:**S3 完成,待 S4 稽核**
- 需求本體:`E:\@sourceCode\cc-fuse\docs\blueprints\2026-11-15-cc-fuse-design.md`(單一事實來源,本計畫不重複其內容;I19(c) 以路徑引用)
- 專案根:`E:\@sourceCode\cc-fuse`

## 決策清單(D1–D9,皆已由使用者裁定)

| # | 決策 |
|---|---|
| D1 | Go 自製、精簡版 UI(匣選單+小設定窗),**完全取代** cc-switch 本機用途(外部同步已證實不可行) |
| D2 | 偵測=z.ai 帳號級額度端點 `GET https://api.z.ai/api/monitor/usage/quota/limit`(免費、含隊友用量);解析陷阱清單見 T01 文件 |
| D3 | 切換=原子替換 `settings.json` `env` 區塊的供應商欄位;身分一律以 `ANTHROPIC_API_KEY` 比對;不相關欄位零碰觸 |
| D4 | 永遠優先 A;切換/恢復/預警閾值與輪詢節奏全部可設定(預設值見設計文件 §9) |
| D5 | B 配速預算:週總量預設 90%、保底 3%、工作日預設排除週六日(勾選)、預算日以 B 週重置時刻自動錨定、嚴格執行(天花板到=切回 A 寧停等) |
| D6 | `quota-mode.md` 只翻 `mode:` 行,卡片內容不動;檔案不存在則跳過 |
| D7 | cc-switch 退休;過渡期以其手動切換為「使用者意志」採納,未知供應商則暫停 |
| D8 | 過渡期偵測:Claude Code 自己改 settings.json(model 等)一律忽略,只看 API key 變化 |
| D9 | **going-dark 執政令(2026-11-15)**:過夜全配;**Gate 2 預批准**(稽核乾淨三條件成立即自動開工);git 規矩=**零 commit、零 push**;`git init` 與 `git add`(stage)屬實作前置、允許(措辭修正 per audit A6);派工死掉自動重派一次;完成定義=三單元全部實作+測試綠燈 |

## 任務表(狀態欄為看板)

| id | 任務(白話) | depends-on | expected files | status |
|---|---|---|---|---|
| T01 | 核心服務層:共用型別+設定檔+狀態存檔+額度查詢與解析+切換器+quota-mode 寫入 | – | `go.mod`、`internal/domain/`、`internal/config/`、`internal/store/`、`internal/monitor/`、`internal/switcher/`、`internal/quotamode/`、`internal/domain/*_test.go` 等 | pending |
| T02 | 政策引擎:切換規則 P1–P8 + B 配速預算數學(純邏輯,無 IO) | T01 | `internal/policy/`(含測試) | pending |
| T03 | 外殼與接線:系統匣+通知+設定窗+cc-switch 匯入+主迴圈+單例+開機自啟 | T01, T02 | `cmd/cc-fuse/`、`internal/tray/`、`internal/notify/`、`internal/ui/`、`internal/importer/`、`internal/app/` | pending |

- 鏈狀依賴(T01→T02→T03),波寬 1 → 序列執行,無平行波。
- 對抗級掃描:三張卡皆無(金錢移動/授權/不可逆資料毀損皆不命中;B 預算為個人省錢護欄,失敗模式=超支風險,屬 money-adjacent)。無 pulled/external 列。
- `go.mod` 由 T01 建立;序列執行下 T02/T03 跑 `go mod tidy` 允許觸碰(無並行衝突)。

## 任務文件

- `docs/blueprints/tasks/T01-core-services.md`
- `docs/blueprints/tasks/T02-policy-engine.md`
- `docs/blueprints/tasks/T03-app-shell.md`

## 完成定義(D9)

T01+T02+T03 全部實作、各自驗收標準綠燈;`go build ./...` 與 `go test ./...` 全綠;所有變更 stage 不 commit;回報文件列出:各單元證據、測試摘要、手動驗收清單(匣選單/通知/設定窗)、任何殘留風險。

## 稽核與 Gate 2 記錄

### S4 稽核記錄(per audit,spec-2nd-opinion,2026-11-15)

- **稽核員立場:同意本計畫**(架構、D1–D9、序列 T01→T02→T03、三卡契約);C1–C8 逐項:成立/成立/成立/成立/成立/成立/部分成立/成立;引用行號零漂移;`go version`=go1.26.2 實測可用;cc-switch.db=有效 SQLite、providers 表存在。
- **修正案已摺入(全部標註 per audit)**:
  - A1(必改)→ T02 恆等式 4 加但書:保底發動時 ceiling 可越過 weeklyTotal、夾在真牆 100 以下(冷啟動週尾案例 88+3=91 以此通過)。
  - A2(必改)→ T01 釘死差集語意:切換後 env 供應商欄位=目標 profile 完整集合,缺的 DEFAULT_* 鍵移除;驗收加差集測試。
  - A3(強烈建議)→ T01 quotamode.Flip 加同款 mtime 防衝(此檔有 quota 技能群這個第二寫入者家族)。
  - A4(建議)→ T03 領域依據補 §4、Tick 契約補退避/沿用;手動清單補 D7/D8 外部變動端到端(cc-switch 仍活躍,db mtime=今天凌晨)。
  - A5(建議)→ T02 補 Decision.WarnFired 欄;ExtEvent/ManualCmd 給形狀;Event→ExtEvent 調適歸 T03。
  - A6(行政)→ D9 措辭修正(上表)+ git init 認領給實作第 0 步(T03 注意事項 7)。
- **待決事項(pending user decisions,屬 Benjamin,不停默折入)**:
  - PD1 對外代理:**事實前提已被探測駁回**(2026-11-15 01:5x 直連 `api.z.ai` 得 HTTP 200;WinHTTP=Direct、環境變數無代理)→ 決策消解;保險做法已摺入(config 選配 `proxyURL`,空=直連)。**Benjamin 追認(2026-11-15 ~02:0x)。**
  - PD2 app 自我更新:**Benjamin 核可(2026-11-15 ~02:0x,「both agree」)**——v1 不做自動更新,連續解析失敗(預設 5 次,可設定)→ 紅圖示+通知+log;已摺入設計 §11 第 8 條、T03 注意事項 9。
  - PD3 手動切換翻 quota-mode:**Benjamin 核可(2026-11-15 ~02:0x,「both agree」)**——手動切 B=SAVING、切回 A=RACING;已摺入設計 §6 P1、T02 規則 P1。
- **殘留風險(稽核點出、未擋)**:quota-mode 雙寫者競爭(已用 A3 防衝緩解,極端交錯仍 last-writer-wins);cc-switch 過渡期雙寫 settings.json(D7/D8+手動驗收 10 涵蓋);app 無自我更新路徑(PD2)。
- 無 IRREVERSIBLE 標記(稽核確認)。稽核後修正皆為契約文字精度,無新代碼庫主張 → 不觸發 targeted re-audit(依 2nd-opinion 之「may」,披露此裁量)。

### Gate 2 記錄

- 預批准三條件檢核(2026-11-15):①未中(有 A1–A6)②未中(PD2/PD3 待決)③中 → 預批准不成立,曾停等。
- **2026-11-15 ~02:0x Benjamin 回:「both agree, go」**——PD2/PD3 照建議核可(已摺入)、PD1 追認、A1–A6 無否決。
- **Gate 2 approved (2026-11-15): route = serial multi-unit(T01→T02→T03,spec-tdd multi-unit run)**。
- S5 開火形態披露:同一 going-dark 窗口內的 attended 批准+已宣告離席(過夜全配、完成定義=三卡全綠)→ 依 going-dark 反滯留條款 **in-session 開火**(v1.37.0:寫一條沒人執行的交接線=滯留整個階段)。
- I21 披露:orchestrator 以 GLM 跑(非 opus-class workhorse tier);考量環境既定選擇(GLM+夜間無人值守)按 ignore 處理,列入最終報告披露。
