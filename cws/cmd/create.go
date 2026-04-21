package cmd

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"github.com/adamtait/remote-agent/cws/internal/gcloud"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

var createCmd = &cobra.Command{
	Use:   "create",
	Short: "Provision a Google Cloud Workstation with Claude Code",
	Long: `Idempotent: safe to re-run. Each step checks for existence first.
Requires .workstation.yaml in the project root with project_id and github_repo_url set.`,
	RunE: runCreate,
}

func init() {
	rootCmd.AddCommand(createCmd)
}

func runCreate(cmd *cobra.Command, args []string) error {
	// a) Apply env var fallbacks
	if cfg.ProjectID == "" {
		cfg.ProjectID = os.Getenv("PROJECT_ID")
	}
	if cfg.GithubRepoURL == "" {
		cfg.GithubRepoURL = os.Getenv("GITHUB_REPO_URL")
	}
	if cfg.ProjectID == "" {
		return fmt.Errorf("project_id is required: set it in .workstation.yaml or PROJECT_ID env var")
	}
	if cfg.GithubRepoURL == "" {
		return fmt.Errorf("github_repo_url is required: set it in .workstation.yaml or GITHUB_REPO_URL env var")
	}
	if cfg.GitUserName == "" {
		cfg.GitUserName = "Developer"
	}
	if cfg.GitUserEmail == "" {
		cfg.GitUserEmail = "dev@example.com"
	}

	// b) Create runner
	r := gcloud.RealRunner{}

	// c) Compute derived values
	imageURI := fmt.Sprintf("%s-docker.pkg.dev/%s/%s/%s:%s",
		cfg.Region, cfg.ProjectID, cfg.RepoName, cfg.ImageName, cfg.ImageTag)
	serviceAccountEmail := fmt.Sprintf("%s@%s.iam.gserviceaccount.com",
		cfg.ServiceAccountName, cfg.ProjectID)

	// d) Helper for step logging
	logStep := func(msg string) { fmt.Printf("\033[1;34m==>\033[0m %s\n", msg) }

	// Step 1 — Enable APIs
	logStep("Enabling required GCP APIs...")
	if err := r.Run("services", "enable",
		"workstations.googleapis.com",
		"artifactregistry.googleapis.com",
		"secretmanager.googleapis.com",
		"iam.googleapis.com",
		"--project", cfg.ProjectID, "--quiet"); err != nil {
		return fmt.Errorf("enabling APIs: %w", err)
	}

	// Step 2 — Artifact Registry repo
	logStep(fmt.Sprintf("Ensuring Artifact Registry repo '%s'...", cfg.RepoName))
	if !r.Check("artifacts", "repositories", "describe", cfg.RepoName, "--location", cfg.Region, "--project", cfg.ProjectID) {
		if err := r.Run("artifacts", "repositories", "create", cfg.RepoName,
			"--repository-format=docker",
			"--location", cfg.Region,
			"--description=Claude Code workstation images",
			"--project", cfg.ProjectID); err != nil {
			return fmt.Errorf("creating artifact registry repo: %w", err)
		}
	}

	// Step 3 — Build and push image
	logStep(fmt.Sprintf("Building image %s...", imageURI))
	if err := r.Run("auth", "configure-docker", fmt.Sprintf("%s-docker.pkg.dev", cfg.Region), "--quiet"); err != nil {
		return fmt.Errorf("configuring docker auth: %w", err)
	}
	if err := (dockerRunner{}).Run("buildx", "build", "--platform", "linux/amd64", "-t", imageURI, "--push", "."); err != nil {
		return fmt.Errorf("building and pushing image: %w", err)
	}

	// Step 4 — Secrets
	logStep("Ensuring Claude secret...")
	claudeAuthMode, claudeSecretName, err := ensureClaudeSecret(r, cfg.ProjectID)
	if err != nil {
		return fmt.Errorf("ensuring Claude secret: %w", err)
	}
	logStep("Ensuring GitHub PAT secret...")
	if err := ensureSecret(r, cfg.ProjectID, "github-pat", "Enter GitHub PAT (fine-grained, repo-scoped)"); err != nil {
		return fmt.Errorf("ensuring github-pat secret: %w", err)
	}

	// Step 5 — Service account and IAM
	logStep(fmt.Sprintf("Ensuring service account '%s'...", serviceAccountEmail))
	if !r.Check("iam", "service-accounts", "describe", serviceAccountEmail, "--project", cfg.ProjectID) {
		if err := r.Run("iam", "service-accounts", "create", cfg.ServiceAccountName,
			"--display-name=Claude Workstation SA",
			"--project", cfg.ProjectID); err != nil {
			return fmt.Errorf("creating service account: %w", err)
		}
	}

	// Bind secrets
	for _, secret := range []string{claudeSecretName, "github-pat"} {
		if err := r.Run("secrets", "add-iam-policy-binding", secret,
			"--member=serviceAccount:"+serviceAccountEmail,
			"--role=roles/secretmanager.secretAccessor",
			"--project", cfg.ProjectID,
			"--condition=None"); err != nil {
			return fmt.Errorf("binding secret %s IAM: %w", secret, err)
		}
	}

	// Bind Artifact Registry repo
	logStep(fmt.Sprintf("Granting artifactregistry.reader on '%s'...", cfg.RepoName))
	if err := r.Run("artifacts", "repositories", "add-iam-policy-binding", cfg.RepoName,
		"--location", cfg.Region,
		"--member=serviceAccount:"+serviceAccountEmail,
		"--role=roles/artifactregistry.reader",
		"--project", cfg.ProjectID,
		"--condition=None"); err != nil {
		return fmt.Errorf("binding artifact registry IAM: %w", err)
	}

	// Bind project-level roles
	logStep("Granting logWriter and metricWriter on project...")
	for _, role := range []string{"roles/logging.logWriter", "roles/monitoring.metricWriter"} {
		if err := r.Run("projects", "add-iam-policy-binding", cfg.ProjectID,
			"--member=serviceAccount:"+serviceAccountEmail,
			"--role="+role,
			"--condition=None"); err != nil {
			return fmt.Errorf("binding project role %s: %w", role, err)
		}
	}

	// Step 6 — Workstations cluster
	logStep(fmt.Sprintf("Ensuring workstations cluster '%s'...", cfg.ClusterName))
	if !r.Check("workstations", "clusters", "describe", cfg.ClusterName, "--region", cfg.Region, "--project", cfg.ProjectID) {
		fmt.Printf("\033[1;33mWARN:\033[0m Creating cluster — this takes ~20 minutes on first run.\n")
		if err := r.Run("workstations", "clusters", "create", cfg.ClusterName,
			"--region", cfg.Region,
			"--project", cfg.ProjectID); err != nil {
			return fmt.Errorf("creating workstations cluster: %w", err)
		}
	}

	// Step 7 — Workstations config
	logStep(fmt.Sprintf("Ensuring workstations config '%s'...", cfg.ConfigName))
	containerEnv := strings.Join([]string{
		"CLAUDE_AUTH_MODE=" + claudeAuthMode,
		"CLAUDE_AUTH_SECRET_NAME=" + claudeSecretName,
		"GITHUB_PAT_SECRET_NAME=github-pat",
		"GCP_PROJECT_ID=" + cfg.ProjectID,
		"GITHUB_REPO_URL=" + cfg.GithubRepoURL,
		"GIT_USER_NAME=" + cfg.GitUserName,
		"GIT_USER_EMAIL=" + cfg.GitUserEmail,
	}, ",")

	fmt.Printf("Auth mode: %s (Claude secret: %s, GitHub secret: github-pat)\n", claudeAuthMode, claudeSecretName)

	configExists := r.Check("workstations", "configs", "describe", cfg.ConfigName,
		"--cluster", cfg.ClusterName, "--region", cfg.Region, "--project", cfg.ProjectID)

	if configExists {
		logStep("Config exists — updating image, env, service account, and scopes...")
		if err := r.Run("workstations", "configs", "update", cfg.ConfigName,
			"--cluster", cfg.ClusterName, "--region", cfg.Region, "--project", cfg.ProjectID,
			"--container-custom-image="+imageURI,
			"--service-account="+serviceAccountEmail,
			"--service-account-scopes=https://www.googleapis.com/auth/cloud-platform",
			"--container-env="+containerEnv); err != nil {
			return fmt.Errorf("updating workstations config: %w", err)
		}
	} else {
		if err := r.Run("workstations", "configs", "create", cfg.ConfigName,
			"--cluster", cfg.ClusterName, "--region", cfg.Region, "--project", cfg.ProjectID,
			"--machine-type="+cfg.MachineType,
			"--pd-disk-size="+strconv.Itoa(cfg.DiskSizeGB),
			"--pd-disk-type=pd-balanced",
			"--container-custom-image="+imageURI,
			"--service-account="+serviceAccountEmail,
			"--service-account-scopes=https://www.googleapis.com/auth/cloud-platform",
			"--container-env="+containerEnv); err != nil {
			return fmt.Errorf("creating workstations config: %w", err)
		}
	}

	// Step 8 — Create and start workstation
	logStep(fmt.Sprintf("Ensuring workstation '%s'...", cfg.WorkstationName))
	if !r.Check("workstations", "describe", cfg.WorkstationName,
		"--cluster", cfg.ClusterName, "--config", cfg.ConfigName,
		"--region", cfg.Region, "--project", cfg.ProjectID) {
		if err := r.Run("workstations", "create", cfg.WorkstationName,
			"--cluster", cfg.ClusterName, "--config", cfg.ConfigName,
			"--region", cfg.Region, "--project", cfg.ProjectID); err != nil {
			return fmt.Errorf("creating workstation: %w", err)
		}
	}

	logStep("Starting workstation...")
	if err := r.Run("workstations", "start", cfg.WorkstationName,
		"--cluster", cfg.ClusterName, "--config", cfg.ConfigName,
		"--region", cfg.Region, "--project", cfg.ProjectID); err != nil {
		return fmt.Errorf("starting workstation: %w", err)
	}

	// Step 9 — Print access info
	wsClient := gcloud.NewClient(r)
	info, err := wsClient.Describe(cfg.ProjectID, cfg.Region, cfg.ClusterName, cfg.ConfigName, cfg.WorkstationName)
	if err != nil {
		return fmt.Errorf("describing workstation: %w", err)
	}

	fmt.Printf(`
================================================================
  Claude Code workstation is ready
================================================================
  Browser IDE  : https://80-%s
  Workstation  : %s
  Cluster      : %s
  Config       : %s
  Region       : %s

  Once the IDE loads, open a terminal and run:
      cd ~/workspace
      claude

  SSH to the workstation:
  gcloud workstations ssh %s \
    --cluster=%s \
    --config=%s \
    --region=%s \
    --project=%s

  Stop the workstation (to save cost):
      gcloud workstations stop %s \
        --cluster=%s --config=%s \
        --region=%s --project=%s

  Rotate a secret:
      echo -n 'new-value' | gcloud secrets versions add SECRET_NAME \
        --data-file=- --project=%s
      # then re-run this script to pick up the new version
================================================================
`,
		info.Host,
		cfg.WorkstationName,
		cfg.ClusterName,
		cfg.ConfigName,
		cfg.Region,
		cfg.WorkstationName, cfg.ClusterName, cfg.ConfigName, cfg.Region, cfg.ProjectID,
		cfg.WorkstationName, cfg.ClusterName, cfg.ConfigName, cfg.Region, cfg.ProjectID,
		cfg.ProjectID,
	)

	return nil
}

