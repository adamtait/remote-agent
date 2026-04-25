package scaffold_test

import (
	"strings"
	"testing"

	"github.com/adamtait/remote-agent/internal/scaffold"
)

func TestRenderUserConfig_EmitsAllRequiredKeysUncommented(t *testing.T) {
	uv := scaffold.UserValues{
		GitUserName:  "Alice",
		GitUserEmail: "alice@example.com",
		Region:       "us-central1", MachineType: "e2-standard-4", DiskSizeGB: 50,
		ClusterName: "claude-dev-cluster", ConfigName: "claude-dev-config",
		ServiceAccountName: "claude-workstation-sa",
		ImageName:          "claude-code-workstation",
		ImageTag:           "latest",
		RepoName:           "claude-workstation-images",
		AutoSleepHours:     2,
		AutoShutdownHours:  12,
		ClaudeEnabled:      true,
		ClaudeAuthMode:     scaffold.ClaudeAuthOAuth,
	}
	got := scaffold.RenderUserConfig(uv)
	for _, want := range []string{
		`git:`,
		`  user_name: "Alice"`,
		`  user_email: "alice@example.com"`,
		`  github_repo_url: https://github.com/my-org/my-repo.git`,
		`gcloud:`,
		`  project: my-gcp-project`,
		`  workstation:`,
		`    region: us-central1`,
		`    machine_type: e2-standard-4`,
		`    disk_size_gb: 50`,
		`    cluster_name: claude-dev-cluster`,
		`    config_name: claude-dev-config`,
		`    service_account_name: claude-workstation-sa`,
		`    image_name: claude-code-workstation`,
		`    image_tag: latest`,
		`    repo_name: claude-workstation-images`,
		`    auto_sleep_hours: 2`,
		`    auto_shutdown_hours: 12`,
		`claude:`,
		`  enabled: true`,
		`  auth_mode: oauth`,
		`secrets:`,
		`  claude_oauth:`,
		`    secret_name: claude-code-oauth-token`,
		`    env_var: CLAUDE_CODE_OAUTH_TOKEN`,
		`  github_pat:`,
		`    secret_name: github-pat`,
		`    env_var: GH_TOKEN`,
		`env: {}`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("user config missing %q\n---\n%s", want, got)
		}
	}
}

func TestRenderUserConfig_APIKeyModeSelectsApiKeyEntry(t *testing.T) {
	uv := scaffold.UserValues{ClaudeEnabled: true, ClaudeAuthMode: scaffold.ClaudeAuthAPIKey}
	got := scaffold.RenderUserConfig(uv)
	if !strings.Contains(got, "claude_apikey:") {
		t.Errorf("apikey mode did not write claude_apikey entry:\n%s", got)
	}
	if strings.Contains(got, "claude_oauth:") {
		t.Errorf("apikey mode also wrote claude_oauth entry:\n%s", got)
	}
	if !strings.Contains(got, "env_var: ANTHROPIC_API_KEY") {
		t.Errorf("apikey env_var should be ANTHROPIC_API_KEY:\n%s", got)
	}
	if !strings.Contains(got, "auth_mode: apikey") {
		t.Errorf("auth_mode: apikey not emitted:\n%s", got)
	}
}

func TestRenderUserConfig_ClaudeDisabled_OmitsClaudeSecrets(t *testing.T) {
	uv := scaffold.UserValues{ClaudeEnabled: false}
	got := scaffold.RenderUserConfig(uv)
	if !strings.Contains(got, "claude:\n  enabled: false") {
		t.Errorf("expected enabled: false block:\n%s", got)
	}
	if strings.Contains(got, "claude_oauth:") || strings.Contains(got, "claude_apikey:") {
		t.Errorf("disabled mode must not emit claude_* secret entries:\n%s", got)
	}
	if strings.Contains(got, "auth_mode:") {
		t.Errorf("disabled mode must not emit auth_mode:\n%s", got)
	}
	// github_pat is shared, still required.
	if !strings.Contains(got, "github_pat:") {
		t.Errorf("github_pat entry must still be emitted when claude disabled:\n%s", got)
	}
}

func TestRenderProjectConfig_EmitsRequiredKeysAndCommentedExamples(t *testing.T) {
	pv := scaffold.ProjectValues{
		ProjectID:     "my-proj",
		GithubRepoURL: "https://example.com/r.git",
	}
	got := scaffold.RenderProjectConfig(pv)
	for _, want := range []string{
		`git:`,
		`  github_repo_url: https://example.com/r.git`,
		`gcloud:`,
		`  project: my-proj`,
		`# gcloud:`,
		`#   workstation:`,
		`#     machine_type: e2-standard-8`,
		`# claude:`,
		`#   enabled: false`,
		`# secrets:`,
		`# env:`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("project config missing %q\n---\n%s", want, got)
		}
	}
}

func TestRenderProjectConfig_ZeroValuesStillShowCommentedExamples(t *testing.T) {
	got := scaffold.RenderProjectConfig(scaffold.ProjectValues{})
	for _, want := range []string{`# gcloud:`, `#   workstation:`, `# claude:`, `# secrets:`, `# env:`} {
		if !strings.Contains(got, want) {
			t.Errorf("missing commented example %q", want)
		}
	}
	if !strings.Contains(got, "project:") || !strings.Contains(got, "github_repo_url:") {
		t.Error("required keys should still be present as placeholders")
	}
}
