package gcloud

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

// SSHReady judges connectability by whether sshReadySentinel round-trips on the
// probe's stdout — a content-verified signal robust to two unreliable
// alternatives: gcloud's error-message wording (changes across versions/locales)
// and its exit code (`workstations ssh` exits 0 even on connection failures).
//
// The probe runs sshReadyProbeCmd, which *computes* the sentinel remotely via
// shell arithmetic rather than echoing a literal. So the success token never
// appears verbatim in the command line — it can only show up in the output if
// the remote shell actually executed. That guards against a false "ready" if
// gcloud ever echoed the probe command back into the stream we scan (today
// stdout and stderr are kept separate; this keeps it correct regardless).
const (
	sshReadyProbeCmd = `echo "ra_$((6*7))_ready"`
	sshReadySentinel = "ra_42_ready"

	// etReadySentinel is printed by the etserver probe only if a TCP connection
	// to the port succeeds (see ETServerReady). Like sshReadySentinel it is
	// computed remotely (via arithmetic) so it can't appear from a command-line
	// echo — only from the remote shell actually reaching a listener.
	etReadySentinel = "ra_et_84_ready"
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
	Region      string // populated by ListAll
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
// Pass "-" as region to aggregate across all regions (gcloud's wildcard convention).
// Each WorkstationInfo is populated with ProjectName/Region/ClusterName/ConfigName
// parsed from the full resource URI returned by the workstations API.
func (c *Client) ListAll(project, region string) ([]WorkstationInfo, error) {
	if region == "" {
		return nil, fmt.Errorf("region is required for ListAll (use \"-\" for all regions)")
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
		Name       string `json:"name"`
		State      string `json:"state"`
		Host       string `json:"host"`
		CreateTime string `json:"createTime"`
	}
	if err := json.Unmarshal([]byte(trimmed), &raw); err != nil {
		return nil, fmt.Errorf("parsing workstations list JSON: %w", err)
	}
	result := make([]WorkstationInfo, 0, len(raw))
	for _, r := range raw {
		info := WorkstationInfo{
			Name:       r.Name,
			State:      r.State,
			Host:       r.Host,
			CreateTime: r.CreateTime,
		}
		// full resource name: projects/P/locations/R/workstationClusters/C/workstationConfigs/F/workstations/N
		if segments := strings.Split(r.Name, "/"); len(segments) == 10 {
			info.ProjectName = segments[1]
			info.Region = segments[3]
			info.ClusterName = segments[5]
			info.ConfigName = segments[7]
			info.Name = segments[9]
		}
		result = append(result, info)
	}
	return result, nil
}

// ListProjects returns the IDs of all GCP projects the caller can access.
func (c *Client) ListProjects() ([]string, error) {
	out, err := c.runner.Output(
		"projects", "list",
		"--format", "value(projectId)",
	)
	if err != nil {
		return nil, err
	}
	var result []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		result = append(result, line)
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

// DeleteConfig deletes a workstations config (caller must confirm first, and
// must delete the config's workstation instances first). The reverse of the
// `configs create` step in `ra create`.
func (c *Client) DeleteConfig(project, region, cluster, cfgName string) error {
	return c.runner.Run(
		"workstations", "configs", "delete", cfgName,
		"--cluster", cluster,
		"--region", region, "--project", project,
		"--quiet",
	)
}

// ListConfigs returns the names of the workstations configs in a cluster. Used
// by `ra teardown` to detect whether other projects still share the cluster
// before deleting shared infrastructure. Names may come back as short IDs or
// full resource URIs depending on gcloud; callers should compare by basename.
func (c *Client) ListConfigs(project, region, cluster string) ([]string, error) {
	out, err := c.runner.Output(
		"workstations", "configs", "list",
		"--cluster", cluster,
		"--region", region, "--project", project,
		"--format", "value(name)",
	)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			names = append(names, line)
		}
	}
	return names, nil
}

// DeleteCluster deletes a workstations cluster (caller must confirm first, and
// must delete the cluster's configs first). This is the resource that accrues
// the fixed control-plane fee, so it is the primary target of `ra teardown`.
func (c *Client) DeleteCluster(project, region, cluster string) error {
	return c.runner.Run(
		"workstations", "clusters", "delete", cluster,
		"--region", region, "--project", project,
		"--quiet",
	)
}

