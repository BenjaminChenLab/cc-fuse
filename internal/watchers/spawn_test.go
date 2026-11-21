package watchers

// Acceptance tests — 2026-11-20 RACING/SAVING wake notify spawn gate. Spec:
// docs/blueprints/2026-11-20-racing-wake-notify.md (decisions 3 and 7,
// acceptance criteria). Black-box through Spawn, with ONE white-box seam:
// production exposes the unexported var startProcess (wrapping cmd.Start)
// exactly so tests can capture the constructed command without launching a
// real claude session.
//
// The two prompt constants below are INDEPENDENT transcriptions of the
// authority blocks in C:\Users\Benjamin\.claude\skills\go-when-racing\SKILL.md
// (section "The watcher registry & the cc-fuse notify", RACING block L102-115,
// SAVING block L121-133), NOT copies of the production constants — the
// spawn-argument assertions compare production output against them, so a
// prompt edited anywhere breaks the test. Byte identity against the authority
// file is additionally verified by the scratch audit tool
// (B:\claude-temp\racing-wake-notify-audit\main.go). Trailing-newline
// convention: the block ends at its last line, no trailing newline.

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// createNoWindow is the Windows CREATE_NO_WINDOW flag: the tray app is a
// windowsgui binary, so spawning without it would flash a console window on
// every account switch.
const createNoWindow = 0x08000000

// testRacingPrompt is the verbatim RACING notify prompt (authority: SKILL.md
// RACING block). Backticks are concatenated so the raw string stays verbatim.
const testRacingPrompt = `📨 quota-mode notify — flip TO RACING (spawned by cc-fuse after an account switch) — one job, then exit.
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

// testSavingPrompt is the verbatim SAVING notify prompt (authority: SKILL.md
// SAVING block).
const testSavingPrompt = `📨 quota-mode notify — flip TO SAVING (spawned by cc-fuse after an account switch) — one job, then exit.
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

// TestSpawnGate covers every zero-spawn branch of the cost gate: a missing,
// empty, or whitespace-only registry spawns nothing at zero cost (nil), and
// a mode outside {"RACING","SAVING"} is rejected without spawning even when
// the registry has lines (defensive; policy only emits the two values).
func TestSpawnGate(t *testing.T) {
	cases := []struct {
		name    string
		absent  bool
		content string
		mode    string
		wantErr bool
	}{
		{"missing file", true, "", "RACING", false},
		{"empty file", false, "", "RACING", false},
		{"whitespace-only lines", false, " \n\t\n   \n\n", "SAVING", false},
		{"CRLF whitespace-only lines", false, " \r\n\t \r\n", "RACING", false},
		{"invalid mode", false, "10:00 | chain: x.md | session: a", "FLEE", true},
		{"empty mode", false, "10:00 | chain: x.md | session: a", "", true},
		{"lowercase mode", false, "10:00 | chain: x.md | session: a", "racing", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sc := withCapture(t)
			path := filepath.Join(t.TempDir(), "watchers.md")
			if !tc.absent {
				if err := os.WriteFile(path, []byte(tc.content), 0o644); err != nil {
					t.Fatalf("write registry: %v", err)
				}
			}
			err := Spawn(path, tc.mode)
			if tc.wantErr != (err != nil) {
				t.Fatalf("Spawn(%q) error = %v, wantErr = %v", tc.mode, err, tc.wantErr)
			}
			if n := len(*sc); n != 0 {
				t.Fatalf("zero spawns required, got %d", n)
			}
		})
	}
}

