# 2026-11-21 RESET notify(A 帳號 429 牆重置自動 retry 令)

## 需求(來源:claude-7a 合約 + go-when-racing SKILL.md 正本)

auto-switch 關掉(或 NoFleeOnWeekend 留守)時,A 帳號撞 429 牆 → 5 小時後池子重置,但
mode 檔一個字都沒變,沒有任何訊號叫停住的 session 續做——它們永遠停在那裡。本功能讓
cc-fuse 偵測到牆的 episode 後,在**池子重置的精確時刻**自動 spawn 同一套無頭通知,帶
RETRY 令(不是 GO/STOP,**絕不寫 quota-mode.md**,訊息自帶 self-filtering 尾句;
receiver 端零改動)。

**合約正本(SKILL.md L99,已含「Current 離開 A」精確化)**:觸發 = A 是 Current 且
5h 用量過牆線;episode = 過線到掉回線下;牆內重複偵測不重排;Current 離開 A 中途取消
(flip 通知已涵蓋被移走的 session);fire time = 額度 API 的精確 `FiveHourReset` +
buffer;`FiveHourReset` 缺才 fallback anchor+5h+buffer;重啟中途 → 下一輪對同一個
`FiveHourReset` 重排剩餘時間;重啟窗口的理論雙發由 prompt self-filtering 兜住。

**用戶已核可的兩個合約修正(2026-11-21,Benjamin;與 skill 正本現文不同處,以本文件
為準)**:
1. buffer 預設 **1 分**(skill 正本寫 3 分)。
2. **提早發**:episode 進行中用量掉回牆線下且排程還在 → 立即發(每 episode 限一次),
   非取消——此路徑同時關閉「重置瞬間 pct 先掉回線下、排程 timer 被取消、通知丟失」的
   競態;並配兩次 RESET 通知最小間隔(防洗版)。

**RESET notify prompt 逐字內嵌(= SKILL.md L141-151;L152 是圍籬收尾行,writeback 2026-11-21 更新)**:

```
📨 quota-mode notify — A-account 429 wall CLEARED (5h reset, spawned by cc-fuse; NO mode flip happened) — one job, then exit.
1. Read S:/claude-cache/go-when-racing/watchers.md. Missing or no lines → exit silently.
2. ToolSearch select:ListAgents,SendMessage; ListAgents once.
3. For each line whose session name appears in ListAgents → SendMessage (fire-and-forget):
   「A 帳號的 429 牆已過 5 小時重置 — 若你有因 429/usage limit 停下的工作,立即原地 retry 續做
   (依全局規則,reset 時間不構成等待理由);若你是 go-when-racing chain,照 poll guard 跑,
   一切以 quota-mode.md 的 mode 為準。沒有停住的工作就忽略本訊息,不做任何事。」
   A name absent from ListAgents means that window is closed → skip the line.
4. Lines with `session: unknown` → covered by ONE self-filtering broadcast to every local
   session, same text minus the chain pointer.
5. Exit. No acks, no reports, no waiting.
```

## 決策

1. **偵測(每輪 Tick,只吃 FRESH 樣本;stale 輪不改變任何狀態)**:條件 =
   `Current=="A"` && `cfg.ResetNotify` && `A.FiveHourPct != nil &&
   *A.FiveHourPct >= cfg.ResetNotifyAfterPct` → episode 開始:錨定一次,fireAt =
   `A.FiveHourReset + cfg.ResetNotifyBufferMin`(`FiveHourReset` nil → fallback
   anchor+5h+buffer,常數 5h 揭露),`time.AfterFunc` 排程(fireAt 已在過去 → 立即發,
   即合約第 5 點的可測性)。
2. **不重排**:牆內重複偵測不動作;`FiveHourReset` 中途被 API 改報 → 以新時刻重排剩餘
   時間(冪等,每 episode 仍只發一次)。
3. **取消**:`Current` 離開 A → cancel timer、關 episode(flip 通知已涵蓋);`ResetNotify`
   被關 → 同。
4. **提早發**:episode 中 FRESH pct < 牆線且 timer 還在且本 episode 未發過且距上次
   RESET 發射 ≥ `ResetNotifyMinGapMin` → 立即發、cancel timer、關 episode。
5. **發射**:呼叫既有 wake seam `Wake(cfg.WatchersFile, "RESET")` → `Spawn` 派第三段
   prompt;fire 後標記本 episode 已發;timer 與狀態機誰先到都只發一次;fire 失敗只
   log,絕不影響輪詢與切換。
6. **絕不寫 quota-mode.md**:結構上只呼叫 Spawn,不碰 Flip(驗收要有結構性斷言)。
7. **Spawn 擴充**(`internal/watchers`):mode gate 接受 `"RESET"`(第三 prompt 常數
   `resetNotifyPrompt` 逐字);RACING/SAVING 行為不變;registry 閘/無窗/
   剝 `ANTHROPIC_*`/fire-and-forget 全共用。
