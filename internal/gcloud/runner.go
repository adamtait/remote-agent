package gcloud

import (
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
)

// Runner executes gcloud subcommands.
type Runner interface {
	// Run executes gcloud with args, streaming stdout/stderr to the terminal.
	Run(args ...string) error
	// RunContext executes gcloud like Run, but kills the gcloud process when
	// ctx is cancelled. Used for long-lived foreground commands (e.g. a TCP
	// tunnel) that must be torn down on demand. A ctx-driven kill surfaces as
	// a non-nil error, same as any other early exit.
	RunContext(ctx context.Context, args ...string) error
	// RunWithStdin executes gcloud with args, piping stdin into the process.
	// stdout/stderr stream to the terminal.
	RunWithStdin(stdin io.Reader, args ...string) error
	// RunSoft executes gcloud like Run, but on failure logs at Debug
	// instead of Error. Use for known-optional calls (e.g. enabling an
	// API the org may not have access to) where the runner's own log
	// line would otherwise mislead the user into thinking something is
	// wrong. The caller is expected to surface a WARN/INFO of its own.
	RunSoft(args ...string) error
	// RunCapture executes gcloud like Run, additionally returning the
	// captured stderr so callers can inspect it (e.g. retry on a specific
	// transient error message).
	RunCapture(args ...string) (stderr string, err error)
	// Output executes gcloud with args and returns stdout bytes. Stderr streams to terminal.
	Output(args ...string) ([]byte, error)
	// OutputQuiet executes gcloud like Output but discards stderr instead of
	// streaming it to the terminal. Use for probes that run repeatedly and
	// whose transient gcloud errors (e.g. a not-yet-ready SSH backend) would
	// otherwise spam the terminal. The returned error is unreliable for some
	// gcloud subcommands (`workstations ssh` exits 0 even on failure), so
	// callers should verify success from the returned stdout, not the error.
	OutputQuiet(args ...string) ([]byte, error)
	// Check executes gcloud with args, discarding all output. Returns true if exit code is 0.
	Check(args ...string) bool
}

// RealRunner shells out to the gcloud binary on PATH.
type RealRunner struct{}

func (r RealRunner) Run(args ...string) error {
	cmd := exec.Command("gcloud", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	return cmd.Run()
}

func (r RealRunner) RunContext(ctx context.Context, args ...string) error {
	cmd := exec.CommandContext(ctx, "gcloud", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	return cmd.Run()
}

func (r RealRunner) RunWithStdin(stdin io.Reader, args ...string) error {
	cmd := exec.Command("gcloud", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = stdin
	return cmd.Run()
}

func (r RealRunner) RunSoft(args ...string) error {
	cmd := exec.Command("gcloud", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	return cmd.Run()
}

func (r RealRunner) RunCapture(args ...string) (string, error) {
	var stderrBuf bytes.Buffer
	cmd := exec.Command("gcloud", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = io.MultiWriter(os.Stderr, &stderrBuf)
	cmd.Stdin = os.Stdin
	err := cmd.Run()
	return stderrBuf.String(), err
}

func (r RealRunner) Output(args ...string) ([]byte, error) {
	cmd := exec.Command("gcloud", args...)
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func (r RealRunner) OutputQuiet(args ...string) ([]byte, error) {
	cmd := exec.Command("gcloud", args...)
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = io.Discard
	err := cmd.Run()
	return buf.Bytes(), err
}

func (r RealRunner) Check(args ...string) bool {
	cmd := exec.Command("gcloud", args...)
	return cmd.Run() == nil
}
