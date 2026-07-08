package cmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"time"

	"github.com/adamtait/remote-agent/internal/config"
	"github.com/adamtait/remote-agent/internal/etconn"
	"github.com/adamtait/remote-agent/internal/gcloud"
	"github.com/spf13/cobra"
)

// etWorkstationUser is the login user inside the ra workstation image. It is a
// fixed image user (`gcloud workstations ssh` connects as it), so the ET
// bootstrap ssh uses the same name.
const etWorkstationUser = "user"

// etKeepaliveSecs is the et client keepalive interval (`et -k`). It governs how
// quickly the local process exits after the remote shell/tmux ends: gcloud's
// start-tcp-tunnel keeps the local socket open even once the remote etserver
// session is gone, so et can only detect the ended session when a keepalive goes
// unanswered (then it reconnects, learns the session is dead, and terminates —
// at which point ra tears down the tunnels and exits). et's default (~6s) makes
// an `exit` look like a ~12-15s hang; 3s roughly halves that while staying well
// clear of spurious reconnects on the IAP relay (sub-second RTT), measured live
// against the workstation. It also speeds recovery from genuine mid-session
// drops. See docs/adr/0011-eternal-terminal-connect.md.
const etKeepaliveSecs = 3

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
		// A plugin may declare a `login-command:` (e.g. the tmux plugin's
		// `tmux new-session -A -s main`) that ra runs in place of the login
		// shell — shell-agnostic, unlike per-shell login hooks in the image.
		// Core stays plugin-agnostic: the registry returns whichever single
		// enabled plugin declares one (error if more than one).
		loginCmd, _, err := reg.LoginCommand(cfg)
		if err != nil {
			return err
		}
		if et, _ := cmd.Flags().GetBool("et"); et {
			return runConnectET(cfg, client, name, readLocationOverrides(cmd), loginCmd, cmd.ErrOrStderr())
		}
		return runConnect(cfg, client, name, readLocationOverrides(cmd), loginCmd, cmd.ErrOrStderr())
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

// runConnectET connects via the local Eternal Terminal client instead of plain
// `gcloud workstations ssh`, so a dropped connection auto-reconnects and
// resumes the session. It reuses the same resolve/start/wait front-half as
// runConnect, then hands off to the etconn orchestrator (two supervised
// tunnels + the foreground et client).
func runConnectET(cfg *config.Config, client *gcloud.Client, name string, override wsOverride, loginCmd string, errOut io.Writer) error {
	if err := etconn.EnsureETInstalled(); err != nil {
		return err
	}
	ref, err := resolveWorkstationRef(cfg, client, name, override)
	if err != nil {
		return err
	}
	alreadyRunning, err := ensureRunning(client, ref, errOut)
	if err != nil {
		return err
	}
	if !alreadyRunning {
		waitSSHReady(client, ref, errOut)
	}
	// etserver (started by the `et` plugin's boot script) comes up tens of
	// seconds after sshd, so SSH-readiness does not imply the data channel is
	// reachable. Without this gate, the data tunnel races ahead and gcloud
	// reports "does not have a server running on port 2022", and the et client
	// fails to authenticate. Wait until etserver actually answers.
	if err := waitETServerReady(client, ref, etconn.RemoteDataPort, errOut); err != nil {
		return err
	}

	ports, err := etconn.PickPorts()
	if err != nil {
		return fmt.Errorf("allocating local tunnel ports: %w", err)
	}
	opts := etconn.ETOptions{
		Host:          "localhost",
		DataPort:      ports.Data,
		BootstrapPort: ports.Bootstrap,
		User:          etWorkstationUser,
		IdentityFile:  etIdentityFile(),
		KeepaliveSecs: etKeepaliveSecs,
		Command:       loginCmd,
	}

	// SIGINT/SIGTERM tear down the tunnels (et grabs the terminal in raw mode,
	// so an in-session Ctrl-C is forwarded to the remote shell rather than
	// reaching us; this handler matters for aborting before connect and for
	// cleaning up the child tunnel processes on signal).
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	fmt.Fprintf(errOut, "Connecting to %q via Eternal Terminal (auto-reconnecting)...\n", ref.Name)
	tunneler := etconn.NewGcloudTunneler(client)
	etClient := etconn.NewETRunner(os.Stdin, os.Stdout, errOut)
	return etconn.Connect(ctx, tunneler, etClient, ref, ports, opts, errOut)
}

// etReadyTimeout/etReadyInterval bound how long runConnectET waits for etserver
// to start listening after the workstation is SSH-ready. etserver starts later
// in boot than sshd (after secret-fetch), so this window can be tens of seconds
// on a cold start. Package-level so tests can shrink them.
var (
	etReadyTimeout  = 120 * time.Second
	etReadyInterval = 3 * time.Second
)

// waitETServerReady blocks until the et server answers on the given port inside
// the workstation, or returns a clear error after etReadyTimeout. Unlike
// waitSSHReady (which warns and proceeds, letting the real ssh report any
// problem), this gates the connection: launching `et` before etserver is up
// reproduces the "does not have a server running on port N" failure, so if the
// server never appears we stop with an actionable message rather than handing a
// doomed connection to the et client.
func waitETServerReady(client *gcloud.Client, ref gcloud.WorkstationRef, port int, errOut io.Writer) error {
	if client.ETServerReady(ref.Project, ref.Region, ref.Cluster, ref.Config, ref.Name, port) {
		return nil
	}
	fmt.Fprintf(errOut, "Waiting for the et server on %q (port %d) to become ready...\n", ref.Name, port)
	deadline := time.Now().Add(etReadyTimeout)
	for time.Now().Before(deadline) {
		time.Sleep(etReadyInterval)
		if client.ETServerReady(ref.Project, ref.Region, ref.Cluster, ref.Config, ref.Name, port) {
			return nil
		}
	}
	return fmt.Errorf("et server not reachable on port %d of %q after %s; "+
		"is the `et` plugin installed and enabled (`ra plugin list`) and the image rebuilt (`ra create`)?",
		port, ref.Name, etReadyTimeout)
}

// etIdentityFile returns the ssh key gcloud uses for workstation/compute SSH if
// it exists, so the ET bootstrap ssh authenticates the same way gcloud does.
// Returns "" when absent, letting ssh fall back to its default key resolution.
func etIdentityFile() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	p := filepath.Join(home, ".ssh", "google_compute_engine")
	if _, err := os.Stat(p); err != nil {
		return ""
	}
	return p
}

func init() {
	connectCmd.Flags().Bool("et", false, "Connect via Eternal Terminal (auto-reconnecting) instead of plain SSH")
	addLocationOverrideFlags(connectCmd)
	rootCmd.AddCommand(connectCmd)
}
