package cmd

import (
	"testing"
)

func TestKeepAwakeCommand_Darwin(t *testing.T) {
	name, args, ok := keepAwakeCommand("darwin", 4242)
	if !ok {
		t.Fatalf("expected darwin to be supported")
	}
	if name != "caffeinate" {
		t.Errorf("name = %q, want caffeinate", name)
	}
	// Must assert idle-sleep prevention (-i) and tie its lifetime to the ra
	// pid (-w 4242) so a killed ra never leaves a dangling assertion.
	want := []string{"-i", "-w", "4242"}
	if len(args) != len(want) {
		t.Fatalf("args = %v, want %v", args, want)
	}
	for i := range want {
		if args[i] != want[i] {
			t.Errorf("args[%d] = %q, want %q", i, args[i], want[i])
		}
	}
}

func TestKeepAwakeCommand_UnsupportedPlatform(t *testing.T) {
	for _, goos := range []string{"linux", "windows", "freebsd"} {
		if _, _, ok := keepAwakeCommand(goos, 1); ok {
			t.Errorf("goos %q: expected unsupported (ok=false)", goos)
		}
	}
}

// keepAwake(false, ...) must be a pure no-op: it returns a callable stop func
// and never spawns a process, so disabling the feature can never error or leak.
func TestKeepAwake_DisabledIsNoop(t *testing.T) {
	stop := keepAwake(false, nil)
	if stop == nil {
		t.Fatalf("stop func must be non-nil even when disabled")
	}
	stop() // must not panic
	stop() // idempotent: calling twice must not panic
}
