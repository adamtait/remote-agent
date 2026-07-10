package cmd

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/adamtait/remote-agent/internal/config"
	"github.com/adamtait/remote-agent/internal/gcloud"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

// errStdinNotTerminal is returned by confirmYes when stdin is not a terminal,
// so interactive callers can advise the user to pass --force instead.
var errStdinNotTerminal = errors.New("stdin is not a terminal")

// confirmYes writes prompt to stderr and returns true only if the user types
// "y" (case-insensitive). It returns errStdinNotTerminal when stdin is not a
// terminal. Shared by the destructive commands (`ra delete`, `ra teardown`) so
// their confirmation behavior stays identical.
func confirmYes(prompt string) (bool, error) {
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		return false, errStdinNotTerminal
	}
	fmt.Fprint(os.Stderr, prompt)
	scanner := bufio.NewScanner(os.Stdin)
	if !scanner.Scan() {
		return false, fmt.Errorf("no input received")
	}
	return strings.ToLower(strings.TrimSpace(scanner.Text())) == "y", nil
}

// waitRunningTimeout/waitRunningInterval bound how long ensureRunning polls for
// a workstation to reach STATE_RUNNING. Cloud Workstations cold starts (image
// pull + startup scripts) routinely take 1-2 min, so 5 min is generous while
// still failing a genuinely stuck start in bounded time. Package-level so tests
// can shrink them.
var (
	waitRunningTimeout  = 5 * time.Minute
	waitRunningInterval = 5 * time.Second
)

// Interactive-picker seams. Package-level (like waitRunningTimeout above) so
// tests can drive selection deterministically without a real terminal: override
// stdinIsTerminal to force the interactive path, and promptIn/promptOut to feed
// the menu. Mirrors how confirmYes reads os.Stdin / term.IsTerminal directly.
var (
	stdinIsTerminal           = func() bool { return term.IsTerminal(int(os.Stdin.Fd())) }
	promptIn        io.Reader = os.Stdin
	promptOut       io.Writer = os.Stderr
)

// addLocationOverrideFlags registers --project/--cluster/--config on a command.
// Read them at RunE time via readLocationOverrides(cmd). Shared by every command
// that targets a workstation (connect, tunnel, delete, stop, url, logs, teardown,
// plugin commands).
func addLocationOverrideFlags(c *cobra.Command) {
	c.Flags().String("project", "", "GCP project (overrides config)")
	c.Flags().String("cluster", "", "Workstation cluster (overrides config)")
	c.Flags().String("config", "", "Workstation config (overrides config)")
}

// addSelectionFlags registers the selection flags --workstation/-w and
// --select/-s. Only commands that opt into interactive selection (connect,
// tunnel, and needs_workstation plugin commands) register these; the workstation
// *management* commands (delete/stop/url/logs/teardown) deliberately do not, so
// they never pop a picker. Pair with readSelectionOverrides.
func addSelectionFlags(c *cobra.Command) {
	c.Flags().StringP("workstation", "w", "", "workstation name to use (skips the interactive picker)")
	c.Flags().BoolP("select", "s", false, "interactively pick the workstation from a menu (requires a terminal)")
}

// readLocationOverrides extracts the project/cluster/config override flags. The
// returned wsOverride is non-interactive (Interactive=false), so resolution stays
// on the scripted path. Used by the workstation-management commands.
func readLocationOverrides(c *cobra.Command) wsOverride {
	p, _ := c.Flags().GetString("project")
	cl, _ := c.Flags().GetString("cluster")
	cf, _ := c.Flags().GetString("config")
	return wsOverride{Project: p, Cluster: cl, Config: cf}
}

// readSelectionOverrides is readLocationOverrides plus the selection flags added
// by addSelectionFlags, and marks the call interactive so resolveWorkstationRef
// may offer the picker. Used by connect, tunnel, and plugin commands.
func readSelectionOverrides(c *cobra.Command) wsOverride {
	o := readLocationOverrides(c)
	o.Workstation, _ = c.Flags().GetString("workstation")
	o.Select, _ = c.Flags().GetBool("select")
	o.Interactive = true
	return o
}

// wsOverride is the per-command override of the project/cluster/config that
// would otherwise come from the merged config. Empty fields fall back to the
// config defaults; explicit values take precedence. Workstation names the
// target explicitly (equivalent to the positional name; skips the picker);
// Select forces the interactive picker for the resolved cluster/config;
// Interactive lets resolveWorkstationRef offer the picker when there's no clear
// default (only set by commands that opt in via readSelectionOverrides).
type wsOverride struct {
	Project     string
	Cluster     string
	Config      string
	Workstation string
	Select      bool
	Interactive bool
}

