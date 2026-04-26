package plugins_test

import (
	"bufio"
	"io"
	"testing"

	"github.com/adamtait/remote-agent/internal/config"
	"github.com/adamtait/remote-agent/internal/plugins"
)

// stubPlugin is a minimal Plugin for registry tests.
type stubPlugin struct {
	name   string
	assets []plugins.Asset
	errs   []string
	envs   []string
}

func (s *stubPlugin) Name() string { return s.name }
func (s *stubPlugin) Assets() []plugins.Asset { return s.assets }
func (s *stubPlugin) Validate(_ *config.Config) []string { return s.errs }
func (s *stubPlugin) Prompt(_ *bufio.Reader, _ io.Writer) (string, string, error) {
	return "", "", nil
}
func (s *stubPlugin) DefaultConfigFragments() (string, string) { return "", "" }
func (s *stubPlugin) ContainerEnvPairs(_ *config.Config) []string { return s.envs }

var _ plugins.Plugin = (*stubPlugin)(nil)

// ---------- nil-receiver safety ----------

func TestRegistry_Nil_All(t *testing.T) {
	var r *plugins.Registry
	if r.All() != nil {
		t.Error("nil Registry.All() should return nil")
	}
}

func TestRegistry_Nil_AllAssets(t *testing.T) {
	var r *plugins.Registry
	if r.AllAssets() != nil {
		t.Error("nil Registry.AllAssets() should return nil")
	}
}

func TestRegistry_Nil_ValidateAll(t *testing.T) {
	var r *plugins.Registry
	if r.ValidateAll(nil) != nil {
		t.Error("nil Registry.ValidateAll() should return nil")
	}
}

func TestRegistry_Nil_AllContainerEnvPairs(t *testing.T) {
	var r *plugins.Registry
	if r.AllContainerEnvPairs(nil) != nil {
		t.Error("nil Registry.AllContainerEnvPairs() should return nil")
	}
}

// ---------- multi-plugin aggregation ----------

func TestRegistry_AllAssets_AggregatesInOrder(t *testing.T) {
	a1 := plugins.Asset{Name: "p1/a.sh", Bytes: []byte("a"), FileMode: 0755}
	a2 := plugins.Asset{Name: "p2/b.sh", Bytes: []byte("b"), FileMode: 0755}
	r := plugins.NewRegistry(
		&stubPlugin{name: "p1", assets: []plugins.Asset{a1}},
		&stubPlugin{name: "p2", assets: []plugins.Asset{a2}},
	)
	got := r.AllAssets()
	if len(got) != 2 {
		t.Fatalf("expected 2 assets, got %d", len(got))
	}
	if got[0].Name != "p1/a.sh" || got[1].Name != "p2/b.sh" {
		t.Errorf("wrong order: %v", got)
	}
}

func TestRegistry_ValidateAll_ConcatenatesErrors(t *testing.T) {
	r := plugins.NewRegistry(
		&stubPlugin{name: "p1", errs: []string{"err-from-p1"}},
		&stubPlugin{name: "p2", errs: []string{"err-from-p2a", "err-from-p2b"}},
	)
	errs := r.ValidateAll(&config.Config{})
	if len(errs) != 3 {
		t.Fatalf("expected 3 errors, got %d: %v", len(errs), errs)
	}
	if errs[0] != "err-from-p1" || errs[1] != "err-from-p2a" || errs[2] != "err-from-p2b" {
		t.Errorf("wrong errors: %v", errs)
	}
}

func TestRegistry_AllContainerEnvPairs_ConcatenatesInOrder(t *testing.T) {
	r := plugins.NewRegistry(
		&stubPlugin{name: "p1", envs: []string{"A=1"}},
		&stubPlugin{name: "p2", envs: []string{"B=2", "C=3"}},
	)
	pairs := r.AllContainerEnvPairs(&config.Config{})
	if len(pairs) != 3 || pairs[0] != "A=1" || pairs[1] != "B=2" || pairs[2] != "C=3" {
		t.Errorf("wrong pairs: %v", pairs)
	}
}

func TestRegistry_EmptyRegistry_ReturnsEmpty(t *testing.T) {
	r := plugins.NewRegistry()
	if len(r.All()) != 0 {
		t.Error("empty registry should return empty All()")
	}
	if len(r.AllAssets()) != 0 {
		t.Error("empty registry should return empty AllAssets()")
	}
	if len(r.ValidateAll(nil)) != 0 {
		t.Error("empty registry should return empty ValidateAll()")
	}
}
