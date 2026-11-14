package switcher_test

// 驗收測試(黑箱)——cc-fuse T01:settings.json 安全切換器。
// 契約(設計 §5 + per audit A2 差集語意 + 編碼審計補強 2026-11-15):
//   Apply 後 env 的供應商欄位集合 = 目標 profile 的完整集合(缺的 DEFAULT_* 鍵移除,
//   BASE_URL 與模型鍵「值」切換為目標 profile 之值);非供應商欄位與頂層區塊以
//   深比對證明零碰觸;首次改動前備份(輪替恰 10 份);Identify 以 API key 比對;
//   Watch 回報外部變動三分類+env 內非供應商鍵變動=UnrelatedChange。

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"cc-fuse/internal/domain"
	"cc-fuse/internal/switcher"
)

// 活體 settings.json 的代表性形態(鍵值為測試假資料,非真實金鑰)。
// 文件=B 態;A/B 兩 profile 的 BaseURL 與模型值刻意不同——讓「值切換」可觀察(審計 M8/V2)。
const settingsBState = `{
  "env": {
    "ANTHROPIC_API_KEY": "key-b-value",
    "ANTHROPIC_BASE_URL": "https://api.zhipu-b.example/api/anthropic",
    "ANTHROPIC_DEFAULT_HAIKU_MODEL": "glm-5.3-flash-b",
    "ANTHROPIC_DEFAULT_OPUS_MODEL": "glm-5.3-b",
    "API_TIMEOUT_MS": "3000000",
    "CLAUDE_CODE_DISABLE_TERMINAL_TITLE": "1",
    "CLAUDE_CODE_MAX_SUBAGENT_SPAWN_DEPTH": "3"
  },
  "permissions": {"allow": ["*gradlew*"], "deny": ["sed *"]},
  "model": "opus",
  "statusLine": {"type": "command", "command": "node statusline.js"},
  "outputStyle": "Thorough Explanations",
  "autoCompactEnabled": false
}`

var profileA = domain.Profile{
	ID: "A", Name: "team shared", APIKey: "key-a-value",
	BaseURL: "https://api.z.ai/api/anthropic",
	ModelEnv: map[string]string{
		"ANTHROPIC_DEFAULT_HAIKU_MODEL":       "glm-5.3-flash",
		"ANTHROPIC_DEFAULT_OPUS_MODEL":        "glm-5.3",
		"ANTHROPIC_DEFAULT_SONNET_MODEL":      "glm-5.3-flash",
		"ANTHROPIC_DEFAULT_SONNET_MODEL_NAME": "glm-5.3-flash",
	},
}

var profileB = domain.Profile{
	ID: "B", Name: "personal", APIKey: "key-b-value",
	BaseURL: "https://api.zhipu-b.example/api/anthropic",
	ModelEnv: map[string]string{
		"ANTHROPIC_DEFAULT_HAIKU_MODEL": "glm-5.3-flash-b",
		"ANTHROPIC_DEFAULT_OPUS_MODEL":  "glm-5.3-b",
	},
}

// 供應商欄位集合:{ANTHROPIC_API_KEY, ANTHROPIC_BASE_URL} ∪ 該 profile ModelEnv 的鍵。
var providerKeys = []string{"ANTHROPIC_API_KEY", "ANTHROPIC_BASE_URL"}

func settingsFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

func readSettings(t *testing.T, path string) map[string]any {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("resulting settings.json is not valid JSON: %v", err)
	}
	return m
}

func envOf(t *testing.T, m map[string]any) map[string]any {
	t.Helper()
	env, ok := m["env"].(map[string]any)
	if !ok {
		t.Fatal("env block missing or not an object")
	}
	return env
}

// 深比對工具(審計 M9):兩份文件的「非供應商部分」必須全等——
// env 去掉所有供應商鍵後,與頂層全區塊分別 DeepEqual。
func assertZeroDamage(t *testing.T, before, after map[string]any) {
	t.Helper()
	stripProvider := func(m map[string]any) map[string]any {
		out := make(map[string]any)
		for k, v := range m {
			out[k] = v
		}
		env, ok := out["env"].(map[string]any)
		if !ok {
			return out
		}
		cleanEnv := make(map[string]any)
		for k, v := range env {
			cleanEnv[k] = v
		}
		for _, k := range providerKeys {
			delete(cleanEnv, k)
		}
		for k := range env {
			if len(k) > len("ANTHROPIC_DEFAULT_") && k[:len("ANTHROPIC_DEFAULT_")] == "ANTHROPIC_DEFAULT_" {
				delete(cleanEnv, k)
			}
		}
		out["env"] = cleanEnv
		return out
	}
	if !reflect.DeepEqual(stripProvider(before), stripProvider(after)) {
		t.Fatalf("non-provider content damaged:\n--- before ---\n%v\n--- after  ---\n%v", before, after)
	}
}

// --- Identify:以 env.ANTHROPIC_API_KEY 比對 profile(設計 §5,D7/D8 的地基) ---

