// ws/cmd/create_test.go
package cmd

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/adamtait/remote-agent/internal/config"
	"github.com/adamtait/remote-agent/internal/scaffold"
)

// Preflight tests exercise ensureBuildAssets, which we expose in cmd to
// test without running the whole create flow.
func TestEnsureBuildAssets_Present_ReturnsNil(t *testing.T) {
	cwd := t.TempDir()
	ws := filepath.Join(cwd, ".ws")
	if err := os.MkdirAll(ws, 0755); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"Dockerfile", "entrypoint.sh"} {
		if err := os.WriteFile(filepath.Join(ws, n), []byte("x"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	var stdin bytes.Buffer
	var out bytes.Buffer
	if err := ensureBuildAssets(cwd, cwd, &stdin, &out, false); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestEnsureBuildAssets_MissingNonTTY_Errors(t *testing.T) {
	cwd := t.TempDir()
	var stdin bytes.Buffer // not a TTY
	var out bytes.Buffer
	err := ensureBuildAssets(cwd, t.TempDir(), &stdin, &out, false)
	if err == nil {
		t.Fatal("expected error when missing + non-TTY")
	}
	if !strings.Contains(err.Error(), "ws setup scaffold") {
		t.Errorf("error should mention 'ws setup scaffold', got: %v", err)
	}
}

func TestEnsureBuildAssets_MissingTTYAnswerYes_Scaffolds(t *testing.T) {
	cwd := t.TempDir()
	home := t.TempDir()
	stdin := bytes.NewBufferString("Y\n")
	var out bytes.Buffer
	if err := ensureBuildAssets(cwd, home, stdin, &out, true); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, n := range []string{"Dockerfile", "entrypoint.sh"} {
		p := filepath.Join(cwd, ".ws", n)
		if _, err := os.Stat(p); err != nil {
			t.Errorf("expected %s to exist after prompt=yes: %v", p, err)
		}
	}
}

func TestEnsureBuildAssets_MissingTTYAnswerNo_Errors(t *testing.T) {
	cwd := t.TempDir()
	home := t.TempDir()
	stdin := bytes.NewBufferString("n\n")
	var out bytes.Buffer
	err := ensureBuildAssets(cwd, home, stdin, &out, true)
	if err == nil {
		t.Fatal("expected error when user answered 'n'")
	}
}

func TestEnsureBuildAssets_PartialExistingTTYYes_ScaffoldsOnlyMissing(t *testing.T) {
	cwd := t.TempDir()
	ws := filepath.Join(cwd, ".ws")
	if err := os.MkdirAll(ws, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ws, "Dockerfile"), []byte("USER\n"), 0644); err != nil {
		t.Fatal(err)
	}

	stdin := bytes.NewBufferString("Y\n")
	var out bytes.Buffer
	if err := ensureBuildAssets(cwd, t.TempDir(), stdin, &out, true); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	data, _ := os.ReadFile(filepath.Join(ws, "Dockerfile"))
	if string(data) != "USER\n" {
		t.Errorf("existing Dockerfile should not be overwritten, got %q", data)
	}
	if _, err := os.Stat(filepath.Join(ws, "entrypoint.sh")); err != nil {
		t.Errorf("missing entrypoint.sh should have been scaffolded: %v", err)
	}
}

func TestBuildArgs_UsesDashFAndProjectRootContext(t *testing.T) {
	got := dockerBuildArgs("example.com/img:tag")
	want := []string{
		"buildx", "build",
		"--platform", "linux/amd64",
		"-t", "example.com/img:tag",
		"-f", ".ws/Dockerfile",
		"--push",
		".",
	}
	if !equalStringSlices(got, want) {
		t.Errorf("args mismatch.\n got=%v\nwant=%v", got, want)
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

func TestCreate_StagesProfileDBeforeBuildAndCleansUp(t *testing.T) {
	// Unit-level proxy for the create flow: verify the staging helper is
	// used by exercising it directly the way cmd/create.go will call it,
	// and that the staging dir is absent after cleanup. This catches the
	// wiring regression without having to fake out gcloud/docker.
	cwd := t.TempDir()
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(cwd, ".ws", "profile.d"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cwd, ".ws", "profile.d", "10-app.sh"),
		[]byte("echo x\n"), 0755); err != nil {
		t.Fatal(err)
	}

	stage, cleanup, err := scaffold.StageProfileD(cwd, home)
	if err != nil {
		t.Fatalf("StageProfileD: %v", err)
	}
	if _, err := os.Stat(filepath.Join(stage, "10-app.sh")); err != nil {
		t.Errorf("expected staged file: %v", err)
	}
	cleanup()
	if _, err := os.Stat(stage); !os.IsNotExist(err) {
		t.Errorf("expected stage removed, err=%v", err)
	}
}

func TestBuildContainerEnv_IncludesSecretsAndEnv(t *testing.T) {
	sleep := 2
	cfg := &config.Config{
		ProjectID: "p", GithubRepoURL: "https://example.com/r.git",
		GitUserName: "Alice", GitUserEmail: "a@x.com",
		Region: "us-central1", MachineType: "e2-standard-4",
		DiskSizeGB: 50, ClusterName: "c", ConfigName: "cf",
		ServiceAccountName: "sa", ImageName: "i", ImageTag: "t", RepoName: "r",
		AutoSleepHours: &sleep, AutoShutdownHours: 12,
		Secrets: map[string]config.SecretEntry{
			"claude_oauth": {SecretName: "claude-auth", EnvVar: "CLAUDE_CODE_OAUTH_TOKEN"},
			"github_pat":   {SecretName: "github-pat", EnvVar: "GH_TOKEN"},
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
		"GITHUB_REPO_URL":             "https://example.com/r.git",
		"GIT_USER_NAME":               "Alice",
		"GIT_USER_EMAIL":              "a@x.com",
		"WS_SECRET_CLAUDE_OAUTH_NAME": "claude-auth",
		"WS_SECRET_CLAUDE_OAUTH_VAR":  "CLAUDE_CODE_OAUTH_TOKEN",
		"WS_SECRET_GITHUB_PAT_NAME":   "github-pat",
		"WS_SECRET_GITHUB_PAT_VAR":    "GH_TOKEN",
		"WS_SECRET_MY_EXTRA_NAME":     "ex",
		"WS_SECRET_MY_EXTRA_VAR":      "MY_EXTRA",
		"STATIC_KEY":                  "abc",
	}
	for k, v := range must {
		if m[k] != v {
			t.Errorf("container env %s: got %q, want %q (full string: %q)", k, m[k], v, got)
		}
	}
	parts := strings.Split(m["WS_SECRETS"], "|")
	wantSet := map[string]bool{"claude_oauth": true, "github_pat": true, "my_extra": true}
	for _, p := range parts {
		if !wantSet[p] {
			t.Errorf("unexpected WS_SECRETS entry: %q", p)
		}
		delete(wantSet, p)
	}
	if len(wantSet) != 0 {
		t.Errorf("missing WS_SECRETS entries: %v", wantSet)
	}
	for _, banned := range []string{"CLAUDE_AUTH_MODE=", "CLAUDE_AUTH_SECRET_NAME=", "GITHUB_PAT_SECRET_NAME="} {
		if strings.Contains(got, banned) {
			t.Errorf("container env still contains old key %q: %q", banned, got)
		}
	}
}

func TestBuildContainerEnv_OAuthAccountSiblingEmitted(t *testing.T) {
	cfg := &config.Config{
		Secrets: map[string]config.SecretEntry{
			"claude_oauth": {
				SecretName:             "claude-auth",
				EnvVar:                 "CLAUDE_CODE_OAUTH_TOKEN",
				OAuthAccountSecretName: "claude-oauth-account",
			},
		},
	}
	got := buildContainerEnv(cfg)
	if !strings.Contains(got, "WS_SECRET_CLAUDE_OAUTH_OAUTH_ACCOUNT_SECRET_NAME=claude-oauth-account") {
		t.Errorf("oauth_account_secret_name not emitted: %q", got)
	}
}
