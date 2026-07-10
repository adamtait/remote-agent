package cmd

import (
	"fmt"
	"io"
	"time"

	"github.com/adamtait/remote-agent/internal/config"
	"github.com/adamtait/remote-agent/internal/gcloud"
	"github.com/spf13/cobra"
)

// sshReadyTimeout/sshReadyInterval bound how long runConnect waits for sshd to
// start accepting connections after the workstation reports RUNNING. Each probe
// is itself a real (multi-second) gcloud round-trip, so the interval is small.
// Package-level so tests can shrink them.
var (
	sshReadyTimeout  = 90 * time.Second
	sshReadyInterval = 2 * time.Second
)

var connectCmd = &cobra.Command{
	Use:   "connect [name]",
	Short: "Open an SSH session to a workstation (starts it if stopped)",
	Long: `Open an SSH session to a workstation, starting it if stopped.

Name the workstation with the [name] positional or --workstation/-w (both skip
the picker). If neither is given and you're on a terminal, ra lists the
workstations in the resolved cluster/config and lets you pick one (auto-using the
sole entry). Off a terminal it falls back to the configured default, then the
most recently created workstation. Pass --select/-s to force the picker even when
a default exists.`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		var name string
		if len(args) == 1 {
			name = args[0]
		}
		// connect.login_command (client-side config) runs in place of the login
		// shell when set; empty opens a plain interactive login shell. Other
		// transports (e.g. Eternal Terminal) live in plugins as
		// `ra plugin <name> <command>`, not in core.
		return runConnect(cfg, client, name, readLocationOverrides(cmd), cfg.Connect.LoginCommand, cmd.ErrOrStderr())
	},
}

func runConnect(cfg *config.Config, client *gcloud.Client, name string, override wsOverride, loginCmd string, errOut io.Writer) error {
	ref, err := resolveWorkstationRef(cfg, client, name, override)
	if err != nil {
		return err
	}
	alreadyRunning, err := ensureRunning(client, ref, errOut)
	if err != nil {
		return err
	}
	// Only probe for SSH readiness when we just started or waited for the
	// workstation — that's the only window where the API reports RUNNING before
	// sshd is up. On a warm reconnect sshd is long up, so skip the probe and its
	// multi-second round-trip.
	if !alreadyRunning {
		waitSSHReady(client, ref, errOut)
	}
	return client.SSH(ref.Project, ref.Region, ref.Cluster, ref.Config, ref.Name, loginCmd)
}

// waitSSHReady blocks until the workstation's sshd answers a sentinel probe,
// closing the gap where the API reports RUNNING a few seconds before sshd
// accepts connections — the gap that otherwise surfaces as
// "[SSL: UNEXPECTED_EOF_WHILE_READING]".
//
// If the probe never succeeds within sshReadyTimeout it warns and returns
// rather than failing: the probe is an optimization to avoid the race, not a
// gate. A connection that would have worked must never be blocked by a flaky or
// false-negative probe, so we fall through to the real SSH and let it report
// any genuine problem.
func waitSSHReady(client *gcloud.Client, ref gcloud.WorkstationRef, errOut io.Writer) {
	if client.SSHReady(ref.Project, ref.Region, ref.Cluster, ref.Config, ref.Name) {
		return
	}
	fmt.Fprintf(errOut, "Waiting for SSH on %q to become ready...\n", ref.Name)
	deadline := time.Now().Add(sshReadyTimeout)
	for time.Now().Before(deadline) {
		time.Sleep(sshReadyInterval)
		if client.SSHReady(ref.Project, ref.Region, ref.Cluster, ref.Config, ref.Name) {
			return
		}
	}
	fmt.Fprintf(errOut, "SSH on %q not confirmed ready after %s; connecting anyway...\n", ref.Name, sshReadyTimeout)
}

func init() {
	addLocationOverrideFlags(connectCmd)
	rootCmd.AddCommand(connectCmd)
}