func TestIdentify(t *testing.T) {
	for _, tc := range []struct {
		name  string
		doc   string
		want  string
		known bool
	}{
		{"B key", swapKey(settingsBState, "key-b-value"), "B", true},
		{"A key", swapKey(settingsBState, "key-a-value"), "A", true},
		{"unknown key", swapKey(settingsBState, "key-unknown"), "", false},
		{"missing env key", `{"env":{}}`, "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			id, known := switcher.Identify([]byte(tc.doc), []domain.Profile{profileA, profileB})
			if known != tc.known || id != tc.want {
				t.Fatalf("identify %s: got (%q,%v), want (%q,%v)", tc.name, id, known, tc.want, tc.known)
			}
		})
	}
}

// --- Apply:零損傷(深比對)+ 差集語意 + 值切換(per audit A2、審計 M8/M9/V2) ---

// B→A:gain 2 個 SONNET 鍵、key/BASE_URL/模型鍵全部切換為 A 之值、其餘零碰觸。
func TestApplyUpToA_SetDifference(t *testing.T) {
	path := settingsFile(t, settingsBState)
	before := readSettings(t, path)
	backupDir := filepath.Join(t.TempDir(), "backups")

	if err := switcher.ApplyTo(profileA, path, backupDir); err != nil {
		t.Fatalf("apply: %v", err)
	}

	after := readSettings(t, path)
	env := envOf(t, after)

	if env["ANTHROPIC_API_KEY"] != profileA.APIKey {
		t.Fatalf("api key = %v, want %v", env["ANTHROPIC_API_KEY"], profileA.APIKey)
	}
	if env["ANTHROPIC_BASE_URL"] != profileA.BaseURL {
		t.Fatalf("BASE_URL = %v, want %v (endpoint switching must be observable)", env["ANTHROPIC_BASE_URL"], profileA.BaseURL)
	}
	for k, want := range profileA.ModelEnv {
		if env[k] != want {
			t.Fatalf("model key %q = %v, want %q", k, env[k], want)
		}
	}
	assertZeroDamage(t, before, after)
}

// A→B:SONNET 兩鍵必須被移除(差集語意)——留著會讓 B 路由到沒有的模型映射。
func TestApplyDownToB_SetDifference(t *testing.T) {
	aDoc := swapKey(injectSonnetKeys(settingsBState), "key-a-value")
	path := settingsFile(t, aDoc)
	before := readSettings(t, path)
	backupDir := filepath.Join(t.TempDir(), "backups")

	if err := switcher.ApplyTo(profileB, path, backupDir); err != nil {
		t.Fatalf("apply: %v", err)
	}

	after := readSettings(t, path)
	env := envOf(t, after)
	if env["ANTHROPIC_API_KEY"] != profileB.APIKey {
		t.Fatalf("api key = %v, want %v", env["ANTHROPIC_API_KEY"], profileB.APIKey)
	}
	if env["ANTHROPIC_BASE_URL"] != profileB.BaseURL {
		t.Fatalf("BASE_URL = %v, want %v", env["ANTHROPIC_BASE_URL"], profileB.BaseURL)
	}
	for k := range profileA.ModelEnv {
		if _, ok := envOf(t, after)[k]; ok && profileB.ModelEnv[k] == "" {
			t.Fatalf("key %q not in target profile must be REMOVED (set-difference, per audit A2)", k)
		}
	}
	if env["ANTHROPIC_DEFAULT_HAIKU_MODEL"] != profileB.ModelEnv["ANTHROPIC_DEFAULT_HAIKU_MODEL"] {
		t.Fatalf("B model value must switch to B's: %v", env["ANTHROPIC_DEFAULT_HAIKU_MODEL"])
	}
	assertZeroDamage(t, before, after)
}

