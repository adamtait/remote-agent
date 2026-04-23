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
	uv := scaffold.UserValues{ClaudeAuthMode: scaffold.ClaudeAuthAPIKey}
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
}

func TestRenderUserConfig_IncludesSecretsDocumentationComments(t *testing.T) {
	got := scaffold.RenderUserConfig(scaffold.UserValues{ClaudeAuthMode: scaffold.ClaudeAuthOAuth})
	for _, want := range []string{
		"claude_oauth  → fetches at boot",
		"claude_apikey → installs Claude",
		"github_pat    → installs git",
		"Exactly one of {claude_oauth, claude_apikey}",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("user config missing documentation comment %q", want)
		}
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
	for _, want := range []string{`# gcloud:`, `#   workstation:`, `# secrets:`, `# env:`} {
		if !strings.Contains(got, want) {
			t.Errorf("missing commented example %q", want)
		}
	}
	if !strings.Contains(got, "project:") || !strings.Contains(got, "github_repo_url:") {
		t.Error("required keys should still be present as placeholders")
	}
}
