package plugins

import (
	"fmt"
	"sort"
	"strings"

	"github.com/adamtait/remote-agent/internal/config"
)

// Registry holds the ordered list of active plugins.
type Registry struct {
	plugins []*FilePlugin
}

// NewRegistry creates a Registry from the provided FilePlugin slice.
func NewRegistry(ps []*FilePlugin) *Registry {
	return &Registry{plugins: ps}
}

// All returns all plugins.
func (r *Registry) All() []*FilePlugin {
	if r == nil {
		return nil
	}
	return r.plugins
}

// ValidateAll runs every plugin's Validate and accumulates error strings.
func (r *Registry) ValidateAll(cfg *config.Config) []string {
	if r == nil {
		return nil
	}
	var errs []string
	for _, p := range r.plugins {
		errs = append(errs, p.Validate(cfg)...)
	}
	return errs
}

// PortByName returns the in-workstation port for the plugin with this name.
// The user-configured value at `plugins.<name>.port-for-tunnel` wins; the
// schema's `port-for-tunnel` default is the fallback. Returns 0,false if
// the plugin is not installed or did not declare a `port-for-tunnel` in
// plugin.yaml.
func (r *Registry) PortByName(name string, cfg *config.Config) (int, bool) {
	if r == nil {
		return 0, false
	}
	for _, p := range r.plugins {
		if p.Name() != name {
			continue
		}
		schemaDefault := p.Schema().PortForTunnel
		if schemaDefault == 0 {
			return 0, false
		}
		var pluginCfg map[string]interface{}
		if cfg != nil && cfg.Plugins != nil {
			pluginCfg = cfg.Plugins[name]
		}
		return portForTunnelFromCfg(pluginCfg, schemaDefault), true
	}
	return 0, false
}

// LoginCommand returns the connect-time command declared by the single enabled
// plugin that provides one, rendered against cfg. `ra connect` runs it on the
// workstation in place of an interactive login shell.
//
// Returns ("", false, nil) when no enabled plugin declares a `login-command:`.
// Returns an error when more than one enabled plugin declares one — two plugins
// cannot both own the login shell, so this is a real conflict the user must
// resolve rather than something core silently picks a winner for. Mirrors
// PortByName: the `enabled` gate matches cmd/create.go, and rendering reads the
// plugin's config with the schema default as fallback.
func (r *Registry) LoginCommand(cfg *config.Config) (string, bool, error) {
	if r == nil {
		return "", false, nil
	}
	var cmd string
	var owners []string
	for _, p := range r.plugins {
		if p.Schema().LoginCommand == "" {
			continue
		}
		var pluginCfg map[string]interface{}
		if cfg != nil && cfg.Plugins != nil {
			pluginCfg = cfg.Plugins[p.Name()]
		}
		if en, _ := pluginCfg["enabled"].(bool); !en {
			continue
		}
		owners = append(owners, p.Name())
		cmd = p.RenderLoginCommand(cfg)
	}
	switch len(owners) {
	case 0:
		return "", false, nil
	case 1:
		return cmd, true, nil
	default:
		sort.Strings(owners)
		return "", false, fmt.Errorf(
			"multiple enabled plugins declare a login-command (%s); at most one may — disable all but one",
			strings.Join(owners, ", "))
	}
}

// TunnelablePlugins returns the names of installed plugins that declare a
// non-zero `port-for-tunnel:` field, sorted alphabetically so error
// messages and help output are stable across runs.
func (r *Registry) TunnelablePlugins() []string {
	if r == nil {
		return nil
	}
	var names []string
	for _, p := range r.plugins {
		if p.Schema().PortForTunnel != 0 {
			names = append(names, p.Name())
		}
	}
	sort.Strings(names)
	return names
}

// AllContainerEnvPairs returns env KEY=VALUE pairs from all plugins.
func (r *Registry) AllContainerEnvPairs(cfg *config.Config) []string {
	if r == nil {
		return nil
	}
	var out []string
	for _, p := range r.plugins {
		out = append(out, p.ContainerEnvPairs(cfg)...)
	}
	return out
}

// FindSecretOwner returns the plugin and SecretDef whose logical Name
// matches `logical`. Plugin-level secrets are checked first; if none
// matches, the plugin's currently-active auth_provider (per cfg) is
// consulted. Returns ok=false if no installed plugin claims `logical` —
// e.g. when the plugin has been uninstalled but its `secrets.<name>:`
// block was left behind in config.yaml.
func (r *Registry) FindSecretOwner(cfg *config.Config, logical string) (*FilePlugin, *SecretDef, bool) {
	if r == nil {
		return nil, nil, false
	}
	for _, p := range r.plugins {
		schema := p.Schema()
		for i := range schema.Secrets {
			if schema.Secrets[i].Name == logical {
				return p, &schema.Secrets[i], true
			}
		}
	}
	if cfg == nil {
		return nil, nil, false
	}
	for _, p := range r.plugins {
		schema := p.Schema()
		if len(schema.AuthProviders) == 0 {
			continue
		}
		pluginCfg, ok := cfg.Plugins[p.Name()]
		if !ok {
			continue
		}
		active, _ := pluginCfg["auth_provider"].(string)
		if active == "" {
			continue
		}
		for i := range schema.AuthProviders {
			ap := &schema.AuthProviders[i]
			if ap.Name != active {
				continue
			}
			for j := range ap.Secrets {
				if ap.Secrets[j].Name == logical {
					return p, &ap.Secrets[j], true
				}
			}
		}
	}
	return nil, nil, false
}
