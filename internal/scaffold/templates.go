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

// UserProjectValues holds project-config values the user explicitly chose
// (e.g. via interactive prompts). Non-empty fields render uncommented in
// the generated YAML; empty fields fall back to the commented default
// shown by `ws setup template`.
type UserProjectValues struct {
	ProjectID     string
	GithubRepoURL string
	Region        string
	ClusterName   string
	ConfigName    string
	MachineType   string
	DiskSizeGB    string
}

// RenderProjectTemplate returns a commented .ws/config.yaml. Required
// fields (project_id, github_repo_url) are always uncommented. Optional
// fields render uncommented when set in uv, else commented with defaults.
func RenderProjectTemplate(pv ProbedValues, uv UserProjectValues) string {
	projectID := firstNonEmpty(uv.ProjectID, pv.ProjectID, "my-gcp-project")
	repoURL := firstNonEmpty(uv.GithubRepoURL, pv.GithubRepoURL, "https://github.com/my-org/my-repo.git")

	// optional renders "key: val" uncommented when user provided val,
	// else "# <commentedDefault>" (a full line already including the `# `).
	optional := func(userVal, commentedDefault string) string {
		if userVal != "" {
			// Preserve the trailing comment on the commented-default line
			// (e.g. "# region: us-central1             # optional; ...")
			// by splitting on the first `#` boundary that isn't the leading one.
			key, trailingComment := splitCommentedDefault(commentedDefault)
			if trailingComment != "" {
				return fmt.Sprintf("%s: %s  %s", key, userVal, trailingComment)
			}
			return fmt.Sprintf("%s: %s", key, userVal)
		}
		return commentedDefault
	}

	var b strings.Builder
	fmt.Fprintf(&b, `# ws project config (.ws/config.yaml) — committed to git
# Required fields have no default; uncomment and fill them in.
# Optional fields show the default ws will apply if left unset.

project_id: %s        # REQUIRED — probed: %s
github_repo_url: %s   # REQUIRED — probed: %s

%s
%s
%s
%s
%s
# auto_sleep_hours: 2             # hours idle before stopping (0=never sleep; 1–24; GCP default: 2)
# auto_shutdown_hours: 12         # hours running before force-stop (1–168; GCP default: ~12)
`,
		projectID, probedOr(pv.ProjectID),
		repoURL, probedOr(pv.GithubRepoURL),
		optional(uv.Region, "# region: us-central1             # optional; falls back to global default, then us-central1"),
		optional(uv.ClusterName, "# cluster_name: claude-dev-cluster"),
		optional(uv.ConfigName, "# config_name:  claude-dev-config"),
		optional(uv.MachineType, "# machine_type: e2-standard-4"),
		optional(uv.DiskSizeGB, "# disk_size_gb: 50"),
	)
	return b.String()
}

// splitCommentedDefault parses a commented default line of the form
// "# key: val [  # trailing comment]" and returns ("key", "# trailing comment")
// (trailingComment may be empty). The returned key has no leading `# `.
func splitCommentedDefault(line string) (key, trailingComment string) {
	// Strip leading "# " (or "#").
	s := strings.TrimPrefix(line, "# ")
	s = strings.TrimPrefix(s, "#")
	// Split on a `#` preceded by whitespace to find trailing comment.
	if i := strings.Index(s, "  #"); i >= 0 {
		trailingComment = strings.TrimSpace(s[i:])
		s = s[:i]
	}
	// key is everything before the first `:`.
	if c := strings.Index(s, ":"); c >= 0 {
		return strings.TrimSpace(s[:c]), trailingComment
	}
	return strings.TrimSpace(s), trailingComment
}

// UserGlobalValues holds global-config values the user explicitly chose.
// Non-empty fields render uncommented under `defaults:` in the generated
// YAML; empty fields stay commented.
type UserGlobalValues struct {
	Region       string
	GitUserName  string
	GitUserEmail string
}

// RenderGlobalTemplate returns a commented ~/.config/ws/config.yaml with
// defaults and projects sections. Fields set in uv render uncommented.
func RenderGlobalTemplate(pv ProbedValues, uv UserGlobalValues) string {
	regionDefault := firstNonEmpty(pv.Region, "us-central1")
	gitName := quoteOrPlaceholder(pv.GitUserName)
	gitEmail := quoteOrPlaceholder(pv.GitUserEmail)

	// For git_user_name / git_user_email: if the user provided a value,
	// quote it and render uncommented. Otherwise keep the probed-or-
	// placeholder form, commented.
	renderRegion := "# region: " + regionDefault
	if uv.Region != "" {
		renderRegion = "region: " + uv.Region
	}
	renderGitName := "# git_user_name:  " + gitName
	if uv.GitUserName != "" {
		renderGitName = fmt.Sprintf("git_user_name:  %q", uv.GitUserName)
	}
	renderGitEmail := "# git_user_email: " + gitEmail
	if uv.GitUserEmail != "" {
		renderGitEmail = fmt.Sprintf("git_user_email: %q", uv.GitUserEmail)
	}

	var b strings.Builder
	fmt.Fprintf(&b, `# ws global config — user-wide defaults and per-project overrides
# All fields optional; uncomment what you want to set.

defaults:
  %s
  %s
  %s
  # auto_sleep_hours: 2           # hours idle before stopping (0=never sleep; 1–24; GCP default: 2)
  # auto_shutdown_hours: 12       # hours running before force-stop (1–168; GCP default: ~12)

# projects:
#   my-gcp-project:
#     service_account_name: claude-workstation-sa
#     image_name:           claude-code-workstation
#     image_tag:            latest
#     repo_name:            claude-workstation-images
#     auto_sleep_hours:   1
#     auto_shutdown_hours: 24
`, renderRegion, renderGitName, renderGitEmail)
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
