package etconn

import (
	"errors"
	"strings"
	"testing"
)

func TestEnsureETInstalled_Present(t *testing.T) {
	orig := etLookPath
	t.Cleanup(func() { etLookPath = orig })
	etLookPath = func(string) (string, error) { return "/usr/local/bin/et", nil }

	if err := EnsureETInstalled(); err != nil {
		t.Fatalf("expected nil when et is present, got %v", err)
	}
}

func TestEnsureETInstalled_Missing(t *testing.T) {
	orig := etLookPath
	t.Cleanup(func() { etLookPath = orig })
	etLookPath = func(string) (string, error) { return "", errors.New("not found") }

	err := EnsureETInstalled()
	if err == nil {
		t.Fatal("expected error when et is missing")
	}
	// The message must be actionable: point at both the client install and the
	// server-side plugin.
	for _, want := range []string{"brew install et", "ra-plugin"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error message missing %q; got: %s", want, err.Error())
		}
	}
}
