// Package scaffold — source.go
// Resolve the source bytes for a given build asset.
package scaffold

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// AssetNames enumerates the build assets ra knows how to scaffold.
var AssetNames = []string{
	"Dockerfile",
	"entrypoint.sh",
	"profile.d/00-ra-wait.sh",
	"claude/install.sh",
	"claude/setup.sh",
}

// AssetMode returns the file mode ra uses for a materialized asset.
// Shell scripts are 0755; other files are 0644.
func AssetMode(name string) os.FileMode {
	switch name {
	case "entrypoint.sh",
		"profile.d/00-ra-wait.sh",
		"claude/install.sh",
		"claude/setup.sh":
		return 0755
	default:
		return 0644
	}
}

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
//  1. <projectRoot>/.ra/<name> if present and readable.
//  2. ~/.config/ra/template/<name> if present and readable.
//  3. Embedded default bytes compiled into the binary.
//
// projectRoot may be "" to skip the project-override check.
// Unknown names always return an error.
func ResolveSource(home, projectRoot, name string) ([]byte, string, error) {
	embedded, ok := embeddedFor(name)
	if !ok {
		return nil, "", fmt.Errorf("unknown scaffold asset %q (known: %v)", name, AssetNames)
	}

	// 1. Project override: <projectRoot>/.ra/<name>
	if projectRoot != "" {
		projectPath := filepath.Join(projectRoot, ".ra", name)
		data, err := os.ReadFile(projectPath)
		if err == nil {
			return data, "project-override", nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return nil, "", fmt.Errorf("reading project asset %s: %w", projectPath, err)
		}
	}

	// 2. User template: ~/.config/ra/template/<name>
	userPath := filepath.Join(home, ".config", "ra", "template", name)
	data, err := os.ReadFile(userPath)
	if err == nil {
		return data, "user-template", nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return nil, "", fmt.Errorf("reading user template %s: %w", userPath, err)
	}

	// 3. Embedded default — the binary always carries a working seed.
	return embedded, "embedded-default", nil
}

func embeddedFor(name string) ([]byte, bool) {
	switch name {
	case "Dockerfile":
		return embeddedDockerfile, true
	case "entrypoint.sh":
		return embeddedEntrypoint, true
	case "profile.d/00-ra-wait.sh":
		return embeddedWaitScript, true
	case "claude/install.sh":
		return embeddedClaudeInstall, true
	case "claude/setup.sh":
		return embeddedClaudeSetup, true
	default:
		return nil, false
	}
}
