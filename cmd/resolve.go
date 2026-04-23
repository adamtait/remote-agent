package cmd

import (
	"bufio"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/adamtait/remote-agent/internal/config"
	"github.com/adamtait/remote-agent/internal/gcloud"
)

// resolveWorkstationName returns the workstation name to target.
// If name is non-empty, it is used directly. Otherwise, the default (<repo-slug>-<username>)
// is tried; if not found and exactly one workstation exists, that one is used; if multiple
// exist, the user is prompted to choose.
func resolveWorkstationName(cfg *config.Config, client *gcloud.Client, name string) (string, error) {
	if name != "" {
		return name, nil
	}

	// Try the default workstation name.
	if client.Exists(cfg.ProjectID, cfg.Region, cfg.ClusterName, cfg.ConfigName, cfg.WorkstationName) {
		info, err := client.Describe(cfg.ProjectID, cfg.Region, cfg.ClusterName, cfg.ConfigName, cfg.WorkstationName)
		if err == nil {
			return info.Name, nil
		}
	}

	// Fall back: list all workstations in the cluster.
	workstations, err := client.List(cfg.ProjectID, cfg.Region, cfg.ClusterName)
	if err != nil {
		return "", fmt.Errorf("listing workstations: %w", err)
	}
	if len(workstations) == 0 {
		return "", fmt.Errorf("no workstations found in cluster %q", cfg.ClusterName)
	}
	if len(workstations) == 1 {
		return workstations[0].Name, nil
	}

	// Multiple workstations — prompt.
	fmt.Fprintln(os.Stderr, "Multiple workstations found:")
	for i, ws := range workstations {
		fmt.Fprintf(os.Stderr, "  %d) %s (%s)\n", i+1, ws.Name, ws.State)
	}
	fmt.Fprintf(os.Stderr, "Select workstation [1-%d]: ", len(workstations))
	scanner := bufio.NewScanner(os.Stdin)
	if !scanner.Scan() {
		return "", fmt.Errorf("no selection made")
	}
	n, err := strconv.Atoi(strings.TrimSpace(scanner.Text()))
	if err != nil || n < 1 || n > len(workstations) {
		return "", fmt.Errorf("invalid selection")
	}
	return workstations[n-1].Name, nil
}
