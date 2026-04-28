package cmd

import (
	"fmt"
	"sort"

	"github.com/adamtait/remote-agent/internal/config"
	"github.com/adamtait/remote-agent/internal/gcloud"
)

// resolveWorkstationName returns the workstation name to target.
// If name is non-empty, it is used directly. Otherwise, the default (<project_id>-<username>)
// is tried; if not found, all workstations in the cluster are listed and the most recently
// created one is returned.
func resolveWorkstationName(cfg *config.Config, client *gcloud.Client, name string) (string, error) {
	ws := cfg.Gcloud.Workstation
	if name != "" {
		return name, nil
	}

	// Try the default workstation name.
	if client.Exists(cfg.Gcloud.Project, ws.Region, ws.ClusterName, ws.ConfigName, cfg.WorkstationName) {
		info, err := client.Describe(cfg.Gcloud.Project, ws.Region, ws.ClusterName, ws.ConfigName, cfg.WorkstationName)
		if err == nil {
			return info.Name, nil
		}
	}

	// Fall back: list all workstations and return the most recently created.
	workstations, err := client.List(cfg.Gcloud.Project, ws.Region, ws.ClusterName, ws.ConfigName)
	if err != nil {
		return "", fmt.Errorf("listing workstations: %w", err)
	}
	if len(workstations) == 0 {
		return "", fmt.Errorf("no workstations found in cluster %q", ws.ClusterName)
	}

	sort.Slice(workstations, func(i, j int) bool {
		return workstations[i].CreateTime > workstations[j].CreateTime
	})
	return workstations[0].Name, nil
}
