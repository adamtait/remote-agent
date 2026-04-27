package cmd

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/adamtait/remote-agent/internal/config"
	"github.com/adamtait/remote-agent/internal/configwrite"
	"github.com/adamtait/remote-agent/internal/plugins"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

func init() {
	pluginInstallCmd.Flags().BoolVar(&pluginInstallUser, "user", false, "Install into ~/.config/ra/plugins/ (user level)")
	pluginInstallCmd.Flags().BoolVar(&pluginInstallLocal, "local", false, "Install into .ra/plugins/ (project level)")
	pluginInstallCmd.Flags().BoolVar(&pluginInstallForce, "force", false, "Overwrite existing plugin of the same name")
	pluginCmd.AddCommand(pluginInstallCmd)
	pluginCmd.AddCommand(pluginListCmd)
	pluginCmd.AddCommand(pluginRemoveCmd)
	rootCmd.AddCommand(pluginCmd)
}

var pluginCmd = &cobra.Command{
	Use:   "plugin",
	Short: "Manage ra plugins",
	// Override root PersistentPreRunE: load user config always, project config
	// optionally. Plugin subcommands (list, install --user, remove) must work
	// outside a project directory.
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		home, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("determining home directory: %w", err)
		}
		wd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("determining working directory: %w", err)
		}

		userCfgPath, err := config.UserConfigPath()
		if err != nil {
			return fmt.Errorf("determining user config path: %w", err)
		}
		userCfg, err := config.LoadUserConfig(userCfgPath)
		if err != nil {
			return fmt.Errorf("loading user config: %w", err)
		}

		// Project config is optional — tolerate its absence.
		var projectCfg *config.ProjectConfig
		projectCfgPath, err := config.FindProjectConfig(wd, home)
		if err == nil {
			if loaded, loadErr := config.LoadProjectConfig(projectCfgPath); loadErr == nil {
				projectCfg = loaded
			}
		}

		cfg, err = config.Merge(userCfg, projectCfg)
		if err != nil {
			return err
		}
		if projectCfgPath != "" {
			cfg.ProjectConfigPath = projectCfgPath
		}

		// Load plugins from both directories (each silently tolerates absence).
		userPluginsDir := filepath.Join(home, ".config", "ra", "plugins")
		projPluginsDir := ""
		if projectCfgPath != "" {
			projPluginsDir = filepath.Join(filepath.Dir(projectCfgPath), "plugins")
		}
		loaded, err := plugins.LoadAll(userPluginsDir, projPluginsDir)
		if err != nil {
			return fmt.Errorf("loading plugins: %w", err)
		}
		reg = plugins.NewRegistry(loaded)
		return nil
	},
}

var pluginInstallUser bool
var pluginInstallLocal bool
var pluginInstallForce bool

// installMeta is the structure written/read from .ra-install.yaml inside a
// plugin's installed directory.
type installMeta struct {
	Source      string `yaml:"source"`
	InstalledAt string `yaml:"installed_at"`
}

