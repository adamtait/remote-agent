// Package etconn orchestrates connecting to a GCP Cloud Workstation through
// the local Eternal Terminal (`et`) client, so a dropped connection
// auto-reconnects and resumes the session instead of freezing.
//
// ET needs two connections to the workstation — an SSH bootstrap to port 22
// and a persistent data channel to port 2022 — but the only ingress is
// `gcloud workstations start-tcp-tunnel`, which forwards one local TCP port to
// one remote port. So etconn brings up two local tunnels (one per remote port)
// and points `et` at them. The data tunnel is supervised (auto-restarted) so
// ET, which reconnects to the same local port, always finds a live endpoint.
package etconn

import (
	"fmt"
	"strconv"
)

// Remote ports inside the workstation. The SSH bootstrap targets sshd (already
// present — it is what `gcloud workstations ssh` uses); the data channel
// targets etserver, installed and started by the `et` ra-plugin.
const (
	etRemoteSSHPort = 22
	// RemoteDataPort is the in-workstation port etserver listens on (matched by
	// the `et` plugin's port-for-tunnel). Exported so the connect command can
	// probe it for readiness before launching the et client.
	RemoteDataPort = 2022
)

// ETOptions fully specifies a local `et` invocation. It is built from the
// resolved workstation plus the chosen local ports. The exact flag set was
// validated by the Phase-1 spike (see implementation-notes-et.md): `et`
// bootstraps over ssh, so the bootstrap port is conveyed as an ssh `-o Port=`
// option, and the data channel is selected with `-p`.
type ETOptions struct {
	Host          string   // host both the ssh bootstrap and et data channel dial; always "localhost" here
	DataPort      int      // local data-tunnel port (→ ws:2022); becomes `et -p`
	BootstrapPort int      // local bootstrap-tunnel port (→ ws:22); becomes `--ssh-option Port=`
	User          string   // ssh login user inside the workstation (e.g. "user")
	IdentityFile  string   // ssh private key the workstation authorizes; "" lets ssh resolve a default
	KeepaliveSecs int      // et client keepalive interval (`-k`); 0 leaves et's default (~6s)
	Extra         []string // escape hatch for additional et flags
}

// buildETArgs returns the argv for the local `et` client (excluding the binary
// name itself). This is the single place the spike's invocation lives; if the
// et command line ever needs to change, change it here.
func buildETArgs(o ETOptions) []string {
	var args []string
	// Client keepalive. et only learns the remote session ended by its keepalive
	// timing out: gcloud's start-tcp-tunnel keeps the local socket open after the
	// remote etserver session goes away (an `exit`/logout), so et never sees a
	// socket close — it writes a keepalive, gets no answer, kills the connection,
	// reconnects, and etserver reports the session is gone. A lower interval makes
	// that detection (and thus our process exit on remote `exit`) proportionally
	// faster, and also speeds recovery from real mid-session transport drops. ET's
	// resend buffer makes the extra reconnects seamless.
	if o.KeepaliveSecs > 0 {
		args = append(args, "-k", strconv.Itoa(o.KeepaliveSecs))
	}
	// Bootstrap SSH port: et ignores ssh_config's Port directive, but a
	// command-line `-o Port=` (which is what --ssh-option becomes) is honored.
	args = append(args, "--ssh-option", fmt.Sprintf("Port=%d", o.BootstrapPort))
	if o.IdentityFile != "" {
		args = append(args, "--ssh-option", "IdentityFile="+o.IdentityFile)
	}
	// Match gcloud's own ssh flags: a workstation's host key is regenerated
	// every boot, so verifying/recording it only yields warnings or failures.
	args = append(args,
		"--ssh-option", "StrictHostKeyChecking=no",
		"--ssh-option", "UserKnownHostsFile=/dev/null",
		"-p", strconv.Itoa(o.DataPort),
	)
	args = append(args, o.Extra...)

	host := o.Host
	if o.User != "" {
		host = o.User + "@" + host
	}
	return append(args, host)
}
