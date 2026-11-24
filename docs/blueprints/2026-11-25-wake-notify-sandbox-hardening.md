# Wake-notify sandbox hardening（名冊內嵌提示詞 + --add-dir + 廣播後備條款）

Date: 2026-11-25
Status: implemented same-day under spec-tdd-lite (retroactive-RED variant; see §4)
Related: docs/blueprints/2026-11-20-racing-wake-notify.md (decision 3), docs/blueprints/2026-11-21-reset-notify.md

## 1. 事件（為什麼要修）

2026-11-24 19:57:15 cc-fuse 正確翻檔切到 B（SAVING）並依合約 spawn 一個 headless
通知 session。該 child 的工作目錄只有 `C:\Users\Benjamin`（spawn.go `cmd.Dir = home`），
而它第一步要讀的 `S:\claude-cache\go-when-racing\watchers.md` 在沙箱外；headless
情境下沒有人能批准 S:\ 的讀取授權。這個 child 照提示詞字面（「Missing or no lines →
exit silently」）靜默退出，**沒有通知任何 session**。當時 dtmsCore_dev5 的
go-when-racing chain 正在 mid-run，停手令從未送達 → 該 chain 在付費帳號 B 上多跑
約 23 分鐘才自然收口。同日 14:33 與 21:37 的兩個通知 session 撞同一道牆，但自行
improvise（ListAgents + 全機廣播）而送達——送達與否取決於 child 模型的臨場發揮。

## 2. 需求（verbatim）

- 使用者裁示（2026-11-25 ~01:45，三層全選）：
  > "cc-fuse：名冊內嵌進提示詞（推薦）, cc-fuse：spawn 加 --add-dir,
  > skill 提示詞：把廣播寫成義務, 能不能修沒權限讀這事"
- 原始期望（2026-11-25 ~01:22， contracts 不變，只修運輸層）：
  > "I expect even already under racing mode, then got skill request /go-when-racing,
  > should also register this session, when cc fuse change to saving mode, it will
  > notify this session, this session will pause immediately, until cc fuse change to
  > racing mode notify the session to wake up continue"

## 3. 解讀決策（ambiguous wording 的裁決，審查者須逐條檢驗）

1. `--add-dir` 的授權範圍 = **watchers.md 的父目錄**（`S:\claude-cache\go-when-racing`），
   不是 `S:\claude-cache` 整層、更不是 S:\ 整槽——通知 session 唯一需要讀的就是名冊，
   授權取最小範圍。路徑從傳入的 `watchersFile` 參數推導（`filepath.Dir`），
   自訂 `watchersFile` 位置時自動跟著配，不加新常數。
2. 名冊以**spawn 當下的快照內嵌進提示詞**（Spawn 的成本閘本來就持有檔案位元組）；
   child 若 `--add-dir` 授權生效且重讀成功，**以 live 檔為準**（新鮮度優先），
   快照是地板不是天花板。
3. 「靜默退出」從預設行為降級為**僅在雙保險皆失效時**才允許：提示詞明寫——無內嵌
   快照 **且** live 重讀被權限擋下 → 不得靜默退出，改走 step 4 的全機自我過濾廣播
   （把 14:33/21:37 的隨機救場升級為合約義務）。
4. 內嵌區塊的分隔符文字：`--- go-when-racing watchers.md registry (embedded at spawn
   by cc-fuse) ---` / `--- end of registry ---`，測試以獨立轉寫釘死同一份文字。
5. 提示詞的權威檔仍是 `C:\Users\Benjamin\.claude\skills\go-when-racing\SKILL.md`
   （三段 notify prompt + contract 段同步更新）；Go 端 `prompts.go` 常數 byte-verbatim
   鏡射，測試檔再獨立轉寫一份（既有家族慣例）。機械強制範圍（2026-11-25 審查後修正措辭）：
   impl↔test 這條邊由轉寫全等釘死；權威檔↔impl 這條邊由 `TestPromptAuthorityMirror`
   在權威檔存在的機器上強制（檔案不在——例如 CI——則 skip，該機器只剩人工審計）。
