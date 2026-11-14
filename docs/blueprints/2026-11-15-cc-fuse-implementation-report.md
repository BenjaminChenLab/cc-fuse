# cc-fuse 成果報告(2026-11-15)

cc-fuse 是 Windows 系統匣常駐小工具(Go,單一 exe):監看兩個 z.ai GLM Coding Plan 帳號的額度百分比,自動改寫 Claude Code 的 `settings.json` 供應商欄位——A(團隊共用)快滿就先切到 B(自購)、A 恢復就切回來、B 有每日配速天花板防止週額度提前燒完。同時把 `S:\claude-cache\quota-mode.md` 的 `mode:` 行翻成 RACING/SAVING。全程以 spec-tdd 管線實作:每張卡都是「主控端先寫驗收測試(規格即測試)→ 獨立審計 → 派實作 → 主控端親自重跑驗證」。

## 交付內容

```
cmd/cc-fuse/        main.go 啟動接線、單例鎖、開機自啟、日誌輪替、設定窗 WebView2
internal/domain/    Profile / QuotaSnapshot 資料型別
internal/monitor/   z.ai 額度 HTTP client + 解析(unit=3→5h 窗、unit=6→週窗;含 cc-switch #3036 陷阱)
internal/switcher/  settings.json 原子改寫(備份+mtime 防衝)、身分判定、外部變動監看
internal/quotamode/ quota-mode.md mode: 行翻轉(CRLF 保留、mtime 防衝)
internal/config/    全部數字可設定的設定檔(載入/存檔/預設值)
internal/store/     state.json(預算狀態持久化,重開機不歸零)
internal/policy/    規則引擎 P1–P8 + §7 配速數學(純邏輯,無 IO)
internal/app/       主迴圈(輪詢編排、A4 沿用上次數字、通知一次不洗版、預算落帳)
internal/importer/  cc-switch SQLite 匯入器(best-effort;手動輸入永遠是保底)
internal/ui/        設定窗 HTTP 後端(127.0.0.1;GET/POST /api/config;內嵌頁面)
internal/tray/      系統匣圖示(綠=A/橘=B/灰=過期或暫停/紅=key 失效或連續解析失敗)+ 選單
internal/notify/    Windows Toast(失敗只記 log,不影響切換)
```

## 測試證據(最終一輪,主控端親跑)

- `go build ./...` 通過;`go vet ./...` 通過;`gofmt -l` 乾淨。
- `go test -count=1 ./...`:**11 個測試套件全綠、0 失敗**(domain/notify 為純資料型別與第三方包裝,無獨立測試檔;前者由各消費端套件覆蓋,後者失敗路徑「只記 log 不傳播」)。
- 三份 T03 驗收測試檔雜湊逐位元一致(`16c0786d`/`918f064f`/`1393d5e0`),實作期間未被改動;T01 五份、T02 一份驗收測試同樣雜湊封存於 `.spec-tdd/batch-ledger.md`。
- 完整指令輸出存檔:`.spec-tdd/t01-red2.log`、`t01-verify.log`、`t02-red2.log`、`t02-impl-run.log`、`t03-impl-run.log`。

## 實作過程中的重大裁決(全數披露)

1. **T02 配速常數修正(兩項)**:實作員正確申報驗收測試常數算錯(期望 36.67%,公式值應為 20+70÷6≈31.67%),主控端依設計文件 §7 獨立重推後修正;重跑時再發現 Go 編譯期常數與執行期浮點運算對除不盡的數必差最後一位,改為容差比較(容差 1e-9;算錯天數的結果差 1.6 以上,判別力不損)。測試雜湊演進 `54f1b713`→`8bc8d4a1`,已記錄於賬本。
2. **P6 仲裁(錢優於時間)**:B 預算耗盡(週%≥天花板)時喪失「避難所候選」資格直至下次結算——絕不自動切進一個已經超支的帳號。設計文件 §6 與測試同步修訂。
3. **T03 驗收草稿審計不通過後全數摺入(12 項)**:最關鍵兩項——「健康閒置必須完全安靜」與「冷啟動必須記帳」的跨測試矛盾(修正後:閒置輪也必須落帳,否則重開機會失憶);假查詢件讀真實時鐘的定時炸彈(改決定論時戳)。其餘為判別力補強(編排順序釘死、Reason 傳遞斷言、沿用上次數字整條契約的新測試等)。
4. **對抗抽讀抓到 4 處資料競爭**:主迴圈有四處讀取「設定窗執行緒會併發改寫」的欄位時漏了上鎖(doSwitch 讀設定、採納與查表讀帳號表、輪詢節奏讀設定)。單執行緒測試抓不到,屬真實併發缺陷;已由原實作員補鎖並全綠回歸。

## 手動驗收清單(自動化測試覆蓋不到、需要真實環境的項目)

1. 用真實 API key 查詢額度端點一次,確認解析數字與 z.ai 網頁一致(需使用者同意使用真 key)。
2. 實際 A/B 切換一輪:確認 `settings.json` 只動供應商欄位、其他欄位零損傷;進行中的 Claude Code session 下一個請求即生效(熱切換)。
3. 系統匣圖示四色狀態與 tooltip 數字;選單「切A/切B/暫停/恢復/設定/結束」各項實點。
4. 設定窗:開啟(WebView2;無 runtime 時後備瀏覽器)、改任一數字、存檔、關窗即停該窗的本地伺服器。
5. cc-switch 匯入按鈕:真實資料庫已由實作員唯讀驗證(4 筆 claude 供應商正確映射、其他工具列正確過濾),UI 按鈕流程待手動點一次。
6. 手動改 `settings.json` 後:認得的帳號 → 靜默採納;陌生的 → 暫停+通知一次。
7. `quota-mode.md` 檔案不存在時:翻轉跳過、其餘照常(只記 log)。
8. 開機自啟(登錄檔)實際重開機驗證;開第二份程式 → Toast 提示後退出(單例鎖)。
9. Toast 通知實際彈出(切換/預警/過期/解析失敗紅警示各一)。
10. `quota-mode.md` 的 mode 行正確翻轉(切 B → SAVING、切回 A → RACING)。

## 殘留風險(已知且接受)

- 外部變動的「身分分類」用開機時的帳號表;設定窗改帳號定義後,切換立即生效,但分類要重啟才跟著更新(代碼內已註明)。
- 未在 `-race` 下跑測試(測試皆單執行緒,競爭類缺陷已由人工抽讀修復);未來若加併發測試建議補一輪 `-race`。
- Toast 包裝層(notify)無自動測試;失敗靜默降級是設計行為(§11.5 同款)。
- 無 WebView2 runtime 的機器走瀏覽器後備,該 loopback 伺服器活到程式結束(僅監聽本機)。
- `golang.org/x/sys` 釘在 v0.49.0(v0.50.0 移除了 registry 套件,升級前需改寫自啟寫法)。
- z.ai 若改回應格式:連續解析失敗達警報線 → 紅色圖示+Toast+log(PD2 設計內的失敗模式,響了即知要更新程式)。
- 無自動更新(PD2 決策):升級靠人工替換 exe。

## git 狀態

全部變更已 stage,**零 commit、零 push**——commit 權保留給使用者。建議 commit 切法:每卡一個(T01 核心服務/T02 政策引擎/T03 外殼),或單一初始 commit。
