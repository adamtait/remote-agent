// ra/internal/scaffold/templates.go
package scaffold

import (
	"fmt"
	"strings"

	"github.com/adamtait/remote-agent/internal/config"
)

// UserValues are the prompt-gathered inputs for ~/.config/ra/config.yaml.
type UserValues struct {
	GitUserName   string
	GitUserEmail  string
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
}

// ProjectValues are the prompt-gathered inputs for .ra/config.yaml.
type ProjectValues struct {
	ProjectID string
}

// Seed defaults applied when a UserValues field is zero.
const (
	seedRegion = "us-central1"
	// 2 vCPU / 8 GiB. Sufficient for editor + Claude Code + light builds.
	// Bump per-project (e.g., e2-standard-4/8) when running heavy compilers
	// or test suites — workstations bill per minute while running, so a
	// smaller default keeps idle costs low.
	seedMachineType        = "e2-standard-2"
	seedDiskSizeGB         = 50
	seedClusterName        = "ra-dev-cluster"
	seedServiceAccountName = "ra-workstation-sa"
	seedImageName          = "ra-workstation"
	seedImageTag           = "latest"
	seedRepoName           = "ra-workstation-images"
	seedAutoSleepHours     = 2
	seedAutoShutdownHours  = 12
	placeholderGitUserName = "your-name"
	placeholderGitEmail    = "your-email@example.com"
	placeholderProjectID   = "my-gcp-project"
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
	projectID := orStr(uv.GcloudProject, placeholderProjectID)

	region := orStr(uv.Region, seedRegion)
	machine := orStr(uv.MachineType, seedMachineType)
	disk := orInt(uv.DiskSizeGB, seedDiskSizeGB)
	cluster := orStr(uv.ClusterName, seedClusterName)
	conf := orStr(uv.ConfigName, config.ConfigName(config.CurrentUsername()))
	sa := orStr(uv.ServiceAccountName, seedServiceAccountName)
	img := orStr(uv.ImageName, seedImageName)
	tag := orStr(uv.ImageTag, seedImageTag)
	repo := orStr(uv.RepoName, seedRepoName)
	sleep := orInt(uv.AutoSleepHours, seedAutoSleepHours)
	shutdown := orInt(uv.AutoShutdownHours, seedAutoShutdownHours)

	var b strings.Builder
	fmt.Fprintf(&b, `# ra user config — baseline for all ra commands.
# Every required key is set here. Project configs (.ra/config.yaml)
# override per-key.

git:
  user_name: %q
  user_email: %q

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

# connect — client-side settings for 'ra connect'. login_command, when set, is
# run in place of the login shell so a session lands directly in it. Empty (the
# default) opens a plain interactive login shell. Example (attach tmux, with a
# TERM fallback so it works from any terminal):
#   connect:
#     login_command: "sh -c 'infocmp -- \"$TERM\" >/dev/null 2>&1 || export TERM=xterm-256color; exec tmux new-session -A -s main'"
connect:
  login_command: ""

# plugins — installed via 'ra plugin install <git-url-or-path>'. Each
# plugin contributes its own config block under plugins.<name>.*.
plugins: {}

# secrets — populated by `+"`ra plugin install`"+` for plugins that declare them.
# Each entry has shape {secret_name, env_var}. Logical names matter to
# plugin scripts (e.g. the github plugin reads RA_SECRET_GITHUB_PAT_NAME).
secrets: {}

# env — plain static env vars forwarded to the container (no secret fetch).
env: {}
`,
		gitName, gitEmail,
		projectID,
		region, machine, disk, cluster, conf, sa, img, tag, repo, sleep, shutdown)
	return b.String()
}

// RenderProjectConfig returns a minimal .ra/config.yaml with the
// required project key and commented-out override examples.
func RenderProjectConfig(pv ProjectValues) string {
	projectID := orStr(pv.ProjectID, placeholderProjectID)

	var b strings.Builder
	fmt.Fprintf(&b, `# ra project config — committed to source control.
# Precedence: project overrides user. See user config for defaults and
# the full secrets/env schema.

gcloud:
  project: %s

# Uncomment any of the groups below to override user values for this project.
#
# git:
#   user_name: Project-Specific Name
#   user_email: project@example.com
#
# gcloud:
#   workstation:
#     machine_type: e2-standard-8
#     disk_size_gb: 100
#     auto_sleep_hours: 1
#
# plugins:
#   <plugin-name>:
#     enabled: false        # project-level false overrides user-level true
#
# secrets:
#   my_extra_secret:
#     secret_name: my-extra-secret
#     env_var: MY_EXTRA
#
# env:
#   MY_STATIC_VAR: foo
`, projectID)
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
