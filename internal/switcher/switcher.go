// Package switcher safely rewrites the provider fields inside Claude Code's
// settings.json (design §5).
//
// Scope of a switch: only the env block's provider fields —
// ANTHROPIC_API_KEY, ANTHROPIC_BASE_URL and the target profile's
// ANTHROPIC_DEFAULT_* model mappings. Everything else (permissions,
// statusLine, plugins, unrelated env keys) is untouched, and the write is
// atomic (temp file + rename) with an mtime conflict guard against
// concurrent writers. Identify and Watch classify the live file by API key,
// because A and B share the same endpoint host.
package switcher

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"cc-fuse/internal/domain"
)

// WatchKind classifies one external settings.json change (design §5):
// the key switched to a known profile, an env-internal or non-env change
// (Claude Code editing model etc.), or the key switched to something
// unknown (policy must pause and ask, P8).
type WatchKind string

// The three change classifications Watch emits.
const (
	ProviderChanged WatchKind = "ProviderChanged"
	UnrelatedChange WatchKind = "UnrelatedChange"
	UnknownProvider WatchKind = "UnknownProvider"
)

// WatchEvent is one debounced external change of settings.json.
type WatchEvent struct {
	Kind      WatchKind
	ProfileID string // set only for ProviderChanged
}

// apiKeyEnvKey / baseURLEnvKey / defaultModelPrefix are the provider field
// boundaries inside env: the key+URL always belong to the profile, and the
// DEFAULT_* set is exactly the target profile's ModelEnv.
const (
	apiKeyEnvKey       = "ANTHROPIC_API_KEY"
	baseURLEnvKey      = "ANTHROPIC_BASE_URL"
	defaultModelPrefix = "ANTHROPIC_DEFAULT_"
)

// maxWriteAttempts is the mtime-conflict retry budget (card: ≤3).
const maxWriteAttempts = 3

// maxBackups is the backup retention (design §5, audit C7-v: exactly 10).
const maxBackups = 10

// statFile is the white-box injection seam for the mtime guard tests.
var statFile = os.Stat

// errWriteConflict marks a lost mtime race against an external writer.
var errWriteConflict = errors.New("settings.json changed while applying")

// Identify reads the provider identity out of a settings.json document:
// it compares env.ANTHROPIC_API_KEY against the profiles and returns the
// matching profile ID. known=false means the key is absent/unparseable or
// matches no profile — the caller treats the file as "unknown provider".
func Identify(settingsJSON []byte, profiles []domain.Profile) (string, bool) {
	key, ok := envString(settingsJSON, apiKeyEnvKey)
	if !ok || key == "" {
		return "", false
	}
	for _, p := range profiles {
		if p.APIKey == key {
			return p.ID, true
		}
	}
	return "", false
}

// ApplyTo switches settingsPath onto profile, backing the original up into
// backupDir first.
//
// Semantics (per audit A2 set-difference): after the apply, the env block's
// provider field set equals the target profile's complete set — the key,
// base URL and every target ModelEnv key carry the target's values, and any
// ANTHROPIC_DEFAULT_* key the target lacks is removed. All other env keys
// and every top-level block are preserved verbatim (zero-damage guarantee).
//
// Safety rails: the original bytes are backed up into backupDir (rotated to
// the newest maxBackups files) before every write; the read-modify-write is
// guarded by mtime comparison with up to maxWriteAttempts retries, so an
// external writer racing us can never be silently overwritten.
func ApplyTo(profile domain.Profile, settingsPath, backupDir string) error {
	var lastErr error
	for attempt := 0; attempt < maxWriteAttempts; attempt++ {
		st0, err := statFile(settingsPath)
		if err != nil {
			return fmt.Errorf("stat %s: %w", settingsPath, err)
		}
		original, err := os.ReadFile(settingsPath)
		if err != nil {
			return fmt.Errorf("read %s: %w", settingsPath, err)
		}

		updated, err := applyProfile(original, profile)
		if err != nil {
			return err
		}

		// 防衝:讀前/寫前兩次 mtime 必須一致;外部寫入者介入就整輪重來,
		// 絕不蓋掉別人剛寫的內容。
		st1, err := statFile(settingsPath)
		if err != nil {
			return fmt.Errorf("stat %s: %w", settingsPath, err)
		}
		if !st1.ModTime().Equal(st0.ModTime()) {
			lastErr = errWriteConflict
			continue
		}

		if err := backupOriginal(original, backupDir); err != nil {
			return fmt.Errorf("backup %s: %w", backupDir, err)
		}
		if err := atomicWrite(settingsPath, updated); err != nil {
			return err
		}
		return nil
	}
	return fmt.Errorf("%s: %w after %d attempts", settingsPath, lastErr, maxWriteAttempts)
}

