// ra/internal/scaffold/scaffold.go
package scaffold

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

type InteractiveOptions struct {
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer

	Cwd    string
	Home   string
	Prober Prober

	Force             bool
	ProjectOnly       bool
	GlobalOnly        bool
	Advanced          bool
	WithProjectAssets bool
}

type TemplateOptions struct {
	Stdout io.Writer
	Cwd    string
	Home   string
	Prober Prober

	Force             bool
	ProjectOnly       bool
	GlobalOnly        bool
	WithProjectAssets bool
}

func InteractiveScaffold(opts InteractiveOptions) error {
	if opts.ProjectOnly && opts.GlobalOnly {
		return fmt.Errorf("--project-only and --global-only are mutually exclusive")
	}

	writeProject := !opts.GlobalOnly
	writeUser := !opts.ProjectOnly

	projectPath := filepath.Join(opts.Cwd, ".ra", "config.yaml")
	userPath := filepath.Join(opts.Home, ".config", "ra", "config.yaml")

	if writeProject && !opts.Force && fileExists(projectPath) {
		fmt.Fprintf(opts.Stdout, "skip %s (already present); re-run with --force to regenerate\n", projectPath)
		writeProject = false
	}
	if writeUser && !opts.Force && fileExists(userPath) {
		fmt.Fprintf(opts.Stdout, "skip %s (already present); re-run with --force to regenerate\n", userPath)
		writeUser = false
	}

	stdin := bufio.NewReader(opts.Stdin)

	warnPrereqs(opts.Stderr, opts.Prober)

	probed := probeDefaults(opts.Prober)

	var pv ProjectValues
	var uv UserValues

	if writeProject {
		var err error
		projectID, err := Prompt(stdin, opts.Stdout, "project_id", probed.projectID, ValidateProjectID)
		if err != nil {
			return err
		}
		pv.ProjectID = projectID
		uv.GcloudProject = projectID

		if !opts.Prober.BillingEnabled(projectID) && opts.Stderr != nil {
			fmt.Fprintf(opts.Stderr, "WARN: could not confirm billing is enabled on %s\n", projectID)
		}

		repoURL, err := Prompt(stdin, opts.Stdout, "github_repo_url", probed.repoURL, ValidateGithubRepoURL)
		if err != nil {
			return err
		}
		if normalized := NormalizeGithubRepoURL(repoURL); normalized != repoURL {
			if opts.Stderr != nil {
				fmt.Fprintf(opts.Stderr, "Converting SSH URL to HTTPS: %s → %s\n", repoURL, normalized)
			}
			repoURL = normalized
		}
		pv.GithubRepoURL = repoURL
		uv.GithubRepoURL = repoURL
	}

	if writeUser && !writeProject {
		var err error
		projectID, err := Prompt(stdin, opts.Stdout, "project_id", probed.projectID, ValidateProjectID)
		if err != nil {
			return err
		}
		uv.GcloudProject = projectID

		repoURL, err := Prompt(stdin, opts.Stdout, "github_repo_url", probed.repoURL, ValidateGithubRepoURL)
		if err != nil {
			return err
		}
		if normalized := NormalizeGithubRepoURL(repoURL); normalized != repoURL {
			if opts.Stderr != nil {
				fmt.Fprintf(opts.Stderr, "Converting SSH URL to HTTPS: %s → %s\n", repoURL, normalized)
			}
			repoURL = normalized
		}
		uv.GithubRepoURL = repoURL
	}

	if writeUser {
		var err error
		if uv.GitUserName, err = Prompt(stdin, opts.Stdout, "git_user_name", probed.gitName, nil); err != nil {
			return err
		}
		if uv.GitUserEmail, err = Prompt(stdin, opts.Stdout, "git_user_email", probed.gitEmail, nil); err != nil {
			return err
		}

		uv.ClaudeEnabled, err = promptClaudeEnabled(stdin, opts.Stdout)
		if err != nil {
			return err
		}
		if uv.ClaudeEnabled {
			uv.ClaudeAuthMode, err = promptClaudeAuthMode(stdin, opts.Stdout)
			if err != nil {
				return err
			}
		}
	}

	if writeProject || writeUser {
		fmt.Fprintln(opts.Stdout)
		if writeProject {
			fmt.Fprintln(opts.Stdout, "About to write "+projectPath+":")
			fmt.Fprintln(opts.Stdout, indent(RenderProjectConfig(pv), "  "))
		}
		if writeUser {
			fmt.Fprintln(opts.Stdout, "About to write "+userPath+":")
			fmt.Fprintln(opts.Stdout, indent(RenderUserConfig(uv), "  "))
		}

		confirm, err := Prompt(stdin, opts.Stdout, "Proceed? [Y/n]", "Y", nil)
		if err != nil {
			return err
		}
		if answer := strings.ToLower(strings.TrimSpace(confirm)); answer == "n" || answer == "no" {
			return fmt.Errorf("%w: user declined", ErrCancelled)
		}
	}

	// Materialize build assets to user template dir.
	userTemplateDir := filepath.Join(opts.Home, ".config", "ra", "template")
	if _, err := ScaffoldAssets(ScaffoldAssetsOptions{
		TargetDir: userTemplateDir, Home: opts.Home, Stdout: opts.Stdout,
		Force: opts.Force, OnlyMissing: !opts.Force,
	}); err != nil {
		return err
	}

	// Optionally also write assets into .ra/.
	if opts.WithProjectAssets {
		wsDir := filepath.Join(opts.Cwd, ".ra")
		if _, err := ScaffoldAssets(ScaffoldAssetsOptions{
			TargetDir: wsDir, Home: opts.Home, Stdout: opts.Stdout,
			Force: opts.Force, OnlyMissing: !opts.Force,
		}); err != nil {
			return err
		}
	}

	if writeProject {
		if err := WriteAtomic(projectPath, []byte(RenderProjectConfig(pv)), opts.Force); err != nil {
			return err
		}
	}
	if writeUser {
		if err := WriteAtomic(userPath, []byte(RenderUserConfig(uv)), opts.Force); err != nil {
			return err
		}
	}

	fmt.Fprintln(opts.Stdout, "Run 'ra create' to provision the workstation.")
	return nil
}

