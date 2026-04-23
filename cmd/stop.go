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
		ws := cfg.Gcloud.Workstation
		return client.Stop(cfg.Gcloud.Project, ws.Region, ws.ClusterName, ws.ConfigName, resolved)
	},
}

func init() {
	rootCmd.AddCommand(stopCmd)
}
