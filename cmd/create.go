package cmd

import (
	"fmt"
	"math/rand"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/adamtait/remote-agent/internal/config"
	"github.com/adamtait/remote-agent/internal/naming"
	"github.com/adamtait/remote-agent/internal/scaffold"
	"github.com/adamtait/remote-agent/internal/secrets"
	"github.com/spf13/cobra"
)

func logicalNameToEnvToken(logical string) string {
	return strings.ToUpper(logical)
}

func buildContainerEnv(cfg *config.Config) string {
	var pairs []string
	add := func(k, v string) { pairs = append(pairs, k+"="+v) }

	add("GCP_PROJECT_ID", cfg.Gcloud.Project)
	add("GIT_USER_NAME", cfg.Git.UserName)
	add("GIT_USER_EMAIL", cfg.Git.UserEmail)
	for _, pair := range reg.AllContainerEnvPairs(cfg) {
		pairs = append(pairs, pair)
	}

	logicalNames := make([]string, 0, len(cfg.Secrets))
	for k := range cfg.Secrets {
		logicalNames = append(logicalNames, k)
	}
	sort.Strings(logicalNames)
	add("RA_SECRETS", strings.Join(logicalNames, "|"))
	for _, logical := range logicalNames {
		s := cfg.Secrets[logical]
		tok := logicalNameToEnvToken(logical)
		add("RA_SECRET_"+tok+"_NAME", s.SecretName)
		add("RA_SECRET_"+tok+"_VAR", s.EnvVar)
	}

	envKeys := make([]string, 0, len(cfg.Env))
	for k := range cfg.Env {
		envKeys = append(envKeys, k)
	}
	sort.Strings(envKeys)
	for _, k := range envKeys {
		add(k, cfg.Env[k])
	}

	return strings.Join(pairs, ",")
}

var createCmd = &cobra.Command{
	Use:   "create",
	Short: "Provision a Google Cloud Workstation",
	Long: `Always provisions a new workstation. Cluster, config, image, secrets,
and IAM setup remain idempotent; the workstation itself is freshly created on
every run. Use --name to set the workstation name (auto-suffixed on collision);
omit it for an auto-generated memorable name.
Requires .ra/config.yaml in the project root with git.github_repo_url and gcloud.project set.`,
	RunE: runCreate,
}

func init() {
	rootCmd.AddCommand(createCmd)
	createCmd.Flags().Int("auto-sleep", 0, "Hours idle before stopping (0=never sleep, omit=GCP default ~2h; max 24)")
	createCmd.Flags().Int("auto-shutdown", 0, "Hours running before force-stop (omit=GCP default ~12h; 1–168)")
	createCmd.Flags().String("name", "", "Workstation name (auto-suffixed on collision); auto-generated memorable name if unset")
}

