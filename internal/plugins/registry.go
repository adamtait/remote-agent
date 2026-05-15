package plugins

import "github.com/adamtait/remote-agent/internal/config"

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

// PortByName returns the declared in-workstation port for the plugin with this
// name. Returns 0,false if the plugin is not installed or did not declare a
// `port:` field in plugin.yaml.
func (r *Registry) PortByName(name string) (int, bool) {
	if r == nil {
		return 0, false
	}
	for _, p := range r.plugins {
		if p.Name() != name {
			continue
		}
		port := p.Schema().Port
		if port == 0 {
			return 0, false
		}
		return port, true
	}
	return 0, false
}

// TunnelablePlugins returns the names of installed plugins that declare a
// non-zero `port:` field, in registry order.
func (r *Registry) TunnelablePlugins() []string {
	if r == nil {
		return nil
	}
	var names []string
	for _, p := range r.plugins {
		if p.Schema().Port != 0 {
			names = append(names, p.Name())
		}
	}
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
