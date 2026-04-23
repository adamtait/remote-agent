// Package scaffold — source.go
// Resolve the source bytes for a given build asset, honoring user-global
// template overrides at ~/.config/ws/template/<name>.
package scaffold

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// AssetNames enumerates the build assets ws knows how to scaffold.
var AssetNames = []string{"Dockerfile", "entrypoint.sh", "profile.d/00-ws-wait.sh"}

// ResolveSource returns the bytes to scaffold for the named asset.
//
// Precedence (per asset, independent):
//  1. ~/.config/ws/template/<name> if present and readable.
//  2. Embedded default compiled into the binary.
//
// Returns (bytes, label, error) where label is "user-template" or
// "embedded-default". Unknown names return an error.
func ResolveSource(home, name string) ([]byte, string, error) {
	embedded, ok := embeddedFor(name)
	if !ok {
		return nil, "", fmt.Errorf("unknown scaffold asset %q (known: %v)", name, AssetNames)
	}

	userPath := filepath.Join(home, ".config", "ws", "template", name)
	data, err := os.ReadFile(userPath)
	if err == nil {
		return data, "user-template", nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, "", fmt.Errorf("reading user template %s: %w", userPath, err)
	}
	return embedded, "embedded-default", nil
}

func embeddedFor(name string) ([]byte, bool) {
	switch name {
	case "Dockerfile":
		return embeddedDockerfile, true
	case "entrypoint.sh":
		return embeddedEntrypoint, true
	case "profile.d/00-ws-wait.sh":
		return embeddedWaitScript, true
	default:
		return nil, false
	}
}
