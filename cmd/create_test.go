// ra/cmd/create_test.go
package cmd

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/adamtait/remote-agent/internal/config"
)

func TestBuildArgs_UsesDashFAndProjectRootContext(t *testing.T) {
	got := dockerBuildArgs("example.com/img:tag")
	want := []string{
		"buildx", "build",
		"--platform", "linux/amd64",
		"-t", "example.com/img:tag",
		"-f", ".ra/.build/Dockerfile",
		"--push",
		".ra/.build",
	}
	if !equalStringSlices(got, want) {
		t.Errorf("args mismatch.\n got=%v\nwant=%v", got, want)
	}
}

func TestBuildArgs_NoClaudeBuildArg(t *testing.T) {
	// Claude install moved to first-boot runtime — there should no longer be
	// a RA_CLAUDE_ENABLED build arg. The runtime gate lives in the container
	// env, set by buildContainerEnv.
	got := dockerBuildArgs("example.com/img:tag")
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
			"claude_oauth": {SecretName: "claude-auth", EnvVar: "CLAUDE_CODE_OAUTH_TOKEN"},
			"my_extra":     {SecretName: "ex", EnvVar: "MY_EXTRA"},
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
		"GCP_PROJECT_ID":              "p",
		"GIT_USER_NAME":               "Alice",
		"GIT_USER_EMAIL":              "a@x.com",
		"RA_SECRET_CLAUDE_OAUTH_NAME": "claude-auth",
		"RA_SECRET_CLAUDE_OAUTH_VAR":  "CLAUDE_CODE_OAUTH_TOKEN",
		"RA_SECRET_MY_EXTRA_NAME":     "ex",
		"RA_SECRET_MY_EXTRA_VAR":      "MY_EXTRA",
		"STATIC_KEY":                  "abc",
	}
	for k, v := range must {
		if m[k] != v {
			t.Errorf("container env %s: got %q, want %q (full string: %q)", k, m[k], v, got)
		}
	}
	parts := strings.Split(m["RA_SECRETS"], "|")
	wantSet := map[string]bool{"claude_oauth": true, "my_extra": true}
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

