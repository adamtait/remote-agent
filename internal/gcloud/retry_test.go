package gcloud

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
	"time"
)

// retryFakeRunner is a Runner stub for retry tests. It emits a sequence of
// (stderr, err) results from RunCapture, one per call.
type retryFakeRunner struct {
	results []retryResult
	calls   int
}

type retryResult struct {
	stderr string
	err    error
}

func (f *retryFakeRunner) Run(args ...string) error                           { return nil }
func (f *retryFakeRunner) RunWithStdin(stdin io.Reader, args ...string) error { return nil }
func (f *retryFakeRunner) RunSoft(args ...string) error                       { return nil }
func (f *retryFakeRunner) Output(args ...string) ([]byte, error)              { return nil, nil }
func (f *retryFakeRunner) Check(args ...string) bool                          { return false }
func (f *retryFakeRunner) RunCapture(args ...string) (string, error) {
	if f.calls >= len(f.results) {
		return "", errors.New("retryFakeRunner: no more results")
	}
	res := f.results[f.calls]
	f.calls++
	return res.stderr, res.err
}

var _ Runner = (*retryFakeRunner)(nil)

func TestRetryOnSANotFound_RetriesUntilSuccess(t *testing.T) {
	transient := errors.New("exit status 1")
	r := &retryFakeRunner{results: []retryResult{
		{stderr: "ERROR: Service account foo@bar.iam.gserviceaccount.com does not exist.", err: transient},
		{stderr: "ERROR: Service account foo@bar.iam.gserviceaccount.com does not exist.", err: transient},
		{stderr: "", err: nil},
	}}
	notice := new(bytes.Buffer)
	err := RetryOnSANotFound(r, 5, 1*time.Millisecond, notice, "iam", "service-accounts", "describe")
	if err != nil {
		t.Fatalf("expected success after retries, got %v", err)
	}
	if r.calls != 3 {
		t.Errorf("expected 3 calls, got %d", r.calls)
	}
	if !strings.Contains(notice.String(), "waiting for service account propagation") {
		t.Errorf("expected propagation notice on terminal, got %q", notice.String())
	}
}

func TestRetryOnSANotFound_ExhaustsBudget(t *testing.T) {
	transient := errors.New("exit status 1")
	r := &retryFakeRunner{results: []retryResult{
		{stderr: "ERROR: Service account x does not exist.", err: transient},
		{stderr: "ERROR: Service account x does not exist.", err: transient},
		{stderr: "ERROR: Service account x does not exist.", err: transient},
	}}
	err := RetryOnSANotFound(r, 3, 1*time.Millisecond, nil, "args")
	if err == nil {
		t.Fatal("expected error after exhausting retries")
	}
	if r.calls != 3 {
		t.Errorf("expected 3 calls, got %d", r.calls)
	}
}

func TestRetryOnSANotFound_DoesNotRetryUnrelatedErrors(t *testing.T) {
	transient := errors.New("exit status 1")
	r := &retryFakeRunner{results: []retryResult{
		{stderr: "ERROR: PERMISSION_DENIED: caller lacks permission.", err: transient},
		{stderr: "", err: nil}, // never reached
	}}
	err := RetryOnSANotFound(r, 5, 1*time.Millisecond, nil, "args")
	if err == nil {
		t.Fatal("expected error to bubble up unchanged")
	}
	if r.calls != 1 {
		t.Errorf("expected exactly 1 call (no retry on unrelated error), got %d", r.calls)
	}
}

func TestRetryOnSANotFound_FirstAttemptSucceeds(t *testing.T) {
	r := &retryFakeRunner{results: []retryResult{
		{stderr: "", err: nil},
	}}
	notice := new(bytes.Buffer)
	if err := RetryOnSANotFound(r, 5, 1*time.Millisecond, notice, "args"); err != nil {
		t.Fatalf("expected success, got %v", err)
	}
	if notice.Len() != 0 {
		t.Errorf("expected no propagation notice on first-try success, got %q", notice.String())
	}
}
