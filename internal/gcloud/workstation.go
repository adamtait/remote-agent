package gcloud

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// WorkstationRef fully identifies a workstation across projects/regions/configs.
// Used by callers that need to target a workstation whose location is not
// implied by local config (e.g. `ra delete <name>` where <name> lives in a
// different cluster than the one in .ra/config.yaml).
type WorkstationRef struct {
	Project string
	Region  string
	Cluster string
	Config  string
	Name    string
}

// WorkstationInfo holds the parsed fields from a gcloud workstations describe/list call.
type WorkstationInfo struct {
	Name        string
	State       string
	Host        string
	CreateTime  string // RFC3339; populated by List
	ProjectName string // populated by ListAll
	ClusterName string // populated by ListAll
	ConfigName  string // populated by ListAll
}

// Client wraps a Runner with typed gcloud workstation operations.
type Client struct {
	runner Runner
}

// NewClient creates a Client using the provided Runner.
func NewClient(r Runner) *Client {
	return &Client{runner: r}
}

// List returns all workstations in the given config.
func (c *Client) List(project, region, cluster, cfgName string) ([]WorkstationInfo, error) {
	out, err := c.runner.Output(
		"workstations", "list",
		"--cluster", cluster,
		"--config", cfgName,
		"--region", region,
		"--project", project,
		"--format", "value(name,state,createTime)",
	)
	if err != nil {
		return nil, err
	}
	var result []WorkstationInfo
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		parts := strings.Split(line, "\t")
		info := WorkstationInfo{}
		if len(parts) > 0 {
			info.Name = parts[0]
		}
		if len(parts) > 1 {
			info.State = parts[1]
		}
		if len(parts) > 2 {
			info.CreateTime = parts[2]
		}
		result = append(result, info)
	}
	return result, nil
}

// ListAll returns all workstations across all configs in the given project and region.
// region must be non-empty; it is validated by config.Validate before any command runs.
// Each WorkstationInfo is populated with ProjectName/ClusterName/ConfigName
// parsed from the full resource URI returned by the workstations API.
func (c *Client) ListAll(project, region string) ([]WorkstationInfo, error) {
	if region == "" {
		return nil, fmt.Errorf("region is required for ListAll")
	}
	out, err := c.runner.Output(
		"workstations", "list",
		"--project", project,
		"--region", region,
		"--format", "json",
	)
	if err != nil {
		return nil, err
	}
	trimmed := strings.TrimSpace(string(out))
	if trimmed == "" || trimmed == "[]" {
		return nil, nil
	}
	var raw []struct {
		Name  string `json:"name"`
		State string `json:"state"`
	}
	if err := json.Unmarshal([]byte(trimmed), &raw); err != nil {
		return nil, fmt.Errorf("parsing workstations list JSON: %w", err)
	}
	result := make([]WorkstationInfo, 0, len(raw))
	for _, r := range raw {
		info := WorkstationInfo{State: r.State, Name: r.Name}
		// full resource name: projects/P/locations/R/workstationClusters/C/workstationConfigs/F/workstations/N
		if segments := strings.Split(r.Name, "/"); len(segments) == 10 {
			info.ProjectName = segments[1]
			info.ClusterName = segments[5]
			info.ConfigName = segments[7]
			info.Name = segments[9]
		}
		result = append(result, info)
	}
	return result, nil
}

// Describe returns details for a specific workstation.
func (c *Client) Describe(project, region, cluster, cfgName, name string) (*WorkstationInfo, error) {
	out, err := c.runner.Output(
		"workstations", "describe", name,
		"--cluster", cluster,
		"--config", cfgName,
		"--region", region,
		"--project", project,
		"--format", "value(name,state,host)",
	)
	if err != nil {
		return nil, err
	}
	line := strings.TrimSpace(string(out))
	parts := strings.Split(line, "\t")
	info := &WorkstationInfo{}
	if len(parts) > 0 {
		info.Name = parts[0]
	}
	if len(parts) > 1 {
		info.State = parts[1]
	}
	if len(parts) > 2 {
		info.Host = parts[2]
	}
	return info, nil
}

// Exists returns true if the workstation can be described without error.
func (c *Client) Exists(project, region, cluster, cfgName, name string) bool {
	return c.runner.Check(
		"workstations", "describe", name,
		"--cluster", cluster, "--config", cfgName,
		"--region", region, "--project", project,
	)
}

// Start starts a stopped workstation.
func (c *Client) Start(project, region, cluster, cfgName, name string) error {
	return c.runner.Run(
		"workstations", "start", name,
		"--cluster", cluster, "--config", cfgName,
		"--region", region, "--project", project,
	)
}

// Stop stops a running workstation.
func (c *Client) Stop(project, region, cluster, cfgName, name string) error {
	return c.runner.Run(
		"workstations", "stop", name,
		"--cluster", cluster, "--config", cfgName,
		"--region", region, "--project", project,
	)
}

// Delete deletes a workstation (caller must confirm first).
func (c *Client) Delete(project, region, cluster, cfgName, name string) error {
	return c.runner.Run(
		"workstations", "delete", name,
		"--cluster", cluster, "--config", cfgName,
		"--region", region, "--project", project,
		"--quiet",
	)
}

// SSH opens an interactive SSH session to the workstation.
func (c *Client) SSH(project, region, cluster, cfgName, name string) error {
	return c.runner.Run(
		"workstations", "ssh", name,
		"--cluster", cluster, "--config", cfgName,
		"--region", region, "--project", project,
	)
}

// Logs streams or snapshots container logs for the workstation from Cloud Logging.
// If follow is true, streams indefinitely (until Ctrl-C). If tail > 0, limits to that many entries.
func (c *Client) Logs(project, name string, follow bool, tail int) error {
	filter := fmt.Sprintf(
		`resource.type="workstations.googleapis.com/Workstation" AND resource.labels.workstation_id=%q`,
		name,
	)
	args := []string{"logging", "read", filter, "--project", project}
	if follow {
		args = append(args, "--follow")
	}
	if tail > 0 {
		args = append(args, "--limit", strconv.Itoa(tail))
	}
	return c.runner.Run(args...)
}
