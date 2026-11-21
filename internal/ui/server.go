// Package ui is the settings window's loopback JSON API (design §8).
//
// The settings page (embedded HTML, opened by the tray 設定 item) talks to
// this server on 127.0.0.1 over a temporary port: GET /api/config returns
// the live in-memory config, POST /api/config validates and hands the parsed
// value to onSave (which the app wires to config.Save + live apply), and
// POST /api/import runs the cc-switch importer. Unknown paths are 404, bad
// JSON never reaches onSave, and the handler is safe for concurrent use —
// the Loop may read/write the same *Config between requests.
package ui

import (
	"encoding/json"
	"io"
	"net/http"
	"sync"
	"time"

	"cc-fuse/internal/config"
	"cc-fuse/internal/domain"
	"cc-fuse/internal/importer"
)

// onSaveGuard caps how large a request body we accept: the config JSON is
// a few KB; anything bigger is hostile or broken.
const maxBodyBytes = 1 << 20

// Server serves the settings API for one live config pointer. NewServer
// returns it as a plain http.Handler so the app can host it on any
// loopback listener. The status/switch/autopilot functions power the
// switcher screen (all nil in the plain NewServer — those routes answer
// 501 then).
type Server struct {
	mu        sync.Mutex
	cfg       *config.Config
	onSave    func(config.Config) error
	status    func() StatusView
	switcher  func(target string)
	autopilot func(on bool)
}

// StatusView is the switcher-screen payload: who is live, whether the
// autopilot is on, the health flags the page surfaces, and each slot's
// latest quota snapshot. Profiles carries the A/B definitions so the page
// can render names and URLs. CeilingToday + EnforceDailyCeiling feed B's
// ceiling chip and its 強制/不強制 annotation (2026-11-16 plan R1/R2);
// Until carries the next cut-off instant so the chip can prefix
// "Until …" (2026-11-19 D3a).
type StatusView struct {
	Current             string                `json:"current"`
	Autopilot           bool                  `json:"autopilot"`
	Stale               bool                  `json:"stale"`
	KeyDead             bool                  `json:"keyDead"`
	ParseFailing        bool                  `json:"parseFailing"`
	A                   *domain.QuotaSnapshot `json:"a"`
	B                   *domain.QuotaSnapshot `json:"b"`
	Profiles            []domain.Profile      `json:"profiles"`
	CeilingToday        *float64              `json:"ceilingToday"`        // B's pacing ceiling for today (null = not settled)
	EnforceDailyCeiling bool                  `json:"enforceDailyCeiling"` // ceiling-forced-switch toggle state
	Until               *time.Time            `json:"until,omitempty"`     // next settlement/cut-off instant (BudgetState.DayBoundary; nil → key omitted)
}

// NewServer builds the settings API handler over cfg (the app's live config
// cell) and onSave (called with every successfully parsed POST; returning an
// error rejects the save with HTTP 500). GET always reflects the current
// *cfg contents, so unsaved in-memory edits are visible immediately.
func NewServer(cfg *config.Config, onSave func(config.Config) error) http.Handler {
	return &Server{cfg: cfg, onSave: onSave}
}

// NewServerWithStatus additionally wires the switcher screen: status feeds
// the provider cards' quota numbers, switcher executes a manual P1 switch,
// autopilot flips the Loop's on/off flag (2026-11-16 plan R5). All are
// app-level closures the main wiring adapts from *app.Loop.
func NewServerWithStatus(cfg *config.Config, onSave func(config.Config) error,
	status func() StatusView, switcher func(target string), autopilot func(on bool)) http.Handler {
	return &Server{cfg: cfg, onSave: onSave, status: status, switcher: switcher, autopilot: autopilot}
}

// ServeHTTP routes by exact path; anything unlisted is 404 so probe paths
// can never fall through to the settings page.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/api/config":
		switch r.Method {
		case http.MethodGet:
			s.getConfig(w)
		case http.MethodPost:
			s.postConfig(w, r)
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	case "/api/status":
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if s.status == nil {
			http.Error(w, "status not wired in this build", http.StatusNotImplemented)
			return
		}
		writeJSON(w, http.StatusOK, s.status())
	case "/api/switch":
		s.postSwitch(w, r)
	case "/api/uilang":
		s.postUILang(w, r)
	case "/api/autopilot":
		s.postAutopilot(w, r)
	case "/api/import":
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		s.postImport(w, r)
	case "/":
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = io.WriteString(w, indexHTML)
	default:
		http.NotFound(w, r)
	}
}

// getConfig writes the current config as JSON (200).
func (s *Server) getConfig(w http.ResponseWriter) {
	s.mu.Lock()
	c := *s.cfg
	s.mu.Unlock()
	writeJSON(w, http.StatusOK, c)
}

