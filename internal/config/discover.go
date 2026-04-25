// ra/internal/config/discover.go
package config

import (
	"fmt"
	"os"
	"path/filepath"
)

// FindProjectConfig walks up from startDir looking for .ra/config.yaml,
// stopping at stopDir or the filesystem root.
func FindProjectConfig(startDir, stopDir string) (string, error) {
	dir := startDir
	for {
		path := filepath.Join(dir, ".ra", "config.yaml")
		if _, err := os.Stat(path); err == nil {
			return path, nil
		}
		if dir == stopDir || dir == "/" {
			break
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return "", fmt.Errorf("no .ra/config.yaml found; create one with 'ra setup' in your project root")
}
