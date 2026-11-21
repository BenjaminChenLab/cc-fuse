package ui

// White-box marker test — 2026-11-20 RACING/SAVING wake notify master switch
// in the settings window. Spec: docs/blueprints/2026-11-20-racing-wake-notify.md
// decision 5. The page is a static constant, so substring assertions are
// sound (the page_markers_test.go idiom): collect() dropping racingNotify
// would silently switch the notify off on every save (zero-value
// deserialization lesson), so the load() and collect() bindings are pinned
// verbatim, the zh/en dictionaries must both carry the new keys, and the row
// must sit between the quota-mode path row and the proxy row.

import (
	"strings"
	"testing"
)

func TestPageRacingNotifyMarkers(t *testing.T) {
	html := indexHTML
	for _, m := range []string{
		`<span class="switch"><input type="checkbox" id="rnotify"`,     // the switch-style checkbox row (auto/noFleeWknd pattern)
		`document.getElementById("rnotify").checked=!!c.racingNotify;`, // load() binding, verbatim per spec
		`racingNotify:document.getElementById("rnotify").checked,`,     // collect() binding, verbatim per spec
		`data-i18n="lblRacingNotify"`,                                  // the label carries the bilingual key
		`data-i18n-title="tipRacingNotify"`,                            // label + checkbox carry the tip key
	} {
		if !strings.Contains(html, m) {
			t.Fatalf("indexHTML is missing the marker %q", m)
		}
	}
	// Bilingual entries: both the zh and the en dictionary must define them.
	for _, k := range []string{"lblRacingNotify:", "tipRacingNotify:"} {
		if n := strings.Count(html, k); n < 2 {
			t.Fatalf("%s must appear once in zh and once in en, got %d", k, n)
		}
	}
	// Both sides of the binding must exist (load + collect).
	if n := strings.Count(html, "racingNotify"); n < 2 {
		t.Fatalf("racingNotify must appear in load and collect, got %d", n)
	}
	// Position: the new row sits after the quota-mode path row, before proxy.
	qm := strings.Index(html, `id="qm"`)
	rn := strings.Index(html, `id="rnotify"`)
	px := strings.Index(html, `id="proxy"`)
	if qm < 0 || rn < 0 || px < 0 || !(qm < rn && rn < px) {
		t.Fatalf("rnotify row must sit between qm (%d) and proxy (%d), got %d", qm, px, rn)
	}
}

// TestPageWatchersFilePassthroughMarkers: watchersFile deliberately has NO
// settings row (JSON-only, spec decision 4), so collect() must carry the
// loaded server value through a module variable (the uilang:lang idiom).
// collect() dropping the field makes the first save post a zero-value
// Config — config.json gains an explicit "" that Load's default-layering
// cannot heal, the live cell carries "", and Spawn("") silently no-ops
// through the missing-file gate forever while the master switch shows on
// (the enforceDailyCeiling zero-value lesson; TOP audit finding F1).
// load() stashes the server value, collect() passes it through — both
// pinned verbatim, substring-sound on the static page constant.
func TestPageWatchersFilePassthroughMarkers(t *testing.T) {
	html := indexHTML
	for _, m := range []string{
		`watchersFile=c.watchersFile`, // load() stashes the server value into the module var
		`watchersFile:watchersFile,`,  // collect() passes the module var through (must not be dropped)
	} {
		if !strings.Contains(html, m) {
			t.Fatalf("indexHTML is missing the marker %q", m)
		}
	}
	if n := strings.Count(html, "watchersFile"); n < 2 {
		t.Fatalf("watchersFile must be carried in load and collect, got %d", n)
	}
}
