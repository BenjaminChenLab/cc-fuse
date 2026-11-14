package ui_test

// 白箱單元測試:切換卡片畫面的兩條新路由——GET /api/status(狀態檢視)
// 與 POST /api/switch(P1 手動切換)。未接線(NewServer)時 501;接線後
// 狀態原樣回、切換只認 A/B。

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"cc-fuse/internal/config"
	"cc-fuse/internal/domain"
	"cc-fuse/internal/ui"
)

func TestStatusAndSwitchRoutes(t *testing.T) {
	cfg := config.Default()
	view := ui.StatusView{
		Current: "B", Autopilot: true,
		A:        &domain.QuotaSnapshot{ProfileID: "A", FiveHourPct: ptrF(10)},
		B:        &domain.QuotaSnapshot{ProfileID: "B", FiveHourPct: ptrF(90)},
		Profiles: []domain.Profile{{ID: "A", Name: "team"}, {ID: "B", Name: "mine"}},
	}
	var switched string
	srv := httptest.NewServer(ui.NewServerWithStatus(&cfg, nil,
		func() ui.StatusView { return view },
		func(target string) { switched = target }))
	defer srv.Close()

	// GET /api/status:檢視原樣(當前槽位、快照數字、帳號定義)。
	resp, err := http.Get(srv.URL + "/api/status")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var got ui.StatusView
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.Current != "B" || got.A == nil || got.A.FiveHourPct == nil || *got.A.FiveHourPct != 10 {
		t.Fatalf("status payload broken: %+v", got)
	}

	// POST /api/switch:合法槽位 → 200 且切換函式收到目標。
	resp2, err := http.Post(srv.URL+"/api/switch", "application/json",
		bytes.NewReader([]byte(`{"target":"A"}`)))
	if err != nil {
		t.Fatal(err)
	}
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK || switched != "A" {
		t.Fatalf("switch: code=%d switched=%q", resp2.StatusCode, switched)
	}

	// 非法槽位與壞 JSON → 400,切換函式不被呼叫。
	switched = ""
	resp3, err := http.Post(srv.URL+"/api/switch", "application/json",
		bytes.NewReader([]byte(`{"target":"C"}`)))
	if err != nil {
		t.Fatal(err)
	}
	resp3.Body.Close()
	if resp3.StatusCode != http.StatusBadRequest || switched != "" {
		t.Fatalf("bad slot must 400 without switching: code=%d switched=%q", resp3.StatusCode, switched)
	}
	resp4, err := http.Post(srv.URL+"/api/switch", "application/json", bytes.NewReader([]byte("{broken")))
	if err != nil {
		t.Fatal(err)
	}
	resp4.Body.Close()
	if resp4.StatusCode != http.StatusBadRequest || switched != "" {
		t.Fatalf("bad JSON must 400: code=%d", resp4.StatusCode)
	}
}

// 未接線(plain NewServer):狀態與切換都回 501,不得 panic。
func TestStatusRoutesUnwired(t *testing.T) {
	cfg := config.Default()
	srv := httptest.NewServer(ui.NewServer(&cfg, nil))
	defer srv.Close()
	if resp, err := http.Get(srv.URL + "/api/status"); err != nil || resp.StatusCode != http.StatusNotImplemented {
		t.Fatalf("unwired status: code=%d err=%v", resp.StatusCode, err)
	} else {
		resp.Body.Close()
	}
	resp, err := http.Post(srv.URL+"/api/switch", "application/json", bytes.NewReader([]byte(`{"target":"A"}`)))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotImplemented {
		t.Fatalf("unwired switch: code=%d", resp.StatusCode)
	}
}

func ptrF(v float64) *float64 { return &v }
