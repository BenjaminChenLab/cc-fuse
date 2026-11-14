package store_test

// 驗收測試(黑箱)——cc-fuse T01:跨重啟狀態存檔。
// 契約:Save/Load 往返;缺檔=空 State 無錯(冷啟);壞檔=空 State+錯誤、不 panic。

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"cc-fuse/internal/store"
)

// Budget payload 往返:任意 JSON(含巢狀)完整保留(T02 的 BudgetState 藉此持久化)。
func TestBudgetRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")

	in := store.State{
		Budget: json.RawMessage(`{"weekAnchor":"2026-11-20T15:00:00+08:00","dayStartWeekly":25.5,"ceiling":45.5}`),
	}
	if err := store.SaveState(path, in); err != nil {
		t.Fatalf("save: %v", err)
	}
	out, err := store.LoadState(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	var inMap, outMap map[string]any
	if err := json.Unmarshal(in.Budget, &inMap); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(out.Budget, &outMap); err != nil {
		t.Fatalf("loaded budget is not valid JSON: %v", err)
	}
	// 以序列化位元組比對(審計 T3:any 直接比較對巢狀結構會 panic)。
	inBytes, _ := json.Marshal(inMap)
	outBytes, _ := json.Marshal(outMap)
	if string(inBytes) != string(outBytes) {
		t.Fatalf("budget round-trip mismatch:\nwant %s\ngot  %s", inBytes, outBytes)
	}
}

// 缺檔 → 空 State、無錯誤(冷啟:呼叫端以當下值為基準)。
func TestLoadMissingFile(t *testing.T) {
	out, err := store.LoadState(filepath.Join(t.TempDir(), "absent.json"))
	if err != nil {
		t.Fatalf("missing file must not error: %v", err)
	}
	if len(out.Budget) != 0 {
		t.Fatalf("missing file must give empty state: %s", out.Budget)
	}
}

// 壞檔 → 空 State + 明確錯誤,不 panic(呼叫端決定重來)。
func TestLoadCorruptFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	if err := osWrite(path, "{broken"); err != nil {
		t.Fatal(err)
	}
	out, err := store.LoadState(path)
	if err == nil {
		t.Fatal("corrupt state must error")
	}
	if len(out.Budget) != 0 {
		t.Fatalf("corrupt load must give empty state: %s", out.Budget)
	}
}

// 空 State 的 Save/Load 也可往返。
func TestEmptyStateRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	if err := store.SaveState(path, store.State{}); err != nil {
		t.Fatalf("save empty: %v", err)
	}
	out, err := store.LoadState(path)
	if err != nil {
		t.Fatalf("load empty: %v", err)
	}
	if len(out.Budget) != 0 {
		t.Fatalf("empty state must stay empty: %s", out.Budget)
	}
}

func osWrite(path, s string) error {
	return osWriteImpl(path, s)
}
