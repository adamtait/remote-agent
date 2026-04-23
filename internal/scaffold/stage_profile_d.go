// Package scaffold — stage_profile_d.go
// Compose .ws/profile.d.staged/ from .ws/profile.d/ (project) and
// ~/.config/ws/profile.d/ (user-global) for the Dockerfile to COPY.
package scaffold

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// StageProfileD merges project-level and user-global profile.d scripts into
// <cwd>/.ws/profile.d.staged/. User-global wins on filename collision.
//
// Returns the stage path, a cleanup func the caller MUST invoke (typically
// via defer) to remove the staging dir, and any error.
//
// Only *.sh files are copied. Any preexisting staging directory is removed
// first so stale content from a prior run cannot leak into the next build.
// If neither source exists, the staging dir is created empty so the
// Dockerfile's `COPY .ws/profile.d.staged/ /etc/profile.d/` does not fail.
func StageProfileD(cwd, home string) (string, func(), error) {
	stage := filepath.Join(cwd, ".ws", "profile.d.staged")

	if err := os.RemoveAll(stage); err != nil {
		return "", func() {}, fmt.Errorf("clearing stale stage %s: %w", stage, err)
	}
	if err := os.MkdirAll(stage, 0755); err != nil {
		return "", func() {}, fmt.Errorf("creating stage %s: %w", stage, err)
	}

	cleanup := func() { _ = os.RemoveAll(stage) }

	sources := []string{
		filepath.Join(cwd, ".ws", "profile.d"),
		filepath.Join(home, ".config", "ws", "profile.d"),
	}
	for _, src := range sources {
		if err := copyShellScriptsInto(src, stage); err != nil {
			cleanup()
			return "", func() {}, err
		}
	}

	return stage, cleanup, nil
}

// copyShellScriptsInto copies every *.sh file from src into dst, overwriting
// any existing file of the same name in dst. A non-existent src is a no-op.
func copyShellScriptsInto(src, dst string) error {
	entries, err := os.ReadDir(src)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("reading %s: %w", src, err)
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if !strings.HasSuffix(e.Name(), ".sh") {
			continue
		}
		if err := copyFile(filepath.Join(src, e.Name()), filepath.Join(dst, e.Name())); err != nil {
			return err
		}
	}
	return nil
}

func copyFile(srcPath, dstPath string) error {
	in, err := os.Open(srcPath)
	if err != nil {
		return fmt.Errorf("opening %s: %w", srcPath, err)
	}
	defer in.Close()

	out, err := os.Create(dstPath)
	if err != nil {
		return fmt.Errorf("creating %s: %w", dstPath, err)
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return fmt.Errorf("copying to %s: %w", dstPath, err)
	}
	if err := out.Close(); err != nil {
		return fmt.Errorf("closing %s: %w", dstPath, err)
	}
	if err := os.Chmod(dstPath, 0755); err != nil {
		return fmt.Errorf("chmod %s: %w", dstPath, err)
	}
	return nil
}