func runCreate(cmd *cobra.Command, args []string) error {
	ws := &cfg.Gcloud.Workstation

	for field, val := range map[string]string{
		"git_user_name":  cfg.Git.UserName,
		"git_user_email": cfg.Git.UserEmail,
	} {
		if strings.ContainsAny(val, ",=") {
			return fmt.Errorf("%s must not contain ',' or '=' (gcloud --container-env limitation): %q", field, val)
		}
	}

	if cmd.Flags().Changed("auto-sleep") {
		v, _ := cmd.Flags().GetInt("auto-sleep")
		ws.AutoSleepHours = &v
	}
	if cmd.Flags().Changed("auto-shutdown") {
		v, _ := cmd.Flags().GetInt("auto-shutdown")
		ws.AutoShutdownHours = v
	}

	if ws.AutoSleepHours != nil {
		h := *ws.AutoSleepHours
		if h < 0 || h > 24 {
			return fmt.Errorf("auto_sleep_hours must be 0 (never) or 1–24; got %d", h)
		}
	}
	if ws.AutoShutdownHours != 0 {
		if ws.AutoShutdownHours < 1 || ws.AutoShutdownHours > 168 {
			return fmt.Errorf("auto_shutdown_hours must be 1–168; got %d", ws.AutoShutdownHours)
		}
	}

	r := runner

	imageURI := fmt.Sprintf("%s-docker.pkg.dev/%s/%s/%s:%s",
		ws.Region, cfg.Gcloud.Project, ws.RepoName, ws.ImageName, ws.ImageTag)
	serviceAccountEmail := fmt.Sprintf("%s@%s.iam.gserviceaccount.com",
		ws.ServiceAccountName, cfg.Gcloud.Project)

	logStep := func(msg string) { fmt.Printf("\033[1;34m==>\033[0m %s\n", msg) }

	// Step 1 — Enable APIs
	logStep("Enabling required GCP APIs...")
	if err := r.Run("services", "enable",
		"workstations.googleapis.com",
		"artifactregistry.googleapis.com",
		"secretmanager.googleapis.com",
		"iam.googleapis.com",
		"--project", cfg.Gcloud.Project, "--quiet"); err != nil {
		return fmt.Errorf("enabling APIs: %w", err)
	}

	// reauth.googleapis.com is only enableable by orgs that have been
	// allowlisted for it; on other projects Service Usage rejects it with
	// "not available to this consumer". It's only needed when the caller's
	// org enforces a session-length/reauth policy — gcloud calls it during
	// OAuth token refresh, and commands like `gcloud workstations ssh` fail
	// with SERVICE_DISABLED if that policy is on and the API is off. Try
	// best-effort; surface a warning but don't block the rest of setup.
	if err := r.Run("services", "enable", "reauth.googleapis.com",
		"--project", cfg.Gcloud.Project, "--quiet"); err != nil {
		fmt.Printf("\033[1;33mWARN:\033[0m could not enable reauth.googleapis.com (%v).\n"+
			"      This is only required if your org enforces a session/reauth policy.\n"+
			"      If `gcloud workstations ssh` later fails with SERVICE_DISABLED for\n"+
			"      reauth.googleapis.com, ask an org admin to enable it.\n", err)
	}

	// Step 2 — Artifact Registry repo
	logStep(fmt.Sprintf("Ensuring Artifact Registry repo '%s'...", ws.RepoName))
	if !r.Check("artifacts", "repositories", "describe", ws.RepoName, "--location", ws.Region, "--project", cfg.Gcloud.Project) {
		if err := r.Run("artifacts", "repositories", "create", ws.RepoName,
			"--repository-format=docker",
			"--location", ws.Region,
			"--description=ra workstation images",
			"--project", cfg.Gcloud.Project); err != nil {
			return fmt.Errorf("creating artifact registry repo: %w", err)
		}
	}

	// Step 3 — Build and push image
	logStep(fmt.Sprintf("Building image %s...", imageURI))

	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("determining home directory: %w", err)
	}
	cwd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("determining working directory: %w", err)
	}
	_, buildCleanup, err := scaffold.AssembleBuildContext(scaffold.BuildContextOptions{
		Cwd:     cwd,
		Home:    home,
		Plugins: reg.All(),
	})
	if err != nil {
		return fmt.Errorf("assembling build context: %w", err)
	}
	defer buildCleanup()

	if err := r.Run("auth", "configure-docker", fmt.Sprintf("%s-docker.pkg.dev", ws.Region), "--quiet"); err != nil {
		return fmt.Errorf("configuring docker auth: %w", err)
	}
	if err := (dockerRunner{}).Run(dockerBuildArgs(imageURI)...); err != nil {
		return fmt.Errorf("building and pushing image: %w", err)
	}

	// Step 4 — Secrets: ensure every configured secret exists in GCP.
	logStep(fmt.Sprintf("Ensuring %d GCP secret(s)...", len(cfg.Secrets)))
	secretLogicalOrder := make([]string, 0, len(cfg.Secrets))
	for k := range cfg.Secrets {
		secretLogicalOrder = append(secretLogicalOrder, k)
	}
	sort.Strings(secretLogicalOrder)
	for _, logical := range secretLogicalOrder {
		s := cfg.Secrets[logical]
		prompt := fmt.Sprintf("Enter value for secret %q (env var %s)", s.SecretName, s.EnvVar)
		if err := secrets.EnsureSecret(r, cfg.Gcloud.Project, s.SecretName, prompt, os.Stderr, os.Stdin); err != nil {
			return fmt.Errorf("ensuring secret %s: %w", s.SecretName, err)
		}
	}

	// Step 5 — Service account and IAM
	logStep(fmt.Sprintf("Ensuring service account '%s'...", serviceAccountEmail))
	if !r.Check("iam", "service-accounts", "describe", serviceAccountEmail, "--project", cfg.Gcloud.Project) {
		if err := r.Run("iam", "service-accounts", "create", ws.ServiceAccountName,
			"--display-name=Claude Workstation SA",
			"--project", cfg.Gcloud.Project); err != nil {
			return fmt.Errorf("creating service account: %w", err)
		}
	}

	// Bind secrets
	secretNames := make([]string, 0, len(cfg.Secrets))
	seen := map[string]bool{}
	for _, logical := range secretLogicalOrder {
		s := cfg.Secrets[logical]
		if !seen[s.SecretName] {
			secretNames = append(secretNames, s.SecretName)
			seen[s.SecretName] = true
		}
	}
	for _, secret := range secretNames {
		if err := r.Run("secrets", "add-iam-policy-binding", secret,
			"--member=serviceAccount:"+serviceAccountEmail,
			"--role=roles/secretmanager.secretAccessor",
			"--project", cfg.Gcloud.Project,
			"--condition=None"); err != nil {
			return fmt.Errorf("binding secret %s IAM: %w", secret, err)
		}
	}

	// Bind Artifact Registry repo
	logStep(fmt.Sprintf("Granting artifactregistry.reader on '%s'...", ws.RepoName))
	if err := r.Run("artifacts", "repositories", "add-iam-policy-binding", ws.RepoName,
		"--location", ws.Region,
		"--member=serviceAccount:"+serviceAccountEmail,
		"--role=roles/artifactregistry.reader",
		"--project", cfg.Gcloud.Project,
		"--condition=None"); err != nil {
		return fmt.Errorf("binding artifact registry IAM: %w", err)
	}

	// Bind project-level roles
	logStep("Granting logWriter and metricWriter on project...")
	for _, role := range []string{"roles/logging.logWriter", "roles/monitoring.metricWriter"} {
		if err := r.Run("projects", "add-iam-policy-binding", cfg.Gcloud.Project,
			"--member=serviceAccount:"+serviceAccountEmail,
			"--role="+role,
			"--condition=None"); err != nil {
			return fmt.Errorf("binding project role %s: %w", role, err)
		}
	}

	// Step 6 — Workstations cluster
	logStep(fmt.Sprintf("Ensuring workstations cluster '%s'...", ws.ClusterName))
	if !r.Check("workstations", "clusters", "describe", ws.ClusterName, "--region", ws.Region, "--project", cfg.Gcloud.Project) {
		fmt.Printf("\033[1;33mWARN:\033[0m Creating cluster — this takes ~20 minutes on first run.\n")
		if err := r.Run("workstations", "clusters", "create", ws.ClusterName,
			"--region", ws.Region,
			"--project", cfg.Gcloud.Project); err != nil {
			return fmt.Errorf("creating workstations cluster: %w", err)
		}
	}

	// Step 7 — Workstations config
	logStep(fmt.Sprintf("Ensuring workstations config '%s'...", ws.ConfigName))
	containerEnv := buildContainerEnv(cfg)

	configExists := r.Check("workstations", "configs", "describe", ws.ConfigName,
		"--cluster", ws.ClusterName, "--region", ws.Region, "--project", cfg.Gcloud.Project)

	if configExists {
		logStep("Config exists — updating image, env, service account, and scopes...")
		updateArgs := []string{
			"workstations", "configs", "update", ws.ConfigName,
			"--cluster", ws.ClusterName, "--region", ws.Region, "--project", cfg.Gcloud.Project,
			"--container-custom-image=" + imageURI,
			"--service-account=" + serviceAccountEmail,
			"--service-account-scopes=https://www.googleapis.com/auth/cloud-platform",
			"--container-env=" + containerEnv,
		}
		if ws.AutoSleepHours != nil {
			updateArgs = append(updateArgs, fmt.Sprintf("--idle-timeout=%d", *ws.AutoSleepHours*3600))
		}
		if ws.AutoShutdownHours > 0 {
			updateArgs = append(updateArgs, fmt.Sprintf("--running-timeout=%d", ws.AutoShutdownHours*3600))
		}
		if err := r.Run(updateArgs...); err != nil {
			return fmt.Errorf("updating workstations config: %w", err)
		}
	} else {
		createArgs := []string{
			"workstations", "configs", "create", ws.ConfigName,
			"--cluster", ws.ClusterName, "--region", ws.Region, "--project", cfg.Gcloud.Project,
			"--machine-type=" + ws.MachineType,
			"--pd-disk-size=" + strconv.Itoa(ws.DiskSizeGB),
			"--pd-disk-type=pd-balanced",
			"--container-custom-image=" + imageURI,
			"--service-account=" + serviceAccountEmail,
			"--service-account-scopes=https://www.googleapis.com/auth/cloud-platform",
			"--container-env=" + containerEnv,
		}
		if ws.AutoSleepHours != nil {
			createArgs = append(createArgs, fmt.Sprintf("--idle-timeout=%d", *ws.AutoSleepHours*3600))
		}
		if ws.AutoShutdownHours > 0 {
			createArgs = append(createArgs, fmt.Sprintf("--running-timeout=%d", ws.AutoShutdownHours*3600))
		}
		if err := r.Run(createArgs...); err != nil {
			return fmt.Errorf("creating workstations config: %w", err)
		}
	}

	// Step 8 — Resolve a unique workstation name and create it.
	existing, err := client.List(cfg.Gcloud.Project, ws.Region, ws.ClusterName, ws.ConfigName)
	if err != nil {
		return fmt.Errorf("listing existing workstations: %w", err)
	}
	taken := make(map[string]bool, len(existing))
	for _, w := range existing {
		taken[w.Name] = true
	}

	var workstationName string
	if requested, _ := cmd.Flags().GetString("name"); requested != "" {
		workstationName = naming.UniqueSuffixed(requested, taken)
		if workstationName != requested {
			fmt.Printf("\033[1;33mWARN:\033[0m workstation %q exists — using %q instead\n", requested, workstationName)
		}
	} else {
		rng := rand.New(rand.NewSource(time.Now().UnixNano()))
		workstationName, err = naming.GenerateMemorable(cfg.WorkstationName, taken, rng)
		if err != nil {
			return fmt.Errorf("generating workstation name: %w", err)
		}
	}

	logStep(fmt.Sprintf("Creating workstation '%s'...", workstationName))
	if err := r.Run("workstations", "create", workstationName,
		"--cluster", ws.ClusterName, "--config", ws.ConfigName,
		"--region", ws.Region, "--project", cfg.Gcloud.Project); err != nil {
		return fmt.Errorf("creating workstation: %w", err)
	}

	wsInfo, err := client.Describe(cfg.Gcloud.Project, ws.Region, ws.ClusterName, ws.ConfigName, workstationName)
	if err != nil {
		return fmt.Errorf("describing workstation: %w", err)
	}
	if wsInfo.State != "STATE_RUNNING" && wsInfo.State != "STATE_STARTING" {
		logStep("Starting workstation...")
		if err := r.Run("workstations", "start", workstationName,
			"--cluster", ws.ClusterName, "--config", ws.ConfigName,
			"--region", ws.Region, "--project", cfg.Gcloud.Project); err != nil {
			return fmt.Errorf("starting workstation: %w", err)
		}
	}

	// Step 9 — Print access info
	info, err := client.Describe(cfg.Gcloud.Project, ws.Region, ws.ClusterName, ws.ConfigName, workstationName)
	if err != nil {
		return fmt.Errorf("describing workstation: %w", err)
	}

	fmt.Printf(`
================================================================
  Workstation is ready
================================================================
  Browser IDE  : https://80-%s
  Workstation  : %s
  Cluster      : %s
  Config       : %s
  Region       : %s

  SSH:
    ra connect %s

  Stop (save cost):
    ra stop %s
================================================================
`,
		info.Host,
		workstationName,
		ws.ClusterName,
		ws.ConfigName,
		ws.Region,
		workstationName,
		workstationName,
	)

	return nil
}

// dockerRunner runs docker commands with stdout/stderr forwarded to the terminal.
type dockerRunner struct{}

func (d dockerRunner) Run(args ...string) error {
	cmd := exec.Command("docker", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// dockerBuildArgs returns the buildx args used by `ra create`, centralized so
// tests can assert on the exact flags. The Dockerfile is harness-agnostic at
// build time — Claude install is gated at runtime by the `RA_PLUGIN_CLAUDE_ENABLED`
// env var emitted by the claude plugin.
func dockerBuildArgs(imageURI string) []string {
	return []string{
		"buildx", "build",
		"--platform", "linux/amd64",
		"-t", imageURI,
		"-f", ".ra/.build/Dockerfile",
		"--push",
		".ra/.build",
	}
}
