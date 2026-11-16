# cc-fuse

## What's this

cc-fuse is a Windows tray utility (Go) that watches the quota of two z.ai GLM Coding Plan accounts and automatically switches the provider in `~/.claude/settings.json`. The shared account (A) is always preferred; when it runs hot, work flees to the personal account (B) under a daily pacing budget, and switches back the moment A recovers. Running Claude Code sessions pick up the switch on their next request.

## Why this

A 429 wall doesn't just stop one request — it kills in-flight work across every session. And quota you save by being frugal doesn't carry over: the shared pool gets drained by teammates either way, so the right move is to finish your work **before** the wall. cc-fuse blows the fuse early — it switches preemptively instead of reacting after the wall — and paces the personal account with a daily budget so it never burns through its whole week on day one. It also replaces cc-switch entirely (switching + quota visibility, no sync issues).

## How to use

1. Build (Go 1.26+; `-H windowsgui` is required — without it you get a console window and closing that window kills the app):

   ```powershell
   go build -ldflags "-H windowsgui" -o cc-fuse.exe ./cmd/cc-fuse
   ```

2. Launch `cc-fuse.exe`. It lives in the system tray:

   - Right-click menu: status | switch to A / B | pause/resume autopilot | settings | quit.
   - Double-click the icon to open the main window: A/B quota cards, today's ceiling, recovery estimate, the autopilot button, and every setting.

3. Fill in the A/B API keys in the settings window (or import them from cc-switch) and hit save. Config lives in `%APPDATA%\cc-fuse\config.json`, pacing state in `state.json`; every change takes effect immediately.

Details: design doc `docs/blueprints/2026-11-15-cc-fuse-design.md`, release notes `CHANGELOG.md`.
