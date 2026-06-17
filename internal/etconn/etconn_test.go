package etconn

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/adamtait/remote-agent/internal/gcloud"
)

// fastTunables shrinks the timing knobs so orchestration tests run instantly,
// restoring the originals on cleanup.
func fastTunables(t *testing.T) {
	t.Helper()
	ob, opbt, oppi := backoffInterval, portBoundTimeout, portPollInterval
	t.Cleanup(func() { backoffInterval, portBoundTimeout, portPollInterval = ob, opbt, oppi })
	backoffInterval = time.Millisecond
	portBoundTimeout = 2 * time.Second
	portPollInterval = 5 * time.Millisecond
}

// eventLog is a concurrency-safe ordered log shared between the fake tunneler
// and fake et so a test can assert relative ordering ("both tunnels before et").
type eventLog struct {
	mu     sync.Mutex
	events []string
}

func (e *eventLog) add(s string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.events = append(e.events, s)
}

func (e *eventLog) snapshot() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return slices.Clone(e.events)
}

// fakeTunneler binds a real local listener per Tunnel call (so the real
// waitForPortsBound path is exercised) and blocks until ctx is cancelled.
type fakeTunneler struct {
	log       *eventLog
	mu        sync.Mutex
	calls     [][2]int // (localPort, remotePort) per call
	dataN     int      // number of data-tunnel (remote 2022) calls
	bootN     int      // number of bootstrap-tunnel (remote 22) calls
	dropData1 bool     // if set, the first data-tunnel call returns early (simulated drop)
	noBind    bool     // if set, never bind a real listener (forces waitForPortsBound to time out)
}

func (f *fakeTunneler) Tunnel(ctx context.Context, _ gcloud.WorkstationRef, localPort, remotePort int) error {
	f.mu.Lock()
	f.calls = append(f.calls, [2]int{localPort, remotePort})
	isData := remotePort == RemoteDataPort
	if isData {
		f.dataN++
	} else {
		f.bootN++
	}
	dropNow := isData && f.dropData1 && f.dataN == 1
	f.mu.Unlock()
	f.log.add(fmt.Sprintf("tunnel:%d", remotePort))

	if dropNow {
		return errors.New("simulated drop")
	}
	if f.noBind {
		<-ctx.Done()
		return ctx.Err()
	}
	l, err := net.Listen("tcp", fmt.Sprintf("localhost:%d", localPort))
	if err != nil {
		// Port momentarily unavailable (e.g. mid-respawn); just block.
		<-ctx.Done()
		return ctx.Err()
	}
	defer l.Close()
	<-ctx.Done()
	return ctx.Err()
}

func (f *fakeTunneler) counts() (boot, data int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.bootN, f.dataN
}

// fakeET records the event log as seen at Run time and either returns
// immediately (clean session end) or blocks until ctx is cancelled.
type fakeET struct {
	log     *eventLog
	mu      sync.Mutex
	ran     bool
	atRun   []string
	block   bool
	sawDone bool
	gotOpts ETOptions
}

func (e *fakeET) Run(ctx context.Context, opts ETOptions) error {
	e.mu.Lock()
	e.ran = true
	e.atRun = e.log.snapshot()
	e.gotOpts = opts
	e.mu.Unlock()
	if e.block {
		<-ctx.Done()
		e.mu.Lock()
		e.sawDone = true
		e.mu.Unlock()
		return ctx.Err()
	}
	return nil
}

func testPorts(t *testing.T) Ports {
	t.Helper()
	p, err := PickPorts()
	if err != nil {
		t.Fatalf("PickPorts: %v", err)
	}
	return p
}

