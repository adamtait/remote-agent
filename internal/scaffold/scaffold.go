// ws/internal/scaffold/scaffold.go
package scaffold

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// fileExists reports whether path refers to an existing regular file or dir.
// A stat error other than "not exists" is treated as "does not exist" for
// the purposes of skip-if-present; the subsequent write will surface any
// real I/O error with more context.
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

	Force       bool
	ProjectOnly bool
	GlobalOnly  bool
	Advanced    bool
}

type TemplateOptions struct {
	Stdout io.Writer
	Cwd    string
	Home   string
	Prober Prober

	Force       bool
	ProjectOnly bool
	GlobalOnly  bool
}

func InteractiveScaffold(opts InteractiveOptions) error {
	if opts.ProjectOnly && opts.GlobalOnly {
		return fmt.Errorf("--project-only and --global-only are mutually exclusive")
	}

	projectSection := !opts.GlobalOnly
	writeProject := projectSection
	writeGlobal := !opts.ProjectOnly

	projectPath := filepath.Join(opts.Cwd, ".ws", "config.yaml")
	globalPath := filepath.Join(opts.Home, ".config", "ws", "config.yaml")

	// Idempotent re-runs: if a config file already exists and --force wasn't
	// passed, skip that section's prompts and writes while still scaffolding
	// build assets. --force bypasses the skip and regenerates everything.
	if writeProject && !opts.Force && fileExists(projectPath) {
		fmt.Fprintf(opts.Stdout, "skip %s (already present); re-run with --force to regenerate\n", projectPath)
		writeProject = false
	}
	if writeGlobal && !opts.Force && fileExists(globalPath) {
		fmt.Fprintf(opts.Stdout, "skip %s (already present); re-run with --force to regenerate\n", globalPath)
		writeGlobal = false
	}

	// Share a single bufio.Reader across all Prompt calls so buffered bytes
	// don't get dropped between prompts. bufio.NewReader returns the input
	// unchanged if it's already a *bufio.Reader of adequate size, so passing
	// this to Prompt is safe.
	stdin := bufio.NewReader(opts.Stdin)

	warnPrereqs(opts.Stderr, opts.Prober)

	pv := ProbedValues{
		ProjectID:     opts.Prober.GcloudProject(),
		Region:        GcloudRegionWithFallback(opts.Prober),
		GitUserName:   opts.Prober.GitUserName(),
		GitUserEmail:  opts.Prober.GitUserEmail(),
		GithubRepoURL: opts.Prober.GitOriginURL(),
	}

	var projectUV UserProjectValues
	var globalUV UserGlobalValues

	if writeProject {
		projectID, err := Prompt(stdin, opts.Stdout, "project_id", pv.ProjectID, ValidateProjectID)
		if err != nil {
			return err
		}
		projectUV.ProjectID = projectID

		if !opts.Prober.BillingEnabled(projectID) && opts.Stderr != nil {
			fmt.Fprintf(opts.Stderr, "WARN: could not confirm billing is enabled on %s (ws create will fail otherwise)\n", projectID)
		}

		repoURL, err := Prompt(stdin, opts.Stdout, "github_repo_url", pv.GithubRepoURL, ValidateGithubRepoURL)
		if err != nil {
			return err
		}
		if normalized := NormalizeGithubRepoURL(repoURL); normalized != repoURL {
			if opts.Stderr != nil {
				fmt.Fprintf(opts.Stderr, "Converting SSH URL to HTTPS (workstation uses PAT auth): %s → %s\n", repoURL, normalized)
			}
			repoURL = normalized
		}
		projectUV.GithubRepoURL = repoURL

		if opts.Advanced {
			if projectUV.Region, err = Prompt(stdin, opts.Stdout, "region", pv.Region, nil); err != nil {
				return err
			}
			if projectUV.ClusterName, err = Prompt(stdin, opts.Stdout, "cluster_name", "claude-dev-cluster", nil); err != nil {
				return err
			}
			if projectUV.ConfigName, err = Prompt(stdin, opts.Stdout, "config_name", "claude-dev-config", nil); err != nil {
				return err
			}
			if projectUV.MachineType, err = Prompt(stdin, opts.Stdout, "machine_type", "e2-standard-4", nil); err != nil {
				return err
			}
			if projectUV.DiskSizeGB, err = Prompt(stdin, opts.Stdout, "disk_size_gb", "50", nil); err != nil {
				return err
			}
		}
	}

	if writeGlobal {
		var err error
		if globalUV.Region, err = Prompt(stdin, opts.Stdout, "region", pv.Region, nil); err != nil {
			return err
		}
		if globalUV.GitUserName, err = Prompt(stdin, opts.Stdout, "git_user_name", pv.GitUserName, nil); err != nil {
			return err
		}
		if globalUV.GitUserEmail, err = Prompt(stdin, opts.Stdout, "git_user_email", pv.GitUserEmail, nil); err != nil {
			return err
		}
	}

	if writeProject || writeGlobal {
		fmt.Fprintln(opts.Stdout)
		if writeProject {
			fmt.Fprintln(opts.Stdout, "About to write "+projectPath+":")
			fmt.Fprintln(opts.Stdout, indent(RenderProjectTemplate(pv, projectUV), "  "))
		}
		if writeGlobal {
			fmt.Fprintln(opts.Stdout, "About to write "+globalPath+":")
			fmt.Fprintln(opts.Stdout, indent(RenderGlobalTemplate(pv, globalUV), "  "))
		}

		confirm, err := Prompt(stdin, opts.Stdout, "Proceed? [Y/n]", "Y", nil)
		if err != nil {
			return err
		}
		answer := strings.ToLower(strings.TrimSpace(confirm))
		if answer == "n" || answer == "no" {
			return fmt.Errorf("%w: user declined", ErrCancelled)
		}
	}

	// Scaffold build assets whenever the user is doing project-side setup —
	// independent of whether a project config already exists. OnlyMissing
	// skips assets that are already present; --force overwrites.
	if projectSection {
		if _, err := ScaffoldAssets(ScaffoldAssetsOptions{
			Cwd: opts.Cwd, Home: opts.Home, Stdout: opts.Stdout,
			Force:       opts.Force,
			OnlyMissing: !opts.Force,
		}); err != nil {
			return err
		}
	}

	if writeProject {
		if err := WriteAtomic(projectPath, []byte(RenderProjectTemplate(pv, projectUV)), opts.Force); err != nil {
			return err
		}
	}
	if writeGlobal {
		if err := WriteAtomic(globalPath, []byte(RenderGlobalTemplate(pv, globalUV)), opts.Force); err != nil {
			return err
		}
	}

	fmt.Fprintln(opts.Stdout, "Run 'ws create' to provision the workstation.")
	return nil
}