// 首次改動前備份:備份內容=改動前原文;輪替恰 10 份(設計 §5,審計 C7-v/V1==10)。
func TestApplyBackupAndRetention(t *testing.T) {
	path := settingsFile(t, settingsBState)
	backupDir := filepath.Join(t.TempDir(), "backups")

	if err := switcher.ApplyTo(profileA, path, backupDir); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(backupDir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("first apply must leave exactly one backup: n=%d err=%v", len(entries), err)
	}
	backupBytes, err := os.ReadFile(filepath.Join(backupDir, entries[0].Name()))
	if err != nil {
		t.Fatal(err)
	}
	if string(backupBytes) != settingsBState {
		t.Fatal("backup must be the pre-apply original bytes")
	}

	// 再跑 11 次(內容各異+微延遲,避免整秒時間戳同名),備份數量應輪替為恰 10。
	for i := 0; i < 11; i++ {
		time.Sleep(10 * time.Millisecond) // 確保備份檔名時間戳唯一
		doc := swapKey(settingsBState, fmt.Sprintf("key-round-%d", i))
		if err := os.WriteFile(path, []byte(doc), 0o644); err != nil {
			t.Fatal(err)
		}
		p := profileA
		if i%2 == 0 {
			p = profileB
		}
		if err := switcher.ApplyTo(p, path, backupDir); err != nil {
			t.Fatal(err)
		}
	}
	entries, err = os.ReadDir(backupDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 10 {
		t.Fatalf("backup retention must rotate to exactly 10, got %d", len(entries))
	}
}

// Apply 後不留暫存殘物、結果檔為合法 JSON(原子寫入的可觀察面)。
func TestApplyLeavesNoTempLitter(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.json")
	if err := os.WriteFile(path, []byte(settingsBState), 0o644); err != nil {
		t.Fatal(err)
	}
	backupDir := filepath.Join(dir, "backups")

	if err := switcher.ApplyTo(profileA, path, backupDir); err != nil {
		t.Fatal(err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name() != "settings.json" && e.Name() != "backups" {
			t.Fatalf("temp litter left behind: %s", e.Name())
		}
	}
}

// Apply(profile, path) 便利形:用預設備份目錄(不可指定)——只驗證功能等價+無錯誤。
func TestApplyConvenienceForm(t *testing.T) {
	path := settingsFile(t, settingsBState)
	if err := switcher.Apply(profileA, path); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if envOf(t, readSettings(t, path))["ANTHROPIC_API_KEY"] != profileA.APIKey {
		t.Fatal("convenience Apply must switch the key")
	}
}

// --- Watch:外部變動分類(D7/D8 的地基) ---

func waitEvent(t *testing.T, ch <-chan switcher.WatchEvent) switcher.WatchEvent {
	t.Helper()
	select {
	case ev := <-ch:
		return ev
	case <-time.After(6 * time.Second):
		t.Fatal("no watch event within 6s")
		return switcher.WatchEvent{}
	}
}

func TestWatchClassifiesExternalChanges(t *testing.T) {
	path := settingsFile(t, settingsBState)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ch, err := switcher.Watch(ctx, path, []domain.Profile{profileA, profileB})
	if err != nil {
		t.Fatalf("watch: %v", err)
	}

	// 1) 外部切到認得的 key → ProviderChanged{A}(cc-switch 過渡期手動切換情境)。
	if err := os.WriteFile(path, []byte(swapKey(settingsBState, "key-a-value")), 0o644); err != nil {
		t.Fatal(err)
	}
	ev := waitEvent(t, ch)
	if ev.Kind != switcher.ProviderChanged || ev.ProfileID != "A" {
		t.Fatalf("event 1 = %+v, want ProviderChanged{A}", ev)
	}

	// 2) env 以外的變動(Claude Code 自己改 model)→ UnrelatedChange。
	cur := readSettings(t, path)
	cur["model"] = "sonnet"
	b, _ := json.Marshal(cur)
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
	ev = waitEvent(t, ch)
	if ev.Kind != switcher.UnrelatedChange {
		t.Fatalf("event 2 = %+v, want UnrelatedChange", ev)
	}

	// 3) key 換成未知 → UnknownProvider。
	if err := os.WriteFile(path, []byte(swapKey(settingsBState, "key-unknown")), 0o644); err != nil {
		t.Fatal(err)
	}
	ev = waitEvent(t, ch)
	if ev.Kind != switcher.UnknownProvider {
		t.Fatalf("event 3 = %+v, want UnknownProvider", ev)
	}

	// 4) env 以內、非供應商鍵變動(審計 S5:決策=UnrelatedChange,只看 key)。
	cur = readSettings(t, path)
	env := cur["env"].(map[string]any)
	env["API_TIMEOUT_MS"] = "9999"
	b, _ = json.Marshal(cur)
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
	ev = waitEvent(t, ch)
	if ev.Kind != switcher.UnrelatedChange {
		t.Fatalf("event 4 = %+v, want UnrelatedChange (env-internal non-provider change)", ev)
	}
}

// ctx 取消 → Watch 收斂(通道關閉),goroutine 不外洩的可觀察面。
func TestWatchStopsOnContextCancel(t *testing.T) {
	path := settingsFile(t, settingsBState)
	ctx, cancel := context.WithCancel(context.Background())

	ch, err := switcher.Watch(ctx, path, []domain.Profile{profileA, profileB})
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	select {
	case _, ok := <-ch:
		if ok {
			t.Fatal("channel should close after cancel")
		}
	case <-time.After(6 * time.Second):
		t.Fatal("watch did not stop within 6s of cancel")
	}
}

// --- 測試小工具 ---

func swapKey(doc, key string) string {
	var m map[string]any
	if err := json.Unmarshal([]byte(doc), &m); err != nil {
		panic(err) // fixture 是編譯期常數,不可能失敗
	}
	env := m["env"].(map[string]any)
	env["ANTHROPIC_API_KEY"] = key
	b, err := json.Marshal(m)
	if err != nil {
		panic(err)
	}
	return string(b)
}

func injectSonnetKeys(doc string) string {
	var m map[string]any
	if err := json.Unmarshal([]byte(doc), &m); err != nil {
		panic(err)
	}
	env := m["env"].(map[string]any)
	env["ANTHROPIC_DEFAULT_SONNET_MODEL"] = "glm-5.3-flash"
	env["ANTHROPIC_DEFAULT_SONNET_MODEL_NAME"] = "glm-5.3-flash"
	b, err := json.Marshal(m)
	if err != nil {
		panic(err)
	}
	return string(b)
}