6. RESET 通知走同一套機制，三個方向（RACING/SAVING/RESET）提示詞同步升級，行為一致。

## 4. 驗收測試（acceptance tests）

- `internal/watchers/spawn_test.go`：RACING 與 SAVING 兩個方向的 spawn 形狀釘死
  （`assertSpawnShape`）：args 必為 `[exe, -p, prompt, --add-dir, <registry 父目錄>]`；
  prompt 必為「verbatim 基底 + 內嵌快照（精確分隔符）」；env 剝掉 ANTHROPIC_*、
  working dir = user home、CREATE_NO_WINDOW 等舊釘不變。
- `internal/watchers/resetnotify_test.go`：RESET 方向同一形狀。
- 廣播後備條款本身是提示詞文字（合約），由 verbatim 轉寫釘死，不另寫行為測試。

### RED 的證明方式（retroactive-RED）

實作與測試實際上是同一輪寫的（違反字面 test-first，此處補誠實版）：將兩個生產檔
（`prompts.go`、`spawn.go`）`git stash` 還原到修改前、保留新測試 → 測試必須因
「args 少了 --add-dir（3 個非 5 個）」與「prompt 無內嵌快照/無後備條款文字」而 RED
——每個失敗都指向本次修改引入的行為，不是測試自身缺陷（RED-purity）；隨後
`git stash pop` 還原實作 → GREEN。

## 5. Out of scope

- SKILL.md 的三段 notify prompt 更新（已同步落地於 skill 端，屬 skill 檔，不在本 repo）。
- exe 的置換與 tray 重啟（`rebuild.bat` 是手動動作，由使用者決定時機）。
- CHANGELOG / 版本號（發版流程另案）。
- 每小時備援 cron 的 freshness 讓位問題（觀察到但本輪不修：notify 運輸層修好後，
  該讓位行為對「double-run 保護」仍是對的）。

## 6. Phase 3 審查記錄與處置（2026-11-25，fresh-context 唯讀審查）

審查者另以一次性唯讀腳本做了三方 byte 比對：三段 prompt 在 SKILL.md ↔ prompts.go ↔
測試轉寫之間全等（prompts.go 實體 CRLF、Go raw string 丟棄 `\r`，emitted 值為 LF，
與權威檔一致）。發現與處置：

1. env-strip 縮窄回歸無測試辨別 → **已修**：三支 verbatim 測試各加第四個
   `ANTHROPIC_MODEL` 金絲雀；突變驗證（寫死三名的縮窄剝除）當場紅——紅因抓到的
   是本機真實環境的 `ANTHROPIC_DEFAULT_HAIKU_MODEL` 洩漏，非僅合成金絲雀。
2. 快照來源（成本閘 bytes vs 重新讀檔）未釘 → **接受不釘**：TOCTOU 差異、行為
   近乎良性，釘它會過度約束實作細節。
3. Windows argv 32k 上限無守門 → **接受不修**：超限會走 grace window 大聲失敗，
   實際 registry 只有幾行。
4. 型別恆真斷言（spawn_test.go `var _ *syscall.SysProcAttr`）→ **無需動作**：
   編譯期意圖標記，真正的釘在其相鄰兩行。
5. §3.5「單端改字會紅」過度承諾 → **已修**：措辭軟化（見 §3.5）＋新增
   `TestPromptAuthorityMirror`（權威檔存在才跑、不存在 skip）；突變驗證：常數側
   單字元改動當場紅。接回 RAM-disk 舊審計工具的職責。
6. §2 註冊期望不在本變更集 → **確認刻意**：註冊行為屬 skill 層 arm 流程，
   本輪只修運輸層，範圍切割無誤。
7. 後備條款僅提示詞文字、無行為測試 → **維持已聲明取捨**（§4）。
8. byte-drift：無。
