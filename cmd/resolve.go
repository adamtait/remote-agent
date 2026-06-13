package cmd

import (
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/adamtait/remote-agent/internal/config"
	"github.com/adamtait/remote-agent/internal/gcloud"
	"github.com/spf13/cobra"
)

// waitRunningTimeout/waitRunningInterval bound how long ensureRunning polls for
// a workstation to reach STATE_RUNNING. Cloud Workstations cold starts (image
// pull + startup scripts) routinely take 1-2 min, so 5 min is generous while
// still failing a genuinely stuck start in bounded time. Package-level so tests
// can shrink them.
var (
	waitRunningTimeout  = 5 * time.Minute
	waitRunningInterval = 5 * time.Second
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

// ensureRunning describes the workstation referenced by ref and ensures it is
// fully RUNNING before returning. If it is stopped it calls Start; if it is
// starting (or was just started) it waits, polling until the state reaches
// STATE_RUNNING. Used by `ra connect` and `ra tunnel`.
//
// Returning while the workstation is merely STARTING is the cause of the
// "[SSL: UNEXPECTED_EOF_WHILE_READING]" failures callers used to hit: the SSH
// tunnel would race ahead of the not-yet-ready backend. Blocking here until
// RUNNING is what makes the subsequent connect reliable.
//
// The returned bool is true only when the workstation was already RUNNING on
// the first describe — i.e. nothing had to be started or waited for. Callers
// use it to skip the post-start SSH-readiness probe on the warm path, where
// sshd is long up and a probe would only add latency.
func ensureRunning(client *gcloud.Client, ref gcloud.WorkstationRef, errOut io.Writer) (alreadyRunning bool, err error) {
	info, err := client.Describe(ref.Project, ref.Region, ref.Cluster, ref.Config, ref.Name)
	if err != nil {
		return false, fmt.Errorf("describing workstation: %w", err)
	}
	switch info.State {
	case "STATE_RUNNING":
		return true, nil
	case "STATE_STARTING":
		fmt.Fprintf(errOut, "Workstation %q is starting; waiting until it's ready...\n", ref.Name)
	default:
		fmt.Fprintf(errOut, "Workstation %q is %s; starting...\n", ref.Name, strings.TrimPrefix(info.State, "STATE_"))
		if err := client.Start(ref.Project, ref.Region, ref.Cluster, ref.Config, ref.Name); err != nil {
			return false, fmt.Errorf("starting workstation: %w", err)
		}
	}
	return false, waitUntilRunning(client, ref, errOut)
}

// waitUntilRunning polls Describe until the workstation reaches STATE_RUNNING,
// giving up after waitRunningTimeout. A workstation that ends up STOPPED again
// (a failed start) is reported as a terminal error rather than waited on.
func waitUntilRunning(client *gcloud.Client, ref gcloud.WorkstationRef, errOut io.Writer) error {
	deadline := time.Now().Add(waitRunningTimeout)
	for {
		info, err := client.Describe(ref.Project, ref.Region, ref.Cluster, ref.Config, ref.Name)
		if err != nil {
			return fmt.Errorf("describing workstation: %w", err)
		}
		switch info.State {
		case "STATE_RUNNING":
			return nil
		case "STATE_STOPPED", "STATE_STOPPING":
			return fmt.Errorf("workstation %q entered state %s while waiting for it to start",
				ref.Name, strings.TrimPrefix(info.State, "STATE_"))
		case "":
			// A successful Describe with no state means we couldn't parse the
			// output (format drift, wrong resource) — polling won't fix that, so
			// fail fast instead of spinning for the full timeout.
			return fmt.Errorf("workstation %q returned no state; cannot determine readiness", ref.Name)
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("workstation %q did not reach RUNNING within %s (last state: %s)",
				ref.Name, waitRunningTimeout, strings.TrimPrefix(info.State, "STATE_"))
		}
		time.Sleep(waitRunningInterval)
	}
}