// Apply is ApplyTo with the default backup directory
// (%APPDATA%\cc-fuse\backups).
func Apply(profile domain.Profile, settingsPath string) error {
	return ApplyTo(profile, settingsPath, DefaultBackupDir())
}

// DefaultBackupDir returns %APPDATA%\cc-fuse\backups (design §5).
func DefaultBackupDir() string {
	if base, err := os.UserConfigDir(); err == nil && base != "" {
		return filepath.Join(base, "cc-fuse", "backups")
	}
	return filepath.Join(os.Getenv("APPDATA"), "cc-fuse", "backups")
}

// applyProfile merges profile into the original settings bytes and returns
// the new document. The doc is a generic map so every unknown top-level
// block and env key survives the round trip untouched.
func applyProfile(original []byte, p domain.Profile) ([]byte, error) {
	var doc map[string]any
	if err := json.Unmarshal(original, &doc); err != nil {
		return nil, fmt.Errorf("settings is not valid JSON: %w", err)
	}
	env, ok := doc["env"].(map[string]any)
	if !ok {
		// 全新安裝可能還沒有 env 區塊:建一個空的繼續切。
		env = map[string]any{}
		doc["env"] = env
	}

	// 差集語意(audit A2)第一步:目標 profile 沒有的模型鍵移除——
	// 留著會讓 Claude Code 把請求路由到目標帳號不存在的模型映射。
	for k := range env {
		if strings.HasPrefix(k, defaultModelPrefix) {
			if _, keep := p.ModelEnv[k]; !keep {
				delete(env, k)
			}
		}
	}
	// 差集第二步:目標集合全數就位(值 = 目標 profile 之值)。
	env[apiKeyEnvKey] = p.APIKey
	env[baseURLEnvKey] = p.BaseURL
	for k, v := range p.ModelEnv {
		env[k] = v
	}

	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode settings: %w", err)
	}
	return append(out, '\n'), nil
}

// backupOriginal writes the pre-apply bytes into backupDir under a
// timestamped name, then rotates the directory down to the newest
// maxBackups files.
func backupOriginal(original []byte, backupDir string) error {
	if err := os.MkdirAll(backupDir, 0o755); err != nil {
		return err
	}
	path := filepath.Join(backupDir, uniqueBackupName(backupDir, time.Now()))
	if err := os.WriteFile(path, original, 0o644); err != nil {
		return err
	}
	return pruneBackups(backupDir)
}

// backupTimeFormat sorts lexicographically in chronological order, so the
// retention prune can pick the newest by name alone.
const backupTimeFormat = "20060102-150405.000000000"

// uniqueBackupName builds "settings-<timestamp>.json" inside dir, appending
// -1/-2/… if the timestamp is somehow already taken. A stat that cannot be
// resolved is treated as usable (best effort, keeps this loop finite).
func uniqueBackupName(dir string, at time.Time) string {
	name := "settings-" + at.Format(backupTimeFormat) + ".json"
	for i := 1; ; i++ {
		if _, err := os.Stat(filepath.Join(dir, name)); err != nil {
			return name
		}
		name = fmt.Sprintf("settings-%s-%d.json", at.Format(backupTimeFormat), i)
	}
}

// pruneBackups deletes the oldest backup files beyond maxBackups. Names are
// timestamp-shaped, so descending lexicographic order is newest-first.
func pruneBackups(backupDir string) error {
	entries, err := os.ReadDir(backupDir)
	if err != nil {
		return err
	}
	var names []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasPrefix(e.Name(), "settings-") {
			names = append(names, e.Name())
		}
	}
	sortStringsDesc(names)
	for i := maxBackups; i < len(names); i++ {
		if err := os.Remove(filepath.Join(backupDir, names[i])); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
	}
	return nil
}

// sortStringsDesc sorts names in descending lexicographic order.
func sortStringsDesc(names []string) {
	for i := 1; i < len(names); i++ {
		for j := i; j > 0 && names[j] > names[j-1]; j-- {
			names[j], names[j-1] = names[j-1], names[j]
		}
	}
}

// atomicWrite replaces path with data via a temp file in the same directory
// plus rename — readers of path never see a partial file. The target's file
// mode is preserved.
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
