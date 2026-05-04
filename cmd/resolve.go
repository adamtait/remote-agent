package cmd

import (
	"fmt"
	"sort"
	"strings"

	"github.com/adamtait/remote-agent/internal/config"
	"github.com/adamtait/remote-agent/internal/gcloud"
	"github.com/spf13/cobra"
)

// addLocationOverrideFlags registers --project/--cluster/--config on a command.
// Read them at RunE time via readLocationOverrides(cmd).
func addLocationOverrideFlags(c *cobra.Command) {
	c.Flags().String("project", "", "GCP project (overrides config)")
	c.Flags().String("cluster", "", "Workstation cluster (overrides config)")
	c.Flags().String("config", "", "Workstation config (overrides config)")
}

// readLocationOverrides extracts the override flag values registered by
// addLocationOverrideFlags. Unset flags yield empty strings.
func readLocationOverrides(c *cobra.Command) wsOverride {
	p, _ := c.Flags().GetString("project")
	cl, _ := c.Flags().GetString("cluster")
	cf, _ := c.Flags().GetString("config")
	return wsOverride{Project: p, Cluster: cl, Config: cf}
}

// wsOverride is the per-command override of the project/cluster/config that
// would otherwise come from the merged config. Empty fields fall back to the
// config defaults; explicit values take precedence.
type wsOverride struct {
	Project string
	Cluster string
	Config  string
}

// resolveWorkstationRef returns a fully-qualified WorkstationRef for the given
// name and overrides.
//
//   - When name is empty: try cfg.WorkstationName, then fall back to the most
//     recently created workstation in the resolved cluster/config.
//
//   - When name is a full resource URI
//     (projects/.../workstationClusters/X/workstationConfigs/Y/workstations/Z):
//     parse it directly. Override flags must agree if also set.
//
//   - When name is a short id and override.Cluster + override.Config are both
//     set: use them directly. Calls Exists as a pre-flight check.
//
//   - When name is a short id and cluster/config come from defaults: search
//     ListAll(project, region) for an entry matching the short name. Zero
//     hits → not-found error; multiple hits → ambiguous error instructing
//     the user to disambiguate via --cluster/--config.
func resolveWorkstationRef(cfg *config.Config, client *gcloud.Client, name string, override wsOverride) (gcloud.WorkstationRef, error) {
	ws := cfg.Gcloud.Workstation
	ref := gcloud.WorkstationRef{
		Project: pickStr(override.Project, cfg.Gcloud.Project),
		Region:  ws.Region,
		Cluster: pickStr(override.Cluster, ws.ClusterName),
		Config:  pickStr(override.Config, ws.ConfigName),
		Name:    name,
	}

	if name != "" && strings.Contains(name, "/workstations/") {
		parsed, err := parseWorkstationURI(name)
		if err != nil {
			return gcloud.WorkstationRef{}, err
		}
		// If overrides are explicit, they must agree with the URI to avoid
		// silent misrouting. Empty overrides defer to the URI.
		if override.Project != "" && override.Project != parsed.Project {
			return gcloud.WorkstationRef{}, fmt.Errorf("--project %q conflicts with URI project %q", override.Project, parsed.Project)
		}
		if override.Cluster != "" && override.Cluster != parsed.Cluster {
			return gcloud.WorkstationRef{}, fmt.Errorf("--cluster %q conflicts with URI cluster %q", override.Cluster, parsed.Cluster)
		}
		if override.Config != "" && override.Config != parsed.Config {
			return gcloud.WorkstationRef{}, fmt.Errorf("--config %q conflicts with URI config %q", override.Config, parsed.Config)
		}
		return parsed, nil
	}

	if name == "" {
		if ref.Cluster == "" || ref.Config == "" {
			return gcloud.WorkstationRef{}, fmt.Errorf("workstation name required (cluster/config not set)")
		}
		if client.Exists(ref.Project, ref.Region, ref.Cluster, ref.Config, cfg.WorkstationName) {
			ref.Name = cfg.WorkstationName
			return ref, nil
		}
		listed, err := client.List(ref.Project, ref.Region, ref.Cluster, ref.Config)
		if err != nil {
			return gcloud.WorkstationRef{}, fmt.Errorf("listing workstations: %w", err)
		}
		if len(listed) == 0 {
			return gcloud.WorkstationRef{}, fmt.Errorf("no workstations found in cluster %q config %q", ref.Cluster, ref.Config)
		}
		sort.Slice(listed, func(i, j int) bool { return listed[i].CreateTime > listed[j].CreateTime })
		ref.Name = listed[0].Name
		return ref, nil
	}

	// Name is a short id.
	if override.Cluster != "" && override.Config != "" {
		if !client.Exists(ref.Project, ref.Region, ref.Cluster, ref.Config, name) {
			return gcloud.WorkstationRef{}, fmt.Errorf("no workstation %q in %s/%s/%s", name, ref.Project, ref.Cluster, ref.Config)
		}
		return ref, nil
	}

	listed, err := client.ListAll(ref.Project, ref.Region)
	if err != nil {
		return gcloud.WorkstationRef{}, fmt.Errorf("listing workstations: %w", err)
	}
	matches := make([]gcloud.WorkstationInfo, 0, 2)
	for _, info := range listed {
		if info.Name != name {
			continue
		}
		if override.Cluster != "" && info.ClusterName != override.Cluster {
			continue
		}
		if override.Config != "" && info.ConfigName != override.Config {
			continue
		}
		matches = append(matches, info)
	}
	switch len(matches) {
	case 0:
		return gcloud.WorkstationRef{}, fmt.Errorf("no workstation %q in project %s region %s", name, ref.Project, ref.Region)
	case 1:
		m := matches[0]
		ref.Cluster = m.ClusterName
		ref.Config = m.ConfigName
		return ref, nil
	default:
		locs := make([]string, 0, len(matches))
		for _, m := range matches {
			locs = append(locs, fmt.Sprintf("%s/%s", m.ClusterName, m.ConfigName))
		}
		return gcloud.WorkstationRef{}, fmt.Errorf("workstation %q is ambiguous; matches %s — pass --cluster and --config to disambiguate", name, strings.Join(locs, ", "))
	}
}

// parseWorkstationURI parses a full workstation resource URI of the form
// projects/P/locations/R/workstationClusters/C/workstationConfigs/F/workstations/N.
func parseWorkstationURI(uri string) (gcloud.WorkstationRef, error) {
	parts := strings.Split(uri, "/")
	if len(parts) != 10 ||
		parts[0] != "projects" ||
		parts[2] != "locations" ||
		parts[4] != "workstationClusters" ||
		parts[6] != "workstationConfigs" ||
		parts[8] != "workstations" {
		return gcloud.WorkstationRef{}, fmt.Errorf("not a valid workstation URI: %q", uri)
	}
	return gcloud.WorkstationRef{
		Project: parts[1],
		Region:  parts[3],
		Cluster: parts[5],
		Config:  parts[7],
		Name:    parts[9],
	}, nil
}

func pickStr(override, fallback string) string {
	if override != "" {
		return override
	}
	return fallback
}
