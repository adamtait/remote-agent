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
		ref, err := resolveWorkstationRef(cfg, client, name, readLocationOverrides(cmd))
		if err != nil {
			return err
		}
		info, err := client.Describe(ref.Project, ref.Region, ref.Cluster, ref.Config, ref.Name)
		if err != nil {
			return err
		}
		if info.Host == "" {
			return fmt.Errorf("workstation %q has no host (is it running?)", ref.Name)
		}
		fmt.Printf("https://80-%s\n", info.Host)
		return nil
	},
}

func init() {
	addLocationOverrideFlags(urlCmd)
	rootCmd.AddCommand(urlCmd)
}
