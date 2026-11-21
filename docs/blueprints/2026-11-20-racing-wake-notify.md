# 2026-11-20 RACING wake notify(切帳號即通知 parked session)

## 需求(來源:claude-7a session 合約 + go-when-racing SKILL.md 正本)

cc-fuse 完成帳號切換、並把 `mode:` 寫進 `S:/claude-cache/quota-mode.md` 之後,若
`S:/claude-cache/go-when-racing/watchers.md` 存在且至少有一行,spawn 一個一次性無頭
session(`claude -p "<notify prompt>"`),fire-and-forget:不等它結束、不阻塞切換流程。
watchers.md 不存在或空 → 完全不做任何事(連 headless session 都不開);翻寫失敗 →
也不 spawn。

**合約 v2(2026-11-20 下午,Benjamin 澄清語意後 claude-7a 修訂,推翻先前 v1 的
「只通知 RACING」)**:go-when-racing 是**循環**不是一次性——RACING 翻過來=開工令,
SAVING 翻回去=停工令(執行中的 chain 立即乾淨停手、標記 PARKED-SAVING,下次 RACING
從停點續做)。所以**每一個成功的 mode 翻轉都要 spawn,兩個方向都要**,spawn 時把方向
帶進 prompt(RACING prompt / SAVING prompt 擇一)。對 parked 中的 chain,SAVING 訊息
到達後照 guard 判定是 no-op 自動忽略(訊息自帶 self-filtering 尾句);對正在跑的
chain,它是合約本體。正本以 skill 檔最新版為準:
`C:\Users\Benjamin\.claude\skills\go-when-racing\SKILL.md`(「The watcher registry &
the cc-fuse notify」節,L97 契約段;writeback 2026-11-20 更新行號)。

### RACING notify prompt(逐字內嵌,= SKILL.md L102-115;writeback 2026-11-20 更新行號)

```
📨 quota-mode notify — flip TO RACING (spawned by cc-fuse after an account switch) — one job, then exit.
1. Read S:/claude-cache/go-when-racing/watchers.md. Missing or no lines → exit silently.
2. ToolSearch select:ListAgents,SendMessage; ListAgents once.
3. For each line whose session name appears in ListAgents → SendMessage (fire-and-forget):
   「quota mode 已翻成 RACING — 你的 go-when-racing chain <chain path> 觸發條件已成立:立刻
   inline 跑它的 poll guard(讀 chain 檔 → 標記檢查 → WOKE token → cross-check → 執行 brief),
   不要等 cron。guard 全文在 C:\Users\Benjamin\.claude\skills\go-when-racing\SKILL.md。」
   A name absent from ListAgents means that window is closed → skip the line (its hourly
   cron backup covers it).
4. Lines with `session: unknown` → covered by ONE self-filtering broadcast to every local
   session: 「quota mode 已翻成 RACING — 若你有 armed 的 go-when-racing chain(Glob
   S:/claude-cache/go-when-racing/ 找 WOKE 空的 chain 檔),立刻 inline 跑它的 poll guard;
   沒有就忽略本訊息。」
5. Exit. No acks, no reports, no waiting.
```

### SAVING notify prompt(逐字內嵌,= SKILL.md L121-133;writeback 2026-11-20 更新行號)

```
📨 quota-mode notify — flip TO SAVING (spawned by cc-fuse after an account switch) — one job, then exit.
1. Read S:/claude-cache/go-when-racing/watchers.md. Missing or no lines → exit silently.
2. ToolSearch select:ListAgents,SendMessage; ListAgents once.
3. For each line whose session name appears in ListAgents → SendMessage (fire-and-forget):
   「quota mode 已翻回 SAVING — 若你正在執行 go-when-racing chain <chain path> 的 brief,
   立即乾淨停手:完成或還原當前原子步(不留半寫檔)、更新 brief 停點、在 ## WOKE 下附加
   `PARKED-SAVING <now> <停點>`,然後結束回合;下一次 RACING 訊號會從停點續做。若你只是
   parked 或沒有這條 chain,忽略本訊息,不做任何事。」
   A name absent from ListAgents means that window is closed → skip the line.
4. Lines with `session: unknown` → covered by ONE self-filtering broadcast to every local
   session: 「quota mode 已翻回 SAVING — 若你正在執行任何 go-when-racing chain 的 brief,
   立即乾淨停手(完成或還原當前原子步、更新停點、標記 PARKED-SAVING、結束回合);沒有就忽略。」
5. Exit. No acks, no reports, no waiting.
```

