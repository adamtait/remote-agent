// cws/cmd/setup.go
package cmd

import (
	"fmt"
	"os"

	"github.com/adamtait/remote-agent/cws/internal/scaffold"
	"github.com/spf13/cobra"
)

// readFlags pulls bool flags from cmd via cmd.Flags().GetBool so values are
// scoped to this invocation — no package-level vars to leak between tests.
func readFlags(cmd *cobra.Command, names ...string) map[string]bool {
	out := map[string]bool{}
	for _, n := range names {
		v, _ := cmd.Flags().GetBool(n)
		out[n] = v
	}
	return out
}

var setupCmd = &cobra.Command{
	Use:   "setup",
	Short: "Scaffold cws config files (.workstation.yaml and ~/.config/cws/config.yaml)",
	Long: `Interactive setup: probes the environment for defaults (gcloud project/region,
git user, git origin) and prompts for each required field. Writes both
project- and user-level config files unless --project-only or --global-only
is set.`,
	// Override root's PersistentPreRunE — setup must work when config files
	// don't exist yet.
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error { return nil },
	RunE: func(cmd *cobra.Command, args []string) error {
		home, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("determining home directory: %w", err)
		}
		cwd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("determining working directory: %w", err)
		}
		f := readFlags(cmd, "force", "project-only", "global-only", "advanced")
		return scaffold.InteractiveScaffold(scaffold.InteractiveOptions{
			Stdin: cmd.InOrStdin(), Stdout: cmd.OutOrStdout(), Stderr: cmd.ErrOrStderr(),
			Cwd: cwd, Home: home, Prober: scaffold.RealProber{},
			Force:       f["force"],
			ProjectOnly: f["project-only"],
			GlobalOnly:  f["global-only"],
			Advanced:    f["advanced"],
		})
	},
}

var setupTemplateCmd = &cobra.Command{
	Use:   "template",
	Short: "Write commented YAML templates without prompting",
	RunE: func(cmd *cobra.Command, args []string) error {
		home, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("determining home directory: %w", err)
		}
		cwd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("determining working directory: %w", err)
		}
		f := readFlags(cmd, "force", "project-only", "global-only")
		return scaffold.TemplateScaffold(scaffold.TemplateOptions{
			Stdout: cmd.OutOrStdout(),
			Cwd:    cwd, Home: home, Prober: scaffold.RealProber{},
			Force:       f["force"],
			ProjectOnly: f["project-only"],
			GlobalOnly:  f["global-only"],
		})
	},
}

func init() {
	setupCmd.Flags().Bool("force", false, "overwrite existing config files")
	setupCmd.Flags().Bool("project-only", false, "scaffold only .workstation.yaml")
	setupCmd.Flags().Bool("global-only", false, "scaffold only ~/.config/cws/config.yaml")
	setupCmd.Flags().Bool("advanced", false, "also prompt for cluster/config/machine/disk fields")

	setupTemplateCmd.Flags().Bool("force", false, "overwrite existing config files")
	setupTemplateCmd.Flags().Bool("project-only", false, "scaffold only .workstation.yaml")
	setupTemplateCmd.Flags().Bool("global-only", false, "scaffold only ~/.config/cws/config.yaml")

	setupCmd.AddCommand(setupTemplateCmd)
	rootCmd.AddCommand(setupCmd)
}
