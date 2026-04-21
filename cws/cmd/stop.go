package cmd

import (
	"github.com/spf13/cobra"
)

var stopCmd = &cobra.Command{
	Use:   "stop [name]",
	Short: "Stop a running workstation",
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
		return client.Stop(cfg.ProjectID, cfg.Region, cfg.ClusterName, cfg.ConfigName, resolved)
	},
}

func init() {
	rootCmd.AddCommand(stopCmd)
}