## 決策(與 claude-7a 對齊;實作核可見本文件尾授權記錄)

1. **掛點**:`internal/app/loop.go` 的 `doSwitch()`,在 `Qm.Flip` 成功(err==nil)之後、
   條件 `dec.QuotaMode != ""`(RACING 或 SAVING 都算)&& `cfg.RacingNotify` &&
   `l.Wake != nil` 才呼叫;失敗只 log,絕不影響已完成的切換。時序保證:通知一定晚於
   mode 寫入。`dec.QuotaMode` 同時是 spawn 的方向參數(policy 只會給 "RACING"/"SAVING")。
2. **Seam**:沿用 adapters.go 既有模式——`app.WakeNotifier interface { Wake(watchersFile,
   mode string) error }` + `WakeNotifierFunc` adapter + 編譯期斷言。Loop 新欄位 `Wake`。
3. **新套件 `internal/watchers`**:`Spawn(watchersFile, mode string) error`:
   - mode 不是 "RACING"/"SAVING" → return error(不 spawn;防禦,正常不會發生);
   - 讀檔:missing / 空檔 / 只有空白行 → return nil,零 spawn(成本閘);
   - 有行 → spawn 一次(一個無頭 session 處理所有行,不是每行一個),prompt 依 mode
     擇一,逐字如上;
   - exe:`exec.LookPath("claude")`,失敗退回 `%USERPROFILE%\.local\bin\claude.exe`
     (實測該處為原生 exe v2.1.311);
   - `exec.Command(exe, "-p", prompt)`:參數陣列直傳,不過 cmd.exe,中文/換行/「」無引號問題;
   - 子程序 Env:從 `os.Environ()` 剝掉**所有** `ANTHROPIC_*`,逼它讀剛切好的 settings.json
     (= 剛切過去的帳號,skill 明文設計);
   - `SysProcAttr{HideWindow: true, CreationFlags: 0x08000000 /*CREATE_NO_WINDOW*/}`
     ——cc-fuse 是 windowsgui 常駐匣,不加會每次切換閃黑窗;
   - `cmd.Dir` 設 user home(無頭 transcript 別落進 cc-fuse 專案目錄);
   - ~~`Start()` 後 goroutine `Wait()` 釋放資源,不等結果~~
     (writeback 2026-11-20,已揭露偏差 1:改為 **250ms 寬限窗非同步啟動** `startGraceWindow`
     ——Start 在背景 goroutine 執行,250ms 內啟動失敗仍回報記 log,之後的失敗靜默丟棄
     =已揭露 residual R1,由 go-when-racing 每小時 cron 備援兜底;reap Wait 在啟動
     goroutine 上,無 handle 洩漏;acceptance test 以阻塞 seam stub 證明永不阻塞切換);
   - 兩段 prompt = Go raw string 常數,逐字如上。
4. **Config 新欄位**(`internal/config/config.go` + Default()):
   - `WatchersFile string`,預設 `S:/claude-cache/go-when-racing/watchers.md`
     (沿 `QuotaModeFile` 前例;**不進 UI**,JSON-only);
   - `RacingNotify bool`,預設 `true`(總開關,兩個方向一起管)。
