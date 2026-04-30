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
		ClusterName: "ra-dev-cluster", ConfigName: "ra-dev-config",
		ServiceAccountName: "ra-workstation-sa",
		ImageName:          "ra-workstation",
		ImageTag:           "latest",
		RepoName:           "ra-workstation-images",
		AutoSleepHours:     2,
		AutoShutdownHours:  12,
	}
	got := scaffold.RenderUserConfig(uv)
	for _, want := range []string{
		`git:`,
		`  user_name: "Alice"`,
		`  user_email: "alice@example.com"`,
		`gcloud:`,
		`  project: my-gcp-project`,
		`  workstation:`,
		`    region: us-central1`,
		`    machine_type: e2-standard-4`,
		`    disk_size_gb: 50`,
		`    cluster_name: ra-dev-cluster`,
		`    config_name: ra-dev-config`,
		`    service_account_name: ra-workstation-sa`,
		`    image_name: ra-workstation`,
		`    image_tag: latest`,
		`    repo_name: ra-workstation-images`,
		`    auto_sleep_hours: 2`,
		`    auto_shutdown_hours: 12`,
		`plugins: {}`,
		`secrets: {}`,
		`env: {}`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("user config missing %q\n---\n%s", want, got)
		}
	}
}

func TestRenderUserConfig_ZeroValues_HasDefaults(t *testing.T) {
	got := scaffold.RenderUserConfig(scaffold.UserValues{})
	// Should contain seed defaults.
	for _, want := range []string{
		`project: my-gcp-project`,
		`region: us-central1`,
		`machine_type: e2-standard-4`,
		`plugins: {}`,
		`secrets: {}`,
		`env: {}`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("zero-value user config missing %q\n---\n%s", want, got)
		}
	}
}

func TestRenderProjectConfig_EmitsRequiredKeysAndCommentedExamples(t *testing.T) {
	pv := scaffold.ProjectValues{
		ProjectID: "my-proj",
	}
	got := scaffold.RenderProjectConfig(pv)
	for _, want := range []string{
		`gcloud:`,
		`  project: my-proj`,
		`# git:`,
		`# gcloud:`,
		`#   workstation:`,
		`#     machine_type: e2-standard-8`,
		`# plugins:`,
		`#   <plugin-name>:`,
		`#     enabled: false`,
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
	for _, want := range []string{`# gcloud:`, `#   workstation:`, `# plugins:`, `#   <plugin-name>:`, `# secrets:`, `# env:`} {
		if !strings.Contains(got, want) {
			t.Errorf("missing commented example %q", want)
		}
	}
	if !strings.Contains(got, "project:") {
		t.Error("required keys should still be present as placeholders")
	}
}