// SSH opens an interactive SSH session to the workstation.
//
// Note: idle sessions are kept alive from the server side (sshd
// ClientAliveInterval, baked into the workstation image) — NOT here. gcloud
// hard-codes the client-side "-o ServerAliveInterval=0" ahead of any
// --ssh-flag we pass, and ssh honors the first value, so a client-side
// keepalive flag would be silently ignored. See the Dockerfile.
func (c *Client) SSH(project, region, cluster, cfgName, name string) error {
	return c.runner.Run(
		"workstations", "ssh", name,
		"--cluster", cluster, "--config", cfgName,
		"--region", region, "--project", project,
	)
}

// SSHReady reports whether the workstation's sshd is accepting connections, by
// running a non-interactive probe that echoes a sentinel and checking whether
// it round-trips on stdout. Readiness is judged by the sentinel's presence, NOT
// the probe's exit code: gcloud's `workstations ssh` exits 0 even when it fails
// to connect (it prints the error to stderr), so the exit code cannot be
// trusted. Used to close the window where the workstation reports RUNNING a few
// seconds before its in-container sshd starts accepting connections.
func (c *Client) SSHReady(project, region, cluster, cfgName, name string) bool {
	out, _ := c.runner.OutputQuiet(
		"workstations", "ssh", name,
		"--cluster", cluster, "--config", cfgName,
		"--region", region, "--project", project,
		"--command", sshReadyProbeCmd,
	)
	return bytes.Contains(out, []byte(sshReadySentinel))
}

// ETServerReady reports whether something is listening on the given TCP port
// inside the workstation — i.e. whether the et server (started by the `et`
// plugin's boot script) is up yet. etserver comes up tens of seconds after
// sshd, so SSHReady passing does not imply the et data channel is reachable;
// `ra connect --et` uses this to avoid launching the et client into a port that
// has no server (which surfaces as "does not have a server running on port N").
//
// The probe reads the kernel's own socket table (/proc/net/tcp + tcp6) for a
// LISTEN socket (state 0A) on the port, and prints the sentinel only if one
// exists. This is passive — unlike a /dev/tcp dial it does not open (and
// immediately drop) a connection against etserver on every poll. It also needs
// no `ss`/`netstat`, which the ra workstation image does not ship; /proc/net/tcp
// is what those tools read anyway. Local addresses there are HEXIP:HEXPORT, so
// the port is matched as an uppercase 4-digit hex suffix. Readiness is judged by
// the sentinel's presence, not the exit code (same rationale as SSHReady); the
// sentinel is computed by awk (42*2) so the literal token never appears in the
// command line and can't be echoed back as a false positive.
func (c *Client) ETServerReady(project, region, cluster, cfgName, name string, port int) bool {
	probe := fmt.Sprintf(`awk -v p=":%04X" '$2 ~ (p"$") && $4 == "0A" {f=1} END {if (f) print "ra_et_" (42*2) "_ready"}' /proc/net/tcp /proc/net/tcp6 2>/dev/null`, port)
	out, _ := c.runner.OutputQuiet(
		"workstations", "ssh", name,
		"--cluster", cluster, "--config", cfgName,
		"--region", region, "--project", project,
		"--command", probe,
	)
	return bytes.Contains(out, []byte(etReadySentinel))
}

// StartTCPTunnel opens a foreground TCP tunnel from localhost:localPort to
// remotePort inside the workstation. Blocks until the underlying gcloud
// process exits (typically on Ctrl-C).
func (c *Client) StartTCPTunnel(project, region, cluster, cfgName, name string, localPort, remotePort int) error {
	return c.runner.Run(
		"workstations", "start-tcp-tunnel",
		"--cluster", cluster, "--config", cfgName,
		"--region", region, "--project", project,
		"--local-host-port", fmt.Sprintf("localhost:%d", localPort),
		name, strconv.Itoa(remotePort),
	)
}

// StartTCPTunnelContext is StartTCPTunnel that also returns (killing the
// underlying gcloud process) when ctx is cancelled. Used by the ET connect
// path, which supervises a long-lived tunnel and must tear it down — or
// respawn it — on demand.
func (c *Client) StartTCPTunnelContext(ctx context.Context, project, region, cluster, cfgName, name string, localPort, remotePort int) error {
	return c.runner.RunContext(ctx,
		"workstations", "start-tcp-tunnel",
		"--cluster", cluster, "--config", cfgName,
		"--region", region, "--project", project,
		"--local-host-port", fmt.Sprintf("localhost:%d", localPort),
		name, strconv.Itoa(remotePort),
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
