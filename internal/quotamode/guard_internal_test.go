package quotamode

// 白箱單元測試——Flip 的 mtime 防衝重試(card 裁決:黑箱驗收刻意不覆蓋,由白箱
// hook 覆蓋)。注入 statFile 把「讀前/寫前兩次 mtime」腳本化,模擬 quota 技能群
// (另一個整檔回寫的寫入者)在讀取與驗證之間介入,不需要不穩定的競寫 goroutine。

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

type fakeInfo struct{ mod time.Time }

func (f fakeInfo) Name() string       { return "fake" }
func (f fakeInfo) Size() int64        { return 0 }
func (f fakeInfo) Mode() os.FileMode  { return 0o644 }
func (f fakeInfo) ModTime() time.Time { return f.mod }
func (f fakeInfo) IsDir() bool        { return false }
func (f fakeInfo) Sys() any           { return nil }

// stubStat installs a scripted statFile and restores it on cleanup.
func stubStat(t *testing.T, script func(call int) (time.Time, error)) {
	t.Helper()
	orig := statFile
	calls := 0
	statFile = func(string) (os.FileInfo, error) {
		calls++
		at, err := script(calls)
		return fakeInfo{mod: at}, err
	}
	t.Cleanup(func() { statFile = orig })
}

// 腳本 1:第一次嘗試的「寫前驗證」mtime 跳掉(模擬整檔回寫)→ 衝突重試;
// 第二次嘗試一致 → 成功寫入,內容為翻轉後的結果。
func TestFlipRetriesOnceOnMtimeConflict(t *testing.T) {
	path := filepath.Join(t.TempDir(), "quota-mode.md")
	original := "mode: RACING\r\nset: 2026-11-14\r\ncard\r\n"
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}

	base := time.Unix(1_700_000_000, 0)
	stubStat(t, func(call int) (time.Time, error) {
		if call == 1 {
			return base, nil // 第一次嘗試讀前的 mtime
		}
		return base.Add(time.Second), nil // 第 2 次起:另一寫入者已回寫整檔
	})

	if err := Flip(path, "SAVING"); err != nil {
		t.Fatalf("flip should succeed on the retry: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if want := "mode: SAVING\r\nset: 2026-11-14\r\ncard\r\n"; string(got) != want {
		t.Fatalf("retry must write the flipped content:\n--- got ---\n%q\n--- want ---\n%q", got, want)
	}
}

// 腳本 2:每次驗證 mtime 都比讀前新 → 永遠衝突 → 3 次後回 errWriteConflict,
// 檔案內容不得被改動。
func TestFlipConflictExhaustsRetriesWithoutWrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "quota-mode.md")
	original := "mode: RACING\nset: x\n"
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}

	base := time.Unix(1_700_000_000, 0)
	stubStat(t, func(call int) (time.Time, error) {
		return base.Add(time.Duration(call) * time.Second), nil // 每次 stat 都更新
	})

	if err := Flip(path, "SAVING"); !errors.Is(err, errWriteConflict) {
		t.Fatalf("exhausted retries must surface errWriteConflict, got: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != original {
		t.Fatalf("exhausted conflict must leave the file untouched:\n%q", got)
	}
}

// 無 mode: 行 → 明確錯誤,不得誤寫整檔。
func TestFlipWithoutModeLineErrors(t *testing.T) {
	path := filepath.Join(t.TempDir(), "quota-mode.md")
	original := "no mode here\njust a card\n"
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := Flip(path, "SAVING"); err == nil || errors.Is(err, ErrNotExist) {
		t.Fatalf("missing mode line must be a plain error, got: %v", err)
	}
	got, _ := os.ReadFile(path)
	if string(got) != original {
		t.Fatalf("failed flip must not touch the file:\n%q", got)
	}
}