var pluginInstallCmd = &cobra.Command{
	Use:   "install <source>",
	Short: "Install a plugin from a git repository or local path",
	Args:  cobra.ExactArgs(1),
	// No PersistentPreRunE override → inherits root PersistentPreRunE which
	// loads cfg + runner.
	RunE: func(cmd *cobra.Command, args []string) error {
		source := args[0]

		// ── 1. acquire plugin into a temp dir ─────────────────────────────
		tmp, err := os.MkdirTemp("", "ra-plugin-*")
		if err != nil {
			return fmt.Errorf("creating temp dir: %w", err)
		}
		defer os.RemoveAll(tmp)

		if isLocalPath(source) {
			// Local filesystem — copy into tmp so the rest of the flow is uniform.
			abs, err := filepath.Abs(source)
			if err != nil {
				return fmt.Errorf("resolving local path: %w", err)
			}
			if err := copyDir(abs, tmp); err != nil {
				return fmt.Errorf("copying local plugin: %w", err)
			}
		} else {
			// Remote git URL.
			clone := exec.Command("git", "clone", "--depth=1", source, tmp)
			clone.Stdout = os.Stdout
			clone.Stderr = os.Stderr
			if err := clone.Run(); err != nil {
				return fmt.Errorf("git clone: %w", err)
			}
		}

		// ── 2. parse plugin.yaml ───────────────────────────────────────────
		p, err := plugins.LoadPlugin(os.DirFS(tmp))
		if err != nil {
			return fmt.Errorf("reading plugin.yaml: %w", err)
		}

		// ── 3. choose install level ────────────────────────────────────────
		level, err := chooseInstallLevel(cmd, pluginInstallUser, pluginInstallLocal)
		if err != nil {
			return err
		}

		// ── 4. determine dest directory ───────────────────────────────────
		var destBase string
		switch level {
		case "project":
			wd, err := os.Getwd()
			if err != nil {
				return fmt.Errorf("determining working directory: %w", err)
			}
			destBase = filepath.Join(wd, ".ra", "plugins")
		default: // "user"
			home, err := os.UserHomeDir()
			if err != nil {
				return fmt.Errorf("determining home directory: %w", err)
			}
			destBase = filepath.Join(home, ".config", "ra", "plugins")
		}
		dest := filepath.Join(destBase, p.Name())

		if _, err := os.Stat(dest); err == nil && !pluginInstallForce {
			return fmt.Errorf("plugin %q already exists at %s; use --force to overwrite", p.Name(), dest)
		}
		if err := os.RemoveAll(dest); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if err := os.MkdirAll(destBase, 0755); err != nil {
			return err
		}
		if err := copyDir(tmp, dest); err != nil {
			return fmt.Errorf("installing plugin: %w", err)
		}

		// ── 5. prompt for config + secrets ────────────────────────────────
		r := bufio.NewReader(cmd.InOrStdin())
		// For secret prompts we need the real stdin fd for term.ReadPassword.
		// In non-interactive contexts (tests) Prompt will be called but secret
		// prompts will be skipped (plugin disabled or no secrets defined).
		res, err := p.Prompt(r, cmd.OutOrStdout(), cmd.ErrOrStderr(), os.Stdin)
		if err != nil {
			return fmt.Errorf("prompting plugin config: %w", err)
		}

		// ── 6. write config.yaml ──────────────────────────────────────────
		cfgPath, err := pluginCfgPath(level)
		if err != nil {
			return err
		}
		if err := writePluginToConfig(cfgPath, p, res); err != nil {
			return err
		}

		// ── 7. upload secrets to GCP ──────────────────────────────────────
		if cfg != nil && cfg.Gcloud.Project != "" {
			if err := uploadPluginSecrets(p, res, cfg.Gcloud.Project); err != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "warning: failed to upload secrets to GCP: %v\n", err)
			}
		} else {
			fmt.Fprintf(cmd.ErrOrStderr(), "note: GCP project not configured — skipping secret upload\n")
		}

		// ── 8. write .ra-install.yaml ─────────────────────────────────────
		meta := installMeta{
			Source:      source,
			InstalledAt: time.Now().UTC().Format(time.RFC3339),
		}
		metaBytes, err := yaml.Marshal(meta)
		if err != nil {
			return fmt.Errorf("marshalling install meta: %w", err)
		}
		if err := os.WriteFile(filepath.Join(dest, ".ra-install.yaml"), metaBytes, 0644); err != nil {
			return fmt.Errorf("writing .ra-install.yaml: %w", err)
		}

		fmt.Fprintf(cmd.OutOrStdout(), "Installed plugin %q to %s\n", p.Name(), dest)
		return nil
	},
}

// ── list ──────────────────────────────────────────────────────────────────────

type listEntry struct {
	Name        string
	Level       string
	Source      string
	Description string
	Shadowed    bool
}

