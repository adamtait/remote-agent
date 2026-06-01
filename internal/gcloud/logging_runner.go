package gcloud

import (
	"bytes"
	"context"
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

// execOpts configures one execAndLog invocation. Zero-value defaults are
// what Run uses: stdout to terminal, stdin from terminal, Error-level
// failure log with "gcloud failed" message.
type execOpts struct {
	op      string    // op name attached to every slog record (e.g. "gcloud.Run")
	stdout  io.Writer // nil → os.Stdout
	stdin   io.Reader // nil → os.Stdin
	failLvl slog.Level
	failMsg string
	args    []string
}

// execAndLog runs gcloud with opts.args, mirroring stderr to the terminal
// and capturing it for the slog record. Returns the captured stderr (raw,
// not trimmed) and the exec error. All public Runner methods are thin
// wrappers around this helper — see them for the per-call variations.
func (r LoggingRunner) execAndLog(opts execOpts) (string, error) {
	start := time.Now()
	l := r.logger.With(
		slog.String("op", opts.op),
		slog.String("subcmd", gcloudSubcmd(opts.args)),
		slog.Any("args", sanitizeArgs(opts.args)),
	)
	l.Debug("gcloud starting")

	stdout := opts.stdout
	if stdout == nil {
		stdout = os.Stdout
	}
	stdin := opts.stdin
	if stdin == nil {
		stdin = os.Stdin
	}

	var stderrBuf bytes.Buffer
	cmd := exec.Command("gcloud", opts.args...)
	cmd.Stdout = stdout
	cmd.Stderr = io.MultiWriter(os.Stderr, &stderrBuf)
	cmd.Stdin = stdin
	err := cmd.Run()

	stderrStr := strings.TrimSpace(stderrBuf.String())
	attrs := []any{slog.Float64("duration_s", time.Since(start).Seconds())}
	if stderrStr != "" {
		attrs = append(attrs, slog.String("stderr", stderrStr))
	}
	if err != nil {
		attrs = append(attrs, slog.String("error", err.Error()))
		failMsg := opts.failMsg
		if failMsg == "" {
			failMsg = "gcloud failed"
		}
		l.Log(context.Background(), opts.failLvl, failMsg, attrs...)
	} else {
		l.Debug("gcloud ok", attrs...)
	}
	return stderrBuf.String(), err
}

// Run executes gcloud with args, streaming stdout to the terminal and
// capturing stderr for logging.
func (r LoggingRunner) Run(args ...string) error {
	_, err := r.execAndLog(execOpts{op: "gcloud.Run", failLvl: slog.LevelError, args: args})
	return err
}

// RunWithStdin executes gcloud with args, piping stdin in, and capturing stderr for logging.
func (r LoggingRunner) RunWithStdin(stdin io.Reader, args ...string) error {
	_, err := r.execAndLog(execOpts{op: "gcloud.RunWithStdin", stdin: stdin, failLvl: slog.LevelError, args: args})
	return err
}

// RunSoft executes gcloud like Run, but on failure logs at Debug instead
// of Error. Use for known-optional calls where a normal Error log line
// would mislead the user — the caller is expected to surface its own
// WARN message describing what was tried and why it didn't matter.
func (r LoggingRunner) RunSoft(args ...string) error {
	_, err := r.execAndLog(execOpts{
		op:      "gcloud.RunSoft",
		failLvl: slog.LevelDebug,
		failMsg: "gcloud soft-fail",
		args:    args,
	})
	return err
}

// RunCapture executes gcloud like Run, but additionally returns the
// captured stderr to the caller so it can inspect the error message
// (e.g. for retry-on-specific-error logic). Stdout/stderr still stream
// to the terminal as in Run.
func (r LoggingRunner) RunCapture(args ...string) (string, error) {
	return r.execAndLog(execOpts{op: "gcloud.RunCapture", failLvl: slog.LevelError, args: args})
}

// Output executes gcloud with args and returns stdout bytes. Stderr streams
// to the terminal and is also captured for logging.
func (r LoggingRunner) Output(args ...string) ([]byte, error) {
	var stdoutBuf bytes.Buffer
	if _, err := r.execAndLog(execOpts{
		op:      "gcloud.Output",
		stdout:  &stdoutBuf,
		failLvl: slog.LevelError,
		args:    args,
	}); err != nil {
		return nil, err
	}
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
