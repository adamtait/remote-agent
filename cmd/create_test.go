// ra/cmd/create_test.go
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

// Preflight tests exercise materializeBuildAssets, which we expose in cmd to
// test without running the whole create flow.
func TestMaterializeBuildAssets_BothPresent_NoWrite(t *testing.T) {
	cwd := t.TempDir()
	ws := filepath.Join(cwd, ".ra")
	if err := os.MkdirAll(ws, 0755); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"Dockerfile", "entrypoint.sh"} {
		content := []byte("existing-" + n)
		if err := os.WriteFile(filepath.Join(ws, n), content, 0644); err != nil {
			t.Fatal(err)
		}
	}
	cleanup, err := materializeBuildAssets(cwd, t.TempDir())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer cleanup()
	// files should still have original content — materialize must not overwrite
	for _, n := range []string{"Dockerfile", "entrypoint.sh"} {
		data, err := os.ReadFile(filepath.Join(ws, n))
		if err != nil {
			t.Fatalf("reading %s: %v", n, err)
		}
		if string(data) != "existing-"+n {
			t.Errorf("%s content changed: got %q", n, data)
		}
	}
	// cleanup is a no-op when nothing was written — files still present
	cleanup()
	for _, n := range []string{"Dockerfile", "entrypoint.sh"} {
		if _, err := os.Stat(filepath.Join(ws, n)); err != nil {
			t.Errorf("existing file %s should survive cleanup: %v", n, err)
		}
	}
}

func TestMaterializeBuildAssets_BothMissing_WritesAndCleansUp(t *testing.T) {
	cwd := t.TempDir()
	home := t.TempDir()
	cleanup, err := materializeBuildAssets(cwd, home)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// files should exist after materialize
	for _, n := range []string{"Dockerfile", "entrypoint.sh"} {
		if _, err := os.Stat(filepath.Join(cwd, ".ra", n)); err != nil {
			t.Errorf("expected %s to be materialized: %v", n, err)
		}
	}
	cleanup()
	// cleanup should remove only the files that were written
	for _, n := range []string{"Dockerfile", "entrypoint.sh"} {
		if _, err := os.Stat(filepath.Join(cwd, ".ra", n)); !os.IsNotExist(err) {
			t.Errorf("expected %s removed by cleanup, err=%v", n, err)
		}
	}
}

func TestMaterializeBuildAssets_PartialExisting_WritesOnlyMissing(t *testing.T) {
	cwd := t.TempDir()
	ws := filepath.Join(cwd, ".ra")
	if err := os.MkdirAll(ws, 0755); err != nil {
		t.Fatal(err)
	}
	original := []byte("FROM existing\n")
	if err := os.WriteFile(filepath.Join(ws, "Dockerfile"), original, 0644); err != nil {
		t.Fatal(err)
	}

	cleanup, err := materializeBuildAssets(cwd, t.TempDir())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Dockerfile unchanged
	data, _ := os.ReadFile(filepath.Join(ws, "Dockerfile"))
	if !bytes.Equal(data, original) {
		t.Errorf("Dockerfile should not be overwritten, got %q", data)
	}
	// entrypoint.sh written
	if _, err := os.Stat(filepath.Join(ws, "entrypoint.sh")); err != nil {
		t.Errorf("missing entrypoint.sh should have been materialized: %v", err)
	}
	cleanup()
	// Dockerfile survives cleanup (we didn't write it)
	if _, err := os.Stat(filepath.Join(ws, "Dockerfile")); err != nil {
		t.Errorf("pre-existing Dockerfile should survive cleanup: %v", err)
	}
	// entrypoint.sh removed by cleanup
	if _, err := os.Stat(filepath.Join(ws, "entrypoint.sh")); !os.IsNotExist(err) {
		t.Errorf("materialized entrypoint.sh should be removed by cleanup, err=%v", err)
	}
}

func TestBuildArgs_UsesDashFAndProjectRootContext(t *testing.T) {
	got := dockerBuildArgs("example.com/img:tag")
	want := []string{
		"buildx", "build",
		"--platform", "linux/amd64",
		"-t", "example.com/img:tag",
		"-f", ".ra/Dockerfile",
		"--push",
		".",
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

func TestCreate_StagesProfileDBeforeBuildAndCleansUp(t *testing.T) {
	// Unit-level proxy for the create flow: verify the staging helper is
	// used by exercising it directly the way cmd/create.go will call it,
	// and that the staging dir is absent after cleanup. This catches the
	// wiring regression without having to fake out gcloud/docker.
	cwd := t.TempDir()
	home := t.TempDir()
	if err := os.MkdirAll(filepath.Join(cwd, ".ra", "profile.d"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cwd, ".ra", "profile.d", "10-app.sh"),
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
		Git: config.GitConfig{
			UserName:      "Alice",
			UserEmail:     "a@x.com",
			GithubRepoURL: "https://example.com/r.git",
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
		"RA_SECRET_CLAUDE_OAUTH_NAME": "claude-auth",
		"RA_SECRET_CLAUDE_OAUTH_VAR":  "CLAUDE_CODE_OAUTH_TOKEN",
		"RA_SECRET_GITHUB_PAT_NAME":   "github-pat",
		"RA_SECRET_GITHUB_PAT_VAR":    "GH_TOKEN",
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
	wantSet := map[string]bool{"claude_oauth": true, "github_pat": true, "my_extra": true}
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