// TestSpawnRacingVerbatim: a registry with content lines (including a
// session: unknown line) spawns exactly ONE headless session carrying the
// verbatim RACING prompt, with the stripped env, the user-home working dir,
// and the windowless process attributes.
func TestSpawnRacingVerbatim(t *testing.T) {
	sc := withCapture(t)
	home := t.TempDir()
	t.Setenv("USERPROFILE", home)
	t.Setenv("ANTHROPIC_API_KEY", "sk-must-not-leak")
	t.Setenv("ANTHROPIC_BASE_URL", "https://previous-account.example")
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "leak-canary")
	t.Setenv("FAKE_CANARY", "kept")
	registry := writeRegistry(t, "10:00 | chain: S:/x/a.md | session: alpha\r\n11:00 | chain: S:/x/b.md | session: unknown\r\n")

	if err := Spawn(registry, "RACING"); err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	if n := len(*sc); n != 1 {
		t.Fatalf("a multi-line registry spawns exactly once, got %d", n)
	}
	assertSpawnShape(t, (*sc)[0], testRacingPrompt, home)
}

// TestSpawnSavingVerbatim: the same spawn contract with the SAVING direction
// and its own verbatim prompt — direction mixups cannot pass.
func TestSpawnSavingVerbatim(t *testing.T) {
	sc := withCapture(t)
	home := t.TempDir()
	t.Setenv("USERPROFILE", home)
	t.Setenv("ANTHROPIC_API_KEY", "sk-must-not-leak")
	t.Setenv("ANTHROPIC_BASE_URL", "https://previous-account.example")
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "leak-canary")
	t.Setenv("FAKE_CANARY", "kept")
	registry := writeRegistry(t, "10:00 | chain: S:/x/a.md | session: alpha\r\n")

	if err := Spawn(registry, "SAVING"); err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	if n := len(*sc); n != 1 {
		t.Fatalf("a registry with content spawns exactly once, got %d", n)
	}
	assertSpawnShape(t, (*sc)[0], testSavingPrompt, home)
}

// TestSpawnExeFallback: with claude absent from PATH, the exe falls back to
// the documented %USERPROFILE%\.local\bin\claude.exe — the operative path on
// this machine per the spec's rationale, so a fallback-less resolution must
// not pass.
func TestSpawnExeFallback(t *testing.T) {
	sc := withCapture(t)
	home := t.TempDir()
	t.Setenv("USERPROFILE", home)
	t.Setenv("PATH", "") // force LookPath("claude") to fail
	exe := filepath.Join(home, ".local", "bin", "claude.exe")
	if err := os.MkdirAll(filepath.Dir(exe), 0o755); err != nil {
		t.Fatalf("create .local/bin: %v", err)
	}
	if err := os.WriteFile(exe, nil, 0o644); err != nil {
		t.Fatalf("write fake claude.exe: %v", err)
	}
	registry := writeRegistry(t, "10:00 | chain: x.md | session: a\r\n")
	if err := Spawn(registry, "RACING"); err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	if n := len(*sc); n != 1 {
		t.Fatalf("exactly one spawn expected, got %d", n)
	}
	if got := (*sc)[0].Path; got != exe {
		t.Fatalf("exe must fall back to the USERPROFILE .local/bin path:\n got %q\nwant %q", got, exe)
	}
}

// TestSpawnFireAndForget: Spawn must return immediately without waiting for
// the child to finish — a synchronous Wait would freeze every account switch
// for the lifetime of the headless session.
func TestSpawnFireAndForget(t *testing.T) {
	release := make(chan struct{})
	orig := startProcess
	startProcess = func(cmd *exec.Cmd) error {
		<-release // simulate a long-running headless session
		return nil
	}
	t.Cleanup(func() { close(release); startProcess = orig })
	registry := writeRegistry(t, "10:00 | chain: x.md | session: a\r\n")
	done := make(chan error, 1)
	go func() { done <- Spawn(registry, "RACING") }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Spawn must return right after start, got error %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatalf("Spawn blocked until the child finished — must return immediately (fire-and-forget)")
	}
}

