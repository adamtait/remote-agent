// ws/cmd/scaffold.go
package cmd

import (
	"fmt"
	"os"

	"github.com/adamtait/remote-agent/internal/scaffold"
	"github.com/spf13/cobra"
)

var setupScaffoldCmd = &cobra.Command{
	Use:   "scaffold",
	Short: "Write customizable .ws/Dockerfile, .ws/entrypoint.sh, and .ws/profile.d/ into the project",
	Long: `Materialize the default build assets (Dockerfile, entrypoint.sh, and
.ws/profile.d/00-ws-wait.sh) into <project>/.ws/. Users (and AI agents) edit
these files freely; ws does not modify them on upgrade. Re-run with --force
to overwrite with current embedded defaults.

Source precedence per file: ~/.config/ws/template/<name> if present, else
the embedded default compiled into the ws binary.`,
	// Must run in fresh repos that have no .ws/config.yaml yet.
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
		f := readFlags(cmd, "force", "dry-run")
		_, err = scaffold.ScaffoldAssets(scaffold.ScaffoldAssetsOptions{
			Cwd: cwd, Home: home, Stdout: cmd.OutOrStdout(),
			Force:  f["force"],
			DryRun: f["dry-run"],
		})
		return err
	},
}

func init() {
	setupScaffoldCmd.Flags().Bool("force", false, "overwrite existing .ws/* files")
	setupScaffoldCmd.Flags().Bool("dry-run", false, "print what would be written without touching disk")
	setupCmd.AddCommand(setupScaffoldCmd)
}