var pluginListCmd = &cobra.Command{
	Use:   "list",
	Short: "List installed plugins",
	// No PersistentPreRunE override — inherits root preRunE.
	RunE: func(cmd *cobra.Command, args []string) error {
		home, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("determining home directory: %w", err)
		}
		wd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("determining working directory: %w", err)
		}
		userDir := filepath.Join(home, ".config", "ra", "plugins")
		projDir := filepath.Join(wd, ".ra", "plugins")

		userPlugins, err := plugins.LoadAll(userDir, "")
		if err != nil {
			return err
		}
		projPlugins, err := plugins.LoadAll("", projDir)
		if err != nil {
			return err
		}

		// Build a set of project plugin names to detect shadowing.
		projNames := make(map[string]bool)
		for _, p := range projPlugins {
			projNames[p.Name()] = true
		}

		var entries []listEntry

		for _, p := range userPlugins {
			meta := readInstallMeta(filepath.Join(userDir, p.Name()))
			e := listEntry{
				Name:        p.Name(),
				Level:       "user",
				Source:      meta.Source,
				Description: p.Schema().Description,
				Shadowed:    projNames[p.Name()],
			}
			entries = append(entries, e)
		}
		for _, p := range projPlugins {
			meta := readInstallMeta(filepath.Join(projDir, p.Name()))
			entries = append(entries, listEntry{
				Name:        p.Name(),
				Level:       "project",
				Source:      meta.Source,
				Description: p.Schema().Description,
			})
		}

		if len(entries) == 0 {
			fmt.Fprintln(cmd.OutOrStdout(), "(no plugins installed)")
			return nil
		}

		w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 3, ' ', 0)
		fmt.Fprintln(w, "NAME\tLEVEL\tSOURCE\tDESCRIPTION")
		for _, e := range entries {
			name := e.Name
			if e.Shadowed {
				name += " (shadowed)"
			}
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", name, e.Level, e.Source, e.Description)
		}
		return w.Flush()
	},
}

// ── remove ────────────────────────────────────────────────────────────────────

var pluginRemoveCmd = &cobra.Command{
	Use:   "remove <name>",
	Short: "Remove an installed plugin",
	Args:  cobra.ExactArgs(1),
	// No PersistentPreRunE override — inherits root preRunE.
	RunE: func(cmd *cobra.Command, args []string) error {
		name := args[0]
		home, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("determining home directory: %w", err)
		}
		wd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("determining working directory: %w", err)
		}

		userPath := filepath.Join(home, ".config", "ra", "plugins", name)
		projPath := filepath.Join(wd, ".ra", "plugins", name)

		userExists := dirExists(userPath)
		projExists := dirExists(projPath)

		if !userExists && !projExists {
			return fmt.Errorf("plugin %q not found (checked %s and %s)", name, userPath, projPath)
		}

		// If both exist, prompt which to remove.
		var chosenPath, level string
		if userExists && projExists {
			r := bufio.NewReader(cmd.InOrStdin())
			fmt.Fprintln(cmd.OutOrStdout(), "Plugin exists at both user and project level. Which to remove?")
			fmt.Fprintln(cmd.OutOrStdout(), "  1) user  ("+userPath+")")
			fmt.Fprintln(cmd.OutOrStdout(), "  2) project ("+projPath+")")
			fmt.Fprintf(cmd.OutOrStdout(), "choice [1-2] [1]: ")
			line, _ := r.ReadString('\n')
			choice := strings.TrimSpace(line)
			if choice == "2" {
				chosenPath = projPath
				level = "project"
			} else {
				chosenPath = userPath
				level = "user"
			}
		} else if userExists {
			chosenPath = userPath
			level = "user"
		} else {
			chosenPath = projPath
			level = "project"
		}

		// Load plugin.yaml to know which secrets to remove from config.
		p, err := plugins.LoadPlugin(os.DirFS(chosenPath))
		if err != nil {
			return fmt.Errorf("reading plugin.yaml: %w", err)
		}

		// Determine all relevant secret logical names.
		var secretLogicals []string
		for _, s := range p.Schema().Secrets {
			secretLogicals = append(secretLogicals, s.Name)
		}
		for _, ap := range p.Schema().AuthProviders {
			for _, s := range ap.Secrets {
				secretLogicals = append(secretLogicals, s.Name)
			}
		}

		// Update config.yaml.
		cfgPath, err := pluginCfgPath(level)
		if err != nil {
			return err
		}
		original, err := os.ReadFile(cfgPath)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("reading config.yaml: %w", err)
		}
		if err == nil {
			updated, err := configwrite.RemovePluginConfig(string(original), name, secretLogicals)
			if err != nil {
				return fmt.Errorf("updating config.yaml: %w", err)
			}
			if err := os.WriteFile(cfgPath, []byte(updated), 0644); err != nil {
				return fmt.Errorf("writing config.yaml: %w", err)
			}
		}

		// Delete the plugin directory.
		if err := os.RemoveAll(chosenPath); err != nil {
			return fmt.Errorf("removing plugin directory: %w", err)
		}

		fmt.Fprintf(cmd.OutOrStdout(), "Removed plugin %q from %s\n", name, chosenPath)

		// Hint about GCP secrets.
		if len(secretLogicals) > 0 {
			fmt.Fprintln(cmd.OutOrStdout(), "Secret values remain in GCP Secret Manager. To delete them manually:")
			projectID := ""
			if cfg != nil {
				projectID = cfg.Gcloud.Project
			}
			for _, s := range allSchemaDefs(p) {
				gcpName := gcpSecretName(s)
				if projectID != "" {
					fmt.Fprintf(cmd.OutOrStdout(), "  gcloud secrets delete %s --project %s\n", gcpName, projectID)
				} else {
					fmt.Fprintf(cmd.OutOrStdout(), "  gcloud secrets delete %s --project <your-project>\n", gcpName)
				}
			}
		}

		return nil
	},
}

