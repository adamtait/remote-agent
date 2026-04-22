// ws/internal/config/migrate.go
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// MigrateLegacyProjectConfig moves <dir>/.ws.yaml to <dir>/.ws/config.yaml
// when only the legacy file is present. Returns (migrated, err):
//
//   - (true, nil)  — migration happened
//   - (false, nil) — no legacy file to migrate
//   - (false, err) — both files present (ambiguous), or the move failed
//
// The move is atomic via os.Rename on the same filesystem; .ws/ is created
// at 0755 if missing.
func MigrateLegacyProjectConfig(dir string) (bool, error) {
	legacy := filepath.Join(dir, ".ws.yaml")
	newPath := filepath.Join(dir, ".ws", "config.yaml")

	_, legacyErr := os.Stat(legacy)
	legacyExists := legacyErr == nil
	if !legacyExists && !errors.Is(legacyErr, os.ErrNotExist) {
		return false, fmt.Errorf("stat %s: %w", legacy, legacyErr)
	}
	if !legacyExists {
		return false, nil
	}

	if _, err := os.Stat(newPath); err == nil {
		return false, fmt.Errorf("conflicting config: %s and %s both present — remove %s",
			newPath, legacy, legacy)
	} else if !errors.Is(err, os.ErrNotExist) {
		return false, fmt.Errorf("stat %s: %w", newPath, err)
	}

	if err := os.MkdirAll(filepath.Dir(newPath), 0755); err != nil {
		return false, fmt.Errorf("creating %s: %w", filepath.Dir(newPath), err)
	}
	if err := os.Rename(legacy, newPath); err != nil {
		return false, fmt.Errorf("moving %s to %s: %w", legacy, newPath, err)
	}
	return true, nil
}
