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

// slowFakeRunner returns the same (stderr, err) from every RunCapture, but only
// after sleeping dur — used to exercise RetryOnConnDrop's maxAge guard.
type slowFakeRunner struct {
	dur    time.Duration
	stderr string
	err    error
	calls  int
}

func (f *slowFakeRunner) Run(args ...string) error                           { return nil }
func (f *slowFakeRunner) RunWithStdin(stdin io.Reader, args ...string) error { return nil }
func (f *slowFakeRunner) RunSoft(args ...string) error                       { return nil }
func (f *slowFakeRunner) Output(args ...string) ([]byte, error)              { return nil, nil }
func (f *slowFakeRunner) Check(args ...string) bool                          { return false }
func (f *slowFakeRunner) RunCapture(args ...string) (string, error) {
	f.calls++
	time.Sleep(f.dur)
	return f.stderr, f.err
}

var _ Runner = (*slowFakeRunner)(nil)

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

func TestRetryOnConnDrop_RetriesPastTheSSLEOF(t *testing.T) {
	transient := errors.New("exit status 1")
	// The exact symptom reported for `ra connect` against a freshly-started
	// workstation, followed by a clean connect once sshd is up.
	r := &retryFakeRunner{results: []retryResult{
		{stderr: "ERROR: Error connecting to workstation: [SSL: UNEXPECTED_EOF_WHILE_READING] EOF occurred in violation of protocol (_ssl.c:1081)", err: transient},
		{stderr: "kex_exchange_identification: Connection closed by remote host", err: transient},
		{stderr: "", err: nil},
	}}
	notice := new(bytes.Buffer)
	if err := RetryOnConnDrop(r, 6, 1*time.Millisecond, time.Minute, notice, "workstations", "ssh"); err != nil {
		t.Fatalf("expected success after retries, got %v", err)
	}
	if r.calls != 3 {
		t.Errorf("expected 3 calls, got %d", r.calls)
	}
	if !strings.Contains(notice.String(), "not accepting connections yet") {
		t.Errorf("expected a retry notice, got %q", notice.String())
	}
}

func TestRetryOnConnDrop_DoesNotRetryCleanFailure(t *testing.T) {
	// A non-zero exit that is NOT a connection drop (e.g. the user's remote
	// command failed, or an auth error) must surface immediately.
	r := &retryFakeRunner{results: []retryResult{
		{stderr: "ERROR: PERMISSION_DENIED: caller lacks workstations.workstations.use", err: errors.New("exit status 1")},
		{stderr: "", err: nil}, // never reached
	}}
	if err := RetryOnConnDrop(r, 6, 1*time.Millisecond, time.Minute, nil, "workstations", "ssh"); err == nil {
		t.Fatal("expected the error to bubble up unchanged")
	}
	if r.calls != 1 {
		t.Errorf("expected exactly 1 call (no retry on unrelated failure), got %d", r.calls)
	}
}

func TestRetryOnConnDrop_DoesNotRetryMidSessionDrop(t *testing.T) {
	// A connection-drop pattern that surfaces only after the attempt ran longer
	// than maxAge is a session ending, not a failed connect — surface it without
	// reconnecting under the user.
	r := &slowFakeRunner{
		dur:    20 * time.Millisecond,
		stderr: "client_loop: send disconnect: Connection reset by peer",
		err:    errors.New("exit status 255"),
	}
	if err := RetryOnConnDrop(r, 6, 1*time.Millisecond, 5*time.Millisecond, nil, "workstations", "ssh"); err == nil {
		t.Fatal("expected the mid-session drop to surface unchanged")
	}
	if r.calls != 1 {
		t.Errorf("expected exactly 1 call (no reconnect on a long-lived drop), got %d", r.calls)
	}
}
