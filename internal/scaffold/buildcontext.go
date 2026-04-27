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
	return root, cleanup, nil
}
