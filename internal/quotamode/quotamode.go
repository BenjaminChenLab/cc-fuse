// Package quotamode flips the `mode:` line inside S:\claude-cache\quota-mode.md.
//
// The file is a live hand-edited artifact shared with the quota skill family
// (its card content is rewritten wholesale by another writer family), so the
// write path is guarded: read → transform → compare mtime → atomic write,
// re-reading and retrying when someone else wrote in between. Everything
// except the `mode:` line must survive byte-for-byte, line endings included —
// a CRLF file must never come out rewritten as LF (Windows live file).
package quotamode

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// ErrNotExist reports the quota-mode file itself is missing; the caller
// skips the flip and carries on (design §11.5 — file absent is not fatal).
var ErrNotExist = errors.New("quota-mode file does not exist")

// errWriteConflict marks a lost mtime race against the other writer family.
var errWriteConflict = errors.New("quota-mode changed while flipping")

// maxWriteAttempts is the mtime-conflict retry budget (card: ≤3).
const maxWriteAttempts = 3

// statFile is the white-box injection seam for the mtime guard tests.
var statFile = os.Stat

// Flip rewrites only the `mode:` line of the file at path to the given mode
// value, e.g. "RACING"/"SAVING".
//
// Policies:
//   - the first line matching `mode:` + whitespace is replaced with the
//     normalized form `mode: <mode>` (single space), loose input spacing
//     like `mode:   RACING` matches too; every other byte is untouched,
//     line endings included;
//   - flipping to the value already present is a no-op success (content
//     unchanged — no mtime promise, per audit V3);
//   - a file with no `mode:` line is a definite error, not a silent skip;
//   - missing file → ErrNotExist.
//
// Concurrency guard: mtime is taken before the read and compared before the
// atomic write; on mismatch the whole read-flip cycle retries, at most
// maxWriteAttempts times, so an interleaved whole-file rewrite by the quota
// skill family can never be silently rolled back.
func Flip(path, mode string) error {
	var lastErr error
	for attempt := 0; attempt < maxWriteAttempts; attempt++ {
		st0, err := statFile(path)
		if err != nil {
			return wrapNotExist(err, path)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return wrapNotExist(err, path)
		}

		updated, found := flipModeLine(data, mode)
		if !found {
			return fmt.Errorf("no `mode:` line found in %s", path)
		}
		// 同值翻轉:內容已經是目標值 → 不動檔案,直接成功。
		if bytes.Equal(updated, data) {
			return nil
		}

		// 防衝:讀前/寫前兩次 mtime 必須一致,否則視為第二寫入者介入,重讀重試。
		st1, err := statFile(path)
		if err != nil {
			return fmt.Errorf("stat %s: %w", path, err)
		}
		if !st1.ModTime().Equal(st0.ModTime()) {
			lastErr = errWriteConflict
			continue
		}

		if err := atomicWrite(path, updated); err != nil {
			return err
		}
		return nil
	}
	return fmt.Errorf("%s: %w after %d attempts", path, lastErr, maxWriteAttempts)
}

// flipModeLine replaces the first `mode:` line's content with
// "mode: <mode>", preserving that line's terminator (LF or CRLF) and every
// other byte of the document. found=false means no mode line exists.
func flipModeLine(data []byte, mode string) (out []byte, found bool) {
	out = make([]byte, 0, len(data)+len(mode))
	replacement := []byte("mode: " + mode)
	for i := 0; i < len(data); {
		// 逐行掃描但保留原始行尾位元組:LF 行尾保持 LF、CRLF 保持 CRLF。
		nl := bytes.IndexByte(data[i:], '\n')
		var line, term []byte
		if nl < 0 {
			line, term = data[i:], nil
			i = len(data)
		} else {
			end := i + nl
			if end > i && data[end-1] == '\r' {
				line, term = data[i:end-1], []byte{'\r', '\n'}
			} else {
				line, term = data[i:end], []byte{'\n'}
			}
			i = end + 1
		}
		if !found && isModeLine(line) {
			out = append(out, replacement...)
			out = append(out, term...)
			found = true
			continue
		}
		out = append(out, line...)
		out = append(out, term...)
	}
	return out, found
}

// isModeLine reports whether line is the `mode:` line: the exact prefix
// "mode:" followed by whitespace (loose spacing matches) or end of line.
func isModeLine(line []byte) bool {
	if !bytes.HasPrefix(line, []byte("mode:")) {
		return false
	}
	rest := line[len("mode:"):]
	if len(rest) == 0 {
		return true
	}
	return rest[0] == ' ' || rest[0] == '\t'
}

// wrapNotExist maps a stat/read failure on the live file to ErrNotExist
// when the file is genuinely absent, wrapping otherwise.
func wrapNotExist(err error, path string) error {
	if errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("%w: %s", ErrNotExist, path)
	}
	return fmt.Errorf("read %s: %w", path, err)
}

// atomicWrite replaces path with data via a temp file in the same directory
// plus rename, preserving the target's file mode.
func atomicWrite(path string, data []byte) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".cc-fuse-*.tmp")
	if err != nil {
		return fmt.Errorf("create temp in %s: %w", dir, err)
	}
	tmpName := tmp.Name()
	// Rename 之後這個 Remove 只是無害的 not-exist;失敗路徑上它清掉暫存殘物。
	defer func() {
		if tmpName != "" {
			_ = os.Remove(tmpName)
		}
	}()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write temp %s: %w", tmpName, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp %s: %w", tmpName, err)
	}
	mode := os.FileMode(0o644)
	if st, err := os.Stat(path); err == nil {
		mode = st.Mode().Perm()
	}
	if err := os.Chmod(tmpName, mode); err != nil {
		return fmt.Errorf("chmod temp %s: %w", tmpName, err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("rename %s -> %s: %w", tmpName, path, err)
	}
	tmpName = "" // 已改名,deferred cleanup 不用再清
	return nil
}
