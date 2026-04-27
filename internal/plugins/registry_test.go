package plugins

import (
	"testing"
	"testing/fstest"

	"github.com/adamtait/remote-agent/internal/config"
)

func makeTestPlugin(t *testing.T, name string) *FilePlugin {
	t.Helper()
	yaml := "name: " + name + "\nversion: \"1.0.0\"\ndescription: \"d\"\n"
	fsys := fstest.MapFS{
		"plugin.yaml": &fstest.MapFile{Data: []byte(yaml)},
		"setup.sh":    &fstest.MapFile{Data: []byte("#!/bin/bash\n"), Mode: 0755},
	}
	p, err := LoadPlugin(fsys)
	if err != nil {
		t.Fatalf("makeTestPlugin(%q): %v", name, err)
	}
	return p
}

func TestRegistry_NilSafe(t *testing.T) {
	var r *Registry
	if r.All() != nil {
		t.Error("nil registry All() should return nil")
	}
	if r.ValidateAll(nil) != nil {
		t.Error("nil registry ValidateAll() should return nil")
	}
	if r.AllContainerEnvPairs(nil) != nil {
		t.Error("nil registry AllContainerEnvPairs() should return nil")
	}
}

func TestRegistry_ValidateAll_NoErrors(t *testing.T) {
	p := makeTestPlugin(t, "alpha")
	r := NewRegistry([]*FilePlugin{p})
	if errs := r.ValidateAll(&config.Config{}); len(errs) != 0 {
		t.Errorf("unexpected errors: %v", errs)
	}
}

func TestRegistry_ContainerEnvPairs(t *testing.T) {
	p := makeTestPlugin(t, "alpha")
	r := NewRegistry([]*FilePlugin{p})
	pairs := r.AllContainerEnvPairs(&config.Config{})
	// alpha has no config fields, so no pairs
	if len(pairs) != 0 {
		t.Errorf("expected 0 pairs, got %v", pairs)
	}
}
