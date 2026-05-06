package cmd

import (
	"fmt"
	"os"
	"sort"
	"text/tabwriter"

	"github.com/adamtait/remote-agent/internal/gcloud"
	"github.com/adamtait/remote-agent/internal/logging"
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

// runListAll lists workstations across every accessible GCP project and region.
func runListAll() error {
	projects, err := client.ListProjects()
	if err != nil {
		return err
	}
	if len(projects) == 0 {
		fmt.Println("No accessible GCP projects (check `gcloud auth list`).")
		return nil
	}
	var all []gcloud.WorkstationInfo
	for _, p := range projects {
		ws, err := client.ListAll(p, "-")
		if err != nil {
			logging.L().Debug("listing workstations failed", "project", p, "err", err)
			continue
		}
		all = append(all, ws...)
	}
	if len(all) == 0 {
		fmt.Println("No workstations found.")
		return nil
	}
	sort.Slice(all, func(i, j int) bool {
		a, b := all[i], all[j]
		if a.ProjectName != b.ProjectName {
			return a.ProjectName < b.ProjectName
		}
		if a.Region != b.Region {
			return a.Region < b.Region
		}
		if a.ClusterName != b.ClusterName {
			return a.ClusterName < b.ClusterName
		}
		if a.ConfigName != b.ConfigName {
			return a.ConfigName < b.ConfigName
		}
		return a.Name < b.Name
	})
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tSTATE\tPROJECT\tREGION\tCLUSTER\tCONFIG")
	for _, ws := range all {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n",
			ws.Name, ws.State, ws.ProjectName, ws.Region, ws.ClusterName, ws.ConfigName)
	}
	return w.Flush()
}

func init() {
	listCmd.Flags().BoolVar(&listAll, "all", false, "list all workstations across every accessible project and region")
	rootCmd.AddCommand(listCmd)
}
