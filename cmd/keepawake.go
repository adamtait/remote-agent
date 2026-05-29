package cmd

import (
	"io"
	"os"
	"os/exec"
	"runtime"
	"strconv"

	"github.com/adamtait/remote-agent/internal/logging"
	"github.com/spf13/cobra"
)

// addKeepAwakeFlag registers the shared --no-keep-awake opt-out on a
// foreground-connection command (connect, tunnel). Keep-awake is on by default
// so the fix applies without the user having to discover a flag; the opt-out is
// for when battery preservation matters more than holding the connection.
func addKeepAwakeFlag(cmd *cobra.Command) {
	cmd.Flags().Bool("no-keep-awake", false,
		"Don't prevent the local machine from idle-sleeping while connected (macOS); sleeping drops the tunnel")
}

// keepAwake prevents the local machine from idle-sleeping for the lifetime of
// a foreground workstation connection (`ra connect` / `ra tunnel`).
//
// Why this is needed even with the server-side sshd keepalive (ADR 0003):
// gcloud's IAP tunnel relay runs on the *local* machine. The sshd keepalive
// keeps a connected idle session alive indefinitely (verified to 12+ min), but
// it cannot help once the laptop itself goes away. When macOS idle-sleeps —
// the default Energy Saver kicks in after a few minutes when you step away from
// the keyboard — the relay process and its TCP socket freeze, no traffic flows
// on the tunnel in either direction, and the Cloud Workstations backend reaps
// the idle websocket after ~30s. On wake, gcloud prints the familiar
// "ERROR: Connection to Cloud Workstation lost." The server keepalive is
// powerless here because the *client* has stopped responding, not the server.
//
// Holding a "prevent idle system sleep" assertion for the duration of the
// connection keeps the relay running so keepalives keep flowing and the session
// survives an unattended laptop (as long as the lid stays open — see below).
//
// Scope: this only defends against *idle* sleep (walking away, lid open).
// Closing the lid (clamshell sleep) still suspends the machine; the workstation
// runs a persistent tmux session, so reconnecting with `ra connect` reattaches
// with no work lost.
//
// Returns a stop func the caller MUST defer. It is a no-op (returning a no-op
// stop) when disabled, on unsupported platforms, or when the keep-awake tool is
// not on PATH — keeping the connection path working everywhere.
func keepAwake(enabled bool, errOut io.Writer) func() {
	if !enabled {
		return func() {}
	}
	name, args, ok := keepAwakeCommand(runtime.GOOS, os.Getpid())
	if !ok {
		logging.L().Debug("keep-awake unsupported on platform", "goos", runtime.GOOS)
		return func() {}
	}
	path, err := exec.LookPath(name)
	if err != nil {
		logging.L().Debug("keep-awake tool not found; sleeping laptop may still drop the connection", "tool", name, "err", err)
		return func() {}
	}
	cmd := exec.Command(path, args...)
	if err := cmd.Start(); err != nil {
		logging.L().Debug("keep-awake failed to start", "tool", name, "err", err)
		return func() {}
	}
	logging.L().Debug("keep-awake active", "tool", name, "pid", cmd.Process.Pid)

	var stopped bool
	return func() {
		if stopped {
			return
		}
		stopped = true
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}
}

// keepAwakeCommand returns the OS-specific command that holds a
// prevent-idle-sleep assertion until the given pid exits. ok is false on
// platforms with no supported mechanism (the connection still works; it is
// just not protected against the local machine idle-sleeping).
func keepAwakeCommand(goos string, pid int) (name string, args []string, ok bool) {
	switch goos {
	case "darwin":
		// caffeinate -i: assert PreventUserIdleSystemSleep (does NOT prevent
		// lid-close sleep, which is the intended scope).
		// -w <pid>: caffeinate self-terminates when ra exits, so a killed or
		// crashed ra never leaves a dangling assertion behind even if the
		// deferred stop() does not run.
		return "caffeinate", []string{"-i", "-w", strconv.Itoa(pid)}, true
	default:
		return "", nil, false
	}
}
