package quotamode_test

// 驗收測試(黑箱)——cc-fuse T01:quota-mode.md 單行翻轉。
// 契約:只改 mode: 行,其餘 byte 不變;檔案不存在回 ErrNotExist。

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"cc-fuse/internal/quotamode"
)

const card = `
## RACE card — 共用帳號(搶速度)

- **目標函數**:pool 被隊友抽乾前完成自己的事。
- **平行是預設**。

切回自購帳號 → /quota-saving
`

func writeFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "quota-mode.md")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func mustFlip(t *testing.T, path, mode string) {
	t.Helper()
	if err := quotamode.Flip(path, mode); err != nil {
		t.Fatalf("Flip(%q): %v", mode, err)
	}
}

// 翻轉後:mode 行換新值,其餘內容 byte 不變。
func TestFlipChangesOnlyModeLine(t *testing.T) {
	original := "mode: RACING\nset: 2026-11-14\n" + card
	path := writeFile(t, original)

	mustFlip(t, path, "SAVING")

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want := "mode: SAVING\nset: 2026-11-14\n" + card
	if string(got) != want {
		t.Fatalf("flip damaged content:\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

// 往返:SAVING → RACING → SAVING,內容可預期。
func TestFlipRoundTrip(t *testing.T) {
	path := writeFile(t, "mode: RACING\nset: x\n"+card)
	mustFlip(t, path, "SAVING")
	mustFlip(t, path, "RACING")
	mustFlip(t, path, "SAVING")

	got, _ := os.ReadFile(path)
	if string(got) != "mode: SAVING\nset: x\n"+card {
		t.Fatalf("round-trip mismatch:\n%s", got)
	}
}

// 翻成相同 mode → 內容語義不變、無錯誤(審計 V3:不承諾 mtime 不動,只承諾內容)。
func TestFlipSameModeNoop(t *testing.T) {
	original := "mode: RACING\nset: x\n" + card
	path := writeFile(t, original)

	mustFlip(t, path, "RACING")

	got, _ := os.ReadFile(path)
	if string(got) != original {
		t.Fatal("same-mode flip must keep content identical")
	}
}

// CRLF 行尾(審計 N8b):Windows 活體檔是 CRLF——翻轉 mode 行後,
// 其餘行尾必須維持 CRLF,不得被整檔改寫成 LF。
func TestFlipPreservesCRLF(t *testing.T) {
	original := "mode: RACING\r\nset: 2026-11-14\r\n## RACE card\r\n- bullet one\r\n"
	path := writeFile(t, original)

	mustFlip(t, path, "SAVING")

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	want := "mode: SAVING\r\nset: 2026-11-14\r\n## RACE card\r\n- bullet one\r\n"
	if string(got) != want {
		t.Fatalf("CRLF content damaged:\n--- got ---\n%q\n--- want ---\n%q", got, want)
	}
}

// 原子寫入的可觀察面:Flip 後同目錄不留暫存殘物。
func TestFlipLeavesNoTempLitter(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "quota-mode.md")
	if err := os.WriteFile(path, []byte("mode: RACING\nset: x\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	mustFlip(t, path, "SAVING")

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name() != "quota-mode.md" {
			t.Fatalf("temp litter left behind: %s", e.Name())
		}
	}
}

// 檔案不存在 → ErrNotExist(呼叫端據此跳過)。
func TestFlipMissingFile(t *testing.T) {
	err := quotamode.Flip(filepath.Join(t.TempDir(), "nope.md"), "SAVING")
	if !errors.Is(err, quotamode.ErrNotExist) {
		t.Fatalf("want ErrNotExist, got %v", err)
	}
}

// mode 行有多餘空白變體(mode:   RACING)也要能命中翻轉。
func TestFlipLooseSpacing(t *testing.T) {
	path := writeFile(t, "mode:   RACING\nset: x\n"+card)
	mustFlip(t, path, "SAVING")
	got, _ := os.ReadFile(path)
	if string(got) != "mode: SAVING\nset: x\n"+card {
		t.Fatalf("loose spacing not handled:\n%s", got)
	}
}
