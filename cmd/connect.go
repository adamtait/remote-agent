package cmd

import (
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
		resolved, err := resolveWorkstationName(cfg, client, name)
		if err != nil {
			return err
		}
		return client.SSH(cfg.ProjectID, cfg.Region, cfg.ClusterName, cfg.ConfigName, resolved)
	},
}

func init() {
	rootCmd.AddCommand(connectCmd)
}