// Happy path: both tunnels come up and are observed before et launches; et
// returns cleanly and Connect returns nil.
func TestConnect_TunnelsBeforeET(t *testing.T) {
	fastTunables(t)
	log := &eventLog{}
	tn := &fakeTunneler{log: log}
	et := &fakeET{log: log}
	ports := testPorts(t)

	err := Connect(context.Background(), tn, et, gcloud.WorkstationRef{}, ports,
		ETOptions{Host: "localhost", DataPort: ports.Data, BootstrapPort: ports.Bootstrap}, io.Discard)
	if err != nil {
		t.Fatalf("Connect returned error: %v", err)
	}
	if !et.ran {
		t.Fatal("et never ran")
	}
	if !slices.Contains(et.atRun, "tunnel:22") || !slices.Contains(et.atRun, "tunnel:2022") {
		t.Fatalf("et ran before both tunnels started; log at run: %v", et.atRun)
	}
	// ETOptions are passed through unchanged.
	if et.gotOpts.DataPort != ports.Data || et.gotOpts.BootstrapPort != ports.Bootstrap {
		t.Errorf("et got wrong ports: %+v (want data=%d boot=%d)", et.gotOpts, ports.Data, ports.Bootstrap)
	}
}

// The data tunnel drops once and must be respawned while et stays connected.
func TestConnect_RespawnsDataTunnel(t *testing.T) {
	fastTunables(t)
	log := &eventLog{}
	tn := &fakeTunneler{log: log, dropData1: true}
	et := &fakeET{log: log, block: true}
	ports := testPorts(t)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- Connect(ctx, tn, et, gcloud.WorkstationRef{}, ports,
			ETOptions{Host: "localhost", DataPort: ports.Data, BootstrapPort: ports.Bootstrap}, io.Discard)
	}()

	// et can only launch once the data tunnel is bound, which requires the
	// post-drop respawn. So wait until et is running, then confirm the data
	// tunnel was spawned at least twice (drop + respawn).
	deadline := time.Now().Add(2 * time.Second)
	for {
		et.mu.Lock()
		ran := et.ran
		et.mu.Unlock()
		if ran {
			break
		}
		if time.Now().After(deadline) {
			cancel()
			t.Fatalf("et never ran despite tunnels coming up")
		}
		time.Sleep(5 * time.Millisecond)
	}
	if _, data := tn.counts(); data < 2 {
		cancel()
		t.Fatalf("data tunnel was not respawned (data calls = %d, want >= 2)", data)
	}
	cancel()
	if err := <-done; err != nil && !errors.Is(err, context.Canceled) {
		t.Fatalf("Connect returned unexpected error: %v", err)
	}
}

// Cancelling the parent context tears everything down: et observes ctx.Done
// and Connect returns.
func TestConnect_SignalTeardown(t *testing.T) {
	fastTunables(t)
	log := &eventLog{}
	tn := &fakeTunneler{log: log}
	et := &fakeET{log: log, block: true}
	ports := testPorts(t)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- Connect(ctx, tn, et, gcloud.WorkstationRef{}, ports,
			ETOptions{Host: "localhost", DataPort: ports.Data, BootstrapPort: ports.Bootstrap}, io.Discard)
	}()

	// Wait until et is running, then cancel.
	deadline := time.Now().Add(2 * time.Second)
	for {
		et.mu.Lock()
		ran := et.ran
		et.mu.Unlock()
		if ran {
			break
		}
		if time.Now().After(deadline) {
			cancel()
			t.Fatal("et did not start in time")
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Connect did not return after ctx cancel")
	}
	et.mu.Lock()
	defer et.mu.Unlock()
	if !et.sawDone {
		t.Fatal("et did not observe ctx cancellation")
	}
}

// If the tunnels never bind, Connect fails the readiness wait and never
// launches et.
func TestConnect_PortsNeverBind_ETNotRun(t *testing.T) {
	fastTunables(t)
	portBoundTimeout = 200 * time.Millisecond // fail fast
	log := &eventLog{}
	tn := &fakeTunneler{log: log, noBind: true}
	et := &fakeET{log: log}
	ports := testPorts(t)

	err := Connect(context.Background(), tn, et, gcloud.WorkstationRef{}, ports,
		ETOptions{Host: "localhost", DataPort: ports.Data, BootstrapPort: ports.Bootstrap}, io.Discard)
	if err == nil {
		t.Fatal("expected error when tunnels never bind")
	}
	if et.ran {
		t.Fatal("et must not run when tunnels are not ready")
	}
}
