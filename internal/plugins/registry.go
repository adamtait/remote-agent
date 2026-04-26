package plugins

import "github.com/adamtait/remote-agent/internal/config"

// Registry holds the ordered list of active plugins.
type Registry struct {
	plugins []Plugin
}

// NewRegistry creates a Registry containing the provided plugins.
func NewRegistry(ps ...Plugin) *Registry {
	return &Registry{plugins: ps}
}

// All returns all registered plugins in registration order.
func (r *Registry) All() []Plugin {
	if r == nil {
		return nil
	}
	return r.plugins
}

// AllAssets returns the union of all plugin assets in registration order.
func (r *Registry) AllAssets() []Asset {
	if r == nil {
		return nil
	}
	var out []Asset
	for _, p := range r.plugins {
		out = append(out, p.Assets()...)
	}
	return out
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
