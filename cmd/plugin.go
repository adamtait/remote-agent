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
	osuser "os/user"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/adamtait/remote-agent/internal/config"
	"github.com/adamtait/remote-agent/internal/configwrite"
	"github.com/adamtait/remote-agent/internal/gcs"
	"github.com/adamtait/remote-agent/internal/logging"
	"github.com/adamtait/remote-agent/internal/plugins"
	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"
)

func init() {
	pluginInstallCmd.Flags().BoolVar(&pluginInstallUser, "user", false, "Install into ~/.config/ra/plugins/ (user level)")
	pluginInstallCmd.Flags().BoolVar(&pluginInstallLocal, "local", false, "Install into .ra/plugins/ (project level)")
	pluginInstallCmd.Flags().BoolVar(&pluginInstallForce, "force", false, "Overwrite existing plugin of the same name")
	pluginInstallCmd.Flags().StringVar(&pluginInstallRef, "ref", "", "Git ref (branch, tag, or commit) to install from a remote repo")
	pluginCmd.AddCommand(pluginInstallCmd)
	pluginCmd.AddCommand(pluginListCmd)
	pluginCmd.AddCommand(pluginRemoveCmd)
	rootCmd.AddCommand(pluginCmd)
}

var pluginCmd = &cobra.Command{
	Use:   "plugin",
	Short: "Manage ra plugins",
	// Inherits root.PersistentPreRunE: user config required, project config
	// optional, full Validate runs. Plugin install uploads secrets to GCP, so
	// failing fast on missing config is preferable to mid-pipeline errors.
}

var pluginInstallUser bool
var pluginInstallLocal bool
var pluginInstallForce bool
var pluginInstallRef string

// installMeta is the structure written/read from .ra-install.yaml inside a
// plugin's installed directory.
//
// New installs populate Repo/Path/Ref. The legacy Source field is retained for
// reading .ra-install.yaml files written by older ra versions (single flat
// source string, no subpath); it is no longer written.
type installMeta struct {
	Repo        string `yaml:"repo,omitempty"`   // git URL or local path the plugin came from
	Path        string `yaml:"path,omitempty"`   // subdirectory within Repo (empty = root)
	Ref         string `yaml:"ref,omitempty"`    // git ref checked out, when specified
	InstalledAt string `yaml:"installed_at"`     // RFC3339 install timestamp
	Source      string `yaml:"source,omitempty"` // legacy; read-only for back-compat
}

// displaySource renders a human-readable source for `ra plugin list`. It prefers
// the new Repo/Path/Ref fields and falls back to the legacy Source field.
func (m installMeta) displaySource() string {
	if m.Repo == "" {
		return m.Source
	}
	s := m.Repo
	if m.Path != "" {
		s += " (" + m.Path + ")"
	}
	if m.Ref != "" {
		s += " @" + m.Ref
	}
	return s
}

