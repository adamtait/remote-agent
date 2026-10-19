package gcs_test

import (
	"context"
	"encoding/json"
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
func (f *fakeRunner) RunContext(_ context.Context, args ...string) error {
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
func (f *fakeRunner) OutputQuiet(args ...string) ([]byte, error) {
	return f.Output(args...)
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

func TestResolveBucketName_TemplateExpansion(t *testing.T) {
	cases := []struct {
		tmpl, proj, email, want string
	}{
		{"ra-claude-{project_id}-{user_email_hash}", "my-proj", "adam@example.com", ""}, // checked below
		{"static-bucket-name", "my-proj", "a@x.com", "static-bucket-name"},
		{"{project_id}-only", "p", "", "p-only"},
	}
	for _, tc := range cases {
		got, err := gcs.ResolveBucketName(tc.tmpl, tc.proj, tc.email)
		if err != nil {
			t.Fatalf("ResolveBucketName(%q): unexpected error %v", tc.tmpl, err)
		}
		if tc.want != "" && got != tc.want {
			t.Errorf("ResolveBucketName(%q) = %q, want %q", tc.tmpl, got, tc.want)
		}
		if !strings.Contains(got, "{") {
			continue
		}
		t.Errorf("ResolveBucketName(%q) = %q still contains an unresolved placeholder", tc.tmpl, got)
	}
}

func TestResolveBucketName_DeterministicHash(t *testing.T) {
	a, _ := gcs.ResolveBucketName("p-{user_email_hash}", "p", "Adam@Example.com")
	b, _ := gcs.ResolveBucketName("p-{user_email_hash}", "p", "  adam@example.com  ")
	if a != b {
		t.Errorf("user_email_hash should be case/whitespace-insensitive; got %q vs %q", a, b)
	}
	suffix := strings.TrimPrefix(a, "p-")
	if len(suffix) != 8 {
		t.Errorf("expected 8-char hex hash, got %q (len=%d)", suffix, len(suffix))
	}
	for _, c := range suffix {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			t.Errorf("non-hex char %q in hash %q", c, suffix)
		}
	}
}

func TestResolveBucketName_RejectsUnknownPlaceholder(t *testing.T) {
	_, err := gcs.ResolveBucketName("p-{user_id}", "p", "a@x.com")
	if err == nil {
		t.Fatal("expected error for unknown placeholder {user_id}")
	}
	if !strings.Contains(err.Error(), "{user_id}") {
		t.Errorf("error should name the unknown placeholder, got %v", err)
	}
}

func TestResolveBucketName_DerivedNamesAlwaysValid(t *testing.T) {
	// Any bucket name produced by the generic plugin bucket template must
	// pass ValidateBucketName for every combination of project + email that
	// callers could realistically pass in.
	const tmpl = "ra-{project_id}-{user_email_hash}"
	for _, p := range []string{"p", "my-proj", "abc-123-xyz"} {
		for _, e := range []string{"a@x.com", "User+Tag@Sub.Domain.tld", ""} {
			got, err := gcs.ResolveBucketName(tmpl, p, e)
			if err != nil {
				t.Fatalf("ResolveBucketName(%q,%q,%q): %v", tmpl, p, e, err)
			}
			if err := gcs.ValidateBucketName(got); err != nil {
				t.Errorf("Resolved %q failed validation: %v", got, err)
			}
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

func TestGrantRole_FirstAttemptSucceeds(t *testing.T) {
	f := &fakeRunner{}
	if err := gcs.GrantRole(f, 3, time.Millisecond, io.Discard, "b", "sa@p.iam.gserviceaccount.com", "roles/storage.objectAdmin"); err != nil {
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

func TestGrantRole_HonorsRoleParameter(t *testing.T) {
	f := &fakeRunner{}
	if err := gcs.GrantRole(f, 1, time.Millisecond, io.Discard, "b", "sa@p.iam.gserviceaccount.com", "roles/storage.objectViewer"); err != nil {
		t.Fatal(err)
	}
	got := strings.Join(f.calls[0], " ")
	if !strings.Contains(got, "--role=roles/storage.objectViewer") {
		t.Errorf("role parameter not propagated: %v", f.calls[0])
	}
}

func TestGrantRole_RetriesOnSANotFound(t *testing.T) {
	key := "storage buckets add-iam-policy-binding gs://b --member=serviceAccount:sa@p.iam.gserviceaccount.com --role=roles/storage.objectAdmin --condition=None"
	f := &fakeRunner{
		captures: map[string]string{key: "ERROR: service account sa@p.iam.gserviceaccount.com does not exist"},
		captErr:  map[string]error{key: errors.New("exit status 1")},
	}
	// Use very short delay so the test is fast.
	err := gcs.GrantRole(f, 2, time.Millisecond, io.Discard, "b", "sa@p.iam.gserviceaccount.com", "roles/storage.objectAdmin")
	if err == nil {
		t.Fatal("expected eventual failure after exhausting retries")
	}
	if len(f.calls) != 2 {
		t.Errorf("expected 2 attempts, got %d", len(f.calls))
	}
}

func TestBuildLifecycleJSON_RendersGcsShape(t *testing.T) {
	data, err := gcs.BuildLifecycleJSON([]gcs.LifecycleRule{
		{Action: "SetStorageClass", StorageClass: "COLDLINE", AgeDays: 90, Prefixes: []string{"plugin-data/"}},
		{Action: "Delete", AgeDays: 365},
	})
	if err != nil {
		t.Fatalf("BuildLifecycleJSON: %v", err)
	}
	// Parse back into the gcloud --lifecycle-file shape and assert fields.
	var parsed struct {
		Rule []struct {
			Action struct {
				Type         string `json:"type"`
				StorageClass string `json:"storageClass"`
			} `json:"action"`
			Condition struct {
				Age           *int     `json:"age"`
				MatchesPrefix []string `json:"matchesPrefix"`
			} `json:"condition"`
		} `json:"rule"`
	}
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", err, data)
	}
	if len(parsed.Rule) != 2 {
		t.Fatalf("expected 2 rules, got %d: %s", len(parsed.Rule), data)
	}
	r0 := parsed.Rule[0]
	if r0.Action.Type != "SetStorageClass" || r0.Action.StorageClass != "COLDLINE" {
		t.Errorf("rule 0 action wrong: %+v", r0.Action)
	}
	if r0.Condition.Age == nil || *r0.Condition.Age != 90 {
		t.Errorf("rule 0 age wrong: %+v", r0.Condition.Age)
	}
	if len(r0.Condition.MatchesPrefix) != 1 || r0.Condition.MatchesPrefix[0] != "plugin-data/" {
		t.Errorf("rule 0 prefix wrong: %+v", r0.Condition.MatchesPrefix)
	}
	r1 := parsed.Rule[1]
	if r1.Action.Type != "Delete" || r1.Action.StorageClass != "" {
		t.Errorf("rule 1 action wrong: %+v", r1.Action)
	}
	if len(r1.Condition.MatchesPrefix) != 0 {
		t.Errorf("rule 1 should have no prefix condition: %+v", r1.Condition.MatchesPrefix)
	}
}

func TestBuildLifecycleJSON_Validation(t *testing.T) {
	cases := []struct {
		name  string
		rules []gcs.LifecycleRule
	}{
		{"empty", nil},
		{"unknown action", []gcs.LifecycleRule{{Action: "Archive", AgeDays: 10}}},
		{"setclass without class", []gcs.LifecycleRule{{Action: "SetStorageClass", AgeDays: 10}}},
		{"delete with class", []gcs.LifecycleRule{{Action: "Delete", StorageClass: "COLDLINE", AgeDays: 10}}},
		{"negative age", []gcs.LifecycleRule{{Action: "Delete", AgeDays: -1}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := gcs.BuildLifecycleJSON(c.rules); err == nil {
				t.Errorf("expected error for %s, got nil", c.name)
			}
		})
	}
}

func TestApplyLifecycle_NoRulesIsNoop(t *testing.T) {
	f := &fakeRunner{}
	if err := gcs.ApplyLifecycle(f, "p", "b", nil); err != nil {
		t.Fatalf("ApplyLifecycle with no rules should be a no-op, got %v", err)
	}
	if len(f.calls) != 0 {
		t.Errorf("expected no gcloud calls for empty rules, got %v", f.calls)
	}
}

func TestApplyLifecycle_RunsUpdateWithLifecycleFile(t *testing.T) {
	f := &fakeRunner{}
	err := gcs.ApplyLifecycle(f, "my-proj", "my-bucket", []gcs.LifecycleRule{
		{Action: "SetStorageClass", StorageClass: "COLDLINE", AgeDays: 90, Prefixes: []string{"plugin-data/"}},
	})
	if err != nil {
		t.Fatalf("ApplyLifecycle: %v", err)
	}
	if len(f.calls) != 1 {
		t.Fatalf("expected 1 gcloud call, got %d: %v", len(f.calls), f.calls)
	}
	got := strings.Join(f.calls[0], " ")
	for _, want := range []string{"storage", "buckets", "update", "gs://my-bucket", "--project", "my-proj", "--lifecycle-file="} {
		if !strings.Contains(got, want) {
			t.Errorf("update call missing %q: %v", want, f.calls[0])
		}
	}
}

func TestApplySoftDelete_SetsDuration(t *testing.T) {
	f := &fakeRunner{}
	if err := gcs.ApplySoftDelete(f, "my-proj", "my-bucket", 7); err != nil {
		t.Fatalf("ApplySoftDelete: %v", err)
	}
	if len(f.calls) != 1 {
		t.Fatalf("expected 1 gcloud call, got %d: %v", len(f.calls), f.calls)
	}
	got := strings.Join(f.calls[0], " ")
	want := "storage buckets update gs://my-bucket --project my-proj --soft-delete-duration=7d"
	if got != want {
		t.Errorf("call = %q, want %q", got, want)
	}
}

func TestApplySoftDelete_ZeroClears(t *testing.T) {
	f := &fakeRunner{}
	if err := gcs.ApplySoftDelete(f, "p", "b", 0); err != nil {
		t.Fatalf("ApplySoftDelete: %v", err)
	}
	if got := strings.Join(f.calls[0], " "); !strings.HasSuffix(got, "--clear-soft-delete") {
		t.Errorf("call = %q, want --clear-soft-delete", got)
	}
}

func TestApplySoftDelete_RejectsOutOfRange(t *testing.T) {
	for _, days := range []int{-1, 1, 6, 91} {
		f := &fakeRunner{}
		if err := gcs.ApplySoftDelete(f, "p", "b", days); err == nil {
			t.Errorf("days=%d: expected error", days)
		}
		if len(f.calls) != 0 {
			t.Errorf("days=%d: expected no gcloud call, got %v", days, f.calls)
		}
	}
}
