package ui_test

// 驗收測試(黑箱)——2026-11-19 介面語言持久化:設定窗每次開啟綁臨時埠,
// localStorage 以 origin(含埠)為鑰,語言選擇每次開窗都歸零。修法:選擇
// 改存 config.json 的 uilang 欄位;#lang 按鈕 POST /api/uilang 即時落盤,
// 頁面載入時以 /api/config 的 uilang 糾正顯示語言。
// 規格:docs/blueprints/2026-11-19-uilang-persist.md。

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"cc-fuse/internal/config"
	"cc-fuse/internal/ui"
)

func TestUILangEndpoint(t *testing.T) {
	cfg := config.Default()
	var saved []string // 每次 onSave 收到的 uilang 值(依序錄案)
	onSave := func(c config.Config) error {
		saved = append(saved, c.UILang)
		return nil
	}
	srv := httptest.NewServer(ui.NewServer(&cfg, onSave))
	defer srv.Close()

	// 合法值 zh → 200、live cell 更新、onSave 收到。
	if resp, err := http.Post(srv.URL+"/api/uilang", "application/json",
		bytes.NewReader([]byte(`{"lang":"zh"}`))); err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("POST uilang zh must 200, got %v/%d", err, respStatusCode(resp))
	}
	if cfg.UILang != "zh" {
		t.Fatalf("live cell 必須更新為 zh, got %q", cfg.UILang)
	}
	if len(saved) != 1 || saved[0] != "zh" {
		t.Fatalf("onSave 必須收到 zh, got %v", saved)
	}

	// 合法值 en → 同規則。
	if resp, err := http.Post(srv.URL+"/api/uilang", "application/json",
		bytes.NewReader([]byte(`{"lang":"en"}`))); err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("POST uilang en must 200, got %v/%d", err, respStatusCode(resp))
	}
	if cfg.UILang != "en" {
		t.Fatalf("live cell must become en, got %q", cfg.UILang)
	}
	if len(saved) != 2 || saved[1] != "en" {
		t.Fatalf("onSave must receive en on the second call, got %v", saved)
	}

	// 非法值 → 400、cell 與 onSave 都不動。
	if resp, err := http.Post(srv.URL+"/api/uilang", "application/json",
		bytes.NewReader([]byte(`{"lang":"xx"}`))); err != nil || resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("invalid lang must 400, got %v/%d", err, respStatusCode(resp))
	}
	if cfg.UILang != "en" {
		t.Fatalf("invalid lang 不得動 cell, got %q", cfg.UILang)
	}
	if len(saved) != 2 {
		t.Fatalf("invalid lang 不得呼叫 onSave (calls=%d)", len(saved))
	}

	// 壞 JSON → 400、不動 onSave。
	if resp, err := http.Post(srv.URL+"/api/uilang", "application/json",
		bytes.NewReader([]byte("{broken"))); err != nil || resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("bad JSON must 400, got %v/%d", err, respStatusCode(resp))
	}
	if len(saved) != 2 {
		t.Fatalf("bad JSON 不得呼叫 onSave (calls=%d)", len(saved))
	}

	// GET /api/config 反映現值(頁面載入時據此糾正顯示語言)。
	resp, err := http.Get(srv.URL + "/api/config")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var got config.Config
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.UILang != "en" {
		t.Fatalf("GET /api/config must carry uilang=en, got %q", got.UILang)
	}

	// D-L2:GET on /api/uilang → 405(mutant dropping the method check answers
	// 400 instead and must fail here).
	if resp5, err := http.Get(srv.URL + "/api/uilang"); err != nil || resp5.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("GET /api/uilang must 405, got %v/%d", err, respStatusCode(resp5))
	}

	// D-L1/D-L2:the legal "" value (follow-browser default) → 200, cell
	// cleared, onSave receives "".
	if resp6, err := http.Post(srv.URL+"/api/uilang", "application/json",
		bytes.NewReader([]byte(`{"lang":""}`))); err != nil || resp6.StatusCode != http.StatusOK {
		t.Fatalf("POST uilang \"\" must 200, got %v/%d", err, respStatusCode(resp6))
	}
	if cfg.UILang != "" {
		t.Fatalf("live cell must clear to \"\", got %q", cfg.UILang)
	}
	if len(saved) != 3 || saved[2] != "" {
		t.Fatalf("onSave must receive \"\" on the third call, got %v", saved)
	}

	// D-L2:onSave failure → 500 and the live cell stays untouched (adoption
	// happens only after a successful save).
	cfg2 := config.Default()
	failSave := func(c config.Config) error { return errOops }
	srv2 := httptest.NewServer(ui.NewServer(&cfg2, failSave))
	defer srv2.Close()
	if resp7, err := http.Post(srv2.URL+"/api/uilang", "application/json",
		bytes.NewReader([]byte(`{"lang":"zh"}`))); err != nil || resp7.StatusCode != http.StatusInternalServerError {
		t.Fatalf("failing onSave must 500, got %v/%d", err, respStatusCode(resp7))
	}
	if cfg2.UILang != "" {
		t.Fatalf("failed save must not adopt into the live cell, got %q", cfg2.UILang)
	}
}

// errOops is the canned onSave failure for the 500-path test.
var errOops = errorString("oops")

// errorString is a tiny static error (avoids importing errors for one value).
type errorString string

func (e errorString) Error() string { return string(e) }

func respStatusCode(resp *http.Response) int {
	if resp == nil {
		return -1
	}
	resp.Body.Close()
	return resp.StatusCode
}
