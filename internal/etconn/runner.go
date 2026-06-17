package etconn

import (
	"context"
	"io"
	"os/exec"

	"github.com/adamtait/remote-agent/internal/gcloud"
)

// gcloudTunneler is the production Tunneler: each Tunnel call is one
// context-cancellable `gcloud workstations start-tcp-tunnel` process.
type gcloudTunneler struct{ client *gcloud.Client }

// NewGcloudTunneler returns a Tunneler backed by the gcloud client.
func NewGcloudTunneler(client *gcloud.Client) Tunneler {
	return gcloudTunneler{client: client}
}

func (g gcloudTunneler) Tunnel(ctx context.Context, ref gcloud.WorkstationRef, localPort, remotePort int) error {
	return g.client.StartTCPTunnelContext(ctx,
		ref.Project, ref.Region, ref.Cluster, ref.Config, ref.Name,
		localPort, remotePort)
}

// etRunner is the production ETClient: it execs the local `et` binary wired to
// the real terminal (stdin/stdout/stderr) for an interactive session. Mirrors
// the dockerRunner pattern in cmd/create.go, with stdin attached.
type etRunner struct {
	stdin  io.Reader
	stdout io.Writer
	stderr io.Writer
}

// NewETRunner returns an ETClient that runs the local `et` binary.
func NewETRunner(stdin io.Reader, stdout, stderr io.Writer) ETClient {
	return etRunner{stdin: stdin, stdout: stdout, stderr: stderr}
}

func (e etRunner) Run(ctx context.Context, o ETOptions) error {
	c := exec.CommandContext(ctx, "et", buildETArgs(o)...)
	c.Stdin = e.stdin
	c.Stdout = e.stdout
	c.Stderr = e.stderr
	return c.Run()
}
