// Package scaffold — scaffold_assets.go
// Materialize the default Dockerfile and entrypoint.sh into <project>/.ws/.
package scaffold

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

type ScaffoldAssetsOptions struct {
	// TargetDir is the absolute directory that will hold the materialized
	// assets (e.g. /home/alice/.config/ws/template or /path/to/project/.ws).
	TargetDir string

	Home string // needed so ResolveSource can find user-template overrides

	Stdout io.Writer

	Force       bool
	DryRun      bool
	OnlyMissing bool
}

// AssetResult records, per asset name, whether the file was (or would be)
// written and which source provided its bytes.
type AssetResult struct {
	wrote  map[string]bool
	source map[string]string
}

func (r AssetResult) Wrote(name string) bool    { return r.wrote[name] }
func (r AssetResult) Source(name string) string { return r.source[name] }

// assetMode returns the file mode for a named asset. Thin wrapper around the
// exported AssetMode so callers outside the package use the same source of truth.
func assetMode(name string) os.FileMode { return AssetMode(name) }

// ScaffoldAssets writes each asset in AssetNames to opts.TargetDir/<name>.
func ScaffoldAssets(opts ScaffoldAssetsOptions) (AssetResult, error) {
	res := AssetResult{
		wrote:  map[string]bool{},
		source: map[string]string{},
	}

	if !opts.DryRun {
		if err := os.MkdirAll(opts.TargetDir, 0755); err != nil {
			return res, fmt.Errorf("creating %s: %w", opts.TargetDir, err)
		}
	}

	for _, name := range AssetNames {
		target := filepath.Join(opts.TargetDir, name)
		if !opts.DryRun {
			if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
				return res, fmt.Errorf("creating parent dir for %s: %w", target, err)
			}
		}

		_, statErr := os.Stat(target)
		exists := statErr == nil
		if !exists && !errors.Is(statErr, os.ErrNotExist) {
			return res, fmt.Errorf("stat %s: %w", target, statErr)
		}

		if exists {
			switch {
			case opts.OnlyMissing:
				res.source[name] = "preexisting"
				fmt.Fprintf(opts.Stdout, "skip %s (already present)\n", name)
				continue
			case !opts.Force:
				return res, fmt.Errorf("%s already exists; re-run with --force to overwrite", target)
			}
		}

		body, err := EmbeddedSeed(name)
		if err != nil {
			return res, err
		}
		source := "embedded-default"
		res.source[name] = source

		if opts.DryRun {
			fmt.Fprintf(opts.Stdout, "would write %s (%d bytes, from %s)\n", name, len(body), source)
			continue
		}

		if err := writeFileAtomic(target, body, assetMode(name)); err != nil {
			return res, err
		}
		res.wrote[name] = true
		fmt.Fprintf(opts.Stdout, "wrote %s (from %s)\n", name, source)
	}

	return res, nil
}

// writeFileAtomic writes body to path with mode, via a temp file + rename
// in the same directory so partial writes cannot corrupt an existing file.
func writeFileAtomic(path string, body []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".ws-asset-*")
	if err != nil {
		return fmt.Errorf("creating temp file in %s: %w", dir, err)
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(body); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return fmt.Errorf("writing temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("closing temp file: %w", err)
	}
	if err := os.Chmod(tmpName, mode); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("chmod temp file: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("renaming to %s: %w", path, err)
	}
	return nil
}
