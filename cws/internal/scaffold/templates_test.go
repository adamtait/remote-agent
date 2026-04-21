// cws/internal/scaffold/templates_test.go
package scaffold_test

import (
	"strings"
	"testing"

	"github.com/adamtait/remote-agent/cws/internal/scaffold"
)

func TestRenderProjectTemplate_WithProbes(t *testing.T) {
	pv := scaffold.ProbedValues{
		ProjectID:     "my-proj",
		GithubRepoURL: "https://github.com/a/b.git",
	}
	got := scaffold.RenderProjectTemplate(pv)

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
	got := scaffold.RenderProjectTemplate(scaffold.ProbedValues{})
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
