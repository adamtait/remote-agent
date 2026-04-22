package cmd

import (
	"io"

	"github.com/adamtait/remote-agent/internal/config"
	"github.com/adamtait/remote-agent/internal/gcloud"
	"github.com/spf13/cobra"
)

var connectCmd = &cobra.Command{
	Use:   "connect [name]",
	Short: "Open an SSH session to a workstation",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		var name string
		if len(args) == 1 {
			name = args[0]
		}
		return runConnect(cfg, client, name, cmd.ErrOrStderr())
	},
}

func runConnect(cfg *config.Config, client *gcloud.Client, name string, errOut io.Writer) error {
	_ = errOut // unused until Task 3 adds the auto-start notice
	resolved, err := resolveWorkstationName(cfg, client, name)
	if err != nil {
		return err
	}
	return client.SSH(cfg.ProjectID, cfg.Region, cfg.ClusterName, cfg.ConfigName, resolved)
}

func init() {
	rootCmd.AddCommand(connectCmd)
}
