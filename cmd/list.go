package cmd

import (
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/spf13/cobra"
)

var listAll bool

var listCmd = &cobra.Command{
	Use:   "list",
	Short: "List workstations in the cluster",
	RunE: func(cmd *cobra.Command, args []string) error {
		if listAll {
			return runListAll()
		}
		return runList()
	},
}

// runList lists workstations filtered to the project/cluster/config in config.
func runList() error {
	ws := cfg.Gcloud.Workstation
	fmt.Printf("# project=%s  cluster=%s  config=%s\n", cfg.Gcloud.Project, ws.ClusterName, ws.ConfigName)
	workstations, err := client.List(cfg.Gcloud.Project, ws.Region, ws.ClusterName, ws.ConfigName)
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
}

// runListAll lists all workstations in the project across all configs.
func runListAll() error {
	ws := cfg.Gcloud.Workstation
	workstations, err := client.ListAll(cfg.Gcloud.Project, ws.Region)
	if err != nil {
		return err
	}
	if len(workstations) == 0 {
		fmt.Println("No workstations found.")
		return nil
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "PROJECT\tCONFIG\tNAME\tSTATE")
	for _, ws := range workstations {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", ws.ProjectName, ws.ConfigName, ws.Name, ws.State)
	}
	return w.Flush()
}

func init() {
	listCmd.Flags().BoolVar(&listAll, "all", false, "list all workstations across all configs in the project")
	rootCmd.AddCommand(listCmd)
}