func TemplateScaffold(opts TemplateOptions) error {
	if opts.ProjectOnly && opts.GlobalOnly {
		return fmt.Errorf("--project-only and --global-only are mutually exclusive")
	}

	// Materialize build assets.
	userTemplateDir := filepath.Join(opts.Home, ".config", "ra", "template")
	if !opts.GlobalOnly {
		if _, err := ScaffoldAssets(ScaffoldAssetsOptions{
			TargetDir: userTemplateDir, Home: opts.Home, Stdout: opts.Stdout,
			Force: opts.Force, OnlyMissing: !opts.Force,
		}); err != nil {
			return err
		}
	}
	if opts.WithProjectAssets {
		wsDir := filepath.Join(opts.Cwd, ".ra")
		if _, err := ScaffoldAssets(ScaffoldAssetsOptions{
			TargetDir: wsDir, Home: opts.Home, Stdout: opts.Stdout,
			Force: opts.Force, OnlyMissing: !opts.Force,
		}); err != nil {
			return err
		}
	}

	if !opts.GlobalOnly {
		path := filepath.Join(opts.Cwd, ".ra", "config.yaml")
		if !opts.Force && fileExists(path) {
			fmt.Fprintf(opts.Stdout, "skip %s (already present); re-run with --force to regenerate\n", path)
		} else {
			if err := WriteAtomic(path, []byte(RenderProjectConfig(ProjectValues{})), opts.Force); err != nil {
				return err
			}
			fmt.Fprintln(opts.Stdout, "Wrote "+path)
		}
	}
	if !opts.ProjectOnly {
		path := filepath.Join(opts.Home, ".config", "ra", "config.yaml")
		if !opts.Force && fileExists(path) {
			fmt.Fprintf(opts.Stdout, "skip %s (already present); re-run with --force to regenerate\n", path)
		} else {
			if err := WriteAtomic(path, []byte(RenderUserConfig(UserValues{})), opts.Force); err != nil {
				return err
			}
			fmt.Fprintln(opts.Stdout, "Wrote "+path)
		}
	}
	return nil
}

// probedDefaults holds environment-probed values used as prompt defaults.
type probedDefaults struct {
	projectID string
	repoURL   string
	gitName   string
	gitEmail  string
}

func probeDefaults(p Prober) probedDefaults {
	return probedDefaults{
		projectID: p.GcloudProject(),
		repoURL:   p.GitOriginURL(),
		gitName:   p.GitUserName(),
		gitEmail:  p.GitUserEmail(),
	}
}

// promptClaudeEnabled asks whether Claude Code should be installed in the
// workstation image. Defaults to N so existing users don't accidentally
// opt in; matches the "breaking-change with explicit opt-in" stance taken
// on the claude.enabled field.
func promptClaudeEnabled(r *bufio.Reader, out io.Writer) (bool, error) {
	choice, err := Prompt(r, out,
		"Enable Claude Code? (installs @anthropic-ai/claude-code into the image) [y/N]",
		"N", nil)
	if err != nil {
		return false, err
	}
	switch strings.ToLower(strings.TrimSpace(choice)) {
	case "y", "yes":
		return true, nil
	default:
		return false, nil
	}
}

// promptClaudeAuthMode asks the user whether to use OAuth or API key auth.
func promptClaudeAuthMode(r *bufio.Reader, out io.Writer) (ClaudeAuthMode, error) {
	fmt.Fprintln(out, "Claude auth mode:")
	fmt.Fprintln(out, "  1) OAuth token (recommended)")
	fmt.Fprintln(out, "  2) API key (via apiKeyHelper)")
	choice, err := Prompt(r, out, "choice [1/2]", "1", nil)
	if err != nil {
		return ClaudeAuthOAuth, err
	}
	if strings.TrimSpace(choice) == "2" {
		return ClaudeAuthAPIKey, nil
	}
	return ClaudeAuthOAuth, nil
}

func warnPrereqs(out io.Writer, p Prober) {
	if out == nil {
		return
	}
	if !p.HasGcloud() {
		fmt.Fprintln(out, "WARN: gcloud CLI not found on PATH — ra create will fail until installed")
	} else if !p.HasGcloudAuth() {
		fmt.Fprintln(out, "WARN: no active gcloud credentials — run 'gcloud auth login' before 'ra create'")
	}
	if !p.HasDocker() {
		fmt.Fprintln(out, "WARN: docker not found on PATH — ra create needs it to build the workstation image")
	}
}

func indent(s, prefix string) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	for i, l := range lines {
		lines[i] = prefix + l
	}
	return strings.Join(lines, "\n")
}
