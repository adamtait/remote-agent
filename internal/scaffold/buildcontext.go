// Package scaffold — buildcontext.go
// Assemble an ephemeral .ra/.build/ docker build context from layered sources:
// project (.ra/) > user-global (~/.config/ra/) > plugin-contributed.
package scaffold

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

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
	if err := mergePluginDirs(opts, root); err != nil {
		cleanup()
		return "", func() {}, err
	}
	if err := mergeDirs(opts, root); err != nil {
		cleanup()
		return "", func() {}, err
	}
	if err := writeKeepIfEmpty(root); err != nil {
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

// mergedDirs are the three image-mirror directories that get merged from
// project, user, and (later) plugin sources.
var mergedDirs = []string{"Dockerfile.d", "workstation-startup.d", "profile.d"}

// mergePluginDirs overlays each plugin's contributed dirs (Dockerfile.d/,
// workstation-startup.d/, profile.d/) into <root>/<dir>/. Called BEFORE
// the user/project merge so user/project files shadow plugin files.
func mergePluginDirs(opts BuildContextOptions, root string) error {
	for _, p := range opts.Plugins {
		for _, dir := range mergedDirs {
			dst := filepath.Join(root, dir)
			if err := os.MkdirAll(dst, 0755); err != nil {
				return err
			}
			if err := copyShellScriptsFromFS(p.FS(), dir, dst); err != nil {
				return fmt.Errorf("plugin %s %s: %w", p.Name(), dir, err)
			}
		}
	}
	return nil
}

// copyShellScriptsFromFS is the fs.FS variant of copyShellScripts. Used
// for plugin trees, which are accessed via fs.FS.
func copyShellScriptsFromFS(fsys fs.FS, dir, dst string) error {
	entries, err := fs.ReadDir(fsys, dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sh") {
			continue
		}
		data, err := fs.ReadFile(fsys, dir+"/"+e.Name())
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(dst, e.Name()), data, 0755); err != nil {
			return err
		}
	}
	return nil
}

// mergeDirs writes <root>/<dir>/ for each dir in mergedDirs by overlaying
// user-global, then project-local. Project files shadow user files.
// Plugin contributions are layered in by mergePluginDirs (Task 5).
func mergeDirs(opts BuildContextOptions, root string) error {
	for _, dir := range mergedDirs {
		dst := filepath.Join(root, dir)
		if err := os.MkdirAll(dst, 0755); err != nil {
			return fmt.Errorf("creating %s: %w", dst, err)
		}
		// User-global first (lowest priority), then project (overwrites).
		sources := []string{
			filepath.Join(opts.Home, ".config", "ra", dir),
			filepath.Join(opts.Cwd, ".ra", dir),
		}
		for _, src := range sources {
			if err := copyShellScripts(src, dst); err != nil {
				return err
			}
		}
	}
	return nil
}

// copyShellScripts copies *.sh files from src to dst (overwriting on
// collision). Non-existent src is a no-op. Used for the layered merges.
func copyShellScripts(src, dst string) error {
	entries, err := os.ReadDir(src)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("reading %s: %w", src, err)
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sh") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(src, e.Name()))
		if err != nil {
			return fmt.Errorf("reading %s/%s: %w", src, e.Name(), err)
		}
		if err := os.WriteFile(filepath.Join(dst, e.Name()), data, 0755); err != nil {
			return fmt.Errorf("writing %s/%s: %w", dst, e.Name(), err)
		}
	}
	return nil
}

// writeKeepIfEmpty drops a .ra-keep placeholder so docker COPY of an
// otherwise-empty directory does not fail. Called after all merges.
func writeKeepIfEmpty(root string) error {
	for _, dir := range mergedDirs {
		full := filepath.Join(root, dir)
		entries, err := os.ReadDir(full)
		if err != nil {
			return fmt.Errorf("reading %s: %w", full, err)
		}
		hasContent := false
		for _, e := range entries {
			if e.Name() != ".ra-keep" {
				hasContent = true
				break
			}
		}
		if !hasContent {
			if err := os.WriteFile(filepath.Join(full, ".ra-keep"), nil, 0644); err != nil {
				return fmt.Errorf("writing .ra-keep in %s: %w", full, err)
			}
		}
	}
	return nil
}
