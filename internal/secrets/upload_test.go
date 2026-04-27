package secrets

import (
	"bytes"
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

func (f *fakeRunner) Check(args ...string) bool { return f.exists }
func (f *fakeRunner) Run(args ...string) error  { return nil }
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
func (f *fakeRunner) Output(args ...string) ([]byte, error) { return nil, nil }

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
