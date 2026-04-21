// Package scaffold builds cws config files from probed + prompted values.
package scaffold

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// WriteAtomic writes data to path. If the file exists and force is false, it
// returns an error without modifying the existing file. Writes go via a temp
// file + rename so partial writes cannot corrupt an existing file.
func WriteAtomic(path string, data []byte, force bool) error {
	if _, err := os.Stat(path); err == nil {
		if !force {
			return fmt.Errorf("%s already exists; re-run with --force to overwrite", path)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("stat %s: %w", path, err)
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("creating %s: %w", dir, err)
	}

	tmp, err := os.CreateTemp(dir, ".ws-scaffold-*")
	if err != nil {
		return fmt.Errorf("creating temp file: %w", err)
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return fmt.Errorf("writing temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("closing temp file: %w", err)
	}
	if err := os.Chmod(tmpName, 0644); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("chmod temp file: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("renaming to %s: %w", path, err)
	}
	return nil
}
