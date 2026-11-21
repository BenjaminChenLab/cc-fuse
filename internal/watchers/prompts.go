package watchers

// The three wake-notify prompts, embedded VERBATIM from the authority skill
// file C:\Users\Benjamin\.claude\skills\go-when-racing\SKILL.md (section
// "The watcher registry & the cc-fuse notify": RACING block L102-115, SAVING
// block L121-133, RESET notify block L141-152). The acceptance tests
// (spawn_test.go, resetnotify_test.go) carry independent byte-verified copies
// and pin Spawn's arguments at runtime, so a prompt edited anywhere breaks
// the tests; byte identity against the authority file is additionally audited
// by the scratch tool (B:\claude-temp\racing-wake-notify-audit\main.go).
// Trailing-newline convention: the block ends at its last line, no trailing
// newline. Backtick runs are concatenated so the raw strings stay verbatim.

// racingNotifyPrompt is the flip-TO-RACING go order: parked go-when-racing
// chains are woken and told to run their poll guard inline instead of waiting
// for the hourly cron backup.
const racingNotifyPrompt = `📨 quota-mode notify — flip TO RACING (spawned by cc-fuse after an account switch) — one job, then exit.
1. Read S:/claude-cache/go-when-racing/watchers.md. Missing or no lines → exit silently.
2. ToolSearch select:ListAgents,SendMessage; ListAgents once.
3. For each line whose session name appears in ListAgents → SendMessage (fire-and-forget):
   「quota mode 已翻成 RACING — 你的 go-when-racing chain <chain path> 觸發條件已成立:立刻
   inline 跑它的 poll guard(讀 chain 檔 → 標記檢查 → WOKE token → cross-check → 執行 brief),
   不要等 cron。guard 全文在 C:\Users\Benjamin\.claude\skills\go-when-racing\SKILL.md。」
   A name absent from ListAgents means that window is closed → skip the line (its hourly
   cron backup covers it).
4. Lines with ` + "`" + `session: unknown` + "`" + ` → covered by ONE self-filtering broadcast to every local
   session: 「quota mode 已翻成 RACING — 若你有 armed 的 go-when-racing chain(Glob
   S:/claude-cache/go-when-racing/ 找 WOKE 空的 chain 檔),立刻 inline 跑它的 poll guard;
   沒有就忽略本訊息。」
5. Exit. No acks, no reports, no waiting.`

// savingNotifyPrompt is the flip-TO-SAVING stop order: chains executing a
// go-when-racing brief must stop cleanly right now and mark PARKED-SAVING;
// parked chains treat the message as a self-filtering no-op.
const savingNotifyPrompt = `📨 quota-mode notify — flip TO SAVING (spawned by cc-fuse after an account switch) — one job, then exit.
1. Read S:/claude-cache/go-when-racing/watchers.md. Missing or no lines → exit silently.
2. ToolSearch select:ListAgents,SendMessage; ListAgents once.
3. For each line whose session name appears in ListAgents → SendMessage (fire-and-forget):
   「quota mode 已翻回 SAVING — 若你正在執行 go-when-racing chain <chain path> 的 brief,
   立即乾淨停手:完成或還原當前原子步(不留半寫檔)、更新 brief 停點、在 ## WOKE 下附加
   ` + "`" + `PARKED-SAVING <now> <停點>` + "`" + `,然後結束回合;下一次 RACING 訊號會從停點續做。若你只是
   parked 或沒有這條 chain,忽略本訊息,不做任何事。」
   A name absent from ListAgents means that window is closed → skip the line.
4. Lines with ` + "`" + `session: unknown` + "`" + ` → covered by ONE self-filtering broadcast to every local
   session: 「quota mode 已翻回 SAVING — 若你正在執行任何 go-when-racing chain 的 brief,
   立即乾淨停手(完成或還原當前原子步、更新停點、標記 PARKED-SAVING、結束回合);沒有就忽略。」
5. Exit. No acks, no reports, no waiting.`

// resetNotifyPrompt is the RESET retry order (2026-11-21 reset-notify): fired
// at the A-account 5h wall's reset instant, telling sessions parked on the
// 429 wall to retry in place — NO mode flip happened, so the message carries
// its own self-filtering tail and cc-fuse never writes the quota-mode file
// on this path.
const resetNotifyPrompt = `📨 quota-mode notify — A-account 429 wall CLEARED (5h reset, spawned by cc-fuse; NO mode flip happened) — one job, then exit.
1. Read S:/claude-cache/go-when-racing/watchers.md. Missing or no lines → exit silently.
2. ToolSearch select:ListAgents,SendMessage; ListAgents once.
3. For each line whose session name appears in ListAgents → SendMessage (fire-and-forget):
   「A 帳號的 429 牆已過 5 小時重置 — 若你有因 429/usage limit 停下的工作,立即原地 retry 續做
   (依全局規則,reset 時間不構成等待理由);若你是 go-when-racing chain,照 poll guard 跑,
   一切以 quota-mode.md 的 mode 為準。沒有停住的工作就忽略本訊息,不做任何事。」
   A name absent from ListAgents means that window is closed → skip the line.
4. Lines with ` + "`" + `session: unknown` + "`" + ` → covered by ONE self-filtering broadcast to every local
   session, same text minus the chain pointer.
5. Exit. No acks, no reports, no waiting.`