var pluginInstallCmd = &cobra.Command{
	Use:   "install <source> [<plugin-path>]",
	Short: "Install a plugin from a git repository or local path",
	Long: `Install a plugin from a git repository or local path.

Single-argument form installs from a path or repo whose root contains
plugin.yaml:

  ra plugin install ./my-plugin
  ra plugin install https://github.com/owner/my-plugin

Two-argument form installs one plugin from a subdirectory of a repo (or local
directory) that holds several plugins — useful for a plugins monorepo:

  ra plugin install https://github.com/owner/ra-plugins github
  ra plugin install https://github.com/owner/ra-plugins claude --ref v1.2.0`,
	Args: cobra.RangeArgs(1, 2),
	// No PersistentPreRunE override → inherits pluginCmd.PersistentPreRunE
	// which loads cfg (logging and runner are not initialized for plugin subcommands).
	RunE: func(cmd *cobra.Command, args []string) error {
		// Parse args. With two args the first is the repo/dir and the second is
		// the subpath to the plugin within it; with one arg a bare name may
		// resolve to a local plugins/<name> dir (see resolvePluginSource).
		var source, subpath string
		if len(args) == 2 {
			source = args[0]
			subpath = args[1]
		} else {
			source = resolvePluginSource(args[0])
		}
		ref := pluginInstallRef
		local := isLocalPath(source)
		if ref != "" && local {
			fmt.Fprintln(cmd.ErrOrStderr(), "--ref is ignored for local-path installs")
			ref = ""
		}

		// ── 1. acquire plugin into a temp dir ─────────────────────────────
		tmp, err := os.MkdirTemp("", "ra-plugin-*")
		if err != nil {
			return fmt.Errorf("creating temp dir: %w", err)
		}
		defer os.RemoveAll(tmp)

		if local {
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
			if err := cloneRepo(source, ref, tmp); err != nil {
				return err
			}
		}

		// Rebase onto the requested subdirectory (if any) before reading the
		// plugin so a single repo can host many plugins.
		pluginDir, err := resolvePluginDir(tmp, subpath)
		if err != nil {
			return err
		}

		// ── 2. parse plugin.yaml ───────────────────────────────────────────
		p, err := plugins.LoadPlugin(os.DirFS(pluginDir))
		if err != nil {
			return fmt.Errorf("reading plugin.yaml: %w", err)
		}

		// ── 3. choose install level ────────────────────────────────────────
		level, err := chooseInstallLevel(cmd, pluginInstallUser, pluginInstallLocal)
		if err != nil {
			return err
		}

		// ── 4. determine dest directory ───────────────────────────────────
		wd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("determining working directory: %w", err)
		}
		var destBase string
		switch level {
		case "project":
			destBase = filepath.Join(wd, ".ra", "plugins")
		default: // "user"
			home, err := os.UserHomeDir()
			if err != nil {
				return fmt.Errorf("determining home directory: %w", err)
			}
			destBase = filepath.Join(home, ".config", "ra", "plugins")
		}
		dest := filepath.Join(destBase, p.Name())

		dest, err = preparePluginDestination(destBase, p.Name(), pluginInstallForce)
		if err != nil {
			return err
		}
		if err := copyDir(pluginDir, dest); err != nil {
			return fmt.Errorf("installing plugin: %w", err)
		}

		// ── 5. prompt for config + secret values ──────────────────────────
		r := bufio.NewReader(cmd.InOrStdin())
		// For secret prompts we need the real stdin fd for term.ReadPassword.
		// In non-interactive contexts (tests) Prompt will be called but secret
		// prompts will be skipped (plugin disabled or no secrets defined).
		res, err := p.Prompt(r, cmd.OutOrStdout(), cmd.ErrOrStderr(), os.Stdin)
		if err != nil {
			return fmt.Errorf("prompting plugin config: %w", err)
		}

		// ── 5.25. sync_all_projects: auto-derive shared bucket ────────────────
		// When the plugin is installed at user level with sync_all_projects=true
		// and the user hasn't manually set bucket_name_override, resolve the
		// shared bucket name from the user-level default project so every
		// workstation across all GCP projects converges on one bucket.
		if level == "user" {
			if v, _ := res.Config["sync_all_projects"].(bool); v {
				if override, _ := res.Config["bucket_name_override"].(string); override == "" {
					if err := deriveSyncAllProjectsBucket(p, res, cmd.OutOrStdout(), cmd.ErrOrStderr()); err != nil {
						fmt.Fprintf(cmd.ErrOrStderr(), "sync_all_projects: %v; set bucket_name_override manually\n", err)
					}
				}
			}
		}

		// ── 5.5. resolve GCP secret names (level follows plugin install level) ─
		secretLevel := level
		secretNames := map[string]string{}
		if len(allRelevantSecretDefs(p, res)) > 0 {
			secretNames = resolveSecretNames(r, cmd.OutOrStdout(), p, res, secretLevel, wd)
		}

		// ── 6. require GCP project; enable API; upload secrets ────────────
		if cfg == nil || cfg.Gcloud.Project == "" {
			return fmt.Errorf("ra config not loaded — run 'ra setup' first")
		}
		if len(allRelevantSecretDefs(p, res)) > 0 {
			enableCmd := exec.Command("gcloud", "services", "enable",
				"secretmanager.googleapis.com",
				"--project", cfg.Gcloud.Project, "--quiet")
			enableCmd.Stdout = cmd.OutOrStdout()
			enableCmd.Stderr = cmd.ErrOrStderr()
			if err := enableCmd.Run(); err != nil {
				_ = os.RemoveAll(dest)
				return fmt.Errorf("enabling Secret Manager API: %w", err)
			}
		}
		if err := uploadPluginSecrets(p, res, cfg.Gcloud.Project, secretNames); err != nil {
			_ = os.RemoveAll(dest)
			return fmt.Errorf("uploading secrets: %w", err)
		}

		// ── 7. write plugin config + secret entries ───────────────────────
		pluginCfg, err := pluginCfgPath(level)
		if err != nil {
			return err
		}
		secretCfg, err := pluginCfgPath(secretLevel)
		if err != nil {
			return err
		}
		if err := writePluginAndSecrets(pluginCfg, secretCfg, p, res, secretNames); err != nil {
			return err
		}

		// ── 8. write .ra-install.yaml ─────────────────────────────────────
		meta := installMeta{
			Repo:        source,
			Path:        subpath,
			Ref:         ref,
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
		if msg := strings.TrimSpace(p.Schema().PostInstallMessage); msg != "" {
			fmt.Fprintf(cmd.OutOrStdout(), "\n%s\n", msg)
		}
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
				Source:      meta.displaySource(),
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
				Source:      meta.displaySource(),
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

func resolvePluginSource(source string) string {
	localPlugin := filepath.Join("plugins", source)
	if fi, err := os.Stat(localPlugin); err == nil && fi.IsDir() {
		return localPlugin
	}
	return source
}

// resolvePluginDir returns the directory within an acquired tree (root) that
// holds the plugin's plugin.yaml. subpath selects a subdirectory; an empty
// subpath means the plugin lives at the root. It rejects subpaths that escape
// root (e.g. via "..").
func resolvePluginDir(root, subpath string) (string, error) {
	if subpath == "" {
		return root, nil
	}
	// Clean as if absolute so leading "/" or ".." cannot escape root, then strip
	// the leading separator before joining.
	clean := strings.TrimPrefix(filepath.Clean(string(filepath.Separator)+subpath), string(filepath.Separator))
	pluginDir := filepath.Join(root, clean)
	rel, err := filepath.Rel(root, pluginDir)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("plugin path %q escapes the repository root", subpath)
	}
	if fi, err := os.Stat(pluginDir); err != nil || !fi.IsDir() {
		return "", fmt.Errorf("plugin path %q not found in source", subpath)
	}
	return pluginDir, nil
}

// cloneRepo clones source into dest (an existing empty dir). When ref is empty
// it shallow-clones the default branch. When ref is a branch or tag a shallow
// clone of that ref is attempted first; if that fails (e.g. ref is a commit
// SHA, which --branch rejects) it falls back to a full clone + checkout.
func cloneRepo(source, ref, dest string) error {
	logging.L().Debug("git clone starting", "source", source, "ref", ref)
	if ref == "" {
		if err := runGit("", "clone", "--depth=1", source, dest); err != nil {
			logging.L().Debug("git clone failed", "source", source, "error", err.Error())
			return fmt.Errorf("git clone: %w", err)
		}
		logging.L().Debug("git clone ok", "source", source)
		return nil
	}
	if err := runGit("", "clone", "--depth=1", "--branch", ref, source, dest); err == nil {
		logging.L().Debug("git clone ok", "source", source, "ref", ref)
		return nil
	}
	// Fall back to a full clone + checkout to support commit SHAs and refs that
	// --branch does not accept. Clear anything the failed attempt left behind.
	logging.L().Debug("git clone --branch failed; retrying full clone", "source", source, "ref", ref)
	if err := clearDir(dest); err != nil {
		return fmt.Errorf("preparing clone dir: %w", err)
	}
	if err := runGit("", "clone", source, dest); err != nil {
		return fmt.Errorf("git clone: %w", err)
	}
	if err := runGit(dest, "checkout", ref); err != nil {
		return fmt.Errorf("git checkout %s: %w", ref, err)
	}
	logging.L().Debug("git clone ok", "source", source, "ref", ref)
	return nil
}

// runGit runs a git subcommand, optionally inside dir, wiring stdout/stderr
// through to the process.
func runGit(dir string, args ...string) error {
	c := exec.Command("git", args...)
	c.Dir = dir
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	return c.Run()
}

// clearDir removes all entries inside dir without removing dir itself.
func clearDir(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if err := os.RemoveAll(filepath.Join(dir, e.Name())); err != nil {
			return err
		}
	}
	return nil
}

