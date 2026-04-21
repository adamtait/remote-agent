// ws/internal/scaffold/templates.go
package scaffold

import (
	"fmt"
	"strings"
)

// ProbedValues carries values discovered from the user's environment,
// used to pre-fill both templates and interactive defaults.
type ProbedValues struct {
	ProjectID     string
	Region        string
	GitUserName   string
	GitUserEmail  string
	GithubRepoURL string
}

const probedNone = "(none — set manually)"

func probedOr(v string) string {
	if v == "" {
		return probedNone
	}
	return v
}

// RenderProjectTemplate returns a commented .ws.yaml with required
// fields filled from probes (or placeholders) and optional fields commented.
func RenderProjectTemplate(pv ProbedValues) string {
	projectID := pv.ProjectID
	if projectID == "" {
		projectID = "my-gcp-project"
	}
	repoURL := pv.GithubRepoURL
	if repoURL == "" {
		repoURL = "https://github.com/my-org/my-repo.git"
	}

	var b strings.Builder
	fmt.Fprintf(&b, `# ws project config — committed to the repo root
# Required fields have no default; uncomment and fill them in.
# Optional fields show the default ws will apply if left unset.

project_id: %s        # REQUIRED — probed: %s
github_repo_url: %s   # REQUIRED — probed: %s

# region: us-central1             # optional; falls back to global default, then us-central1
# cluster_name: claude-dev-cluster
# config_name:  claude-dev-config
# machine_type: e2-standard-4
# disk_size_gb: 50
`, projectID, probedOr(pv.ProjectID), repoURL, probedOr(pv.GithubRepoURL))
	return b.String()
}

// RenderGlobalTemplate returns a commented ~/.config/ws.yaml with
// defaults and projects sections.
func RenderGlobalTemplate(pv ProbedValues) string {
	var b strings.Builder
	fmt.Fprintf(&b, `# ws global config — user-wide defaults and per-project overrides
# All fields optional; uncomment what you want to set.

defaults:
  # region: %s
  # git_user_name:  %s
  # git_user_email: %s

# projects:
#   my-gcp-project:
#     service_account_name: claude-workstation-sa
#     image_name:           claude-code-workstation
#     image_tag:            latest
#     repo_name:            claude-workstation-images
`, firstNonEmpty(pv.Region, "us-central1"), quoteOrPlaceholder(pv.GitUserName), quoteOrPlaceholder(pv.GitUserEmail))
	return b.String()
}

// quoteOrPlaceholder returns Go-quoted v for YAML-safe output when v is
// non-empty; otherwise it returns the unquoted probedNone sentinel (so the
// en-dash isn't mangled into \u2014).
func quoteOrPlaceholder(v string) string {
	if v == "" {
		return probedNone
	}
	return fmt.Sprintf("%q", v)
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
