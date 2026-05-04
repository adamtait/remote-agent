// ra/internal/config/discover.go
package config

import (
	"os"
	"path/filepath"

	"github.com/adamtait/remote-agent/internal/logging"
)

// FindProjectConfig walks up from startDir looking for .ra/config.yaml,
// stopping at stopDir or the filesystem root. Returns ("", nil) when no
// project config is found — project-level config is optional and the
// user-level config is the floor.
func FindProjectConfig(startDir, stopDir string) (string, error) {
	dir := startDir
	for {
		path := filepath.Join(dir, ".ra", "config.yaml")
		if _, err := os.Stat(path); err == nil {
			logging.L().Debug("project config found", "path", path)
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
	logging.L().Debug("no project config found")
	return "", nil
}
