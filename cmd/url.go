package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

var urlCmd = &cobra.Command{
	Use:   "url [name]",
	Short: "Print the browser IDE URL for a workstation",
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
		info, err := client.Describe(cfg.Gcloud.Project, ws.Region, ws.ClusterName, ws.ConfigName, resolved)
		if err != nil {
			return err
		}
		if info.Host == "" {
			return fmt.Errorf("workstation %q has no host (is it running?)", resolved)
		}
		fmt.Printf("https://80-%s\n", info.Host)
		return nil
	},
}

func init() {
	rootCmd.AddCommand(urlCmd)
}
