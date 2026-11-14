// Package store persists cross-restart state in state.json (§7).
//
// T02's pacing-budget state (day basis weekly pct, ceiling, settle instant)
// travels through the Budget field as raw JSON, so the policy card can
// evolve its payload without touching this package. Contract: missing file
// = empty State + nil error (cold start, caller bases on current values);
// corrupt file = empty State + definite error, never a panic.
package store

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// State is one snapshot of the cross-restart state file. Budget holds the
// T02 pacing-budget payload verbatim; nil/empty means "no recorded budget".
type State struct {
	Budget json.RawMessage `json:"budget,omitempty"`
}

// SaveState writes s to path atomically (temp file + rename), creating the
// parent directory when needed.
func SaveState(path string, s State) error {
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("encode state: %w", err)
	}
	if dir := filepath.Dir(path); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("create state dir %s: %w", dir, err)
		}
	}
	return atomicWrite(path, data)
}

// LoadState reads state.json from path. Missing file → empty State + nil
// error (cold start). Corrupt JSON → empty State + definite error; the
// caller decides to restart from current values.
func LoadState(path string) (State, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return State{}, nil
		}
		return State{}, fmt.Errorf("read state %s: %w", path, err)
	}
	var s State
	if err := json.Unmarshal(data, &s); err != nil {
		return State{}, fmt.Errorf("state %s is not valid JSON: %w", path, err)
	}
	// 空 State 存檔時 budget 被省略,讀回不該殘留 JSON 的 null 字樣。
	if bytes.Equal(bytes.TrimSpace(s.Budget), []byte("null")) {
		s.Budget = nil
	}
	return s, nil
}

// atomicWrite replaces path with data via a temp file in the same directory
// plus rename — readers of path never see a partial file.
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
