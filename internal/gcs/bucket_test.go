package gcs_test

import (
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/adamtait/remote-agent/internal/gcs"
)

type fakeRunner struct {
	calls    [][]string
	checks   map[string]bool
	runErr   error
	captures map[string]string // joined-args → stderr to return
	captErr  map[string]error  // joined-args → error to return
}

func (f *fakeRunner) key(args []string) string { return strings.Join(args, " ") }

func (f *fakeRunner) Run(args ...string) error {
	f.calls = append(f.calls, args)
	return f.runErr
}
func (f *fakeRunner) RunWithStdin(_ io.Reader, args ...string) error {
	f.calls = append(f.calls, args)
	return f.runErr
}
func (f *fakeRunner) RunSoft(args ...string) error { return f.Run(args...) }
func (f *fakeRunner) RunCapture(args ...string) (string, error) {
	f.calls = append(f.calls, args)
	k := f.key(args)
	if err, ok := f.captErr[k]; ok {
		return f.captures[k], err
	}
	return "", nil
}
func (f *fakeRunner) Output(args ...string) ([]byte, error) {
	f.calls = append(f.calls, args)
	return nil, nil
}
func (f *fakeRunner) Check(args ...string) bool {
	f.calls = append(f.calls, args)
	if v, ok := f.checks[f.key(args)]; ok {
		return v
	}
	return false
}

func TestValidateBucketName(t *testing.T) {
	cases := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{"valid lowercase", "ra-claude-my-proj-12345678", false},
		{"valid all digits", "012345678901", false},
		{"too short", "ab", true},
		{"too long", strings.Repeat("a", 64), true},
		{"uppercase rejected", "RA-Claude-Bad", true},
		{"underscore rejected", "ra_claude_x", true},
		{"dot rejected", "ra.claude.x", true},
		{"starts with hyphen", "-ra-claude-x", true},
		{"ends with hyphen", "ra-claude-x-", true},
		{"consecutive hyphens", "ra--claude", true},
		{"empty", "", true},
		{"two-char valid pattern but too short", "ab", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := gcs.ValidateBucketName(tc.input)
			if tc.wantErr && err == nil {
				t.Errorf("expected error for %q, got nil", tc.input)
			}
			if !tc.wantErr && err != nil {
				t.Errorf("expected no error for %q, got %v", tc.input, err)
			}
		})
	}
}

func TestValidateBucketName_DerivedNamesAlwaysValid(t *testing.T) {
	// The names we auto-derive must always pass validation; otherwise
	// turning the plugin on alone would break ra create.
	for _, p := range []string{"p", "my-proj", "abc-123-xyz"} {
		for _, e := range []string{"a@x.com", "User+Tag@Sub.Domain.tld", ""} {
			name := gcs.BucketName(p, e)
			if err := gcs.ValidateBucketName(name); err != nil {
				t.Errorf("BucketName(%q,%q)=%q failed validation: %v", p, e, name, err)
			}
		}
	}
}

func TestBucketName_Deterministic(t *testing.T) {
	a := gcs.BucketName("my-proj", "Adam@Example.com")
	b := gcs.BucketName("my-proj", "  adam@example.com  ")
	if a != b {
		t.Errorf("expected case/whitespace-insensitive equality, got %q vs %q", a, b)
	}
}

func TestBucketName_Format(t *testing.T) {
	name := gcs.BucketName("my-proj", "adam@example.com")
	if !strings.HasPrefix(name, "ra-claude-my-proj-") {
		t.Errorf("unexpected prefix: %q", name)
	}
	suffix := strings.TrimPrefix(name, "ra-claude-my-proj-")
	if len(suffix) != 8 {
		t.Errorf("expected 8-char hex suffix, got %q (len=%d)", suffix, len(suffix))
	}
	for _, c := range suffix {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			t.Errorf("non-hex char %q in suffix %q", c, suffix)
		}
	}
}

func TestEnsureBucket_AlreadyExists(t *testing.T) {
	f := &fakeRunner{
		checks: map[string]bool{
			"storage buckets describe gs://b --project p": true,
		},
	}
	if err := gcs.EnsureBucket(f, "p", "b", "us-central1"); err != nil {
		t.Fatal(err)
	}
	if len(f.calls) != 1 {
		t.Errorf("expected only the describe call, got %d calls: %v", len(f.calls), f.calls)
	}
}

func TestEnsureBucket_CreatesWhenMissing(t *testing.T) {
	f := &fakeRunner{
		checks: map[string]bool{
			"storage buckets describe gs://b --project p": false,
		},
	}
	if err := gcs.EnsureBucket(f, "p", "b", "us-central1"); err != nil {
		t.Fatal(err)
	}
	if len(f.calls) != 2 {
		t.Fatalf("expected describe + create, got %d: %v", len(f.calls), f.calls)
	}
	create := f.calls[1]
	want := []string{
		"storage", "buckets", "create", "gs://b",
		"--project", "p",
		"--location", "us-central1",
		"--uniform-bucket-level-access",
		"--quiet",
	}
	if strings.Join(create, " ") != strings.Join(want, " ") {
		t.Errorf("create args mismatch:\n got  %v\n want %v", create, want)
	}
}

func TestEnsureBucket_PropagatesCreateError(t *testing.T) {
	f := &fakeRunner{
		checks: map[string]bool{
			"storage buckets describe gs://b --project p": false,
		},
		runErr: errors.New("boom"),
	}
	if err := gcs.EnsureBucket(f, "p", "b", "us-central1"); err == nil {
		t.Fatal("expected error to propagate")
	}
}

func TestGrantObjectAdmin_FirstAttemptSucceeds(t *testing.T) {
	f := &fakeRunner{}
	if err := gcs.GrantObjectAdmin(f, 3, time.Millisecond, io.Discard, "b", "sa@p.iam.gserviceaccount.com"); err != nil {
		t.Fatal(err)
	}
	if len(f.calls) != 1 {
		t.Errorf("expected single attempt, got %d: %v", len(f.calls), f.calls)
	}
	args := f.calls[0]
	want := []string{
		"storage", "buckets", "add-iam-policy-binding", "gs://b",
		"--member=serviceAccount:sa@p.iam.gserviceaccount.com",
		"--role=roles/storage.objectAdmin",
		"--condition=None",
	}
	if strings.Join(args, " ") != strings.Join(want, " ") {
		t.Errorf("binding args mismatch:\n got  %v\n want %v", args, want)
	}
}

func TestGrantObjectAdmin_RetriesOnSANotFound(t *testing.T) {
	key := "storage buckets add-iam-policy-binding gs://b --member=serviceAccount:sa@p.iam.gserviceaccount.com --role=roles/storage.objectAdmin --condition=None"
	f := &fakeRunner{
		captures: map[string]string{key: "ERROR: service account sa@p.iam.gserviceaccount.com does not exist"},
		captErr:  map[string]error{key: errors.New("exit status 1")},
	}
	// Use very short delay so the test is fast.
	err := gcs.GrantObjectAdmin(f, 2, time.Millisecond, io.Discard, "b", "sa@p.iam.gserviceaccount.com")
	if err == nil {
		t.Fatal("expected eventual failure after exhausting retries")
	}
	if len(f.calls) != 2 {
		t.Errorf("expected 2 attempts, got %d", len(f.calls))
	}
}