// ensureClaudeSecret checks for existing Claude auth secrets and prompts to create one if needed.
// Returns (claudeAuthMode, claudeSecretName, error).
func ensureClaudeSecret(r gcloud.Runner, projectID string) (string, string, error) {
	if r.Check("secrets", "describe", "claude-code-oauth-token", "--project", projectID) {
		fmt.Println("Found existing 'claude-code-oauth-token' secret — using Claude subscription auth.")
		return "oauth", "claude-code-oauth-token", nil
	}
	if r.Check("secrets", "describe", "anthropic-api-key", "--project", projectID) {
		fmt.Println("Found existing 'anthropic-api-key' secret — using API key auth.")
		return "apikey", "anthropic-api-key", nil
	}

	fmt.Print(`
Choose Claude Code authentication:
  1) Claude Pro/Max/Team OAuth token  (recommended for subscribers — uses your plan)
  2) Anthropic API key                (pay-as-you-go API billing)

To generate an OAuth token: run ` + "`claude setup-token`" + ` locally, authorize in
the browser, and copy the printed sk-ant-oat01-... token. Tokens are valid
for ~1 year and can be rotated with ` + "`gcloud secrets versions add`" + `.
`)

	scanner := bufio.NewScanner(os.Stdin)
	fmt.Print("Enter choice [1/2]: ")
	var choice string
	if scanner.Scan() {
		choice = strings.TrimSpace(scanner.Text())
	}

	switch choice {
	case "1":
		if err := ensureSecret(r, projectID, "claude-code-oauth-token",
			"Paste CLAUDE_CODE_OAUTH_TOKEN (sk-ant-oat01-...)"); err != nil {
			return "", "", err
		}
		return "oauth", "claude-code-oauth-token", nil
	case "2":
		if err := ensureSecret(r, projectID, "anthropic-api-key", "Enter ANTHROPIC_API_KEY"); err != nil {
			return "", "", err
		}
		return "apikey", "anthropic-api-key", nil
	default:
		return "", "", fmt.Errorf("invalid choice: %s", choice)
	}
}

// ensureSecret checks if a secret exists and creates it if not, prompting the user for the value.
func ensureSecret(r gcloud.Runner, projectID, name, prompt string) error {
	if r.Check("secrets", "describe", name, "--project", projectID) {
		fmt.Printf("Secret '%s' already exists (use 'gcloud secrets versions add' to rotate)\n", name)
		return nil
	}

	fmt.Printf("%s: ", prompt)
	value, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Println() // newline after hidden input
	if err != nil {
		return fmt.Errorf("reading secret value: %w", err)
	}
	if len(value) == 0 {
		return fmt.Errorf("empty value for secret %s", name)
	}

	f, err := os.CreateTemp("", "cws-secret-*")
	if err != nil {
		return fmt.Errorf("creating temp file: %w", err)
	}
	defer os.Remove(f.Name())

	if _, err := f.Write(value); err != nil {
		f.Close()
		return fmt.Errorf("writing secret to temp file: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("closing temp file: %w", err)
	}

	if err := r.Run("secrets", "create", name,
		"--replication-policy=automatic",
		"--data-file="+f.Name(),
		"--project", projectID); err != nil {
		return fmt.Errorf("creating secret %s: %w", name, err)
	}
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