func TemplateScaffold(opts TemplateOptions) error {
	if opts.ProjectOnly && opts.GlobalOnly {
		return fmt.Errorf("--project-only and --global-only are mutually exclusive")
	}

	pv := ProbedValues{
		ProjectID:     opts.Prober.GcloudProject(),
		Region:        opts.Prober.GcloudRegion(),
		GitUserName:   opts.Prober.GitUserName(),
		GitUserEmail:  opts.Prober.GitUserEmail(),
		GithubRepoURL: opts.Prober.GitOriginURL(),
	}

	// Scaffold build assets first; they remain idempotent via OnlyMissing
	// so re-runs are no-ops for already-materialized files.
	if !opts.GlobalOnly {
		if _, err := ScaffoldAssets(ScaffoldAssetsOptions{
			Cwd: opts.Cwd, Home: opts.Home, Stdout: opts.Stdout,
			Force:       opts.Force,
			OnlyMissing: !opts.Force,
		}); err != nil {
			return err
		}
	}
	if !opts.GlobalOnly {
		path := filepath.Join(opts.Cwd, ".ws", "config.yaml")
		if !opts.Force && fileExists(path) {
			fmt.Fprintf(opts.Stdout, "skip %s (already present); re-run with --force to regenerate\n", path)
		} else {
			if err := WriteAtomic(path, []byte(RenderProjectTemplate(pv, UserProjectValues{})), opts.Force); err != nil {
				return err
			}
			fmt.Fprintln(opts.Stdout, "Wrote "+path)
		}
	}
	if !opts.ProjectOnly {
		path := filepath.Join(opts.Home, ".config", "ws", "config.yaml")
		if !opts.Force && fileExists(path) {
			fmt.Fprintf(opts.Stdout, "skip %s (already present); re-run with --force to regenerate\n", path)
		} else {
			if err := WriteAtomic(path, []byte(RenderGlobalTemplate(pv, UserGlobalValues{})), opts.Force); err != nil {
				return err
			}
			fmt.Fprintln(opts.Stdout, "Wrote "+path)
		}
	}
	return nil
}

func warnPrereqs(out io.Writer, p Prober) {
	if out == nil {
		return
	}
	if !p.HasGcloud() {
		fmt.Fprintln(out, "WARN: gcloud CLI not found on PATH — ws create will fail until installed")
	} else if !p.HasGcloudAuth() {
		fmt.Fprintln(out, "WARN: no active gcloud credentials — run 'gcloud auth login' before 'ws create'")
	}
	if !p.HasDocker() {
		fmt.Fprintln(out, "WARN: docker not found on PATH — ws create needs it to build the workstation image")
	}
}

func indent(s, prefix string) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	for i, l := range lines {
		lines[i] = prefix + l
	}
	return strings.Join(lines, "\n")
}
