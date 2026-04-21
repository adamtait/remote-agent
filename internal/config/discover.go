// cws/internal/config/discover.go
package config

import (
	"fmt"
	"os"
	"path/filepath"
)

// FindProjectConfig walks up from startDir looking for .workstation.yaml,
// stopping when it reaches stopDir or the filesystem root.
func FindProjectConfig(startDir, stopDir string) (string, error) {
	dir := startDir
	for {
		candidate := filepath.Join(dir, ".workstation.yaml")
		if _, err := os.Stat(candidate); err == nil {
			return candidate, nil
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
	return "", fmt.Errorf("no .workstation.yaml found; create one in your project root (see README)")
}