func preparePluginDestination(destBase, name string, force bool) (string, error) {
	dest := filepath.Join(destBase, name)
	if _, err := os.Stat(dest); err == nil && !force {
		return "", fmt.Errorf("plugin %q already exists at %s; use --force to overwrite", name, dest)
	}
	if err := os.RemoveAll(dest); err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	if err := os.MkdirAll(destBase, 0755); err != nil {
		return "", err
	}
	return dest, nil
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

// writePluginAndSecrets writes plugin config to pluginCfg and secret entries to
// secretCfg. When they are the same path both are written in one YAML pass;
// when different, each file receives only its own portion.
func writePluginAndSecrets(pluginCfg, secretCfg string, p *plugins.FilePlugin, res *plugins.PromptResult, secretNames map[string]string) error {
	secretEnvVars := make(map[string]string)
	for _, sd := range allRelevantSecretDefs(p, res) {
		secretEnvVars[sd.Name] = sd.EnvVar
	}

	readCfg := func(path string) (string, error) {
		b, err := os.ReadFile(path)
		if errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("config.yaml not found at %s — run 'ra setup' first", path)
		}
		return string(b), err
	}

	if pluginCfg == secretCfg {
		original, err := readCfg(pluginCfg)
		if err != nil {
			return err
		}
		updated, err := configwrite.MergePluginConfig(original, p.Name(), res.Config, secretNames, secretEnvVars)
		if err != nil {
			return fmt.Errorf("merging plugin config: %w", err)
		}
		return os.WriteFile(pluginCfg, []byte(updated), 0644)
	}

	// Plugin config file — write plugin block only (nil secretNames → no secret entries).
	pluginOriginal, err := readCfg(pluginCfg)
	if err != nil {
		return err
	}
	pluginUpdated, err := configwrite.MergePluginConfig(pluginOriginal, p.Name(), res.Config, nil, nil)
	if err != nil {
		return fmt.Errorf("merging plugin config: %w", err)
	}
	if err := os.WriteFile(pluginCfg, []byte(pluginUpdated), 0644); err != nil {
		return fmt.Errorf("writing plugin config: %w", err)
	}

	// Secret config file — write only the secrets block.
	secretOriginal, err := readCfg(secretCfg)
	if err != nil {
		return err
	}
	secretUpdated, err := configwrite.MergeSecretEntries(secretOriginal, secretNames, secretEnvVars)
	if err != nil {
		return fmt.Errorf("merging secret entries: %w", err)
	}
	return os.WriteFile(secretCfg, []byte(secretUpdated), 0644)
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

// resolveSecretNames returns the GCP secret name for each logical secret.
// User-level secrets get a username suffix; project-level get the project
// dirname. A prompt is always shown so the user can override the proposed name.
func resolveSecretNames(r *bufio.Reader, out io.Writer, p *plugins.FilePlugin, res *plugins.PromptResult, secretLevel, wd string) map[string]string {
	names := make(map[string]string)
	for _, sd := range allRelevantSecretDefs(p, res) {
		base := gcpSecretName(sd)
		var proposed string
		if secretLevel == "project" {
			proposed = base + "-" + filepath.Base(wd)
		} else {
			proposed = base + "-" + currentUsername()
		}
		fmt.Fprintf(out, "GCP secret name for %q [%s]: ", sd.Name, proposed)
		line, _ := r.ReadString('\n')
		if name := strings.TrimSpace(line); name != "" {
			names[sd.Name] = name
		} else {
			names[sd.Name] = proposed
		}
	}
	return names
}

// currentUsername returns the current OS user's login name for use in GCP secret naming.
func currentUsername() string {
	if u, err := osuser.Current(); err == nil && u.Username != "" {
		return u.Username
	}
	if v := os.Getenv("USER"); v != "" {
		return v
	}
	return "user"
}

// deriveSyncAllProjectsBucket resolves a shared GCS bucket name from the
// user-level config and sets it as bucket_name_override in res.Config. The
// template is read from the plugin schema rather than hard-coded so it stays
// in sync with plugin.yaml automatically.
func deriveSyncAllProjectsBucket(p *plugins.FilePlugin, res *plugins.PromptResult, out, errOut io.Writer) error {
	// Find the bucket spec whose override_field is bucket_name_override.
	var tmpl string
	for _, spec := range p.Schema().Buckets {
		if spec.OverrideField == "bucket_name_override" {
			tmpl = spec.NameTemplate
			break
		}
	}
	if tmpl == "" {
		return fmt.Errorf("plugin has no bucket spec with override_field=bucket_name_override")
	}

	userCfgPath, err := config.UserConfigPath()
	if err != nil {
		return fmt.Errorf("cannot locate user config: %w", err)
	}
	userCfg, err := config.LoadUserConfig(userCfgPath)
	if err != nil {
		return fmt.Errorf("cannot load user config: %w", err)
	}
	if userCfg.Gcloud.Project == "" {
		return fmt.Errorf("user config has no gcloud.project set")
	}

	bucketName, err := gcs.ResolveBucketName(tmpl, userCfg.Gcloud.Project, userCfg.Git.UserEmail)
	if err != nil {
		return fmt.Errorf("resolving bucket name from template %q: %w", tmpl, err)
	}

	res.Config["bucket_name_override"] = bucketName
	fmt.Fprintf(out, "sync_all_projects: shared bucket → %q (user project: %s)\n",
		bucketName, userCfg.Gcloud.Project)
	return nil
}

// uploadPluginSecrets uploads each secret value gathered during Prompt to GCP
// Secret Manager using the resolved GCP secret names.
func uploadPluginSecrets(p *plugins.FilePlugin, res *plugins.PromptResult, projectID string, secretNames map[string]string) error {
	for _, sd := range allRelevantSecretDefs(p, res) {
		value, ok := res.SecretValues[sd.Name]
		if !ok {
			continue
		}
		if err := uploadSecretValue(projectID, secretNames[sd.Name], value); err != nil {
			return err
		}
	}
	return nil
}

// uploadSecretValue creates the secret if it doesn't exist, then adds a version.
func uploadSecretValue(projectID, gcpSecretName, value string) error {
	// Check if secret exists.
	logging.L().Debug("gcloud starting", "subcmd", "secrets describe")
	checkStart := time.Now()
	checkCmd := exec.Command("gcloud", "secrets", "describe", gcpSecretName, "--project", projectID)
	exists := checkCmd.Run() == nil
	if exists {
		logging.L().Debug("gcloud ok", "subcmd", "secrets describe", "duration_s", time.Since(checkStart).Seconds())
	} else {
		logging.L().Debug("gcloud not found", "subcmd", "secrets describe", "duration_s", time.Since(checkStart).Seconds())
	}

	if !exists {
		logging.L().Debug("gcloud starting", "subcmd", "secrets create")
		createStart := time.Now()
		createCmd := exec.Command("gcloud", "secrets", "create", gcpSecretName,
			"--replication-policy=automatic",
			"--project", projectID)
		createCmd.Stdin = os.Stdin
		createCmd.Stdout = os.Stdout
		createCmd.Stderr = os.Stderr
		if err := createCmd.Run(); err != nil {
			logging.L().Debug("gcloud failed", "subcmd", "secrets create", "duration_s", time.Since(createStart).Seconds())
			return fmt.Errorf("creating secret %s: %w", gcpSecretName, err)
		}
		logging.L().Debug("gcloud ok", "subcmd", "secrets create", "duration_s", time.Since(createStart).Seconds())
	}

	// Add a new version.
	logging.L().Debug("gcloud starting", "subcmd", "secrets versions add")
	addStart := time.Now()
	addCmd := exec.Command("gcloud", "secrets", "versions", "add", gcpSecretName,
		"--data-file=-",
		"--project", projectID)
	addCmd.Stdin = bytes.NewReader([]byte(value))
	addCmd.Stdout = os.Stdout
	addCmd.Stderr = os.Stderr
	if err := addCmd.Run(); err != nil {
		logging.L().Debug("gcloud failed", "subcmd", "secrets versions add", "duration_s", time.Since(addStart).Seconds())
		return fmt.Errorf("adding version to secret %s: %w", gcpSecretName, err)
	}
	logging.L().Debug("gcloud ok", "subcmd", "secrets versions add", "duration_s", time.Since(addStart).Seconds())
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

// copyDir recursively copies src directory to dst. Git metadata (.git) is
// skipped so cloned repositories don't drag their history into the installed
// plugin (and, downstream, into the assembled docker build context).
func copyDir(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() && info.Name() == ".git" {
			return filepath.SkipDir
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