8. **重啟自癒**:episode 狀態不落盤;重啟後下一輪重偵測(牆仍在)→ 以同一個
   `FiveHourReset` 重排剩餘時間;lastFire 時戳也在記憶體(重啟歸零 → 理論雙發,
   由 prompt self-filtering 兜住,揭露)。
9. **Config 四欄**(+Default,沿既有 json tag 慣例):`ResetNotify bool`(預設 true)、
   `ResetNotifyAfterPct float64`(預設 99)、`ResetNotifyBufferMin int`(預設 **1**,
   Benjamin 指定)、`ResetNotifyMinGapMin int`(預設 30)。
10. **UI**:「其他」區(rnotify 列之後)加 checkbox `id=resetnotify` 繫 `resetNotify`,
    zh「5h 重置通知」/en "5h-reset retry notify" + tooltip;`load()`/`collect()` 各一行
    (`!!c.resetNotify` / `resetNotify:document.getElementById("resetnotify").checked`——
    checkbox 有 collect 支援即無 zero-value wipe 風險);server.go 零改動;static
    fallback 文字與詞典同步縮短版。threshold/buffer/gap JSON-only 不進 UI。
11. **掛點**:episode 狀態機在 app 層(建議新檔 `internal/app/resetnotify.go` 承載,
    `loop.go` 的 Tick 查詢後呼叫;檔案切分實作者可調,語意不變);狀態 = inEpisode/
    fired/timer/lastFire(fire 用既有 `l.Wake` seam,mode="RESET");anchor/cancel/
    early-fire/scheduled-fire 各一行 log。
12. **既有測試零改動**(合約演化逼不得已才動,逐一揭露);新測試優先新檔
    (如 `internal/app/resetnotify_test.go`、`internal/ui/resetnotify_marker_test.go`)。

## 驗收標準(每條錯誤都要有一個殺傷斷言)

- **對**:條件成立 → 錨定一次、排程 = FiveHourReset+1min;到點 spawn 恰一次、prompt
  逐字、registry 閘照走;注入時鐘把 fireAt 撥到過去 → 立即發(合約第 5 點);提早掉線
  → 立即發恰一次;Current 離開 A → 取消且零 spawn;min-gap 內不二發;全程無 Flip 呼叫。
- **錯**:牆內重複偵測重排/重發;Current 離開仍發;timer+狀態機雙發;RESET 觸碰 mode 檔;
  prompt 改字;buffer/gap/threshold 不從 config 來;stale 輪改變狀態。

## 授權記錄(2026-11-21,Benjamin)

- AskUserQuestion 回覆:「重置時刻+1 分即可」(buffer 修正=核可條件)。
- AskUserQuestion 回覆:「納入提早發(推薦)」。
- 訊息:「go」。
- 偵測手段 (b) 與「Current 離開 A」精確化:claude-7a 回信確認,已寫入 skill 正本 L99。
- 04:1x level-1 GREEN(coverage 層,37 頂層測試);04:4x TOP 審計 **PASS**(6 findings
  全 minor/note);仲裁採納 F1/F2(+F3 對齊)、F4/F6 記錄不動;F 輪修復(突變驗證
  M1-M3 先紅後綠)→ 同一審計 delta 複查 **F1/F2/F3 全 CLOSED**;**單元結案,
  commit 由 Benjamin 手動**。

## 實作詮釋記錄(writeback 2026-11-21,源自 TOP 審計後裁量;全部已由測試釘住)

1. min-gap 閘兩條發射路徑;被擋的提早發不關 episode、不取消 timer,之後每輪重評;
   被擋的排程發射靜默消耗(通知丟棄,反洗版優先)——揭露粒度即此兩句。
2. min-gap 以真實牆鐘(time.Since)計,不吃模擬時鐘。
3. 排程發射後 episode 續開,到 FRESH pct 掉回線下才關。
4. fire 失敗只 log 且計入該 episode 的唯一發射(每 episode 一次語意,不重試)。
5. 取消(Current 離開 A / 總開關 off)不吃新鮮度閘,stale 輪照走。
6. 結構性 no-Flip 斷言:含 "RESET" 的生產檔零 ".Flip(" 子串(含註解;機器獨立成檔;
   縱深防禦,行為面另有已走路徑覆蓋——審計 F4 記錄)。
7. 排程觀察通道 = 決策 11 的排程 log 行(RFC3339 fireAt);fallback 精確釘
   (恰 5h+buffer,buffer 單位=分)與「未發+同 reset 重複偵測不重排」已升進鎖定
   驗收檔(TOP 審計 F1/F2 修復,突變 M1-M3 驗證殺傷力)。
8. F 輪+delta:TOP 審計 F1/F2/F3 修復經同一審計 delta 複查 CLOSED(FINAL-AUDIT.md
   DELTA RECHECK 節);resetnotify.go 零淨生產變更(突變暫改已逐位元組還原)。
9. 環境備忘:config 套件測試在部分時段遭 fork/exec "Access is denied"(ERR-01,
   環境死因非測試失敗),協議=go test -c 編譯後直接執行;switcher 的
   TestWatchClassifiesExternalChanges 在全機高併發下偶發逾時 flake(6s deadline),
   單跑即過——重現者先單跑複驗再論。
