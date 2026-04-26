package plugins

import (
	"fmt"
	"io/fs"

	"gopkg.in/yaml.v3"
)

// FilePlugin implements plugin behaviors by reading a plugin directory.
type FilePlugin struct {
	fsys   fs.FS
	schema PluginSchema
}

// LoadPlugin reads plugin.yaml from fsys and returns a FilePlugin.
func LoadPlugin(fsys fs.FS) (*FilePlugin, error) {
	data, err := fs.ReadFile(fsys, "plugin.yaml")
	if err != nil {
		return nil, fmt.Errorf("read plugin.yaml: %w", err)
	}
	var s PluginSchema
	if err := yaml.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("parse plugin.yaml: %w", err)
	}
	if s.Name == "" {
		return nil, fmt.Errorf("plugin.yaml: name is required")
	}
	return &FilePlugin{fsys: fsys, schema: s}, nil
}

// Name returns the plugin's canonical identifier.
func (p *FilePlugin) Name() string { return p.schema.Name }

// Assets returns install.sh and setup.sh if present, named plugins/<name>/<script>.
func (p *FilePlugin) Assets() []Asset {
	var assets []Asset
	for _, script := range []string{"install.sh", "setup.sh"} {
		b, err := fs.ReadFile(p.fsys, script)
		if err != nil {
			continue
		}
		assets = append(assets, Asset{
			Name:     "plugins/" + p.schema.Name + "/" + script,
			Bytes:    b,
			FileMode: 0755,
		})
	}
	return assets
}
