package watchers

// Acceptance tests — 2026-11-21 RESET notify prompt: the third wake mode.
// Spec: docs/blueprints/2026-11-21-reset-notify.md (decisions 5 and 7). The
// mode gate accepts exactly RACING / SAVING / RESET; a RESET spawn carries
// the verbatim RESET prompt (fired at the A-account 5h wall reset, NO mode
// flip involved) through the same registry cost gate, env strip and
// windowless attributes. The RACING/SAVING acceptance (spawn_test.go) is the
// regression wall and stays untouched.
//
// testResetPrompt is an INDEPENDENT transcription of the authority block in
// C:\Users\Benjamin\.claude\skills\go-when-racing\SKILL.md (RESET notify
// prompt), NOT a copy of the production constant — the spawn-argument
// assertion compares production output against it, so a prompt edited
// anywhere breaks the test. Trailing-newline convention: the block ends at
// its last line, no trailing newline. Backticks are concatenated so the raw
// string stays verbatim. The 2026-11-25 embedded-snapshot and --add-dir pins
// live in spawn_test.go's assertSpawnShape, shared with this file.

import (
	"os"
	"path/filepath"
	"testing"
)

const testResetPrompt = `📨 quota-mode notify — A-account 429 wall CLEARED (5h reset, spawned by cc-fuse; NO mode flip happened) — one job, then exit.
1. Registry = the watchers.md snapshot embedded at the END of this prompt (spawn-time copy by
   cc-fuse). A live re-read of S:/claude-cache/go-when-racing/watchers.md is also allowed
   (--add-dir grant on the registry dir) and a successful re-read wins over the snapshot;
   that winning content is missing/empty/no lines → exit silently. If NO embedded snapshot
   is present AND the live re-read fails on permissions → do NOT exit silently: treat every
   line as ` + "`" + `session: unknown` + "`" + `, run step 4's single self-filtering broadcast, then exit.
2. ToolSearch select:ListAgents,SendMessage; ListAgents once.
3. For each line whose session name appears in ListAgents → SendMessage (fire-and-forget):
   「A 帳號的 429 牆已過 5 小時重置 — 若你有因 429/usage limit 停下的工作,立即原地 retry 續做
   (依全局規則,reset 時間不構成等待理由);若你是 go-when-racing chain,照 poll guard 跑,
   一切以 quota-mode.md 的 mode 為準。沒有停住的工作就忽略本訊息,不做任何事。」
   A name absent from ListAgents means that window is closed → skip the line.
4. Lines with ` + "`" + `session: unknown` + "`" + ` → covered by ONE self-filtering broadcast to every local
   session, same text minus the chain pointer.
5. Exit. No acks, no reports, no waiting.`

// TestSpawnResetVerbatim: a registry with content lines spawns exactly ONE
// headless session for the RESET mode, carrying the verbatim RESET prompt
// with the same spawn shape as the flip notifies (stripped env, user-home
// working dir, windowless attributes).
func TestSpawnResetVerbatim(t *testing.T) {
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

	if err := Spawn(registry, "RESET"); err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	if n := len(*sc); n != 1 {
		t.Fatalf("a multi-line registry spawns exactly once, got %d", n)
	}
	assertSpawnShape(t, (*sc)[0], testResetPrompt, registryContent, filepath.Dir(registry), home)
}

// TestSpawnResetGate: RESET walks the same cost gates as the flip modes —
// a missing or blank registry spawns nothing at zero cost (nil), the mode is
// accepted only exact-case, and anything outside {RACING, SAVING, RESET} is
// still rejected without spawning even when the registry has lines.
func TestSpawnResetGate(t *testing.T) {
	cases := []struct {
		name      string
		absent    bool
		content   string
		mode      string
		wantErr   bool
		wantSpawn int
	}{
		{"RESET missing file", true, "", "RESET", false, 0},
		{"RESET empty file", false, "", "RESET", false, 0},
		{"RESET whitespace-only lines", false, " \n\t\n   \r\n", "RESET", false, 0},
		{"RESET accepted", false, "10:00 | chain: x.md | session: a", "RESET", false, 1},
		{"lowercase reset rejected", false, "10:00 | chain: x.md | session: a", "reset", true, 0},
		{"unknown mode still rejected", false, "10:00 | chain: x.md | session: a", "NOTIFY", true, 0},
		{"empty mode still rejected", false, "10:00 | chain: x.md | session: a", "", true, 0},
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
			if n := len(*sc); n != tc.wantSpawn {
				t.Fatalf("spawns = %d, want %d (mode %q, absent=%v)", n, tc.wantSpawn, tc.mode, tc.absent)
			}
		})
	}
}