// resolveWorkstationRef returns a fully-qualified WorkstationRef for the given
// name and overrides. A name supplied via --workstation is merged into name
// here (and must agree with any positional name); everything below treats them
// identically.
//
//   - When name is empty: prefer the conventionally-named workstation
//     (cfg.WorkstationName) when it exists — the "clear default". Otherwise, for
//     an interactive caller on a terminal (override.Interactive), list the
//     resolved cluster/config and pick — auto-using the sole entry, prompting
//     when several exist. override.Select forces that picker even when a default
//     exists. Non-interactive callers (management commands, piped input) fall
//     back to the most recently created workstation.
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

	// A workstation name may be given positionally or via --workstation; when both
	// are present they must agree. Either one (being non-empty) skips the picker.
	if override.Workstation != "" {
		if name != "" && name != override.Workstation {
			return gcloud.WorkstationRef{}, fmt.Errorf("--workstation %q conflicts with the positional name %q", override.Workstation, name)
		}
		name = override.Workstation
	}

	ref := gcloud.WorkstationRef{
		Project: pickStr(override.Project, cfg.Gcloud.Project),
		Region:  ws.Region,
		Cluster: pickStr(override.Cluster, ws.ClusterName),
		Config:  pickStr(override.Config, ws.ConfigName),
		Name:    name,
	}

	// --select picks from a menu, so a name (positional/URI/--workstation) is contradictory.
	if override.Select && name != "" {
		return gcloud.WorkstationRef{}, fmt.Errorf("--select cannot be combined with an explicit workstation name")
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
		// --select forces the picker, which needs a terminal to prompt.
		if override.Select && !stdinIsTerminal() {
			return gcloud.WorkstationRef{}, fmt.Errorf("--select requires an interactive terminal")
		}
		// A "clear default" is the conventionally-named workstation. When it
		// exists and the user didn't force a picker, prefer it without prompting
		// (--select skips this shortcut so it always lists).
		hasDefault := !override.Select &&
			client.Exists(ref.Project, ref.Region, ref.Cluster, ref.Config, cfg.WorkstationName)

		// Offer the picker only when forced (--select) or, for a command that
		// opted into interactivity, when on a terminal with no clear default to
		// fall back to. This keeps the common case (default box present) a
		// zero-prompt connect, and keeps management commands non-interactive.
		if override.Select || (override.Interactive && stdinIsTerminal() && !hasDefault) {
			listed, err := client.List(ref.Project, ref.Region, ref.Cluster, ref.Config)
			if err != nil {
				return gcloud.WorkstationRef{}, fmt.Errorf("listing workstations: %w", err)
			}
			switch len(listed) {
			case 0:
				return gcloud.WorkstationRef{}, fmt.Errorf("no workstations found in cluster %q config %q", ref.Cluster, ref.Config)
			case 1:
				ref.Name = listed[0].Name
				return ref, nil
			default:
				sortCandidates(listed, cfg.WorkstationName)
				chosen, err := pickWorkstation(promptIn, promptOut, listed)
				if err != nil {
					return gcloud.WorkstationRef{}, err
				}
				ref.Name = chosen.Name
				return ref, nil
			}
		}
		// No picker: prefer the default-named workstation, else the most recently
		// created one. This is the behavior for management commands, piped input,
		// and (post-softening) an interactive connect when the default exists.
		if hasDefault {
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

// sortCandidates orders workstations in place so the recommended default comes
// first: the one named defaultName if present, otherwise the most recently
// created. This makes pressing Enter at the picker (which defaults to entry 1)
// equivalent to the non-interactive default pick.
func sortCandidates(list []gcloud.WorkstationInfo, defaultName string) {
	sort.SliceStable(list, func(i, j int) bool {
		if defaultName != "" {
			if list[i].Name == defaultName {
				return true
			}
			if list[j].Name == defaultName {
				return false
			}
		}
		return list[i].CreateTime > list[j].CreateTime
	})
}

// pickWorkstation renders a numbered menu of candidates (NAME + STATE, aligned)
// to out and returns the entry the user picks by reading from in. Bare Enter (or
// EOF with no input) accepts entry 1 — the recommended default, so callers pass a
// list already ordered by sortCandidates. A non-empty but out-of-range/garbage
// choice re-prompts rather than silently defaulting, since picking the wrong
// workstation is consequential (cf. scaffold.Prompt, which also re-prompts on
// invalid input).
func pickWorkstation(in io.Reader, out io.Writer, candidates []gcloud.WorkstationInfo) (gcloud.WorkstationInfo, error) {
	fmt.Fprintln(out, "Multiple workstations found:")
	tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	for i, c := range candidates {
		state := strings.TrimPrefix(c.State, "STATE_")
		if state == "" {
			state = "UNKNOWN"
		}
		fmt.Fprintf(tw, "  %d) %s\t%s\n", i+1, c.Name, state)
	}
	if err := tw.Flush(); err != nil {
		return gcloud.WorkstationInfo{}, err
	}
	r := bufio.NewReader(in)
	for {
		fmt.Fprintf(out, "choice [1-%d] [1]: ", len(candidates))
		line, err := r.ReadString('\n')
		choice := strings.TrimSpace(line)
		if choice == "" {
			// Bare Enter, or EOF with nothing typed: accept the default.
			return candidates[0], nil
		}
		var idx int
		if _, e := fmt.Sscan(choice, &idx); e == nil && idx >= 1 && idx <= len(candidates) {
			return candidates[idx-1], nil
		}
		if err != nil {
			// Invalid entry with no more input to re-prompt (EOF): fall back to
			// the default rather than looping forever.
			return candidates[0], nil
		}
		fmt.Fprintf(out, "  please enter a number between 1 and %d.\n", len(candidates))
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
