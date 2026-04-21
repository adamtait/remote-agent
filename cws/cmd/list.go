package cmd

import (
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/spf13/cobra"
)

var listCmd = &cobra.Command{
	Use:   "list",
	Short: "List workstations in the cluster",
	RunE: func(cmd *cobra.Command, args []string) error {
		workstations, err := client.List(cfg.ProjectID, cfg.Region, cfg.ClusterName)
		if err != nil {
			return err
		}
		if len(workstations) == 0 {
			fmt.Println("No workstations found.")
			return nil
		}
		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "NAME\tSTATE")
		for _, ws := range workstations {
			fmt.Fprintf(w, "%s\t%s\n", ws.Name, ws.State)
		}
		return w.Flush()
	},
}

func init() {
	rootCmd.AddCommand(listCmd)
}
