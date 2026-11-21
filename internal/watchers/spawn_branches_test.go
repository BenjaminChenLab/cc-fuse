package watchers

// Unit tests for the Spawn branches the acceptance file (spawn_test.go) does
// not reach: non-ErrNotExist registry read errors, user-home resolution
// failure, and a failed process start. Each must surface an error with ZERO
// spawns — the app loop logs it and the completed switch is never touched.

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestSpawnReadErrorZeroSpawns: a registry path that cannot be read as a file
// (a directory sits at the path) must error out without spawning — an
// unreadable registry cannot prove there is anything to notify, so it must
// not notify blindly.
func TestSpawnReadErrorZeroSpawns(t *testing.T) {
	sc := withCapture(t)
	blocked := filepath.Join(t.TempDir(), "watchers.md")
	if err := os.MkdirAll(blocked, 0o755); err != nil {
		t.Fatalf("create blocking directory: %v", err)
	}
	if err := Spawn(blocked, "RACING"); err == nil {
		t.Fatalf("an unreadable registry must error, got nil")
	}
	if n := len(*sc); n != 0 {
		t.Fatalf("zero spawns required on a read error, got %d", n)
	}
}

// TestSpawnHomeFailureZeroSpawns: an unresolvable user home must error before
// any spawn — the child's working directory would have nowhere to point.
// Every home-variable candidate is wiped so the test does not depend on which
// one this Go version consults.
func TestSpawnHomeFailureZeroSpawns(t *testing.T) {
	sc := withCapture(t)
	t.Setenv("USERPROFILE", "")
	t.Setenv("HOMEDRIVE", "")
	t.Setenv("HOMEPATH", "")
	registry := writeRegistry(t, "10:00 | chain: x.md | session: a\r\n")
	if err := Spawn(registry, "SAVING"); err == nil {
		t.Fatalf("a failed home resolution must error, got nil")
	}
	if n := len(*sc); n != 0 {
		t.Fatalf("zero spawns required on a home failure, got %d", n)
	}
}

// TestSpawnStartFailureSurfaces: a failed process start (the seam errors
// within the grace window) surfaces as Spawn's wrapped error — the app loop
// logs it and the completed switch is never touched. The seam must have been
// attempted for the error to propagate.
func TestSpawnStartFailureSurfaces(t *testing.T) {
	want := errors.New("no exec allowed")
	orig := startProcess
	startProcess = func(cmd *exec.Cmd) error { return want }
	t.Cleanup(func() { startProcess = orig })
	registry := writeRegistry(t, "10:00 | chain: x.md | session: a\r\n")
	if err := Spawn(registry, "RACING"); !errors.Is(err, want) {
		t.Fatalf("a failed start within the grace window must surface wrapped: got %v", err)
	}
}
