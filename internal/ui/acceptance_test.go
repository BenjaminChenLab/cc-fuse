package ui_test

// 驗收測試(黑箱)——cc-fuse T03:設定窗的 JSON API 層(不打 WebView)。
// 契約:NewServer(cfg, onSave) 回傳 http.Handler;GET /api/config 讀現值;
// POST /api/config 合法 JSON → 呼叫 onSave;壞 JSON → 400 且不呼叫 onSave。

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"cc-fuse/internal/config"
	"cc-fuse/internal/ui"
)

func TestConfigAPIRoundTrip(t *testing.T) {
	cfg := config.Default()
	cfg.WarnThreshold = 77
	var saved *config.Config
	saveCalls := 0 // 呼叫計數:「沒被呼叫」與「被呼叫但值相同」只有計數器分得出來
	onSave := func(c config.Config) error {
		parsed := c
		saved = &parsed
		saveCalls++
		return nil
	}
	srv := httptest.NewServer(ui.NewServer(&cfg, onSave))
	defer srv.Close()

	// GET 回現值(含未存檔的記憶體態)。
	resp, err := http.Get(srv.URL + "/api/config")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET status = %d", resp.StatusCode)
	}
	var got config.Config
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("GET body not valid config JSON: %v", err)
	}
	if got.WarnThreshold != 77 {
		t.Fatalf("GET returned stale config: %+v", got)
	}

	// POST 合法 JSON → onSave 收到解析後的設定。
	post := cfg
	post.WarnThreshold = 66
	body, _ := json.Marshal(post)
	resp2, err := http.Post(srv.URL+"/api/config", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("POST status = %d", resp2.StatusCode)
	}
	if saved == nil || saved.WarnThreshold != 66 {
		t.Fatalf("onSave must receive parsed config: %+v", saved)
	}

	// POST 壞 JSON → 400,onSave 不被呼叫。
	resp3, err := http.Post(srv.URL+"/api/config", "application/json", bytes.NewReader([]byte("{broken")))
	if err != nil {
		t.Fatal(err)
	}
	resp3.Body.Close()
	if resp3.StatusCode != http.StatusBadRequest {
		t.Fatalf("bad JSON must 400: %d", resp3.StatusCode)
	}
	if saveCalls != 1 {
		t.Fatalf("bad JSON must not call onSave (calls=%d)", saveCalls)
	}
	if saved == nil || saved.WarnThreshold != 66 {
		t.Fatalf("bad JSON must not change saved state: %+v", saved)
	}

	// 其他路徑 → 404。
	resp4, _ := http.Get(srv.URL + "/api/nope")
	resp4.Body.Close()
	if resp4.StatusCode != http.StatusNotFound {
		t.Fatalf("unknown path must 404: %d", resp4.StatusCode)
	}
}
