package plugins

import (
	"strings"
	"testing"
	"testing/fstest"

	"github.com/adamtait/remote-agent/internal/config"
)

// tmuxLikePluginYAML is a plugin that declares a login-command referencing a
// config field, plus the `enabled` gate every plugin has.
const tmuxLikePluginYAML = `name: tmux
version: "1.0.0"
description: "persistent tmux session"
login-command: "tmux new-session -A -s {session_name}"
config:
  - name: enabled
    type: bool
    default: false
  - name: session_name
    type: string
    default: main
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

func TestRenderLoginCommand_ConfigValueWins(t *testing.T) {
	p := pluginFromYAML(t, tmuxLikePluginYAML)
	got, err := p.RenderLoginCommand(enabledCfg("tmux", map[string]interface{}{"session_name": "work"}))
	if err != nil {
		t.Fatalf("RenderLoginCommand err: %v", err)
	}
	if want := "tmux new-session -A -s work"; got != want {
		t.Errorf("RenderLoginCommand = %q, want %q", got, want)
	}
}

func TestRenderLoginCommand_FallsBackToSchemaDefault(t *testing.T) {
	p := pluginFromYAML(t, tmuxLikePluginYAML)
	// Enabled but session_name unset in config -> schema default "main".
	got, err := p.RenderLoginCommand(enabledCfg("tmux", nil))
	if err != nil || got != "tmux new-session -A -s main" {
		t.Errorf("RenderLoginCommand = (%q,%v), want (tmux new-session -A -s main,nil)", got, err)
	}
	// nil config -> also the schema default.
	if got, err := p.RenderLoginCommand(nil); err != nil || got != "tmux new-session -A -s main" {
		t.Errorf("RenderLoginCommand(nil) = (%q,%v), want default-rendered", got, err)
	}
}

func TestRenderLoginCommand_NoneDeclared(t *testing.T) {
	p := makeTestPlugin(t, "alpha") // no login-command
	if got, err := p.RenderLoginCommand(nil); err != nil || got != "" {
		t.Errorf("RenderLoginCommand with no declaration = (%q,%v), want (\"\",nil)", got, err)
	}
}

// A config value with whitespace or shell metacharacters must be rejected, not
// baked into a command that only looks shell-safe — the invariant the whole
// feature rests on.
func TestRenderLoginCommand_RejectsUnsafeValue(t *testing.T) {
	p := pluginFromYAML(t, tmuxLikePluginYAML)
	for _, bad := range []string{"two words", "a; rm -rf /", "a|b", "$(x)", "{b}", ""} {
		_, err := p.RenderLoginCommand(enabledCfg("tmux", map[string]interface{}{"session_name": bad}))
		if err == nil {
			t.Errorf("RenderLoginCommand(session_name=%q) = nil error, want rejection", bad)
		}
	}
}

func TestRegistryLoginCommand_SingleEnabled(t *testing.T) {
	reg := NewRegistry([]*FilePlugin{
		makeTestPlugin(t, "alpha"),
		pluginFromYAML(t, tmuxLikePluginYAML),
	})
	cmd, ok, err := reg.LoginCommand(enabledCfg("tmux", map[string]interface{}{"session_name": "main"}))
	if err != nil {
		t.Fatalf("LoginCommand err: %v", err)
	}
	if !ok || cmd != "tmux new-session -A -s main" {
		t.Errorf("LoginCommand = (%q,%v), want (tmux new-session -A -s main,true)", cmd, ok)
	}
}

func TestRegistryLoginCommand_DisabledSkipped(t *testing.T) {
	reg := NewRegistry([]*FilePlugin{pluginFromYAML(t, tmuxLikePluginYAML)})
	// Plugin declares a login-command but is not enabled -> none.
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
	second = strings.Replace(second, "tmux new-session", "screen-new", 1)
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

// A malformed config value on the enabled plugin surfaces as an error from the
// registry (so `ra connect` fails cleanly rather than emitting an unsafe command).
func TestRegistryLoginCommand_UnsafeValueErrors(t *testing.T) {
	reg := NewRegistry([]*FilePlugin{pluginFromYAML(t, tmuxLikePluginYAML)})
	cfg := enabledCfg("tmux", map[string]interface{}{"session_name": "a; rm -rf /"})
	if _, _, err := reg.LoginCommand(cfg); err == nil {
		t.Fatal("expected error for unsafe session_name, got nil")
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

func TestLoadPlugin_RejectsUnknownPlaceholder(t *testing.T) {
	body := `name: tmux
login-command: "tmux new-session -A -s {bogus}"
config:
  - name: session_name
    type: string
`
	if _, err := LoadPlugin(fstest.MapFS{"plugin.yaml": &fstest.MapFile{Data: []byte(body)}}); err == nil ||
		!strings.Contains(err.Error(), "unknown config field") {
		t.Fatalf("expected unknown-placeholder error, got %v", err)
	}
}

func TestLoadPlugin_RejectsMultilineLoginCommand(t *testing.T) {
	body := "name: tmux\nlogin-command: \"a\\nb\"\n"
	if _, err := LoadPlugin(fstest.MapFS{"plugin.yaml": &fstest.MapFile{Data: []byte(body)}}); err == nil ||
		!strings.Contains(err.Error(), "single line") {
		t.Fatalf("expected single-line error, got %v", err)
	}
}
