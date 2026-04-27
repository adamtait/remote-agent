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
