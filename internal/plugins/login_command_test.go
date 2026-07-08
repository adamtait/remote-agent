package plugins

import (
	"strings"
	"testing"
	"testing/fstest"

	"github.com/adamtait/remote-agent/internal/config"
)

// tmuxLikePluginYAML declares a verbatim login-command plus the `enabled` gate
// every plugin has. No config-value interpolation — the command is literal (see
// ADR 0013 for deferred generic templating).
const tmuxLikePluginYAML = `name: tmux
version: "1.0.0"
description: "persistent tmux session"
login-command: "tmux new-session -A -s main"
config:
  - name: enabled
    type: bool
    default: false
`

func pluginFromYAML(t *testing.T, body string) *FilePlugin {
	t.Helper()
	p, err := LoadPlugin(fstest.MapFS{"plugin.yaml": &fstest.MapFile{Data: []byte(body)}})
	if err != nil {
		t.Fatalf("LoadPlugin: %v", err)
	}
	return p
}

func enabledCfg(name string, extra map[string]interface{}) *config.Config {
	pc := map[string]interface{}{"enabled": true}
	for k, v := range extra {
		pc[k] = v
	}
	return &config.Config{Plugins: map[string]map[string]interface{}{name: pc}}
}

func TestRegistryLoginCommand_SingleEnabled(t *testing.T) {
	reg := NewRegistry([]*FilePlugin{
		makeTestPlugin(t, "alpha"),
		pluginFromYAML(t, tmuxLikePluginYAML),
	})
	cmd, ok, err := reg.LoginCommand(enabledCfg("tmux", nil))
	if err != nil {
		t.Fatalf("LoginCommand err: %v", err)
	}
	if !ok || cmd != "tmux new-session -A -s main" {
		t.Errorf("LoginCommand = (%q,%v), want (tmux new-session -A -s main,true)", cmd, ok)
	}
}

func TestRegistryLoginCommand_DisabledSkipped(t *testing.T) {
	reg := NewRegistry([]*FilePlugin{pluginFromYAML(t, tmuxLikePluginYAML)})
	cfg := &config.Config{Plugins: map[string]map[string]interface{}{"tmux": {"enabled": false}}}
	cmd, ok, err := reg.LoginCommand(cfg)
	if err != nil || ok || cmd != "" {
		t.Errorf("LoginCommand(disabled) = (%q,%v,%v), want (\"\",false,nil)", cmd, ok, err)
	}
}

func TestRegistryLoginCommand_NoneDeclared(t *testing.T) {
	reg := NewRegistry([]*FilePlugin{makeTestPlugin(t, "alpha")})
	cmd, ok, err := reg.LoginCommand(enabledCfg("alpha", nil))
	if err != nil || ok || cmd != "" {
		t.Errorf("LoginCommand(none) = (%q,%v,%v), want (\"\",false,nil)", cmd, ok, err)
	}
}

// Only the *enabled* declaring plugin's command is returned when another plugin
// also declares one but is disabled — guards the enabled-branch bookkeeping.
func TestRegistryLoginCommand_MixedEnabledDisabled(t *testing.T) {
	second := strings.Replace(tmuxLikePluginYAML, "name: tmux", "name: screenlike", 1)
	second = strings.Replace(second, "tmux new-session -A -s main", "screen-attach", 1)
	reg := NewRegistry([]*FilePlugin{
		pluginFromYAML(t, tmuxLikePluginYAML),
		pluginFromYAML(t, second),
	})
	cfg := &config.Config{Plugins: map[string]map[string]interface{}{
		"tmux":       {"enabled": true},
		"screenlike": {"enabled": false},
	}}
	cmd, ok, err := reg.LoginCommand(cfg)
	if err != nil || !ok || cmd != "tmux new-session -A -s main" {
		t.Errorf("LoginCommand(mixed) = (%q,%v,%v), want tmux's command", cmd, ok, err)
	}
}

func TestRegistryLoginCommand_MultipleIsError(t *testing.T) {
	second := strings.Replace(tmuxLikePluginYAML, "name: tmux", "name: screenlike", 1)
	reg := NewRegistry([]*FilePlugin{
		pluginFromYAML(t, tmuxLikePluginYAML),
		pluginFromYAML(t, second),
	})
	cfg := &config.Config{Plugins: map[string]map[string]interface{}{
		"tmux":       {"enabled": true},
		"screenlike": {"enabled": true},
	}}
	_, _, err := reg.LoginCommand(cfg)
	if err == nil {
		t.Fatal("expected error when two enabled plugins declare a login-command")
	}
	// Names should be listed (sorted) so the message is actionable.
	if !strings.Contains(err.Error(), "screenlike") || !strings.Contains(err.Error(), "tmux") {
		t.Errorf("error should name both conflicting plugins, got: %v", err)
	}
}

func TestLoadPlugin_RejectsMultilineLoginCommand(t *testing.T) {
	body := "name: tmux\nlogin-command: \"a\\nb\"\n"
	if _, err := LoadPlugin(fstest.MapFS{"plugin.yaml": &fstest.MapFile{Data: []byte(body)}}); err == nil ||
		!strings.Contains(err.Error(), "single line") {
		t.Fatalf("expected single-line error, got %v", err)
	}
}