// postConfig parses the POSTed config, hands it to onSave and only then
// adopts it into the live cell. Bad JSON → 400 without touching onSave;
// onSave failure → 500 with the reason.
func (s *Server) postConfig(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, maxBodyBytes))
	if err != nil {
		http.Error(w, "read body: "+err.Error(), http.StatusBadRequest)
		return
	}
	var c config.Config
	if err := json.Unmarshal(body, &c); err != nil {
		http.Error(w, "not valid config JSON: "+err.Error(), http.StatusBadRequest)
		return
	}
	if s.onSave != nil {
		if err := s.onSave(c); err != nil {
			http.Error(w, "save failed: "+err.Error(), http.StatusInternalServerError)
			return
		}
	}
	s.mu.Lock()
	*s.cfg = c
	s.mu.Unlock()
	writeJSON(w, http.StatusOK, c)
}

// switchReq is the POST /api/switch body: which slot to switch to (P1
// manual — the app's switcher closure wakes the loop immediately).
type switchReq struct {
	Target string `json:"target"`
}

// postSwitch executes a manual switch to "A" or "B" and answers 200; bad
// JSON, an unknown slot or an unwired switcher never reach production code.
func (s *Server) postSwitch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.switcher == nil {
		http.Error(w, "switch not wired in this build", http.StatusNotImplemented)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, maxBodyBytes))
	if err != nil {
		http.Error(w, "read body: "+err.Error(), http.StatusBadRequest)
		return
	}
	var req switchReq
	if err := json.Unmarshal(body, &req); err != nil {
		http.Error(w, "not valid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}
	if req.Target != "A" && req.Target != "B" {
		http.Error(w, "target must be A or B", http.StatusBadRequest)
		return
	}
	s.switcher(req.Target)
	writeJSON(w, http.StatusOK, struct {
		Ok     bool   `json:"ok"`
		Target string `json:"target"`
	}{Ok: true, Target: req.Target})
}

// uilangReq is the POST /api/uilang body: the settings-window language the
// page's #lang button just switched to (2026-11-19 — the choice persists in
// config.json because localStorage dies with the temporary port).
type uilangReq struct {
	Lang string `json:"lang"`
}

// postUILang validates the language value, hands it to onSave (so it lands
// in config.json immediately, no Save press needed) and adopts it into the
// live cell. Only "", "en" and "zh" are accepted; bad JSON, an unknown value
// or an unwired onSave never touch production state — same conventions as
// postConfig.
func (s *Server) postUILang(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, maxBodyBytes))
	if err != nil {
		http.Error(w, "read body: "+err.Error(), http.StatusBadRequest)
		return
	}
	var req uilangReq
	if err := json.Unmarshal(body, &req); err != nil {
		http.Error(w, "not valid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}
	if req.Lang != "" && req.Lang != "en" && req.Lang != "zh" {
		http.Error(w, "lang must be empty, en or zh", http.StatusBadRequest)
		return
	}
	s.mu.Lock()
	c := *s.cfg
	c.UILang = req.Lang
	s.mu.Unlock()
	if s.onSave != nil {
		if err := s.onSave(c); err != nil {
			http.Error(w, "save failed: "+err.Error(), http.StatusInternalServerError)
			return
		}
	}
	s.mu.Lock()
	s.cfg.UILang = req.Lang
	s.mu.Unlock()
	writeJSON(w, http.StatusOK, struct {
		Ok   bool   `json:"ok"`
		Lang string `json:"lang"`
	}{Ok: true, Lang: req.Lang})
}

// autopilotReq is the POST /api/autopilot body: the requested autopilot
// state (2026-11-16 plan R5 — the page's 自動切換 button).
type autopilotReq struct {
	On bool `json:"on"`
}

// postAutopilot flips the autopilot via the wired closure and answers 200;
// bad JSON, an unwired closure (501) or a non-POST method (405) never reach
// production code — same conventions as postSwitch.
func (s *Server) postAutopilot(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if s.autopilot == nil {
		http.Error(w, "autopilot not wired in this build", http.StatusNotImplemented)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, maxBodyBytes))
	if err != nil {
		http.Error(w, "read body: "+err.Error(), http.StatusBadRequest)
		return
	}
	var req autopilotReq
	if err := json.Unmarshal(body, &req); err != nil {
		http.Error(w, "not valid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}
	s.autopilot(req.On)
	writeJSON(w, http.StatusOK, struct {
		Ok bool `json:"ok"`
		On bool `json:"on"`
	}{Ok: true, On: req.On})
}

// importReq is the POST /api/import body: where the cc-switch db lives.
type importReq struct {
	DBPath string `json:"dbPath"`
}

// postImport runs the cc-switch importer and returns the profiles it found,
// so the settings page can offer one-click adoption. Importer failures (bad
// db, missing table, absent file) surface as 400/500 text, never a crash.
func (s *Server) postImport(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, maxBodyBytes))
	if err != nil {
		http.Error(w, "read body: "+err.Error(), http.StatusBadRequest)
		return
	}
	var req importReq
	if err := json.Unmarshal(body, &req); err != nil {
		http.Error(w, "not valid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}
	profiles, err := importer.ReadCcSwitch(req.DBPath)
	if err != nil {
		http.Error(w, "import failed: "+err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, http.StatusOK, struct {
		Profiles []domain.Profile `json:"profiles"`
	}{Profiles: profiles})
}

// writeJSON writes v as a JSON response body.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}
