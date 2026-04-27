// ra/cmd/scaffold.go
package cmd

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/adamtait/remote-agent/internal/scaffold"
	"github.com/spf13/cobra"
)

var setupScaffoldCmd = &cobra.Command{
	Use:   "scaffold",
	Short: "Write customizable Dockerfile and startup scripts into .ra/",
	Long: `Materialize the default build assets (Dockerfile,
workstation-startup.d/200_remote-agent-setup.sh, and profile.d/00-ra-wait.sh)
into <project>/.ra/. Users (and AI agents) edit these files freely; ra does not
modify them on upgrade. Re-run with --force to overwrite with current embedded
defaults.`,
	// Must run in fresh repos that have no .ws/config.yaml yet.
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error { return nil },
	RunE: func(cmd *cobra.Command, args []string) error {
		cwd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("determining working directory: %w", err)
		}
		f := readFlags(cmd, "force")
		return scaffold.WriteCoreAssets(filepath.Join(cwd, ".ra"), f["force"], cmd.OutOrStdout())
	},
}

func init() {
	setupScaffoldCmd.Flags().Bool("force", false, "overwrite existing .ra/* files")
	setupCmd.AddCommand(setupScaffoldCmd)
}
