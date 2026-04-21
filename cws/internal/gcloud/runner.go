package gcloud

import (
	"bytes"
	"io"
	"os"
	"os/exec"
)

// Runner executes gcloud subcommands.
type Runner interface {
	// Run executes gcloud with args, streaming stdout/stderr to the terminal.
	Run(args ...string) error
	// RunWithStdin executes gcloud with args, piping stdin into the process.
	// stdout/stderr stream to the terminal.
	RunWithStdin(stdin io.Reader, args ...string) error
	// Output executes gcloud with args and returns stdout bytes. Stderr streams to terminal.
	Output(args ...string) ([]byte, error)
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

func (r RealRunner) RunWithStdin(stdin io.Reader, args ...string) error {
	cmd := exec.Command("gcloud", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = stdin
	return cmd.Run()
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

func (r RealRunner) Check(args ...string) bool {
	cmd := exec.Command("gcloud", args...)
	return cmd.Run() == nil
}
