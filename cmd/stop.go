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
		ref, err := resolveWorkstationRef(cfg, client, name, readLocationOverrides(cmd))
		if err != nil {
			return err
		}
		return client.Stop(ref.Project, ref.Region, ref.Cluster, ref.Config, ref.Name)
	},
}

func init() {
	addLocationOverrideFlags(stopCmd)
	rootCmd.AddCommand(stopCmd)
}
