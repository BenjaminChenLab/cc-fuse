package switcher

// 白箱單元測試——mtime 防衝重試(card 裁決:黑箱驗收刻意不覆蓋,由實作派工的白箱
// hook 覆蓋)。作法:注入 statFile,把「讀前/寫前兩次 mtime」腳本化,模擬外部寫入者
// 在讀取與驗證之間搶寫,而不需要不穩定的競寫 goroutine。

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"cc-fuse/internal/domain"
)

// fakeInfo 只為 mtime 防衛存在:ApplyTo 只讀 ModTime。
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

// 腳本 1:第一次嘗試的「寫前驗證」mtime 跳掉(模擬外部搶寫)→ 判衝突重試;
// 第二次嘗試兩次 mtime 一致 → 成功。共 4 次 stat,寫入恰一次。
func TestApplyToRetriesOnceOnMtimeConflict(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	if err := os.WriteFile(path, []byte(`{"env":{"ANTHROPIC_API_KEY":"key-b-value"},"model":"opus"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	backupDir := filepath.Join(dir, "backups")

	base := time.Unix(1_700_000_000, 0)
	stubStat(t, func(call int) (time.Time, error) {
		if call == 1 {
			return base, nil // 第一次嘗試讀前的 mtime
		}
		return base.Add(time.Second), nil // 第 2 次起:外部已搶寫 → 讀後 mtime 變新
	})

	p := domain.Profile{ID: "A", APIKey: "key-a-value", BaseURL: "https://a.example/api/anthropic"}
	if err := ApplyTo(p, path, backupDir); err != nil {
		t.Fatalf("apply should succeed on the retry: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "key-a-value") {
		t.Fatalf("retry must still apply the profile, got: %s", got)
	}
	entries, err := os.ReadDir(backupDir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("conflicted attempt must not leave an extra backup: n=%d err=%v", len(entries), err)
	}
}

// 腳本 2:每次驗證 mtime 都比讀前更新 → 永遠衝突 → 3 次後回 errWriteConflict,
// 且原檔內容不得被改動(防衛的意義就是不能蓋掉別人)。
func TestApplyToConflictExhaustsRetriesWithoutWrite(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	original := `{"env":{"ANTHROPIC_API_KEY":"key-b-value"}}`
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}
	backupDir := filepath.Join(dir, "backups")

	base := time.Unix(1_700_000_000, 0)
	stubStat(t, func(call int) (time.Time, error) {
		return base.Add(time.Duration(call) * time.Second), nil // 每次 stat 都更新
	})

	p := domain.Profile{ID: "A", APIKey: "key-a-value", BaseURL: "https://a.example/api/anthropic"}
	err := ApplyTo(p, path, backupDir)
	if !errors.Is(err, errWriteConflict) {
		t.Fatalf("exhausted retries must surface errWriteConflict, got: %v", err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != original {
		t.Fatalf("exhausted conflict must leave the file untouched:\n%s", got)
	}
	if entries, _ := os.ReadDir(backupDir); len(entries) != 0 {
		t.Fatalf("no backup may be written when the guard never clears: n=%d", len(entries))
	}
}

// 差集邊角:目標 profile 沒有 ModelEnv 時,既有 DEFAULT_* 鍵仍須全數移除。
func TestApplyProfileWithEmptyModelEnvRemovesAllDefaults(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	doc := `{"env":{"ANTHROPIC_API_KEY":"key-b-value","ANTHROPIC_BASE_URL":"https://b.example","ANTHROPIC_DEFAULT_OPUS_MODEL":"glm-5.3-b","API_TIMEOUT_MS":"3000"},"model":"opus"}`
	if err := os.WriteFile(path, []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}

	p := domain.Profile{ID: "A", APIKey: "key-a-value", BaseURL: "https://a.example/api/anthropic"}
	if err := ApplyTo(p, path, filepath.Join(dir, "backups")); err != nil {
		t.Fatalf("apply: %v", err)
	}
	got, _ := os.ReadFile(path)
	if strings.Contains(string(got), "ANTHROPIC_DEFAULT_OPUS_MODEL") {
		t.Fatalf("default model key missing from target profile must be removed:\n%s", got)
	}
	if !strings.Contains(string(got), "API_TIMEOUT_MS") {
		t.Fatal("non-provider env key must survive")
	}
}