5. **UI(設定窗主畫面「其他」區)**:`quota-mode.md 路徑`(id=qm)那一列之後加一列
   checkbox `id=rnotify` 繫 `racingNotify`,樣板照 `noFleeWknd`/`auto` 既有 pattern
   (`<span class="switch">…<i></i></span>`);`load()` 加
   `document.getElementById("rnotify").checked=!!c.racingNotify;`;`collect()` 加
   `racingNotify:document.getElementById("rnotify").checked,`;雙語詞條
   `lblRacingNotify`/`tipRacingNotify`(zh+en)。POST /api/config 整包 JSON 綁回
   `config.Config`,server.go **零改動**。
   - **F1 修訂(TOP 審計後 writeback,2026-11-20)**:零改動維持,但 `collect()` 必須
     passthrough `watchersFile`(module var 於 `load()` 暫存,UI 維持無列),且
     `config.Load` 對明確 `""` 癒合回預設值(縱深防禦;已中毒的 config.json 下次啟動自癒)。
     缺這兩層的話,第一次設定存檔會把 `watchersFile` 寫成空字串、下次切換時 `Spawn("")`
     被 ErrNotExist 閘靜默吃掉——功能死、無 log、UI 開關還顯示開著(TOP 審計 F1,
     FINAL-AUDIT.md)。`RacingNotify` 刻意**不**做癒合:明確 false 是合法選擇,`""` 路徑
     永遠是 wipe 產物,兩者不對稱是對的。
6. **接線**:`cmd/cc-fuse/main.go` Loop 加 `Wake: app.WakeNotifierFunc(watchers.Spawn)`。
7. **測試**(spec-tdd 家族):
   - watchers:表格驅動——missing/空/全空白行 → 零 spawn;mode 不合法 → 零 spawn;
     RACING 有行 → spawn 恰一次、args[1]=="-p"、args[2] 與 RACING prompt 逐字一致;
     SAVING 有行 → 同上但為 SAVING prompt;env 無任何 ANTHROPIC_*;
     HideWindow/CreationFlags 有設;
   - app:doSwitch 順序斷言 Flip 先 Wake 後;RACING 與 SAVING 兩方向都恰呼叫一次且帶
     方向與 `cfg.WatchersFile`;Flip 失敗不呼叫;`RacingNotify=false` 不呼叫;
   - ui:比照 `page_markers_test.go` marker 斷言(rnotify 存在、collect 帶 racingNotify);
   - config:Default() 含兩個新欄位預設值,Save/Load roundtrip 不丟。
8. **禁區**:零 git 寫入(含 commit);程式碼註解一律英文;不動 `internal/notify`(那是
   Windows Toast,職能不同);不動 quota-mode/switcher 語意;驗證 =
   `go build ./...` + `go vet ./...` + `go test ./...` 全綠。

## 驗收標準(acceptance test 必須能分辨的對錯)

- **對**:任一方向 + Flip 成功 + registry 有行 + 開關 on → 恰一次 spawn、prompt 逐字
  且方向正確、子 env 零 ANTHROPIC_*、呼叫順序 Flip→Wake。
- **錯(每條都要有一個會讓它過不了的斷言)**:翻寫失敗也發 / registry 空也發(白燒
  一個 headless turn)/ 順序反了(通知早於 mode 寫入)/ 開關 off 仍發 /
  prompt 被改字 / SAVING 誤用 RACING prompt(或反之)/ mode 不合法仍 spawn。

## 授權記錄

- v1(只通知 RACING):Benjamin 16:47「claude -p 這可行嗎? 可以就go」——已實測可行;
  該 run 於 16:52 停止(合約改版),停在讀規格階段,無半成品。
- v2(雙向通知):Benjamin 16:55 AskUserQuestion 核可「照新合約重派(推薦)」,level-1 v2
  同刻重派;18:01 回報 GREEN;TOP 審計 5 findings,F1(HIGH)/F2(LOW)採納修復、
  F3/F4/R1 記錄不動;delta 複查(同一審計)雙 CLOSED;**單元結案,commit 由 Benjamin
  手動執行**(manifest 見最終報告;.spec-tdd/ 為 scratch 永不進 commit)。
