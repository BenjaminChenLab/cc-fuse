package app_test

// Unit test for the wake-notify branch the acceptance file (wakenotify_test.go)
// does not reach: a Loop wired WITHOUT a quota flipper (nil Qm) must skip the
// flip AND the wake — no mode write happened, so there is nothing to notify
// about; the settings switch itself still completes.

import (
	"context"
	"testing"

	"cc-fuse/internal/app"
	"cc-fuse/internal/config"
)

// TestTickNilFlipperSkipsWake: no flip attempted → no wake, even with the
// seam wired and the master switch on.
func TestTickNilFlipperSkipsWake(t *testing.T) {
	mon := &fakeMonitor{a: fresh("A", f(95), f(10), base), b: fresh("B", f(5), f(12), base)}
	sw, nt := &fakeSwitcher{}, &fakeNotifier{}
	wk := &fakeWaker{}
	l := &app.Loop{
		Mon: mon, Sw: sw, Qm: nil, Not: nt,
		Cfg:          config.Default(),
		Profiles:     profiles(),
		StatePath:    "",
		SettingsPath: "C:\\nonexistent\\settings.json",
		Autopilot:    true,
		Current:      "A",
	}
	l.Wake = app.WakeNotifierFunc(wk.Wake)

	r := l.Tick(context.Background(), base)
	if !r.Switched {
		t.Fatalf("a nil flipper must not block the switch: %+v", r)
	}
	if len(wk.calls) != 0 {
		t.Fatalf("no flip attempted → no wake, got %v", wk.calls)
	}
}
