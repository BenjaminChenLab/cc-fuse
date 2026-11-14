package main

// 大小上限觸發的 log 輪替(設計 §10:log 旋轉檔於 %APPDATA%\cc-fuse\logs)。
// 標準庫自製:寫入超過 maxLogBytes 就換新檔,保留最近 keepLogs 份。

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// Rotation tuning: 2 MB per file, keep 5 — decisions and errors are tiny
// lines, so this is weeks of history at worst.
const (
	maxLogBytes = 2 << 20
	keepLogs    = 5
)

// rotatingWriter is an io.Writer that rolls the active file once it grows
// past maxBytes, renaming older ones <name>.1 … and pruning beyond keep.
// Safe for concurrent use (the Loop and the notifier both log).
type rotatingWriter struct {
	mu       sync.Mutex
	dir      string
	name     string // base file name, e.g. "cc-fuse.log"
	maxBytes int64  // rotate threshold (production: maxLogBytes)
	keep     int    // generations to retain (production: keepLogs)
	f        *os.File
	size     int64
}

// newRotatingWriter opens (creating dirs as needed) the rotating log at
// dir/name with the production rotation settings, appending to the current
// generation.
func newRotatingWriter(dir, name string) (*rotatingWriter, error) {
	return newRotatingWriterSized(dir, name, maxLogBytes, keepLogs)
}

// newRotatingWriterSized is the injectable-threshold variant (tests use tiny
// sizes so a rotation cycle writes kilobytes, not megabytes).
func newRotatingWriterSized(dir, name string, maxBytes int64, keep int) (*rotatingWriter, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create log dir %s: %w", dir, err)
	}
	return &rotatingWriter{dir: dir, name: name, maxBytes: maxBytes, keep: keep}, nil
}

// Write implements io.Writer, rotating first when the current file is full.
func (w *rotatingWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.f == nil || w.size+int64(len(p)) > w.maxBytes {
		if err := w.rotate(); err != nil {
			return 0, err
		}
	}
	n, err := w.f.Write(p)
	w.size += int64(n)
	return n, err
}

// Close releases the active file handle.
func (w *rotatingWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.f == nil {
		return nil
	}
	err := w.f.Close()
	w.f, w.size = nil, 0
	return err
}

// rotate closes the current file, shifts <name>.1..N-1 → .2..N, renames the
// live file to .1 and opens a fresh one, pruning anything past keep.
func (w *rotatingWriter) rotate() error {
	if w.f != nil {
		_ = w.f.Close()
		w.f, w.size = nil, 0
	}
	live := filepath.Join(w.dir, w.name)
	// 輪替位移:最新世代是 .1,越老號碼越大;位移順序從老到新,舊檔先讓位。
	for i := w.keep - 1; i >= 1; i-- {
		_ = os.Rename(fmt.Sprintf("%s.%d", live, i), fmt.Sprintf("%s.%d", live, i+1))
	}
	_ = os.Rename(live, live+".1")

	f, err := os.OpenFile(live, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return fmt.Errorf("open log %s: %w", live, err)
	}
	st, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return fmt.Errorf("stat log %s: %w", live, err)
	}
	w.f, w.size = f, st.Size()
	return nil
}
