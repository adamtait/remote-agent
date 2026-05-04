package cmd

import (
	"fmt"
	"io"
	"math/rand"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/adamtait/remote-agent/internal/config"
	"github.com/adamtait/remote-agent/internal/configwrite"
	"github.com/adamtait/remote-agent/internal/gcloud"
	"github.com/adamtait/remote-agent/internal/logging"
	"github.com/adamtait/remote-agent/internal/naming"
	"github.com/adamtait/remote-agent/internal/scaffold"
	"github.com/adamtait/remote-agent/internal/secrets"
	"github.com/spf13/cobra"
)

// totalCreateSteps is the count of numbered pipeline phases in runCreate,
// rendered in the "==> [N/M] ..." progress prefix. Update if you add or
// remove a Step() call.
const totalCreateSteps = 10

// Bindings on a freshly-created service account can briefly fail with
// "Service account ... does not exist" while IAM propagates. Up to
// saBindingAttempts × saBindingDelay = 30s of backoff covers the typical
// window.
const (
	saBindingAttempts = 6
	saBindingDelay    = 5 * time.Second
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
Requires .ra/config.yaml in the project root with gcloud.project set.`,
	RunE: runCreate,
}

func init() {
	rootCmd.AddCommand(createCmd)
	createCmd.Flags().Int("auto-sleep", 0, "Hours idle before stopping (0=never sleep, omit=GCP default ~2h; max 24)")
	createCmd.Flags().Int("auto-shutdown", 0, "Hours running before force-stop (omit=GCP default ~12h; 1–168)")
	createCmd.Flags().String("name", "", "Workstation name (auto-suffixed on collision); auto-generated memorable name if unset")
}

func runCreate(cmd *cobra.Command, args []string) (retErr error) {
	startedAt := time.Now()
	ws := &cfg.Gcloud.Workstation

	requestedName, _ := cmd.Flags().GetString("name")

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

	// Auto-generate a unique config_name per project if none is set at the project level.
	// This scopes workstation listings to this project so `ra connect` finds the right one.
	if cfg.ProjectConfigPath != "" {
		if rawProject, err := config.LoadProjectConfig(cfg.ProjectConfigPath); err == nil &&
			rawProject.Gcloud.Workstation.ConfigName == "" {
			repoDir := filepath.Dir(filepath.Dir(cfg.ProjectConfigPath))
			configName := config.ConfigName(filepath.Base(repoDir))
			if origBytes, readErr := os.ReadFile(cfg.ProjectConfigPath); readErr == nil {
				updated, writeErr := configwrite.MergeConfigName(string(origBytes), configName)
				if writeErr == nil {
					writeErr = os.WriteFile(cfg.ProjectConfigPath, []byte(updated), 0644)
				}
				if writeErr != nil {
					fmt.Printf("\033[1;33mWARN:\033[0m could not save config_name to project config: %v\n", writeErr)
				} else {
					ws.ConfigName = configName
				}
			}
		}
	}

	r := runner

	imageURI := fmt.Sprintf("%s-docker.pkg.dev/%s/%s/%s:%s",
		ws.Region, cfg.Gcloud.Project, ws.RepoName, ws.ImageName, ws.ImageTag)
	serviceAccountEmail := fmt.Sprintf("%s@%s.iam.gserviceaccount.com",
		ws.ServiceAccountName, cfg.Gcloud.Project)

	// Stable order for secret iteration — used by pre-flight, step 4, and step 5.
	secretLogicalOrder := make([]string, 0, len(cfg.Secrets))
	for k := range cfg.Secrets {
		secretLogicalOrder = append(secretLogicalOrder, k)
	}
	sort.Strings(secretLogicalOrder)

	// Pipeline-failure resume hint. Registered after validation/config
	// setup so flag-validation errors (e.g. bad auto_sleep_hours) do not
	// get the misleading "earlier steps are idempotent" suffix.
	defer func() {
		if retErr == nil {
			return
		}
		hint := "re-run `ra create"
		if requestedName != "" {
			hint += fmt.Sprintf(" --name %q", requestedName)
		}
		hint += "` to resume — earlier steps are idempotent."
		fmt.Fprintf(cmd.OutOrStderr(), "\n%s\n", hint)
	}()

	// Pre-flight — collect values for any missing secrets up front so the
	// pipeline runs unattended once it starts. (Inline prompts mid-pipeline
	// force the user to babysit a 30s+ image build before they're asked.)
	type pendingSecret struct {
		gcpName string
		value   []byte
	}
	var pendingSecrets []pendingSecret
	// Register the zeroing defer before the prompt loop so values from
	// successful prompts are still zeroed if a later prompt errors.
	defer func() {
		for i := range pendingSecrets {
			for j := range pendingSecrets[i].value {
				pendingSecrets[i].value[j] = 0
			}
		}
	}()
	for _, logical := range secretLogicalOrder {
		s := cfg.Secrets[logical]
		if secrets.Exists(r, cfg.Gcloud.Project, s.SecretName) {
			fmt.Fprintf(os.Stderr, "Secret %q already exists (use 'gcloud secrets versions add' to rotate)\n", s.SecretName)
			continue
		}
		prompt := fmt.Sprintf("Enter value for secret %q (env var %s)", s.SecretName, s.EnvVar)
		value, err := secrets.PromptValue(prompt, os.Stderr, os.Stdin)
		if err != nil {
			return fmt.Errorf("prompting for secret %s: %w", s.SecretName, err)
		}
		pendingSecrets = append(pendingSecrets, pendingSecret{
			gcpName: s.SecretName,
			value:   value,
		})
	}

	// Step 1 — Enable APIs
	done := logging.Step("enable_apis", 1, totalCreateSteps, "Enabling required GCP APIs...", cmd.OutOrStdout())
	if err := r.Run("services", "enable",
		"workstations.googleapis.com",
		"artifactregistry.googleapis.com",
		"secretmanager.googleapis.com",
		"iam.googleapis.com",
		"--project", cfg.Gcloud.Project, "--quiet"); err != nil {
		done(err)
		return fmt.Errorf("enabling APIs: %w", err)
	}
	done(nil)

	// reauth.googleapis.com is only enableable by orgs that have been
	// allowlisted for it; on other projects Service Usage rejects it with
	// "not available to this consumer". It's only needed when the caller's
	// org enforces a session-length/reauth policy — gcloud calls it during
	// OAuth token refresh, and commands like `gcloud workstations ssh` fail
	// with SERVICE_DISABLED if that policy is on and the API is off. Try
	// best-effort; surface a warning but don't block the rest of setup.
	if err := r.RunSoft("services", "enable", "reauth.googleapis.com",
		"--project", cfg.Gcloud.Project, "--quiet"); err != nil {
		logging.WarnPrintf(cmd.OutOrStdout(), "could not enable reauth.googleapis.com (%v).\n"+
			"      This is only required if your org enforces a session/reauth policy.\n"+
			"      If `gcloud workstations ssh` later fails with SERVICE_DISABLED for\n"+
			"      reauth.googleapis.com, ask an org admin to enable it.", err)
	}

	// Step 2 — Artifact Registry repo
	done = logging.Step("ensure_ar_repo", 2, totalCreateSteps, fmt.Sprintf("Ensuring Artifact Registry repo '%s'...", ws.RepoName), cmd.OutOrStdout())
	if !r.Check("artifacts", "repositories", "describe", ws.RepoName, "--location", ws.Region, "--project", cfg.Gcloud.Project) {
		if err := r.Run("artifacts", "repositories", "create", ws.RepoName,
			"--repository-format=docker",
			"--location", ws.Region,
			"--description=ra workstation images",
			"--project", cfg.Gcloud.Project); err != nil {
			done(err)
			return fmt.Errorf("creating artifact registry repo: %w", err)
		}
	}
	done(nil)

	// Step 3 — Build and push image
	done = logging.Step("build_image", 3, totalCreateSteps, fmt.Sprintf("Building image %s...", imageURI), cmd.OutOrStdout())

	home, err := os.UserHomeDir()
	if err != nil {
		done(err)
		return fmt.Errorf("determining home directory: %w", err)
	}
	cwd, err := os.Getwd()
	if err != nil {
		done(err)
		return fmt.Errorf("determining working directory: %w", err)
	}
	_, buildCleanup, err := scaffold.AssembleBuildContext(scaffold.BuildContextOptions{
		Cwd:     cwd,
		Home:    home,
		Plugins: reg.All(),
		Config:  cfg,
	})
	if err != nil {
		done(err)
		return fmt.Errorf("assembling build context: %w", err)
	}
	defer buildCleanup()

	if err := r.Run("auth", "configure-docker", fmt.Sprintf("%s-docker.pkg.dev", ws.Region), "--quiet"); err != nil {
		done(err)
		return fmt.Errorf("configuring docker auth: %w", err)
	}
	if err := (dockerRunner{stdout: cmd.OutOrStdout(), stderr: cmd.ErrOrStderr()}).Run(dockerBuildArgs(imageURI)...); err != nil {
		done(err)
		return fmt.Errorf("building and pushing image: %w", err)
	}
	done(nil)

	// Step 4 — Create any secrets whose values we collected during pre-flight.
	// Existing secrets were skipped already; this loop only writes new ones.
	done = logging.Step("ensure_secrets", 4, totalCreateSteps, fmt.Sprintf("Creating %d new GCP secret(s)...", len(pendingSecrets)), cmd.OutOrStdout())
	for _, ps := range pendingSecrets {
		if err := secrets.CreateFromValue(r, cfg.Gcloud.Project, ps.gcpName, ps.value); err != nil {
			done(err)
			return fmt.Errorf("creating secret %s: %w", ps.gcpName, err)
		}
	}
	done(nil)

	// Step 5 — Service account and IAM
	done = logging.Step("ensure_service_acct", 5, totalCreateSteps, fmt.Sprintf("Ensuring service account '%s'...", serviceAccountEmail), cmd.OutOrStdout())
	if !r.Check("iam", "service-accounts", "describe", serviceAccountEmail, "--project", cfg.Gcloud.Project) {
		if err := r.Run("iam", "service-accounts", "create", ws.ServiceAccountName,
			"--display-name=ra Workstation SA",
			"--project", cfg.Gcloud.Project); err != nil {
			done(err)
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
	// IAM bindings on a freshly-created service account can race
	// gcloud's eventual-consistency window and fail with "Service
	// account ... does not exist". RetryOnSANotFound waits it out.
	for _, secret := range secretNames {
		if err := gcloud.RetryOnSANotFound(r, saBindingAttempts, saBindingDelay, cmd.OutOrStdout(),
			"secrets", "add-iam-policy-binding", secret,
			"--member=serviceAccount:"+serviceAccountEmail,
			"--role=roles/secretmanager.secretAccessor",
			"--project", cfg.Gcloud.Project,
			"--condition=None"); err != nil {
			done(err)
			return fmt.Errorf("binding secret %s IAM: %w", secret, err)
		}
	}
	done(nil)

	// Bind Artifact Registry repo
	done = logging.Step("bind_ar_iam", 6, totalCreateSteps, fmt.Sprintf("Granting artifactregistry.reader on '%s'...", ws.RepoName), cmd.OutOrStdout())
	if err := gcloud.RetryOnSANotFound(r, saBindingAttempts, saBindingDelay, cmd.OutOrStdout(),
		"artifacts", "repositories", "add-iam-policy-binding", ws.RepoName,
		"--location", ws.Region,
		"--member=serviceAccount:"+serviceAccountEmail,
		"--role=roles/artifactregistry.reader",
		"--project", cfg.Gcloud.Project,
		"--condition=None"); err != nil {
		done(err)
		return fmt.Errorf("binding artifact registry IAM: %w", err)
	}
	done(nil)

	// Bind project-level roles
	done = logging.Step("bind_project_iam", 7, totalCreateSteps, "Granting logWriter and metricWriter on project...", cmd.OutOrStdout())
	for _, role := range []string{"roles/logging.logWriter", "roles/monitoring.metricWriter"} {
		if err := gcloud.RetryOnSANotFound(r, saBindingAttempts, saBindingDelay, cmd.OutOrStdout(),
			"projects", "add-iam-policy-binding", cfg.Gcloud.Project,
			"--member=serviceAccount:"+serviceAccountEmail,
			"--role="+role,
			"--condition=None"); err != nil {
			done(err)
			return fmt.Errorf("binding project role %s: %w", role, err)
		}
	}
	done(nil)

	// Step 8 — Workstations cluster
	done = logging.Step("ensure_cluster", 8, totalCreateSteps, fmt.Sprintf("Ensuring workstations cluster '%s'...", ws.ClusterName), cmd.OutOrStdout())
	if !r.Check("workstations", "clusters", "describe", ws.ClusterName, "--region", ws.Region, "--project", cfg.Gcloud.Project) {
		logging.WarnPrintf(cmd.OutOrStdout(), "Creating cluster — this takes ~20 minutes on first run.")
		if err := r.Run("workstations", "clusters", "create", ws.ClusterName,
			"--region", ws.Region,
			"--project", cfg.Gcloud.Project); err != nil {
			done(err)
			return fmt.Errorf("creating workstations cluster: %w", err)
		}
	}
	done(nil)

	// Step 9 — Workstations config
	done = logging.Step("ensure_config", 9, totalCreateSteps, fmt.Sprintf("Ensuring workstations config '%s'...", ws.ConfigName), cmd.OutOrStdout())
	containerEnv := buildContainerEnv(cfg)

	configExists := r.Check("workstations", "configs", "describe", ws.ConfigName,
		"--cluster", ws.ClusterName, "--region", ws.Region, "--project", cfg.Gcloud.Project)

	if configExists {
		logging.StepPrintf(cmd.OutOrStdout(), "Config exists — updating image, env, service account, and scopes...")
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
			done(err)
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
			done(err)
			return fmt.Errorf("creating workstations config: %w", err)
		}
	}
	done(nil)

	// Step 10 — Resolve a unique workstation name and create it.
	done = logging.Step("create_workstation", 10, totalCreateSteps, "Resolving workstation name and creating...", cmd.OutOrStdout())
	existing, err := client.List(cfg.Gcloud.Project, ws.Region, ws.ClusterName, ws.ConfigName)
	if err != nil {
		done(err)
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
			logging.WarnPrintf(cmd.OutOrStdout(), "workstation %q exists — using %q instead", requested, workstationName)
		}
	} else {
		rng := rand.New(rand.NewSource(time.Now().UnixNano()))
		workstationName, err = naming.GenerateMemorable(cfg.WorkstationName, taken, rng)
		if err != nil {
			done(err)
			return fmt.Errorf("generating workstation name: %w", err)
		}
	}
	if err := r.Run("workstations", "create", workstationName,
		"--cluster", ws.ClusterName, "--config", ws.ConfigName,
		"--region", ws.Region, "--project", cfg.Gcloud.Project); err != nil {
		done(err)
		return fmt.Errorf("creating workstation: %w", err)
	}

	wsInfo, err := client.Describe(cfg.Gcloud.Project, ws.Region, ws.ClusterName, ws.ConfigName, workstationName)
	if err != nil {
		done(err)
		return fmt.Errorf("describing workstation: %w", err)
	}
	if wsInfo.State != "STATE_RUNNING" && wsInfo.State != "STATE_STARTING" {
		logging.StepPrintf(cmd.OutOrStdout(), "Starting workstation...")
		if err := r.Run("workstations", "start", workstationName,
			"--cluster", ws.ClusterName, "--config", ws.ConfigName,
			"--region", ws.Region, "--project", cfg.Gcloud.Project); err != nil {
			done(err)
			return fmt.Errorf("starting workstation: %w", err)
		}
	}
	done(nil)

	// Step 11 — Print access info
	info, err := client.Describe(cfg.Gcloud.Project, ws.Region, ws.ClusterName, ws.ConfigName, workstationName)
	if err != nil {
		return fmt.Errorf("describing workstation: %w", err)
	}

	fmt.Fprintf(cmd.OutOrStdout(), `
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

	fmt.Fprintf(cmd.OutOrStdout(), "Total time: %s\n", elapsedHuman(time.Since(startedAt)))
	if p := logging.LogPath(); p != "" {
		fmt.Fprintf(cmd.OutOrStdout(), "Log: %s\n", p)
	}

	return nil
}

// elapsedHuman formats a duration as human-friendly "MmSSs" or "Ss" with
// sub-second resolution truncated (not rounded — 4m11.9s reads as 4m11s,
// matching what most stopwatches show). Examples: 4m12s, 12s, 1h3m7s.
func elapsedHuman(d time.Duration) string {
	d = d.Truncate(time.Second)
	h := int(d / time.Hour)
	d -= time.Duration(h) * time.Hour
	m := int(d / time.Minute)
	d -= time.Duration(m) * time.Minute
	s := int(d / time.Second)
	switch {
	case h > 0:
		return fmt.Sprintf("%dh%dm%ds", h, m, s)
	case m > 0:
		return fmt.Sprintf("%dm%ds", m, s)
	default:
		return fmt.Sprintf("%ds", s)
	}
}

// dockerRunner runs docker commands with stdout/stderr forwarded to the provided writers.
type dockerRunner struct {
	stdout io.Writer
	stderr io.Writer
}

func (d dockerRunner) Run(args ...string) error {
	start := time.Now()
	logging.L().Debug("docker starting", "args", args)
	c := exec.Command("docker", args...)
	c.Stdout = d.stdout
	c.Stderr = d.stderr
	err := c.Run()
	dur := time.Since(start)
	if err != nil {
		logging.L().Error("docker failed", "duration_s", dur.Seconds(), "error", err.Error())
	} else {
		logging.L().Debug("docker ok", "duration_s", dur.Seconds())
	}
	return err
}

// dockerBuildArgs returns the buildx args used by `ra create`, centralized so
// tests can assert on the exact flags. The Dockerfile is harness-agnostic at
// build time — plugin-specific install steps are gated at runtime by their
// own `RA_PLUGIN_<NAME>_ENABLED` env vars emitted by each plugin.
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
