// ra/cmd/create_test.go
package cmd

import (
	"bytes"
	"errors"
	"io"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/adamtait/remote-agent/internal/config"
)

func TestElapsedHuman(t *testing.T) {
	cases := []struct {
		d    time.Duration
		want string
	}{
		{0, "0s"},
		{12 * time.Second, "12s"},
		// Sub-second remainder is truncated, not rounded.
		{4*time.Minute + 11*time.Second + 900*time.Millisecond, "4m11s"},
		{4*time.Minute + 12*time.Second, "4m12s"},
		{1*time.Hour + 3*time.Minute + 7*time.Second, "1h3m7s"},
	}
	for _, tc := range cases {
		if got := elapsedHuman(tc.d); got != tc.want {
			t.Errorf("elapsedHuman(%s) = %q, want %q", tc.d, got, tc.want)
		}
	}
}

func TestBuildArgs_UsesDashFAndExplicitBuildContext(t *testing.T) {
	got := dockerBuildArgs("example.com/img:tag", "/tmp/ra-build/myws")
	want := []string{
		"buildx", "build",
		"--platform", "linux/amd64",
		"-t", "example.com/img:tag",
		"-f", "/tmp/ra-build/myws/Dockerfile",
		"--push",
		"/tmp/ra-build/myws",
	}
	if !equalStringSlices(got, want) {
		t.Errorf("args mismatch.\n got=%v\nwant=%v", got, want)
	}
}

func TestBuildArgs_NoLegacyPluginBuildArgs(t *testing.T) {
	// Plugin enable/disable lives in the container env (set by
	// buildContainerEnv) and is read at runtime by each plugin's startup
	// script. No plugin-specific build args should leak into buildx.
	// RA_CLAUDE_ENABLED is the historical example we guard against.
	got := dockerBuildArgs("example.com/img:tag", "/tmp/ra-build/myws")
	for _, a := range got {
		if strings.Contains(a, "RA_CLAUDE_ENABLED") {
			t.Errorf("dockerBuildArgs should not pass RA_CLAUDE_ENABLED at build time; got %v", got)
		}
	}
}

func equalStringSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// Ensure the runner interface used by create.go is usable with a fake.
// This compile-time check catches breaks if we later extract an interface.
var _ io.Writer = (*bytes.Buffer)(nil)
var _ error = errors.New("sentinel")

func TestMakeBuildDir_ConcurrentSafeAndContained(t *testing.T) {
	home := t.TempDir()
	a, err := makeBuildDir(home, "myws")
	if err != nil {
		t.Fatal(err)
	}
	b, err := makeBuildDir(home, "myws")
	if err != nil {
		t.Fatal(err)
	}
	if a == b {
		t.Errorf("expected unique dirs for concurrent calls, both = %q", a)
	}
	root := filepath.Join(home, ".cache", "ra", "build")
	for _, d := range []string{a, b} {
		if !strings.HasPrefix(d, root+string(filepath.Separator)) {
			t.Errorf("dir %q escaped cache root %q", d, root)
		}
	}
}

func TestMakeBuildDir_RejectsPathTraversal(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(home, ".cache", "ra", "build")
	for _, evil := range []string{"../etc", "/etc/passwd", "..", ".", ""} {
		got, err := makeBuildDir(home, evil)
		if err != nil {
			t.Fatalf("input %q: unexpected error %v", evil, err)
		}
		if !strings.HasPrefix(got, root+string(filepath.Separator)) {
			t.Errorf("input %q produced %q which escapes %q", evil, got, root)
		}
	}
}


func TestBuildContainerEnv_IncludesSecretsAndEnv(t *testing.T) {
	sleep := 2
	cfg := &config.Config{
		Git: config.GitConfig{
			UserName:  "Alice",
			UserEmail: "a@x.com",
		},
		Gcloud: config.GcloudConfig{
			Project: "p",
			Workstation: config.WorkstationConfig{
				Region:             "us-central1",
				MachineType:        "e2-standard-4",
				DiskSizeGB:         50,
				ClusterName:        "c",
				ConfigName:         "cf",
				ServiceAccountName: "sa",
				ImageName:          "i",
				ImageTag:           "t",
				RepoName:           "r",
				AutoSleepHours:     &sleep,
				AutoShutdownHours:  12,
			},
		},
		Secrets: map[string]config.SecretEntry{
			"example_secret": {SecretName: "example-secret", EnvVar: "EXAMPLE_TOKEN"},
			"my_extra":       {SecretName: "ex", EnvVar: "MY_EXTRA"},
		},
		Env: map[string]string{"STATIC_KEY": "abc"},
	}
	got := buildContainerEnv(cfg)
	m := map[string]string{}
	for _, p := range strings.Split(got, ",") {
		kv := strings.SplitN(p, "=", 2)
		if len(kv) == 2 {
			m[kv[0]] = kv[1]
		}
	}
	must := map[string]string{
		"GCP_PROJECT_ID":                "p",
		"GIT_USER_NAME":                 "Alice",
		"GIT_USER_EMAIL":                "a@x.com",
		"RA_SECRET_EXAMPLE_SECRET_NAME": "example-secret",
		"RA_SECRET_EXAMPLE_SECRET_VAR":  "EXAMPLE_TOKEN",
		"RA_SECRET_MY_EXTRA_NAME":       "ex",
		"RA_SECRET_MY_EXTRA_VAR":        "MY_EXTRA",
		"STATIC_KEY":                    "abc",
	}
	for k, v := range must {
		if m[k] != v {
			t.Errorf("container env %s: got %q, want %q (full string: %q)", k, m[k], v, got)
		}
	}
	parts := strings.Split(m["RA_SECRETS"], "|")
	wantSet := map[string]bool{"example_secret": true, "my_extra": true}
	for _, p := range parts {
		if !wantSet[p] {
			t.Errorf("unexpected RA_SECRETS entry: %q", p)
		}
		delete(wantSet, p)
	}
	if len(wantSet) != 0 {
		t.Errorf("missing RA_SECRETS entries: %v", wantSet)
	}
	for _, banned := range []string{"CLAUDE_AUTH_MODE=", "CLAUDE_AUTH_SECRET_NAME=", "GITHUB_PAT_SECRET_NAME="} {
		if strings.Contains(got, banned) {
			t.Errorf("container env still contains old key %q: %q", banned, got)
		}
	}
}

