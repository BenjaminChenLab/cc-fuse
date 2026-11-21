package ui

// White-box marker test — 2026-11-21 RESET notify master switch in the
// settings window. Spec: docs/blueprints/2026-11-21-reset-notify.md decision
// 10. The page is a static constant, so substring assertions are sound (the
// racingnotify_marker_test.go idiom): collect() dropping resetNotify would
// silently switch the notify off on every save (zero-value deserialization
// lesson), so the load() and collect() bindings are pinned verbatim, the
// zh/en dictionaries must both carry the new label and tip keys with the
// spec'd texts, and the row must sit after the racingNotify row, before the
// proxy row.

import (
	"strings"
	"testing"
)

func TestPageResetNotifyMarkers(t *testing.T) {
	html := indexHTML
	for _, m := range []string{
		`<span class="switch"><input type="checkbox" id="resetnotify" data-i18n-title="tipResetNotify"`, // the switch-style checkbox row (rnotify/auto pattern) with the tip key on the input
		`document.getElementById("resetnotify").checked=!!c.resetNotify;`,                               // load() binding, verbatim per spec
		`resetNotify:document.getElementById("resetnotify").checked,`,                                   // collect() binding, verbatim per spec
		`data-i18n="lblResetNotify"`,       // the label carries the bilingual key
		`data-i18n-title="tipResetNotify"`, // label + checkbox carry the tip key
	} {
		if !strings.Contains(html, m) {
			t.Fatalf("indexHTML is missing the marker %q", m)
		}
	}
	// Bilingual entries: both the zh and the en dictionary must define them.
	for _, k := range []string{"lblResetNotify:", "tipResetNotify:"} {
		if n := strings.Count(html, k); n < 2 {
			t.Fatalf("%s must appear once in zh and once in en, got %d", k, n)
		}
	}
	// The spec'd label texts, exact: zh「5h 重置通知」/ en "5h-reset retry notify".
	if !strings.Contains(html, `lblResetNotify:"5h 重置通知"`) {
		t.Fatalf(`the zh dictionary must carry lblResetNotify:"5h 重置通知"`)
	}
	if !strings.Contains(html, `lblResetNotify:"5h-reset retry notify"`) {
		t.Fatalf(`the en dictionary must carry lblResetNotify:"5h-reset retry notify"`)
	}
	// Both sides of the binding must exist (load + collect).
	if n := strings.Count(html, "resetNotify"); n < 2 {
		t.Fatalf("resetNotify must appear in load and collect, got %d", n)
	}
	// Static title fallback (decision 10: 靜態 fallback 文字與詞典同步縮短版).
	// The row must carry plain title=" attributes on BOTH the label and the
	// input (the pre-i18n hover text for a fresh page), and each fallback
	// must be a SHORTENED variant of the zh dictionary tip — shorter, same
	// meaning. A full-copy fallback (or a missing one) fails the shortening
	// pin. The row region runs from the label's i18n key to the row's own
	// closing </div>, so the next row's title never leaks into the count.
	rowStart := strings.Index(html, `data-i18n="lblResetNotify"`)
	if rowStart < 0 {
		t.Fatalf("cannot locate the resetnotify row (lblResetNotify missing)")
	}
	rowLen := strings.Index(html[rowStart:], "</div>")
	if rowLen < 0 {
		t.Fatalf("cannot locate the end of the resetnotify row (no </div>)")
	}
	row := html[rowStart : rowStart+rowLen]
	// Neutralize the i18n BINDING attribute first: its name ends in
	// `title="` and would otherwise count as a static fallback. The binding
	// itself is already pinned by the data-i18n-title markers above.
	row = strings.ReplaceAll(row, `data-i18n-title=`, `data-i18n-binding=`)
	const tipKey = `tipResetNotify:`
	dictStart := strings.Index(html, tipKey)
	if dictStart < 0 {
		t.Fatalf("the zh dictionary is missing %s", tipKey)
	}
	dictTip := html[dictStart+len(tipKey):]
	dictTip = strings.TrimPrefix(dictTip, `"`)
	if end := strings.Index(dictTip, `"`); end >= 0 {
		dictTip = dictTip[:end]
	}
	if dictTip == "" {
		t.Fatalf("the zh tipResetNotify value is empty")
	}
	var fallbacks []string
	for idx := 0; idx < len(row); {
		at := strings.Index(row[idx:], `title="`)
		if at < 0 {
			break
		}
		start := idx + at + len(`title="`)
		end := strings.Index(row[start:], `"`)
		if end < 0 {
			t.Fatalf("unterminated static title fallback in the resetnotify row")
		}
		fallbacks = append(fallbacks, row[start:start+end])
		idx = start + end + 1
	}
	if len(fallbacks) != 2 {
		t.Fatalf("the resetnotify row must carry exactly 2 static title fallbacks (label + input), got %d", len(fallbacks))
	}
	for i, fb := range fallbacks {
		if fb == "" {
			t.Fatalf("static title fallback #%d is empty", i+1)
		}
		if len(fb) >= len(dictTip) {
			t.Fatalf("static title fallback #%d must be the shortened variant of the zh tip (decision 10), got %d bytes vs tip %d bytes: %q", i+1, len(fb), len(dictTip), fb)
		}
	}
	// Position: the new row sits after the racingNotify row, before proxy.
	rn := strings.Index(html, `id="rnotify"`)
	rs := strings.Index(html, `id="resetnotify"`)
	px := strings.Index(html, `id="proxy"`)
	if rn < 0 || rs < 0 || px < 0 || !(rn < rs && rs < px) {
		t.Fatalf("resetnotify row must sit between rnotify (%d) and proxy (%d), got %d", rn, px, rs)
	}
}
