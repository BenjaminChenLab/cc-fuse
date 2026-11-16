package ui_test

// 驗收測試(黑箱)——2026-11-16 計畫 R1/R2/R5:StatusView 帶天花板與開關狀態、
// POST /api/autopilot(自動切換按鈕)。規格:docs/blueprints/2026-11-16-cc-fuse-budget-ui.md
// §2.2/§2.6(路由規矩與既有 status/switch 一致:未接線 501、壞 JSON 400、非 POST 405)。

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"cc-fuse/internal/config"
	"cc-fuse/internal/domain"
	"cc-fuse/internal/ui"
)

// StatusView 新欄位:天花板(可 nil)與開關狀態,雙向原樣往返;鍵必須永遠
// 在場(Phase-3 審查突變4:加 omitempty 後 false 整鍵消失,struct 解碼零值
// 一樣是 false,舊斷言恆過 — 只有查原始 JSON 的鍵在場才有鑑別力)。
func TestStatusViewBudgetFields(t *testing.T) {
	cfg := config.Default()
	view := ui.StatusView{
		Current: "B", Autopilot: true,
		A:                   &domain.QuotaSnapshot{ProfileID: "A"},
		B:                   &domain.QuotaSnapshot{ProfileID: "B"},
		CeilingToday:        ptrF(33.3),
		EnforceDailyCeiling: false,
	}
	srv := httptest.NewServer(ui.NewServerWithStatus(&cfg, nil,
		func() ui.StatusView { return view },
		func(string) {}, func(bool) {}))
	defer srv.Close()

	for _, enforce := range []bool{true, false} {
		view.EnforceDailyCeiling = enforce
		resp, err := http.Get(srv.URL + "/api/status")
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		var raw map[string]any
		if err := json.Unmarshal(body, &raw); err != nil {
			t.Fatal(err)
		}
		if _, present := raw["enforceDailyCeiling"]; !present {
			t.Fatalf("enforce=%v:鍵必須在場(false 也不得省略)", enforce)
		}
		var got ui.StatusView
		if err := json.Unmarshal(body, &got); err != nil {
			t.Fatal(err)
		}
		if got.EnforceDailyCeiling != enforce {
			t.Fatalf("enforceDailyCeiling 往返:want %v got %v", enforce, got.EnforceDailyCeiling)
		}
		if enforce && (got.CeilingToday == nil || *got.CeilingToday != 33.3) {
			t.Fatalf("ceilingToday 往返:%+v", got.CeilingToday)
		}
	}
}

// R5:POST /api/autopilot — 合法呼叫抵達閉包;壞 JSON 400;非 POST 405;未接線 501。
func TestAutopilotRoute(t *testing.T) {
	cfg := config.Default()
	var calls []bool
	srv := httptest.NewServer(ui.NewServerWithStatus(&cfg, nil,
		func() ui.StatusView { return ui.StatusView{} },
		func(string) {}, func(on bool) { calls = append(calls, on) }))
	defer srv.Close()

	resp, err := http.Post(srv.URL+"/api/autopilot", "application/json",
		bytes.NewReader([]byte(`{"on":false}`)))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || len(calls) != 1 || calls[0] != false {
		t.Fatalf("合法關閉呼叫:code=%d calls=%v", resp.StatusCode, calls)
	}

	resp2, err := http.Post(srv.URL+"/api/autopilot", "application/json",
		bytes.NewReader([]byte("{broken")))
	if err != nil {
		t.Fatal(err)
	}
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusBadRequest || len(calls) != 1 {
		t.Fatalf("壞 JSON 必 400 且不觸碰閉包:code=%d", resp2.StatusCode)
	}

	resp3, err := http.Get(srv.URL + "/api/autopilot")
	if err != nil {
		t.Fatal(err)
	}
	resp3.Body.Close()
	if resp3.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("非 POST 必 405:code=%d", resp3.StatusCode)
	}

	unwired := httptest.NewServer(ui.NewServer(&cfg, nil))
	defer unwired.Close()
	resp4, err := http.Post(unwired.URL+"/api/autopilot", "application/json",
		bytes.NewReader([]byte(`{"on":true}`)))
	if err != nil {
		t.Fatal(err)
	}
	resp4.Body.Close()
	if resp4.StatusCode != http.StatusNotImplemented {
		t.Fatalf("未接線必 501:code=%d", resp4.StatusCode)
	}
}
