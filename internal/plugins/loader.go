package plugins

import (
	"os"
	"path/filepath"
	"sort"
)

// LoadAll returns all plugins from user-global dir and project-local dir.
// Project-local plugins shadow user-global plugins of the same name entirely.
func LoadAll(userDir, projectDir string) ([]*FilePlugin, error) {
	byName := make(map[string]*FilePlugin)

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