// ── helpers ───────────────────────────────────────────────────────────────────

// chooseInstallLevel returns "user" or "project" based on flags or interactive prompt.
func chooseInstallLevel(cmd *cobra.Command, user, local bool) (string, error) {
	if user && local {
		return "", fmt.Errorf("--user and --local are mutually exclusive")
	}
	if user {
		return "user", nil
	}
	if local {
		return "project", nil
	}
	// Interactive prompt.
	r := bufio.NewReader(cmd.InOrStdin())
	fmt.Fprintln(cmd.OutOrStdout(), "Install at user level (~/.config/ra/plugins/) or project level (.ra/plugins/)?")
	fmt.Fprintln(cmd.OutOrStdout(), "  1) user")
	fmt.Fprintln(cmd.OutOrStdout(), "  2) project")
	fmt.Fprintf(cmd.OutOrStdout(), "choice [1-2] [1]: ")
	line, err := r.ReadString('\n')
	if err != nil && err != io.EOF {
		return "", fmt.Errorf("reading input: %w", err)
	}
	choice := strings.TrimSpace(line)
	switch choice {
	case "", "1":
		return "user", nil
	case "2":
		return "project", nil
	default:
		return "", fmt.Errorf("invalid choice %q; enter 1 or 2", choice)
	}
}

// isLocalPath returns true when source looks like a local filesystem path
// rather than a remote git URL.
func isLocalPath(source string) bool {
	// Explicit path prefixes.
	if strings.HasPrefix(source, "/") ||
		strings.HasPrefix(source, "./") ||
		strings.HasPrefix(source, "../") {
		return true
	}
	// Remote URL schemes.
	if strings.HasPrefix(source, "http://") ||
		strings.HasPrefix(source, "https://") ||
		strings.HasPrefix(source, "git@") ||
		strings.HasPrefix(source, "ssh://") {
		return false
	}
	// SCP-style git remote (git@host:path).
	if strings.Contains(source, ":") {
		if _, err := url.ParseRequestURI(source); err == nil {
			return false
		}
	}
	// Bare name that resolves to an existing directory.
	if fi, err := os.Stat(source); err == nil && fi.IsDir() {
		return true
	}
	return false
}

// pluginCfgPath returns the path to config.yaml for the given level.
func pluginCfgPath(level string) (string, error) {
	switch level {
	case "project":
		wd, err := os.Getwd()
		if err != nil {
			return "", fmt.Errorf("determining working directory: %w", err)
		}
		return filepath.Join(wd, ".ra", "config.yaml"), nil
	default: // "user"
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("determining home directory: %w", err)
		}
		return filepath.Join(home, ".config", "ra", "config.yaml"), nil
	}
}

