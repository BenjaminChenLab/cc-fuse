package main

// 單元測試:log 輪替——寫滿就換新檔、舊檔編號位移、保留份數封頂。
// 用可注入的小門檻(16B/3 份)跑整個輪替循環:快、小、不踩 Windows
// TempDir 清理與防毒掃描的賽跑(2MB 大檔曾造成間歇 RemoveAll 失敗)。

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// tempLogDir replaces t.TempDir for these tests: Defender transiently holds
// handles on freshly written files, and one-shot RemoveAll can hit exactly
// that instant and die with "directory is not empty" (flake, field-evidenced
// twice). Handles are millisecond-scale — a few retries always clear them.
func tempLogDir(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(os.TempDir(), "cc-fuse-logtest-"+t.Name()+"-"+time.Now().Format("150405.000000000"))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		for i := 0; i < 10; i++ {
			if os.RemoveAll(dir) == nil {
				return
			}
			time.Sleep(20 * time.Millisecond)
		}
	})
	return dir
}

// TestRotateOnOverflow:超限那一筆前先輪替——live 只留新資料,舊資料進 .1。
func TestRotateOnOverflow(t *testing.T) {
	dir := tempLogDir(t)
	w, err := newRotatingWriterSized(dir, "t.log", 16, 3)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	if n, err := w.Write(bytes.Repeat([]byte("a"), 16)); err != nil || n != 16 {
		t.Fatalf("write full file: n=%d err=%v", n, err)
	}
	if _, err := w.Write([]byte("b")); err != nil {
		t.Fatal(err)
	}
	live, err := os.ReadFile(filepath.Join(dir, "t.log"))
	if err != nil || string(live) != "b" {
		t.Fatalf("live log = %v (err=%v), want [b]", live, err)
	}
	rolled, err := os.ReadFile(filepath.Join(dir, "t.log.1"))
	if err != nil || len(rolled) != 16 {
		t.Fatalf("t.log.1 len = %d (err=%v), want 16", len(rolled), err)
	}
}

// TestRotateKeepsGenerationsCapped:輪替多次後只留 .1..keep,不出超額老檔。
func TestRotateKeepsGenerationsCapped(t *testing.T) {
	const (
		cap  = 16
		keep = 3
	)
	dir := tempLogDir(t)
	w, err := newRotatingWriterSized(dir, "t.log", cap, keep)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	// 輪替 6 次(> keep):每筆 17B 超限,必輪替。
	for i := 0; i < 6; i++ {
		if _, err := w.Write(bytes.Repeat([]byte("x"), cap+1)); err != nil {
			t.Fatal(err)
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	live := 0
	for _, e := range entries {
		if e.Name() == "t.log" {
			live++
		}
		if e.Name() == "t.log."+itoa(keep+1) {
			t.Fatalf("generation beyond keep must be pruned: %s", e.Name())
		}
	}
	if live != 1 {
		t.Fatalf("live log must exist exactly once, got %d", live)
	}
	if _, err := os.Stat(filepath.Join(dir, "t.log."+itoa(keep))); err != nil {
		t.Fatalf("oldest generation .%d missing: %v", keep, err)
	}
}

// TestWriteAcrossRotation:內容跨輪替不丟失——總位元組數守恆(忽略輪替損耗前題下逐檔檢查)。
func TestWriteAcrossRotation(t *testing.T) {
	dir := tempLogDir(t)
	w, err := newRotatingWriterSized(dir, "t.log", 16, 5)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	payload := []byte("0123456789ABCDEF") // 16B:整檔填滿
	for i := 0; i < 4; i++ {
		if _, err := w.Write(payload); err != nil {
			t.Fatal(err)
		}
	}
	// 第 4 筆會在前 3 筆間輪替出 .1.. .3,live 應是第 4 筆原文。
	live, err := os.ReadFile(filepath.Join(dir, "t.log"))
	if err != nil || !bytes.Equal(live, payload) {
		t.Fatalf("live log corrupted: %q err=%v", live, err)
	}
}

// itoa 是測試內的小工具,避免為了測試引 strconv 進生產碼路徑。
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}
