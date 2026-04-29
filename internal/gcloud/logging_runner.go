package gcloud

import (
	"bytes"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/adamtait/remote-agent/internal/logging"
)

// LoggingRunner implements Runner by shelling out to gcloud directly,
// logging each invocation's args, duration, and stderr to a *slog.Logger.
// It satisfies the Runner interface.
type LoggingRunner struct {
	logger *slog.Logger
}

// NewLoggingRunner returns a LoggingRunner that logs to logger.
func NewLoggingRunner(logger *slog.Logger) LoggingRunner {
	if logger == nil {
		logger = logging.L()
	}
	return LoggingRunner{logger: logger}
}

// gcloudSubcmd returns the first two non-flag args joined by a space,
// e.g. "services enable", "workstations configs create".
// It skips args starting with "-" and also skips the value following a
// --key value style flag (a flag arg without "=" that has a separate value).
func gcloudSubcmd(args []string) string {
	var parts []string
	skipNext := false
	for _, a := range args {
		if skipNext {
			skipNext = false
			continue
		}
		if strings.HasPrefix(a, "-") {
			// If the flag doesn't embed its value (no "="), its next token is the value.
			if !strings.Contains(a, "=") {
				skipNext = true
			}
			continue
		}
		parts = append(parts, a)
		if len(parts) == 2 {
			break
		}
	}
	return strings.Join(parts, " ")
}

// Run executes gcloud with args, streaming stdout to the terminal and
// capturing stderr for logging.
func (r LoggingRunner) Run(args ...string) error {
	return r.RunWithStderr(os.Stderr, args...)
}

// RunWithStderr executes gcloud with args, writing stderr to the provided
// writer (while also capturing it for logging). stdout and stdin are terminal.
func (r LoggingRunner) RunWithStderr(stderr io.Writer, args ...string) error {
	start := time.Now()
	l := r.logger.With(
		slog.String("op", "gcloud.Run"),
		slog.String("subcmd", gcloudSubcmd(args)),
		slog.Any("args", sanitizeArgs(args)),
	)
	l.Debug("gcloud starting")

	var stderrBuf bytes.Buffer
	cmd := exec.Command("gcloud", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = io.MultiWriter(stderr, &stderrBuf)
	cmd.Stdin = os.Stdin
	err := cmd.Run()

	dur := time.Since(start)
	if err != nil {
		errAttrs := []any{
			slog.Float64("duration_s", dur.Seconds()),
			slog.String("error", err.Error()),
		}
		if s := strings.TrimSpace(stderrBuf.String()); s != "" {
			errAttrs = append(errAttrs, slog.String("stderr", s))
		}
		l.Error("gcloud failed", errAttrs...)
	} else {
		attrs := []any{slog.Float64("duration_s", dur.Seconds())}
		if s := strings.TrimSpace(stderrBuf.String()); s != "" {
			attrs = append(attrs, slog.String("stderr", s))
		}
		l.Debug("gcloud ok", attrs...)
	}
	return err
}

// RunWithStdin executes gcloud with args, piping stdin in, and capturing stderr for logging.
func (r LoggingRunner) RunWithStdin(stdin io.Reader, args ...string) error {
	start := time.Now()
	l := r.logger.With(
		slog.String("op", "gcloud.RunWithStdin"),
		slog.String("subcmd", gcloudSubcmd(args)),
		slog.Any("args", sanitizeArgs(args)),
	)
	l.Debug("gcloud starting")

	var stderrBuf bytes.Buffer
	cmd := exec.Command("gcloud", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = io.MultiWriter(os.Stderr, &stderrBuf)
	cmd.Stdin = stdin
	err := cmd.Run()

	dur := time.Since(start)
	if err != nil {
		errAttrs := []any{
			slog.Float64("duration_s", dur.Seconds()),
			slog.String("error", err.Error()),
		}
		if s := strings.TrimSpace(stderrBuf.String()); s != "" {
			errAttrs = append(errAttrs, slog.String("stderr", s))
		}
		l.Error("gcloud failed", errAttrs...)
	} else {
		attrs := []any{slog.Float64("duration_s", dur.Seconds())}
		if s := strings.TrimSpace(stderrBuf.String()); s != "" {
			attrs = append(attrs, slog.String("stderr", s))
		}
		l.Debug("gcloud ok", attrs...)
	}
	return err
}

// Output executes gcloud with args and returns stdout bytes. Stderr streams
// to the terminal and is also captured for logging.
func (r LoggingRunner) Output(args ...string) ([]byte, error) {
	start := time.Now()
	l := r.logger.With(
		slog.String("op", "gcloud.Output"),
		slog.String("subcmd", gcloudSubcmd(args)),
		slog.Any("args", sanitizeArgs(args)),
	)
	l.Debug("gcloud starting")

	var stdoutBuf, stderrBuf bytes.Buffer
	cmd := exec.Command("gcloud", args...)
	cmd.Stdout = &stdoutBuf
	cmd.Stderr = io.MultiWriter(os.Stderr, &stderrBuf)
	err := cmd.Run()

	dur := time.Since(start)
	if err != nil {
		errAttrs := []any{
			slog.Float64("duration_s", dur.Seconds()),
			slog.String("error", err.Error()),
		}
		if s := strings.TrimSpace(stderrBuf.String()); s != "" {
			errAttrs = append(errAttrs, slog.String("stderr", s))
		}
		l.Error("gcloud failed", errAttrs...)
		return nil, err
	}
	attrs := []any{slog.Float64("duration_s", dur.Seconds())}
	if s := strings.TrimSpace(stderrBuf.String()); s != "" {
		attrs = append(attrs, slog.String("stderr", s))
	}
	l.Debug("gcloud ok", attrs...)
	return stdoutBuf.Bytes(), nil
}

// Check executes gcloud with args, discarding all output. Returns true if
// exit code is 0.
func (r LoggingRunner) Check(args ...string) bool {
	start := time.Now()
	l := r.logger.With(
		slog.String("op", "gcloud.Check"),
		slog.String("subcmd", gcloudSubcmd(args)),
		slog.Any("args", sanitizeArgs(args)),
	)
	l.Debug("gcloud starting")

	cmd := exec.Command("gcloud", args...)
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	result := cmd.Run() == nil

	dur := time.Since(start)
	l.Debug("gcloud check",
		slog.Float64("duration_s", dur.Seconds()),
		slog.Bool("exists", result),
	)
	return result
}
