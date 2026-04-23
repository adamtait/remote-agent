// Package scaffold — source.go
// Resolve the source bytes for a given build asset.
package scaffold

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// AssetNames enumerates the build assets ws knows how to scaffold.
var AssetNames = []string{"Dockerfile", "entrypoint.sh", "profile.d/00-ws-wait.sh"}

// EmbeddedSeed returns the embedded default bytes for the named asset.
// Used at setup time to materialize initial templates.
func EmbeddedSeed(name string) ([]byte, error) {
	data, ok := embeddedFor(name)
	if !ok {
		return nil, fmt.Errorf("unknown scaffold asset %q (known: %v)", name, AssetNames)
	}
	return data, nil
}

// ResolveSource returns the bytes to use for the named asset at runtime.
//
// Precedence (per asset, independent):
//  1. <projectRoot>/.ws/<name> if present and readable.
//  2. ~/.config/ws/template/<name> if present and readable.
//  3. Error — asset not found; run `ws setup`.
//
// projectRoot may be "" to skip the project-override check.
// Unknown names always return an error.
func ResolveSource(home, projectRoot, name string) ([]byte, string, error) {
	if _, ok := embeddedFor(name); !ok {
		return nil, "", fmt.Errorf("unknown scaffold asset %q (known: %v)", name, AssetNames)
	}

	// 1. Project override: <projectRoot>/.ws/<name>
	if projectRoot != "" {
		projectPath := filepath.Join(projectRoot, ".ws", name)
		data, err := os.ReadFile(projectPath)
		if err == nil {
			return data, "project-override", nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return nil, "", fmt.Errorf("reading project asset %s: %w", projectPath, err)
		}
	}

	// 2. User template: ~/.config/ws/template/<name>
	userPath := filepath.Join(home, ".config", "ws", "template", name)
	data, err := os.ReadFile(userPath)
	if err == nil {
		return data, "user-template", nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, "", fmt.Errorf("reading user template %s: %w", userPath, err)
	}

	// 3. Error — no embedded fallback.
	return nil, "", fmt.Errorf("asset %q not found in project (.ws/) or user template (~/.config/ws/template/); run 'ws setup'", name)
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
