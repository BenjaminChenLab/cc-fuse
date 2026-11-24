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
// (section "The watcher registry & the cc-fuse notify", RACING block and
// SAVING block), NOT copies of the production constants — the spawn-argument
// assertions compare production output against them, so a prompt edited
// anywhere breaks the test. The byte-identity audit scratch tool that used
// to live on the B: RAM disk is gone with a reboot — these transcriptions
// are the standing enforcement. Trailing-newline convention: the block ends
// at its last line, no trailing newline.
//
// 2026-11-25 hardening: the spawned prompt must be the verbatim base PLUS the
// embedded registry snapshot (exact delimiters), and the command must carry
// --add-dir <registry parent dir> — both pinned in assertSpawnShape; the
// prompt's step-1 broadcast fallback (registry unreadable → never exit
// silently) is part of the verbatim block.

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
1. Registry = the watchers.md snapshot embedded at the END of this prompt (spawn-time copy by
   cc-fuse). A live re-read of S:/claude-cache/go-when-racing/watchers.md is also allowed
   (--add-dir grant on the registry dir) and a successful re-read wins over the snapshot;
   that winning content is missing/empty/no lines → exit silently. If NO embedded snapshot
   is present AND the live re-read fails on permissions → do NOT exit silently: treat every
   line as ` + "`" + `session: unknown` + "`" + `, run step 4's single self-filtering broadcast, then exit.
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
1. Registry = the watchers.md snapshot embedded at the END of this prompt (spawn-time copy by
   cc-fuse). A live re-read of S:/claude-cache/go-when-racing/watchers.md is also allowed
   (--add-dir grant on the registry dir) and a successful re-read wins over the snapshot;
   that winning content is missing/empty/no lines → exit silently. If NO embedded snapshot
   is present AND the live re-read fails on permissions → do NOT exit silently: treat every
   line as ` + "`" + `session: unknown` + "`" + `, run step 4's single self-filtering broadcast, then exit.
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
	t.Setenv("ANTHROPIC_MODEL", "leak-canary-2") // a 4th ANTHROPIC_* name: kills any strip narrowed to a hardcoded trio
	t.Setenv("FAKE_CANARY", "kept")
	registryContent := "10:00 | chain: S:/x/a.md | session: alpha\r\n11:00 | chain: S:/x/b.md | session: unknown\r\n"
	registry := writeRegistry(t, registryContent)

	if err := Spawn(registry, "RACING"); err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	if n := len(*sc); n != 1 {
		t.Fatalf("a multi-line registry spawns exactly once, got %d", n)
	}
	assertSpawnShape(t, (*sc)[0], testRacingPrompt, registryContent, filepath.Dir(registry), home)
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
	t.Setenv("ANTHROPIC_MODEL", "leak-canary-2") // a 4th ANTHROPIC_* name: kills any strip narrowed to a hardcoded trio
	t.Setenv("FAKE_CANARY", "kept")
	registryContent := "10:00 | chain: S:/x/a.md | session: alpha\r\n"
	registry := writeRegistry(t, registryContent)

	if err := Spawn(registry, "SAVING"); err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	if n := len(*sc); n != 1 {
		t.Fatalf("a registry with content spawns exactly once, got %d", n)
	}
	assertSpawnShape(t, (*sc)[0], testSavingPrompt, registryContent, filepath.Dir(registry), home)
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
// a resolved exe, args [exe -p prompt --add-dir <registry parent dir>] where
// the prompt is the verbatim base prompt PLUS the embedded registry snapshot
// (exact delimiters — 2026-11-25 hardening: the windowless child could never
// be granted S:\ interactively, so the prompt carries the content itself and
// --add-dir is the live re-read grant), an explicitly built env stripped of
// every ANHROPIC_* entry (never nil — a nil env means inherit and would
// silently keep the old account), the user-home working dir, and the
// windowless CREATE_NO_WINDOW attributes.
func assertSpawnShape(t *testing.T, cmd *exec.Cmd, wantPrompt, wantRegistryContent, wantAddDir, home string) {
	t.Helper()
	if cmd.Path == "" {
		t.Fatalf("claude exe must be resolved (LookPath or the USERPROFILE fallback), got empty Path")
	}
	if len(cmd.Args) != 5 || cmd.Args[1] != "-p" {
		t.Fatalf("args must be [exe -p prompt --add-dir <registry dir>], got %q", cmd.Args)
	}
	wantFull := wantPrompt + "\n\n--- go-when-racing watchers.md registry (embedded at spawn by cc-fuse) ---\n" +
		wantRegistryContent + "\n--- end of registry ---"
	if cmd.Args[2] != wantFull {
		t.Fatalf("prompt must be the verbatim base plus the embedded registry snapshot (direction or text drift):\n got %q\nwant %q", cmd.Args[2], wantFull)
	}
	if cmd.Args[3] != "--add-dir" {
		t.Fatalf("args[3] must be --add-dir, got %q", cmd.Args[3])
	}
	if cmd.Args[4] != wantAddDir {
		t.Fatalf("add-dir must be the registry's parent dir (self-configuring grant):\n got %q\nwant %q", cmd.Args[4], wantAddDir)
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

// TestPromptAuthorityMirror (2026-11-25): the standing enforcement the RAM-disk
// scratch audit tool used to provide. When the authority skill file exists on
// this machine, its three fenced notify-prompt blocks must be byte-identical
// (LF-normalized; Go raw strings drop \r) to the production consts — a
// single-side edit on either side turns this red. Machines without the file
// (CI) skip: the authority file deliberately stays out of the repo, so the
// impl↔test transcription edge stays the only mechanically forced one there.
func TestPromptAuthorityMirror(t *testing.T) {
	const authorityPath = `C:\Users\Benjamin\.claude\skills\go-when-racing\SKILL.md`
	data, err := os.ReadFile(authorityPath)
	if err != nil {
		t.Skipf("authority skill file not present on this machine, mirror check skipped: %v", err)
	}
	text := strings.ReplaceAll(string(data), "\r\n", "\n")

	want := map[string]string{
		"flip TO RACING":   racingNotifyPrompt,
		"flip TO SAVING":   savingNotifyPrompt,
		"429 wall CLEARED": resetNotifyPrompt,
	}
	found := map[string]bool{}
	for _, block := range fencedNotifyBlocks(text) {
		matched := false
		for key, constText := range want {
			if strings.Contains(block, key) {
				found[key] = true
				matched = true
				if block != constText {
					t.Fatalf("authority block %q drifted from the Go const:\nAUTHORITY:\n%s\nCONST:\n%s", key, block, constText)
				}
				break
			}
		}
		if !matched {
			t.Fatalf("a fenced block carries the notify marker but matches no known direction header: %q", block)
		}
	}
	for key := range want {
		if !found[key] {
			t.Fatalf("authority skill file has no notify block for %q — the skill-side blocks were restructured?", key)
		}
	}
}

// fencedNotifyBlocks returns the body of every ```-fenced block carrying the
// notify-marker text (the skill file's poll-guard and registry-example fences
// do not carry it and are filtered out).
func fencedNotifyBlocks(text string) []string {
	var blocks []string
	lines := strings.Split(text, "\n")
	for i := 0; i < len(lines); {
		if strings.TrimSpace(lines[i]) != "```" {
			i++
			continue
		}
		end := -1
		for j := i + 1; j < len(lines); j++ {
			if strings.TrimSpace(lines[j]) == "```" {
				end = j
				break
			}
		}
		if end < 0 {
			break // unterminated fence: trailing prose, nothing more to extract
		}
		body := strings.Join(lines[i+1:end], "\n")
		if strings.Contains(body, "quota-mode notify") {
			blocks = append(blocks, body)
		}
		i = end + 1
	}
	return blocks
}
