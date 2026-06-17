package secrets

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"

	"github.com/adamtait/remote-agent/internal/gcloud"
)

type fakeRunner struct {
	exists       bool
	createdName  string
	createdValue string
}

func (f *fakeRunner) Check(args ...string) bool                          { return f.exists }
func (f *fakeRunner) Run(args ...string) error                           { return nil }
func (f *fakeRunner) RunContext(_ context.Context, args ...string) error { return nil }
func (f *fakeRunner) RunWithStdin(stdin io.Reader, args ...string) error {
	for i, a := range args {
		if a == "create" && i+1 < len(args) {
			f.createdName = args[i+1]
		}
	}
	buf := new(bytes.Buffer)
	_, _ = buf.ReadFrom(stdin)
	f.createdValue = buf.String()
	return nil
}
func (f *fakeRunner) Output(args ...string) ([]byte, error)      { return nil, nil }
func (f *fakeRunner) OutputQuiet(args ...string) ([]byte, error) { return nil, nil }
func (f *fakeRunner) RunSoft(args ...string) error               { return f.Run(args...) }
func (f *fakeRunner) RunCapture(args ...string) (string, error) {
	return "", f.Run(args...)
}

// Compile-time check that fakeRunner implements gcloud.Runner.
var _ gcloud.Runner = (*fakeRunner)(nil)

func TestEnsureSecret_AlreadyExists(t *testing.T) {
	r := &fakeRunner{exists: true}
	stderr := new(bytes.Buffer)
	if err := EnsureSecret(r, "p", "my-secret", "value?", stderr, nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stderr.String(), "already exists") {
		t.Errorf("stderr = %q, want already-exists message", stderr.String())
	}
}

func TestExists(t *testing.T) {
	r := &fakeRunner{exists: true}
	if !Exists(r, "p", "my-secret") {
		t.Error("Exists returned false when fake reports exists=true")
	}
	r.exists = false
	if Exists(r, "p", "my-secret") {
		t.Error("Exists returned true when fake reports exists=false")
	}
}

func TestCreateFromValue(t *testing.T) {
	r := &fakeRunner{}
	if err := CreateFromValue(r, "p", "my-secret", []byte("hunter2")); err != nil {
		t.Fatal(err)
	}
	if r.createdName != "my-secret" {
		t.Errorf("createdName = %q, want my-secret", r.createdName)
	}
	if r.createdValue != "hunter2" {
		t.Errorf("createdValue = %q, want hunter2", r.createdValue)
	}
}
