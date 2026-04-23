// ws/internal/scaffold/templates_test.go
package scaffold_test

import (
	"strings"
	"testing"

	"github.com/adamtait/remote-agent/internal/scaffold"
)

func TestRenderProjectTemplate_WithProbes(t *testing.T) {
	pv := scaffold.ProbedValues{
		ProjectID:     "my-proj",
		GithubRepoURL: "https://github.com/a/b.git",
	}
	got := scaffold.RenderProjectTemplate(pv, scaffold.UserProjectValues{})

	for _, want := range []string{
		"project_id: my-proj",
		"github_repo_url: https://github.com/a/b.git",
		"# region:",
		"# cluster_name:",
		"# machine_type:",
		"# disk_size_gb:",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("RenderProjectTemplate missing %q\ngot:\n%s", want, got)
		}
	}
}

func TestRenderProjectTemplate_EmptyProbesShowPlaceholder(t *testing.T) {
	got := scaffold.RenderProjectTemplate(scaffold.ProbedValues{}, scaffold.UserProjectValues{})
	if !strings.Contains(got, "(none — set manually)") {
		t.Errorf("RenderProjectTemplate missing placeholder\ngot:\n%s", got)
	}
}

func TestRenderGlobalTemplate_WithProbes(t *testing.T) {
	pv := scaffold.ProbedValues{
		Region:       "us-central1",
		GitUserName:  "Alice",
		GitUserEmail: "alice@example.com",
	}
	got := scaffold.RenderGlobalTemplate(pv)

	for _, want := range []string{
		"defaults:",
		"# region: us-central1",
		"# git_user_name:",
		"Alice",
		"# git_user_email:",
		"alice@example.com",
		"# projects:",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("RenderGlobalTemplate missing %q\ngot:\n%s", want, got)
		}
	}
}

func TestRenderGlobalTemplate_NoProbesStillRenders(t *testing.T) {
	got := scaffold.RenderGlobalTemplate(scaffold.ProbedValues{})
	if !strings.Contains(got, "defaults:") {
		t.Errorf("RenderGlobalTemplate missing 'defaults:'\ngot:\n%s", got)
	}
}

func TestRenderGlobalTemplate_EmptyProbesDoNotMangleEnDash(t *testing.T) {
	got := scaffold.RenderGlobalTemplate(scaffold.ProbedValues{})
	// The placeholder sentinel uses a literal en-dash (U+2014). Ensure we
	// didn't %q-escape it into \u2014.
	if strings.Contains(got, `\u2014`) {
		t.Errorf("template contains escaped en-dash \\u2014:\n%s", got)
	}
	if !strings.Contains(got, "(none — set manually)") {
		t.Errorf("template missing literal placeholder with en-dash:\n%s", got)
	}
}

func TestRenderGlobalTemplate_QuotesGitIdentity(t *testing.T) {
	pv := scaffold.ProbedValues{GitUserName: "Alice Example"}
	got := scaffold.RenderGlobalTemplate(pv)
	if !strings.Contains(got, `"Alice Example"`) {
		t.Errorf("expected quoted git_user_name, got:\n%s", got)
	}
}

func TestRenderProjectTemplate_IncludesAutoSleepAndShutdown(t *testing.T) {
	got := scaffold.RenderProjectTemplate(scaffold.ProbedValues{}, scaffold.UserProjectValues{})
	for _, want := range []string{
		"# auto_sleep_hours:",
		"# auto_shutdown_hours:",
		"0=never sleep",
		"1–24",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("RenderProjectTemplate missing %q\ngot:\n%s", want, got)
		}
	}
}

func TestRenderGlobalTemplate_IncludesAutoSleepAndShutdown(t *testing.T) {
	got := scaffold.RenderGlobalTemplate(scaffold.ProbedValues{})
	for _, want := range []string{
		"# auto_sleep_hours:",
		"# auto_shutdown_hours:",
		"0=never sleep",
		"1–168",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("RenderGlobalTemplate missing %q\ngot:\n%s", want, got)
		}
	}
}

func TestRenderProjectTemplate_UserValuesRenderUncommented(t *testing.T) {
	pv := scaffold.ProbedValues{ProjectID: "probed-proj"}
	uv := scaffold.UserProjectValues{
		ProjectID:     "chosen-proj",
		GithubRepoURL: "https://github.com/a/b.git",
		Region:        "us-east1",
		ClusterName:   "my-cluster",
		ConfigName:    "my-config",
		MachineType:   "e2-standard-8",
		DiskSizeGB:    "100",
	}
	got := scaffold.RenderProjectTemplate(pv, uv)

	// User-provided values: uncommented.
	for _, want := range []string{
		"project_id: chosen-proj",
		"github_repo_url: https://github.com/a/b.git",
		"region: us-east1",
		"cluster_name: my-cluster",
		"config_name: my-config",
		"machine_type: e2-standard-8",
		"disk_size_gb: 100",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing uncommented %q\ngot:\n%s", want, got)
		}
	}
	// Probed hint still surfaces even when user overrides.
	if !strings.Contains(got, "probed: probed-proj") {
		t.Errorf("probed hint lost:\n%s", got)
	}
}

func TestRenderProjectTemplate_EmptyUserValuesStayCommented(t *testing.T) {
	got := scaffold.RenderProjectTemplate(
		scaffold.ProbedValues{},
		scaffold.UserProjectValues{
			ProjectID:     "chosen-proj",
			GithubRepoURL: "https://github.com/a/b.git",
		},
	)
	for _, want := range []string{
		"# region:",
		"# cluster_name:",
		"# config_name:",
		"# machine_type:",
		"# disk_size_gb:",
		"# auto_sleep_hours:",
		"# auto_shutdown_hours:",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing commented default %q\ngot:\n%s", want, got)
		}
	}
	// Negative check: no uncommented versions of these keys.
	for _, unwant := range []string{
		"\nregion:",
		"\ncluster_name:",
		"\nmachine_type:",
		"\ndisk_size_gb:",
	} {
		if strings.Contains(got, unwant) {
			t.Errorf("unexpected uncommented key %q\ngot:\n%s", unwant, got)
		}
	}
}
