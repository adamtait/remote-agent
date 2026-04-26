// ra/internal/scaffold/templates.go
package scaffold

import (
	"fmt"
	"strings"
)

// UserValues are the prompt-gathered inputs for ~/.config/ra/config.yaml.
type UserValues struct {
	GitUserName   string
	GitUserEmail  string
	GithubRepoURL string
	GcloudProject string

	Region             string
	MachineType        string
	DiskSizeGB         int
	ClusterName        string
	ConfigName         string
	ServiceAccountName string
	ImageName          string
	ImageTag           string
	RepoName           string
	AutoSleepHours     int
	AutoShutdownHours  int

	// PluginFragments holds [configBlock, secretsBlock] pairs contributed by
	// each registered plugin during interactive setup.
	PluginFragments [][2]string
}

// ProjectValues are the prompt-gathered inputs for .ra/config.yaml.
type ProjectValues struct {
	ProjectID     string
	GithubRepoURL string
}

// Seed defaults applied when a UserValues field is zero.
const (
	seedRegion             = "us-central1"
	seedMachineType        = "e2-standard-4"
	seedDiskSizeGB         = 50
	seedClusterName        = "claude-dev-cluster"
	seedConfigName         = "claude-dev-config"
	seedServiceAccountName = "claude-workstation-sa"
	seedImageName          = "claude-code-workstation"
	seedImageTag           = "latest"
	seedRepoName           = "claude-workstation-images"
	seedAutoSleepHours     = 2
	seedAutoShutdownHours  = 12
	placeholderGitUserName = "your-name"
	placeholderGitEmail    = "your-email@example.com"
	placeholderProjectID   = "my-gcp-project"
	placeholderRepoURL     = "https://github.com/my-org/my-repo.git"
)

func orStr(v, seed string) string {
	if v == "" {
		return seed
	}
	return v
}

func orInt(v, seed int) int {
	if v == 0 {
		return seed
	}
	return v
}

// RenderUserConfig returns a fully-populated ~/.config/ra/config.yaml.
func RenderUserConfig(uv UserValues) string {
	gitName := orStr(uv.GitUserName, placeholderGitUserName)
	gitEmail := orStr(uv.GitUserEmail, placeholderGitEmail)
	repoURL := orStr(uv.GithubRepoURL, placeholderRepoURL)
	projectID := orStr(uv.GcloudProject, placeholderProjectID)

	region := orStr(uv.Region, seedRegion)
	machine := orStr(uv.MachineType, seedMachineType)
	disk := orInt(uv.DiskSizeGB, seedDiskSizeGB)
	cluster := orStr(uv.ClusterName, seedClusterName)
	conf := orStr(uv.ConfigName, seedConfigName)
	sa := orStr(uv.ServiceAccountName, seedServiceAccountName)
	img := orStr(uv.ImageName, seedImageName)
	tag := orStr(uv.ImageTag, seedImageTag)
	repo := orStr(uv.RepoName, seedRepoName)
	sleep := orInt(uv.AutoSleepHours, seedAutoSleepHours)
	shutdown := orInt(uv.AutoShutdownHours, seedAutoShutdownHours)

	var configPluginBlocks strings.Builder
	var secretsPluginBlocks strings.Builder
	for _, frag := range uv.PluginFragments {
		configPluginBlocks.WriteString(frag[0])
		secretsPluginBlocks.WriteString(frag[1])
	}

	var b strings.Builder
	fmt.Fprintf(&b, `# ra user config — baseline for all ra commands.
# Every required key is set here. Project configs (.ra/config.yaml)
# override per-key.

git:
  user_name: %q
  user_email: %q
  github_repo_url: %s

gcloud:
  project: %s
  workstation:
    region: %s
    machine_type: %s
    disk_size_gb: %d
    cluster_name: %s
    config_name: %s
    service_account_name: %s
    image_name: %s
    image_tag: %s
    repo_name: %s
    auto_sleep_hours: %d
    auto_shutdown_hours: %d

# claude — toggles the Claude Code harness in the workstation image.
# When enabled=false the @anthropic-ai/claude-code npm package is not
# installed and claude_oauth / claude_apikey secrets must not be set.
# When enabled=true, auth_mode selects which claude_* secret is required:
#   auth_mode=oauth   → requires secrets.claude_oauth
#   auth_mode=apikey  → requires secrets.claude_apikey
%s
# secrets — map GCP Secret Manager secrets to container env vars. Each
# entry has the same {secret_name, env_var} shape. ws recognizes these
# logical names and wires them into credential helpers; any other name
# is treated generically (fetched at boot, exported as env_var).
#
#   claude_oauth  → (claude.enabled=true, auth_mode=oauth) fetches at
#                   boot, exports env_var (CLAUDE_CODE_OAUTH_TOKEN) and
#                   seeds ~/.claude.json onboarding if
#                   oauth_account_secret_name is set.
#   claude_apikey → (claude.enabled=true, auth_mode=apikey) installs
#                   Claude Code's apiKeyHelper — re-fetches every ~5 min
#                   (rotation without restart). env_var is the canonical
#                   name (ANTHROPIC_API_KEY) for documentation.
#   github_pat    → installs git's credential helper (fresh fetch per git
#                   op) AND exports env_var (GH_TOKEN) for `+"`gh`"+` CLI.
#                   Env var is stale on rotation; git operations stay fresh.
#
# Other logical names: plain fetch + export. Rotation requires ra stop/start.
secrets:
%s  github_pat:
    secret_name: github-pat
    env_var: GH_TOKEN

# env — plain static env vars forwarded to the container (no secret fetch).
env: {}
`,
		gitName, gitEmail, repoURL,
		projectID,
		region, machine, disk, cluster, conf, sa, img, tag, repo, sleep, shutdown,
		configPluginBlocks.String(),
		secretsPluginBlocks.String())
	return b.String()
}

// RenderProjectConfig returns a minimal .ra/config.yaml with the two
// required project keys and commented-out override examples.
func RenderProjectConfig(pv ProjectValues) string {
	projectID := orStr(pv.ProjectID, placeholderProjectID)
	repoURL := orStr(pv.GithubRepoURL, placeholderRepoURL)

	var b strings.Builder
	fmt.Fprintf(&b, `# ra project config — committed to source control.
# Precedence: project overrides user. See user config for defaults and
# the full secrets/env schema.

git:
  github_repo_url: %s

gcloud:
  project: %s

# Uncomment any of the groups below to override user values for this project.
#
# gcloud:
#   workstation:
#     machine_type: e2-standard-8
#     disk_size_gb: 100
#     auto_sleep_hours: 1
#
# claude:
#   enabled: false          # project-level false overrides user-level true
#   # auth_mode: oauth      # only meaningful when enabled: true
#
# secrets:
#   my_extra_secret:
#     secret_name: my-extra-secret
#     env_var: MY_EXTRA
#
# env:
#   MY_STATIC_VAR: foo
`, repoURL, projectID)
	return b.String()
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
