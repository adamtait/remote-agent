package logging

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"os"
	"sync"
	"sync/atomic"
	"time"
)

// buildVersion is set by -ldflags at build time.
var buildVersion = "dev"

// Config configures the logging system for one CLI invocation.
type Config struct {
	// Level is retained for back-compat but no longer drives the terminal
	// display threshold — the terminal handler is fixed at Warn so step
	// lifecycle Info logs do not interleave with human-readable "==>"
	// output. Debug-level records always reach the file log.
	Level     slog.Level
	File      string // explicit log file path; "" = auto-generate
	Format    string // "json" (default) or "text"
	NoFile    bool   // true = disable file output entirely
	SessionID string // auto-generated if blank
	Command   string // cobra command name e.g. "create"
	Project   string // gcloud project id (may be "" before config loads)
}

// pkgLogger is the package-level logger, goroutine-safe via atomic.Pointer.
var pkgLogger atomic.Pointer[slog.Logger]

// prevCleanup stores the cleanup func from the previous Init call so that
// calling Init a second time closes the old log file before opening a new one.
var prevCleanup atomic.Value

// pkgLogPath stores the active session's log file path, set during Init and
// readable via LogPath. Empty when NoFile is true or before Init has run.
var pkgLogPath atomic.Pointer[string]

func init() {
	// Initialize with a discard logger so L() is always safe to call.
	discard := slog.New(slog.NewTextHandler(io.Discard, nil))
	pkgLogger.Store(discard)
}

// L returns the package-level logger. Always safe to call — returns a
// discard logger until Init has been called.
func L() *slog.Logger {
	return pkgLogger.Load()
}

// LogPath returns the absolute path of the active session's log file, or
// "" if Init has not run, was called with NoFile, or used a discard
// logger. Safe to call from any goroutine.
func LogPath() string {
	if p := pkgLogPath.Load(); p != nil {
		return *p
	}
	return ""
}

// newTerminalHandler builds the human-facing slog handler that writes to w.
// Fixed at Warn: step_start / step_ok / session_start / gcloud failed all log
// at Info or below and would otherwise interleave with the human-readable
// "==>" lines. The file handler captures everything at Debug.
func newTerminalHandler(w io.Writer) slog.Handler {
	return slog.NewTextHandler(w, &slog.HandlerOptions{
		Level: slog.LevelWarn,
		ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
			// Remove time key at the top level (redundant on a live terminal).
			if len(groups) == 0 && a.Key == slog.TimeKey {
				return slog.Attr{}
			}
			return a
		},
	})
}

// Init configures the package-level logger from cfg. It is idempotent:
// calling it again closes the previous log file and resets state. Returns
// a cleanup func that logs session_end and closes the log file.
func Init(cfg Config) (cleanup func(), err error) {
	// Close the previous log file if Init was called before.
	if fn, ok := prevCleanup.Load().(func()); ok && fn != nil {
		fn()
	}
	// Reset path so callers don't see a stale value when re-initializing
	// in NoFile mode (e.g. tests).
	pkgLogPath.Store(nil)

	if cfg.SessionID == "" {
		cfg.SessionID = NewSessionID()
	}

	termHandler := newTerminalHandler(os.Stderr)

	// Build file handler.
	var (
		fileHandler slog.Handler
		closeFn     func()
	)

	if !cfg.NoFile {
		var f *os.File
		if cfg.File != "" {
			f, err = os.OpenFile(cfg.File, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
			if err != nil {
				return nil, fmt.Errorf("logging: open log file %s: %w", cfg.File, err)
			}
			var once sync.Once
			closeFn = func() { once.Do(func() { f.Close() }) }
		} else {
			dir, dirErr := DefaultLogDir()
			if dirErr != nil {
				return nil, dirErr
			}
			cmd := cfg.Command
			if cmd == "" {
				cmd = "ra"
			}
			f, closeFn, err = OpenLogFile(dir, cmd)
			if err != nil {
				return nil, err
			}
		}
		path := f.Name()
		pkgLogPath.Store(&path)

		fileReplaceAttr := func(groups []string, a slog.Attr) slog.Attr {
			// Convert time.Duration values to float64 seconds and rename key with _s suffix.
			if a.Value.Kind() == slog.KindAny {
				if d, ok := a.Value.Any().(time.Duration); ok {
					return slog.Float64(a.Key+"_s", d.Seconds())
				}
			}
			return a
		}

		fileOpts := &slog.HandlerOptions{
			Level:       slog.LevelDebug,
			ReplaceAttr: fileReplaceAttr,
		}

		if cfg.Format == "text" {
			fileHandler = slog.NewTextHandler(f, fileOpts)
		} else {
			fileHandler = slog.NewJSONHandler(f, fileOpts)
		}
	}

	tee := &TeeHandler{
		terminal: termHandler,
		file:     fileHandler,
	}

	// Build logger with session-wide attrs.
	logger := slog.New(tee).With(
		slog.String("session_id", cfg.SessionID),
		slog.String("command", cfg.Command),
		slog.String("gcp_project", cfg.Project),
		slog.String("ra_version", buildVersion),
	)
	pkgLogger.Store(logger)

	// Log session_start.
	logger.LogAttrs(context.Background(), slog.LevelInfo, "session_start")

	// Build idempotent cleanup func.
	var cleanupOnce sync.Once
	cleanup = func() {
		cleanupOnce.Do(func() {
			logger.LogAttrs(context.Background(), slog.LevelInfo, "session_end")
			if closeFn != nil {
				closeFn()
			}
		})
	}
	prevCleanup.Store(cleanup)
	return cleanup, nil
}

// Step prints the colored "==>" step marker to out, logs step_start at
// Info, and returns a done func the caller invokes after the step
// completes (logs step_ok or step_failed with duration). When total > 0
// the marker is rendered as "==> [num/total] msg" so the user can see
// overall pipeline progress.
func Step(name string, num, total int, msg string, out io.Writer) func(err error) {
	if total > 0 {
		StepPrintf(out, fmt.Sprintf("[%d/%d] %s", num, total, msg))
	} else {
		StepPrintf(out, msg)
	}
	L().LogAttrs(context.Background(), slog.LevelInfo, "step_start",
		slog.String("step", name),
		slog.Int("step_num", num),
		slog.Int("step_total", total),
	)
	start := time.Now()
	return func(err error) {
		dur := time.Since(start)
		if err != nil {
			L().LogAttrs(context.Background(), slog.LevelInfo, "step_failed",
				slog.String("step", name),
				slog.Int("step_num", num),
				slog.Int("step_total", total),
				slog.Float64("duration_s", dur.Seconds()),
				slog.String("error", err.Error()),
			)
		} else {
			L().LogAttrs(context.Background(), slog.LevelInfo, "step_ok",
				slog.String("step", name),
				slog.Int("step_num", num),
				slog.Int("step_total", total),
				slog.Float64("duration_s", dur.Seconds()),
			)
		}
	}
}

// NewSessionID returns a short, sortable, URL-safe session identifier:
// "<unix-ms>-<4-hex-chars>"  e.g. "1714298732145-a3f7"
func NewSessionID() string {
	ms := time.Now().UnixMilli()
	b := make([]byte, 2)
	if _, err := rand.Read(b); err != nil {
		// Fallback: use zeros; session ID is non-critical.
		b = []byte{0, 0}
	}
	return fmt.Sprintf("%d-%s", ms, hex.EncodeToString(b))
}
