package switcher

// Watch 是本檔的第二半:settings.json 的外部變動監看與分類。
// 獨立成檔是為了讓「寫入路徑」(switcher.go)與「讀側監看」各自聚焦。

import (
	"context"
	"encoding/json"
	"os"
	"time"

	"cc-fuse/internal/domain"
)

// Watch tuning: poll + settle debounce gives at most one event per external
// change with nothing but the standard library (no fsnotify dependency in T01).
const (
	watchPollInterval = 200 * time.Millisecond // mtime check cadence
	watchSettleDelay  = 300 * time.Millisecond // quiet window before reading a detected change
	watchEventBuffer  = 16                     // events the consumer may lag behind
)

// Watch monitors settingsPath for external changes and reports one debounced
// event per change on the returned channel (design §5, D7/D8 groundwork).
//
// Classification, keyed on env.ANTHROPIC_API_KEY versus the previously seen
// key (A/B share one endpoint, the key is the identity):
//   - unchanged key → UnrelatedChange (both non-env edits like /model and
//     env-internal non-provider edits like API_TIMEOUT_MS — decision
//     2026-11-15: classification looks at the key only);
//   - key now matches a profile → ProviderChanged{ProfileID} (adopted as the
//     new reality, e.g. cc-switch hand-over during migration);
//   - key changed but matches nothing → UnknownProvider (policy pauses, P8).
//
// ctx cancel closes the channel and stops the goroutine. The file is read
// only after watchSettleDelay of detected mtime activity, so a burst of
// writes collapses into a single event. No event is emitted for the state
// Watch starts with.
func Watch(ctx context.Context, settingsPath string, profiles []domain.Profile) (<-chan WatchEvent, error) {
	events := make(chan WatchEvent, watchEventBuffer)

	// 基線:啟動當下的 key 與 mtime;啟動前的狀態不發事件。
	lastKey, _ := readAPIKey(settingsPath)
	var lastMod time.Time
	if st, err := os.Stat(settingsPath); err == nil {
		lastMod = st.ModTime()
	}

	go func() {
		defer close(events)
		ticker := time.NewTicker(watchPollInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}

			st, err := os.Stat(settingsPath)
			if err != nil || !st.ModTime().After(lastMod) {
				continue
			}
			// debounce:等寫入落定再一次讀取,一次變動至多一個事件。
			select {
			case <-ctx.Done():
				return
			case <-time.After(watchSettleDelay):
			}
			st, err = os.Stat(settingsPath)
			if err != nil {
				continue
			}
			data, err := os.ReadFile(settingsPath)
			if err != nil {
				continue
			}
			lastMod = st.ModTime()
			newKey, _ := readAPIKeyBytes(data)
			ev := classifyChange(newKey, lastKey, profiles)
			lastKey = newKey

			// 阻塞送出但隨時可被 ctx 打斷,收斂路徑唯一(ctx 取消 → 通道關閉)。
			select {
			case events <- ev:
			case <-ctx.Done():
				return
			}
		}
	}()
	return events, nil
}

// classifyChange compares the freshly read key against the previous one.
func classifyChange(newKey, lastKey string, profiles []domain.Profile) WatchEvent {
	if newKey == lastKey {
		return WatchEvent{Kind: UnrelatedChange}
	}
	for _, p := range profiles {
		if p.APIKey == newKey {
			return WatchEvent{Kind: ProviderChanged, ProfileID: p.ID}
		}
	}
	return WatchEvent{Kind: UnknownProvider}
}

// readAPIKey reads env.ANTHROPIC_API_KEY from the file at path; a missing
// or unreadable file yields an empty key (treated as "no key seen yet").
func readAPIKey(path string) (string, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}
	return readAPIKeyBytes(data)
}

// readAPIKeyBytes extracts one string env value from a settings document.
// Values are read leniently (json.RawMessage per key) so a single non-string
// env entry elsewhere in the block cannot break provider identification.
func readAPIKeyBytes(data []byte) (string, bool) {
	return envString(data, apiKeyEnvKey)
}

// envString decodes one string-typed env entry from a settings document.
func envString(data []byte, key string) (string, bool) {
	var doc struct {
		Env map[string]json.RawMessage `json:"env"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return "", false
	}
	raw, ok := doc.Env[key]
	if !ok {
		return "", false
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return "", false
	}
	return s, true
}