// TestSpawnLookPathPreferred: a claude on PATH must win over the documented
// fallback — pins the LookPath-first half of exe resolution (decision 3),
// killing an implementation that never consults PATH and always lands on
// the USERPROFILE fallback (TOP audit finding F2).
func TestSpawnLookPathPreferred(t *testing.T) {
	sc := withCapture(t)
	bindir := t.TempDir()
	exe := filepath.Join(bindir, "claude.exe")
	if err := os.WriteFile(exe, nil, 0o644); err != nil {
		t.Fatalf("write fake claude.exe: %v", err)
	}
	t.Setenv("PATH", bindir)
	registry := writeRegistry(t, "10:00 | chain: x.md | session: a\r\n")
	if err := Spawn(registry, "RACING"); err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	if n := len(*sc); n != 1 {
		t.Fatalf("exactly one spawn expected, got %d", n)
	}
	if got := (*sc)[0].Path; got != exe {
		t.Fatalf("a PATH-resolved claude must be preferred over the fallback:\n got %q\nwant %q", got, exe)
	}
}

// assertSpawnShape pins the decided spawn mechanics on the captured command:
// a resolved exe, args [exe -p prompt] with the verbatim prompt, an
// explicitly built env stripped of every ANHROPIC_* entry (never nil — a nil
// env means inherit and would silently keep the old account), the user-home
// working dir, and the windowless CREATE_NO_WINDOW attributes.
func assertSpawnShape(t *testing.T, cmd *exec.Cmd, wantPrompt, home string) {
	t.Helper()
	if cmd.Path == "" {
		t.Fatalf("claude exe must be resolved (LookPath or the USERPROFILE fallback), got empty Path")
	}
	if len(cmd.Args) != 3 || cmd.Args[1] != "-p" {
		t.Fatalf("args must be [exe -p prompt], got %q", cmd.Args)
	}
	if cmd.Args[2] != wantPrompt {
		t.Fatalf("prompt not verbatim (direction or text drift):\n got %q\nwant %q", cmd.Args[2], wantPrompt)
	}
	if cmd.Env == nil {
		t.Fatalf("child env must be explicitly built (stripped), not inherited (nil Env)")
	}
	canary := false
	for _, kv := range cmd.Env {
		if strings.HasPrefix(kv, "ANTHROPIC_") {
			t.Fatalf("child env must carry zero ANTHROPIC_* entries, got %q", kv)
		}
		if kv == "FAKE_CANARY=kept" {
			canary = true
		}
	}
	if !canary {
		t.Fatalf("non-ANTHROPIC vars must survive the strip: FAKE_CANARY is missing from the child env")
	}
	if cmd.Dir != home {
		t.Fatalf("cmd.Dir must be the user home %q, got %q", home, cmd.Dir)
	}
	if cmd.SysProcAttr == nil {
		t.Fatalf("SysProcAttr must be set (HideWindow + CREATE_NO_WINDOW)")
	}
	// Type-identity pin: exec.Cmd.SysProcAttr is already the Windows syscall
	// type in this Windows-only repo, so the attributes are read directly.
	var _ *syscall.SysProcAttr = cmd.SysProcAttr
	if !cmd.SysProcAttr.HideWindow {
		t.Fatalf("HideWindow must be set: a windowsgui tray must not flash a console per switch")
	}
	if cmd.SysProcAttr.CreationFlags != createNoWindow {
		t.Fatalf("CreationFlags must be CREATE_NO_WINDOW (0x08000000), got %#x", cmd.SysProcAttr.CreationFlags)
	}
}

// withCapture swaps the process-start seam for a recorder that collects the
// constructed commands without launching anything, restoring it at cleanup.
func withCapture(t *testing.T) *[]*exec.Cmd {
	t.Helper()
	cmds := &[]*exec.Cmd{}
	orig := startProcess
	startProcess = func(cmd *exec.Cmd) error {
		*cmds = append(*cmds, cmd)
		return nil
	}
	t.Cleanup(func() { startProcess = orig })
	return cmds
}

// writeRegistry writes a watchers registry fixture into a temp dir.
func writeRegistry(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "watchers.md")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write registry: %v", err)
	}
	return path
}
