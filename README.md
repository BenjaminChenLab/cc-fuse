# cc-fuse

## What's this

cc-fuse is a Windows tray utility (Go) that watches the quota of two z.ai GLM Coding Plan accounts and automatically switches the provider in `~/.claude/settings.json`. The shared account (A) is always preferred; when it runs hot, work flees to the personal account (B) under a daily pacing budget, and switches back the moment A recovers. Running Claude Code sessions pick up the switch on their next request.

## Why this

A 429 wall doesn't just stop one request — it kills in-flight work across every session. And quota you save by being frugal doesn't carry over: the shared pool gets drained by teammates either way, so the right move is to finish your work **before** the wall. cc-fuse blows the fuse early — it switches preemptively instead of reacting after the wall — and paces the personal account with a daily budget so it never burns through its whole week on day one. It also replaces cc-switch entirely (switching + quota visibility, no sync issues).

## How to use

### Build and run

Requirements: Go 1.26+; WebView2 runtime (standard on Win10/11 — without it the settings page falls back to the default browser).

```powershell
go build -ldflags "-H windowsgui" -o cc-fuse.exe ./cmd/cc-fuse
```

> `-H windowsgui` is required: without it the exe builds as a console-subsystem binary — you get a black console window, and closing it kills the resident app.

Launch `cc-fuse.exe`. It lives in the system tray and starts polling both accounts' quotas immediately (normal interval 60s; automatically speeds up to 20s while the active account runs hot).

### First-time setup

1. Double-click the tray icon (or right-click → **主畫面**) to open the main window.
2. In the **供應商 A** / **供應商 B** sections, fill in each account's name, z.ai API key, endpoint (both accounts normally share the same z.ai endpoint) and the `ANTHROPIC_DEFAULT_*` model mapping as JSON.
3. Or click **從 cc-switch 匯入**: point it at `~/.cc-switch/cc-switch.db`, then use the **設為 A / 設為 B** buttons to fill each profile in one click.
4. Press **存檔**. The switcher starts working right away — no restart needed. Optionally tick **開機自動啟動** so cc-fuse launches at logon.

The identity of the active account is decided by matching the API key in `settings.json` against the A/B definitions — so if you switch providers outside cc-fuse, it silently adopts whoever is live.

### The main window

- **A/B quota cards** — each card shows the account avatar, name, endpoint and two quota **progress bars** (5-hour window and weekly window) that turn orange at 70% and red at 90%, with a reset countdown. The active account's card is highlighted in blue.
- **B's card** also shows a **今日天花板** chip — the weekly-percentage ceiling allocated to today; hitting it forces the switch back to A (when enforcement is on; otherwise the chip is annotated 不強制).
- **A's card** shows a **恢復預計** chip — when the 5-hour window resets, A is expected to be back in the safe zone.
- **提示列** — health flags (自動切換:開/已暫停, 額度資料過期, key 失效, 解析失敗) plus the **暫停自動 / 恢復自動** button and a manual refresh.
- Every switch, warning and pause also raises a Windows toast.

### The tray icon

Right-click menu: status line | 切到 A / 切到 B | 暫停/恢復自動 | 設定 | 結束.

| Icon | Meaning |
|---|---|
| solid green / solid orange | auto-switching, currently on A / B |
| hollow ring (green/orange) | autopilot paused — still shows which account you're on |
| gray | quota data stale or undetermined |
| red | current key dead, or quota parsing failing repeatedly |

Double-click the icon opens the main window. The settings window is a singleton: clicking again just raises the open one instead of spawning another.

### How the switching works

- **Flee to B** when A's 5h% or weekly% crosses the switch line (default 90%) and B is usable (its 5h% is below the line and it hasn't burned through today's budget ceiling).
- **Back to A** once A's windows fall to the recovery lines (default 30/60 — far below the switch lines, so no flapping).
- **Budget pacing**: each budget day (anchored at B's weekly reset time, e.g. Fri 15:00 → daily settlement at 15:00) B gets `(weekly budget total − used) ÷ remaining workdays`, floored by the daily minimum and capped at 100%. Burning through today's ceiling forces the switch back to A even if A hasn't recovered — stop and wait rather than overspend your own money.
- **The 天花板強制切回 A toggle** turns all of that ceiling logic off: the ceiling is then display + accounting only, and B stays a permanent refuge.
- Saving any budget setting recomputes today's ceiling immediately (days are counted from the budget-day start, so saving at 23:50 or 00:10 gives the same number, and a mid-day save never relaxes today's ceiling).
- Every switch flips the `mode:` line in `quota-mode.md` (→ RACING when back on A, SAVING when sheltering on B) so your other Claude Code sessions adjust instantly.

### Files

| File | Contents |
|---|---|
| `%APPDATA%\cc-fuse\config.json` | All settings — A/B definitions, thresholds, polling, budget, autostart, proxy. Every number is editable and hot-applies on save. |
| `%APPDATA%\cc-fuse\state.json` | Pacing bookkeeping (today's baseline, ceiling, settlement instant) — survives reboots. |
| `%APPDATA%\cc-fuse\logs\` | Rotating logs. |
| `quota-mode.md` (path configurable) | The mode line other Claude Code sessions read. |

Details: design doc `docs/blueprints/2026-11-15-cc-fuse-design.md`, release notes `CHANGELOG.md`.

## License

[MIT](LICENSE) © 2026 Benjamin Chen
