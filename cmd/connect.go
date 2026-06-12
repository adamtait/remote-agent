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
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		var name string
		if len(args) == 1 {
			name = args[0]
		}
		return runConnect(cfg, client, name, readLocationOverrides(cmd), cmd.ErrOrStderr())
	},
}

func runConnect(cfg *config.Config, client *gcloud.Client, name string, override wsOverride, errOut io.Writer) error {
	ref, err := resolveWorkstationRef(cfg, client, name, override)
	if err != nil {
		return err
	}
	if err := ensureRunning(client, ref, errOut); err != nil {
		return err
	}
	waitSSHReady(client, ref, errOut)
	return client.SSH(ref.Project, ref.Region, ref.Cluster, ref.Config, ref.Name)
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
