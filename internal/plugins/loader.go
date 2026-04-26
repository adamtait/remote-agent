package plugins

import (
	"io/fs"
	"os"
	"path/filepath"
	"sort"
)

// LoadAll returns all plugins from embedded defaults, user-global dir, and
// project-local dir. Priority (highest last): embedded → user-global → project-local.
// Plugins of the same name are shadowed entirely by the higher-priority source.
func LoadAll(userDir, projectDir string) ([]*FilePlugin, error) {
	byName := make(map[string]*FilePlugin)

	// Embedded defaults (lowest priority).
	entries, _ := defaultsFS.ReadDir("defaults")
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		sub, err := fs.Sub(defaultsFS, "defaults/"+e.Name())
		if err != nil {
			continue
		}
		p, err := LoadPlugin(sub)
		if err != nil {
			continue
		}
		byName[p.Name()] = p
	}

	loadDir(userDir, byName)
	loadDir(projectDir, byName)

	out := make([]*FilePlugin, 0, len(byName))
	for _, p := range byName {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name() < out[j].Name() })
	return out, nil
}

func loadDir(dir string, byName map[string]*FilePlugin) {
	if dir == "" {
		return
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		sub := os.DirFS(filepath.Join(dir, e.Name()))
		p, err := LoadPlugin(sub)
		if err != nil {
			continue
		}
		byName[p.Name()] = p
	}
}
