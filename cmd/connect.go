package cmd

import (
	"io"

	"github.com/adamtait/remote-agent/internal/config"
	"github.com/adamtait/remote-agent/internal/gcloud"
	"github.com/spf13/cobra"
)

var connectCmd = &cobra.Command{
	Use:   "connect [name]",
	Short: "Open an SSH session to a workstation (starts it if stopped)",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		var name string
		if len(args) == 1 {
			name = args[0]
		}
		return runConnect(cfg, client, name, readLocationOverrides(cmd), cmd.ErrOrStderr())
	},
}

func runConnect(cfg *config.Config, client *gcloud.Client, name string, override wsOverride, errOut io.Writer) error {
	ref, err := resolveWorkstationRef(cfg, client, name, override)
	if err != nil {
		return err
	}
	if err := ensureRunning(client, ref, errOut); err != nil {
		return err
	}
	return client.SSH(ref.Project, ref.Region, ref.Cluster, ref.Config, ref.Name)
}

func init() {
	addLocationOverrideFlags(connectCmd)
	rootCmd.AddCommand(connectCmd)
}
