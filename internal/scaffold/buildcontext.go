// Package scaffold — buildcontext.go
// Assemble an ephemeral .ra/.build/ docker build context from layered sources:
// project (.ra/) > user-global (~/.config/ra/) > plugin-contributed.
package scaffold

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/adamtait/remote-agent/internal/plugins"
)

// BuildContextOptions controls AssembleBuildContext.
type BuildContextOptions struct {
	Cwd     string             // project root (contains .ra/)
	Home    string             // user home (contains .config/ra/)
	Plugins []*plugins.FilePlugin
}

// AssembleBuildContext writes a fresh <cwd>/.ra/.build/ docker build context
// from layered sources (see package doc). Returns the build-context path,
// a cleanup func the caller MUST invoke, and any error.
func AssembleBuildContext(opts BuildContextOptions) (string, func(), error) {
	root := filepath.Join(opts.Cwd, ".ra", ".build")

	if err := os.RemoveAll(root); err != nil {
		return "", func() {}, fmt.Errorf("clearing %s: %w", root, err)
	}
	if err := os.MkdirAll(root, 0755); err != nil {
		return "", func() {}, fmt.Errorf("creating %s: %w", root, err)
	}

	cleanup := func() { _ = os.RemoveAll(root) }

	if err := writeDockerfile(opts, root); err != nil {
		cleanup()
		return "", func() {}, err
	}

	return root, cleanup, nil
}

// writeDockerfile chooses project > user > embedded Dockerfile bytes and
// writes them into <root>/Dockerfile.
func writeDockerfile(opts BuildContextOptions, root string) error {
	candidates := []string{
		filepath.Join(opts.Cwd, ".ra", "Dockerfile"),
		filepath.Join(opts.Home, ".config", "ra", "Dockerfile"),
	}
	for _, p := range candidates {
		data, err := os.ReadFile(p)
		if err == nil {
			return os.WriteFile(filepath.Join(root, "Dockerfile"), data, 0644)
		}
		if !os.IsNotExist(err) {
			return fmt.Errorf("reading %s: %w", p, err)
		}
	}
	return os.WriteFile(filepath.Join(root, "Dockerfile"), embeddedDockerfile, 0644)
}
