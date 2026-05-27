// Package scaffold — buildcontext.go
// Assemble an ephemeral docker build context at an explicit destination from
// layered sources: project (.ra/) > user-global (~/.config/ra/) > plugin.
package scaffold

import (
	"bytes"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/adamtait/remote-agent/internal/logging"
	"github.com/adamtait/remote-agent/internal/plugins"
)

// BuildContextOptions controls AssembleBuildContext.
type BuildContextOptions struct {
	Dest    string // absolute output path; required
	Cwd     string // project root (contains .ra/) — sources only
	Home    string // user home (contains .config/ra/) — sources only
	Plugins []*plugins.FilePlugin
	ErrOut  io.Writer // optional; where user-facing warnings are surfaced (nil → log only)
}

// AssembleBuildContext writes a fresh docker build context at opts.Dest from
// layered sources (see package doc). Returns the build-context path, a
// cleanup func the caller MUST invoke, and any error.
func AssembleBuildContext(opts BuildContextOptions) (string, func(), error) {
	if opts.Dest == "" {
		return "", func() {}, fmt.Errorf("BuildContextOptions.Dest is required")
	}
	root := opts.Dest

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
	if err := stagePluginTrees(opts, root); err != nil {
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
//
// A project/user Dockerfile is a full-file override: it replaces the embedded
// default wholesale (unlike the per-file merges for the *.d dirs). A stale
// materialized copy is therefore dangerous — it silently freezes the base
// image and shadows every built-in Dockerfile improvement (e.g. the sshd
// keepalive that stops idle `gcloud workstations ssh` tunnels from being
// reaped). So when an override is used AND it differs from the current
// embedded default, warn with the path and how to adopt the latest.
func writeDockerfile(opts BuildContextOptions, root string) error {
	candidates := []struct {
		path   string
		source string
	}{
		{filepath.Join(opts.Cwd, ".ra", "Dockerfile"), "project"},
		{filepath.Join(opts.Home, ".config", "ra", "Dockerfile"), "user"},
	}
	for _, c := range candidates {
		data, err := os.ReadFile(c.path)
		if err == nil {
			logging.L().Debug("dockerfile source", "source", c.source)
			if !bytes.Equal(data, embeddedDockerfile) {
				warnStaleDockerfile(opts.ErrOut, c.source, c.path)
			}
			return os.WriteFile(filepath.Join(root, "Dockerfile"), data, 0644)
		}
		if !os.IsNotExist(err) {
			return fmt.Errorf("reading %s: %w", c.path, err)
		}
	}
	logging.L().Debug("dockerfile source", "source", "embedded")
	return os.WriteFile(filepath.Join(root, "Dockerfile"), embeddedDockerfile, 0644)
}

// warnStaleDockerfile surfaces that a custom Dockerfile override is shadowing
// the embedded default, so built-in image changes (keepalive, base-image bumps)
// are not applied. Visible on opts.ErrOut when set; otherwise logged only.
func warnStaleDockerfile(errOut io.Writer, source, path string) {
	const format = "using custom %s Dockerfile (%s); it overrides the built-in default, so " +
		"built-in image updates are NOT applied. If you didn't mean to customize it, delete the " +
		"file (or run `ra setup scaffold --force`) to adopt the current default."
	if errOut != nil {
		logging.WarnPrintf(errOut, format, source, path)
		return
	}
	logging.L().Warn(fmt.Sprintf(format, source, path))
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
		sources := []struct {
			path   string
			source string
		}{
			{filepath.Join(opts.Home, ".config", "ra", dir), "user"},
			{filepath.Join(opts.Cwd, ".ra", dir), "project"},
		}
		for _, s := range sources {
			logging.L().Debug("merging dir", "dir", dir, "source", s.source)
			if err := copyShellScripts(s.path, dst); err != nil {
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

// stagePluginTrees copies each plugin's whole directory into
// <root>/plugins/<name>/. .ra-install.yaml is install metadata and is
// skipped. Project-local plugins shadow user-global plugins entirely
// (handled in cmd/create.go's plugin loading; the assembler just walks
// whatever Plugins it's given).
func stagePluginTrees(opts BuildContextOptions, root string) error {
	dst := filepath.Join(root, "plugins")
	if err := os.MkdirAll(dst, 0755); err != nil {
		return err
	}
	for _, p := range opts.Plugins {
		logging.L().Debug("staging plugin", "name", p.Name())
		pdst := filepath.Join(dst, p.Name())
		if err := copyTreeFromFS(p.FS(), ".", pdst); err != nil {
			return fmt.Errorf("staging plugin %s: %w", p.Name(), err)
		}
	}
	return nil
}

// copyTreeFromFS recursively copies fsys rooted at src into dst on disk.
// Skips .ra-install.yaml at any depth.
func copyTreeFromFS(fsys fs.FS, src, dst string) error {
	return fs.WalkDir(fsys, src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.Name() == ".ra-install.yaml" {
			return nil
		}
		rel := p
		if src != "." {
			rel = strings.TrimPrefix(p, src+"/")
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0755)
		}
		data, err := fs.ReadFile(fsys, p)
		if err != nil {
			return err
		}
		mode := os.FileMode(0644)
		if strings.HasSuffix(p, ".sh") {
			mode = 0755
		}
		return os.WriteFile(target, data, mode)
	})
}
