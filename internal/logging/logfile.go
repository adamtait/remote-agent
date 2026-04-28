package logging

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// DefaultLogDir returns the XDG data home log directory:
//
//	~/.local/share/ra/logs/
//
// It creates the directory (with MkdirAll) if it does not exist.
func DefaultLogDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("logging: get home dir: %w", err)
	}
	dir := filepath.Join(home, ".local", "share", "ra", "logs")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", fmt.Errorf("logging: create log dir %s: %w", dir, err)
	}
	return dir, nil
}

// OpenLogFile opens (creating if needed) a log file at:
//
//	dir/YYYY-MM-DDTHHMMSS-<cmd>.log
//
// e.g. ~/.local/share/ra/logs/2026-04-28T153012-create.log
// Returns the *os.File and a close func (safe to call multiple times).
func OpenLogFile(dir, cmd string) (*os.File, func(), error) {
	ts := time.Now().Format("2006-01-02T150405")
	name := fmt.Sprintf("%s-%s.log", ts, cmd)
	path := filepath.Join(dir, name)

	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return nil, nil, fmt.Errorf("logging: open log file %s: %w", path, err)
	}

	var once sync.Once
	closeFn := func() {
		once.Do(func() { f.Close() })
	}
	return f, closeFn, nil
}
