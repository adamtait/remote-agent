// ws/internal/config/discover.go
package config

import (
	"fmt"
	"os"
	"path/filepath"
)

// FindProjectConfig walks up from startDir looking for .ws/config.yaml,
// stopping at stopDir or the filesystem root. At each level, if only a
// legacy .ws.yaml is present, it is auto-migrated to .ws/config.yaml.
// If both exist at the same level, returns an error (ambiguous).
func FindProjectConfig(startDir, stopDir string) (string, error) {
	dir := startDir
	for {
		newPath := filepath.Join(dir, ".ws", "config.yaml")
		if _, err := os.Stat(newPath); err == nil {
			// New path present — check the legacy path isn't also present.
			if _, err := os.Stat(filepath.Join(dir, ".ws.yaml")); err == nil {
				return "", fmt.Errorf(
					"conflicting config: %s and %s both present — remove %s",
					newPath, filepath.Join(dir, ".ws.yaml"), filepath.Join(dir, ".ws.yaml"))
			}
			return newPath, nil
		}

		migrated, err := MigrateLegacyProjectConfig(dir)
		if err != nil {
			return "", err
		}
		if migrated {
			fmt.Fprintf(os.Stderr,
				"note: migrated %s → %s\n",
				filepath.Join(dir, ".ws.yaml"), newPath)
			return newPath, nil
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
	return "", fmt.Errorf("no .ws/config.yaml found; create one with 'ws setup' in your project root")
}
