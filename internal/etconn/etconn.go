package etconn

import (
	"context"
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	"github.com/adamtait/remote-agent/internal/gcloud"
)

// Tunables, as package vars so tests can shrink them.
var (
	// backoffInterval is the wait between supervised-tunnel respawns. It also
	// doubles as the local-port release window before a respawn re-binds the
	// same port (ET only knows the one port number, so we cannot pick a new one).
	backoffInterval = 500 * time.Millisecond
	// portBoundTimeout bounds how long Connect waits for both local tunnels to
	// start accepting before it launches et.
	portBoundTimeout = 15 * time.Second
	// portPollInterval is how often Connect re-checks the local ports while
	// waiting for them to bind.
	portPollInterval = 250 * time.Millisecond
)

// Tunneler opens one gcloud TCP tunnel. Tunnel blocks until the underlying
// process exits or ctx is cancelled (which kills the process).
type Tunneler interface {
	Tunnel(ctx context.Context, ref gcloud.WorkstationRef, localPort, remotePort int) error
}

// ETClient launches the foreground interactive `et` client and blocks until
// the session ends (the user exits, or et dies).
type ETClient interface {
	Run(ctx context.Context, opts ETOptions) error
}

// Connect runs the full ET flow: it brings up the two supervised tunnels
// (bootstrap → ws:22, data → ws:2022), waits for both local ports to accept,
// launches et in the foreground, and tears everything down when et returns or
// the parent ctx is cancelled.
//
// ref must already be RUNNING and SSH-ready — the caller (cmd) does
// ensureRunning/waitSSHReady first. The data tunnel is supervised (respawned
// on drop) because ET reconnects its data channel to the same local port; the
// bootstrap tunnel is supervised too, kept up for the whole session so an ET
// session that has to re-bootstrap still finds port 22 reachable.
func Connect(ctx context.Context, t Tunneler, et ETClient, ref gcloud.WorkstationRef, ports Ports, opts ETOptions, errOut io.Writer) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	var wg sync.WaitGroup
	supervise := func(localPort, remotePort int, label string) {
		defer wg.Done()
		for {
			if ctx.Err() != nil {
				return
			}
			err := t.Tunnel(ctx, ref, localPort, remotePort)
			if ctx.Err() != nil {
				return // cancelled: clean teardown, not a real drop
			}
			fmt.Fprintf(errOut, "ra: %s tunnel dropped (%v); reconnecting...\n", label, err)
			select {
			case <-ctx.Done():
				return
			case <-time.After(backoffInterval):
			}
		}
	}

	wg.Add(2)
	go supervise(ports.Bootstrap, etRemoteSSHPort, "bootstrap")
	go supervise(ports.Data, etRemoteDataPort, "data")

	if err := waitForPortsBound(ctx, []int{ports.Bootstrap, ports.Data}, portBoundTimeout); err != nil {
		cancel()
		wg.Wait()
		return fmt.Errorf("local tunnels did not become ready: %w", err)
	}

	etErr := et.Run(ctx, opts)
	cancel()
	wg.Wait()
	return etErr
}

// waitForPortsBound blocks until every local port accepts a TCP connection, or
// the timeout/ctx elapses. It is the ET analog of waitSSHReady: confirm the
// transport is up before handing off to the client.
func waitForPortsBound(ctx context.Context, ports []int, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		if allBound(ports) {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("local ports %v not listening within %s", ports, timeout)
		}
		time.Sleep(portPollInterval)
	}
}

func allBound(ports []int) bool {
	for _, p := range ports {
		conn, err := net.DialTimeout("tcp", fmt.Sprintf("localhost:%d", p), 500*time.Millisecond)
		if err != nil {
			return false
		}
		conn.Close()
	}
	return true
}