// writePluginToConfig merges a PromptResult into the appropriate config.yaml.
func writePluginToConfig(cfgPath string, p *plugins.FilePlugin, res *plugins.PromptResult) error {
	original, err := os.ReadFile(cfgPath)
	if errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("config.yaml not found at %s — run 'ra setup' first", cfgPath)
	}
	if err != nil {
		return fmt.Errorf("reading config.yaml: %w", err)
	}

	secretNames := make(map[string]string)
	secretEnvVars := make(map[string]string)
	for _, sd := range allRelevantSecretDefs(p, res) {
		gcpName := sd.DefaultSecretName
		if gcpName == "" {
			gcpName = sd.Name
		}
		secretNames[sd.Name] = gcpName
		secretEnvVars[sd.Name] = sd.EnvVar
	}

	updated, err := configwrite.MergePluginConfig(string(original), p.Name(), res.Config, secretNames, secretEnvVars)
	if err != nil {
		return fmt.Errorf("merging plugin config: %w", err)
	}

	if err := os.WriteFile(cfgPath, []byte(updated), 0644); err != nil {
		return fmt.Errorf("writing config.yaml: %w", err)
	}
	return nil
}

// allRelevantSecretDefs returns top-level secrets plus active provider's secrets.
func allRelevantSecretDefs(p *plugins.FilePlugin, res *plugins.PromptResult) []plugins.SecretDef {
	var defs []plugins.SecretDef
	defs = append(defs, p.Schema().Secrets...)
	if res.AuthProvider != nil {
		defs = append(defs, res.AuthProvider.Secrets...)
	}
	return defs
}

// allSchemaDefs returns all secrets in schema (all auth providers, top-level).
func allSchemaDefs(p *plugins.FilePlugin) []plugins.SecretDef {
	var defs []plugins.SecretDef
	defs = append(defs, p.Schema().Secrets...)
	for _, ap := range p.Schema().AuthProviders {
		defs = append(defs, ap.Secrets...)
	}
	return defs
}

// gcpSecretName returns the GCP secret name for a SecretDef.
func gcpSecretName(s plugins.SecretDef) string {
	if s.DefaultSecretName != "" {
		return s.DefaultSecretName
	}
	return s.Name
}

// uploadPluginSecrets uploads each secret value gathered during Prompt to GCP
// Secret Manager. If a secret already exists, a new version is added.
func uploadPluginSecrets(p *plugins.FilePlugin, res *plugins.PromptResult, projectID string) error {
	for _, sd := range allRelevantSecretDefs(p, res) {
		value, ok := res.SecretValues[sd.Name]
		if !ok {
			continue
		}
		if err := uploadSecretValue(projectID, gcpSecretName(sd), value); err != nil {
			return err
		}
	}
	return nil
}

// uploadSecretValue creates the secret if it doesn't exist, then adds a version.
func uploadSecretValue(projectID, gcpSecretName, value string) error {
	// Check if secret exists.
	checkCmd := exec.Command("gcloud", "secrets", "describe", gcpSecretName, "--project", projectID)
	exists := checkCmd.Run() == nil

	if !exists {
		createCmd := exec.Command("gcloud", "secrets", "create", gcpSecretName,
			"--replication-policy=automatic",
			"--project", projectID)
		createCmd.Stdout = os.Stdout
		createCmd.Stderr = os.Stderr
		if err := createCmd.Run(); err != nil {
			return fmt.Errorf("creating secret %s: %w", gcpSecretName, err)
		}
	}

	// Add a new version.
	addCmd := exec.Command("gcloud", "secrets", "versions", "add", gcpSecretName,
		"--data-file=-",
		"--project", projectID)
	addCmd.Stdin = bytes.NewReader([]byte(value))
	addCmd.Stdout = os.Stdout
	addCmd.Stderr = os.Stderr
	if err := addCmd.Run(); err != nil {
		return fmt.Errorf("adding version to secret %s: %w", gcpSecretName, err)
	}
	return nil
}

// readInstallMeta reads .ra-install.yaml from a plugin directory.
// Returns zero-value installMeta on any error.
func readInstallMeta(pluginDir string) installMeta {
	data, err := os.ReadFile(filepath.Join(pluginDir, ".ra-install.yaml"))
	if err != nil {
		return installMeta{}
	}
	var m installMeta
	_ = yaml.Unmarshal(data, &m)
	return m
}

// dirExists returns true if path exists and is a directory.
func dirExists(path string) bool {
	fi, err := os.Stat(path)
	return err == nil && fi.IsDir()
}

// copyDir recursively copies src directory to dst.
func copyDir(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(target, info.Mode())
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, info.Mode())
	})
}
