package cmd

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/adamtait/remote-agent/internal/config"
	"github.com/adamtait/remote-agent/internal/gcloud"
)

// TestRunConnectET_BailsWhenETMissing verifies the ET preflight runs first: if
// the local `et` client is absent, runConnectET returns an actionable error
// and never touches gcloud (no cold-start latency before the failure).
func TestRunConnectET_BailsWhenETMissing(t *testing.T) {
	// Empty PATH → exec.LookPath("et") fails.
	t.Setenv("PATH", t.TempDir())
	runner := &testRunner{}
	client := gcloud.NewClient(runner)

	err := runConnectET(newConnectCfg(), client, "", wsOverride{}, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "Eternal Terminal") {
		t.Fatalf("expected ET-missing error, got %v", err)
	}
	if len(runner.runs) != 0 || len(runner.quietRuns) != 0 {
		t.Fatalf("expected no gcloud calls when preflight fails; runs=%v quiet=%v", runner.runs, runner.quietRuns)
	}
}

// etReadyRef is the workstation waitETServerReady tests probe.
var etReadyRef = gcloud.WorkstationRef{Project: "p", Region: "r", Cluster: "cl", Config: "cfg", Name: "w"}

// etReadyProbeCmdKey is the OutputQuiet key ETServerReady runs for port 2022.
const etReadyProbeCmdKey = `workstations ssh w --cluster cl --config cfg --region r --project p --command bash -c 'exec 3<>/dev/tcp/127.0.0.1/2022' >/dev/null 2>&1 && echo "ra_et_$((42*2))_ready"`

func shrinkETReady(t *testing.T) {
	t.Helper()
	ot, oi := etReadyTimeout, etReadyInterval
	t.Cleanup(func() { etReadyTimeout, etReadyInterval = ot, oi })
	etReadyTimeout = 30 * time.Millisecond
	etReadyInterval = 5 * time.Millisecond
}

// When etserver never comes up, the gate fails with an actionable error rather
// than handing a doomed connection to the et client.
func TestWaitETServerReady_ErrorsWhenNeverReady(t *testing.T) {
	shrinkETReady(t)
	runner := &testRunner{} // OutputQuiet returns "" → sentinel never present
	err := waitETServerReady(gcloud.NewClient(runner), etReadyRef, 2022, io.Discard)
	if err == nil || !strings.Contains(err.Error(), "et server not reachable") {
		t.Fatalf("expected not-reachable error, got %v", err)
	}
}

// When etserver answers, the gate returns nil and lets the connect proceed.
func TestWaitETServerReady_OKWhenReady(t *testing.T) {
	shrinkETReady(t)
	runner := &testRunner{outputs: map[string][]byte{etReadyProbeCmdKey: []byte("ra_et_84_ready\n")}}
	if err := waitETServerReady(gcloud.NewClient(runner), etReadyRef, 2022, io.Discard); err != nil {
		t.Fatalf("expected nil when etserver is ready, got %v", err)
	}
}

func newConnectCfg() *config.Config {
	return &config.Config{
		Gcloud: config.GcloudConfig{
			Project: "p",
			Workstation: config.WorkstationConfig{
				Region:      "r",
				ClusterName: "cl",
				ConfigName:  "cfg",
			},
		},
		WorkstationName: "p-user",
	}
}

func TestRunConnect_Running_SkipsStart(t *testing.T) {
	runner := &testRunner{
		checks: map[string]bool{
			"workstations describe p-user --cluster cl --config cfg --region r --project p": true,
		},
		outputs: map[string][]byte{
			"workstations describe p-user --cluster cl --config cfg --region r --project p --format value(name,state,host)": []byte("p-user\tSTATE_RUNNING\thost\n"),
		},
	}
	client := gcloud.NewClient(runner)
	var errOut bytes.Buffer
	if err := runConnect(newConnectCfg(), client, "", wsOverride{}, &errOut); err != nil {
		t.Fatalf("runConnect: %v", err)
	}
	for _, call := range runner.runs {
		joined := strings.Join(call, " ")
		if strings.HasPrefix(joined, "workstations start ") {
			t.Errorf("did not expect a start call, got: %s", joined)
		}
	}
	foundSSH := false
	for _, call := range runner.runs {
		if strings.HasPrefix(strings.Join(call, " "), "workstations ssh p-user ") {
			foundSSH = true
			break
		}
	}
	if !foundSSH {
		t.Errorf("expected ssh call, got runs=%v", runner.runs)
	}
	// Warm path: already RUNNING, so the SSH-readiness probe must be skipped
	// (it would only add a multi-second round-trip) and nothing is printed.
	if len(runner.quietRuns) != 0 {
		t.Errorf("expected no SSH-readiness probe on the warm path, got %v", runner.quietRuns)
	}
	if errOut.Len() != 0 {
		t.Errorf("expected no stderr output when already running, got %q", errOut.String())
	}
}

// zeroPollInterval makes the ensureRunning and waitSSHReady poll loops spin
// without sleeping, so state-transition tests finish instantly. Restored after
// the test.
//
// It mutates package-global poll knobs, so tests using it (and the timeout
// overrides) must NOT call t.Parallel() — they rely on Go's default serial
// execution within a package.
func zeroPollInterval(t *testing.T) {
	t.Helper()
	prevRun, prevSSH := waitRunningInterval, sshReadyInterval
	waitRunningInterval = 0
	sshReadyInterval = 0
	t.Cleanup(func() {
		waitRunningInterval = prevRun
		sshReadyInterval = prevSSH
	})
}

// sshReadyProbeKey is the OutputQuiet command waitSSHReady runs for the p-user
// fixture; readyProbeOutputs maps it to a sentinel-bearing stdout so the probe
// reports ready on the first try (the common case once RUNNING).
const sshReadyProbeKey = `workstations ssh p-user --cluster cl --config cfg --region r --project p --command echo "ra_$((6*7))_ready"`

func readyProbeOutputs() map[string][]byte {
	return map[string][]byte{sshReadyProbeKey: []byte("ra_42_ready\n")}
}

func TestRunConnect_Stopped_StartsThenSSH(t *testing.T) {
	zeroPollInterval(t)
	runner := &testRunner{
		checks: map[string]bool{
			"workstations describe p-user --cluster cl --config cfg --region r --project p": true,
		},
		// STOPPED on the initial describe, then RUNNING once the wait loop polls
		// after Start.
		outputSeq: map[string][][]byte{
			"workstations describe p-user --cluster cl --config cfg --region r --project p --format value(name,state,host)": {
				[]byte("p-user\tSTATE_STOPPED\t\n"),
				[]byte("p-user\tSTATE_RUNNING\thost\n"),
			},
		},
		outputs: readyProbeOutputs(),
	}
	client := gcloud.NewClient(runner)
	var errOut bytes.Buffer
	if err := runConnect(newConnectCfg(), client, "", wsOverride{}, &errOut); err != nil {
		t.Fatalf("runConnect: %v", err)
	}

	// Expect: a start call, followed by an ssh call, in that order.
	var startIdx, sshIdx = -1, -1
	for i, call := range runner.runs {
		joined := strings.Join(call, " ")
		if startIdx == -1 && strings.HasPrefix(joined, "workstations start p-user ") {
			startIdx = i
		}
		if sshIdx == -1 && strings.HasPrefix(joined, "workstations ssh p-user ") {
			sshIdx = i
		}
	}
	if startIdx == -1 {
		t.Fatalf("expected a start call, runs=%v", runner.runs)
	}
	if sshIdx == -1 {
		t.Fatalf("expected an ssh call, runs=%v", runner.runs)
	}
	if startIdx >= sshIdx {
		t.Errorf("expected start before ssh; start=%d ssh=%d", startIdx, sshIdx)
	}
	if !strings.Contains(errOut.String(), "starting") {
		t.Errorf("expected notice about starting, got %q", errOut.String())
	}
}

func TestRunConnect_Starting_WaitsThenSSH(t *testing.T) {
	zeroPollInterval(t)
	runner := &testRunner{
		checks: map[string]bool{
			"workstations describe p-user --cluster cl --config cfg --region r --project p": true,
		},
		// STARTING initially, then RUNNING — the wait loop must poll past the
		// STARTING state rather than SSHing into a not-yet-ready backend.
		outputSeq: map[string][][]byte{
			"workstations describe p-user --cluster cl --config cfg --region r --project p --format value(name,state,host)": {
				[]byte("p-user\tSTATE_STARTING\t\n"),
				[]byte("p-user\tSTATE_STARTING\t\n"),
				[]byte("p-user\tSTATE_RUNNING\thost\n"),
			},
		},
		outputs: readyProbeOutputs(),
	}
	client := gcloud.NewClient(runner)
	var errOut bytes.Buffer
	if err := runConnect(newConnectCfg(), client, "", wsOverride{}, &errOut); err != nil {
		t.Fatalf("runConnect: %v", err)
	}
	foundSSH := false
	for _, call := range runner.runs {
		joined := strings.Join(call, " ")
		if strings.HasPrefix(joined, "workstations start ") {
			t.Errorf("did not expect a start call for STARTING state, got: %v", call)
		}
		if strings.HasPrefix(joined, "workstations ssh p-user ") {
			foundSSH = true
		}
	}
	if !foundSSH {
		t.Errorf("expected ssh call after waiting for RUNNING, got runs=%v", runner.runs)
	}
	if !strings.Contains(errOut.String(), "waiting") {
		t.Errorf("expected a 'waiting' notice while starting, got %q", errOut.String())
	}
}

func TestEnsureRunning_TerminalStateWhileWaiting(t *testing.T) {
	zeroPollInterval(t)
	runner := &testRunner{
		// Starts STARTING but falls back to STOPPED — a failed start. We must
		// surface this as an error rather than spinning until the timeout.
		outputSeq: map[string][][]byte{
			"workstations describe p-user --cluster cl --config cfg --region r --project p --format value(name,state,host)": {
				[]byte("p-user\tSTATE_STARTING\t\n"),
				[]byte("p-user\tSTATE_STOPPED\t\n"),
			},
		},
	}
	client := gcloud.NewClient(runner)
	ref := gcloud.WorkstationRef{Project: "p", Region: "r", Cluster: "cl", Config: "cfg", Name: "p-user"}
	var errOut bytes.Buffer
	_, err := ensureRunning(client, ref, &errOut)
	if err == nil {
		t.Fatalf("expected an error when the workstation falls back to STOPPED")
	}
	if !strings.Contains(err.Error(), "STOPPED") {
		t.Errorf("error should mention the terminal state, got: %v", err)
	}
}

func TestEnsureRunning_Timeout(t *testing.T) {
	zeroPollInterval(t)
	prevTimeout := waitRunningTimeout
	waitRunningTimeout = 0 // expire immediately on the first deadline check
	t.Cleanup(func() { waitRunningTimeout = prevTimeout })
	runner := &testRunner{
		outputs: map[string][]byte{
			"workstations describe p-user --cluster cl --config cfg --region r --project p --format value(name,state,host)": []byte("p-user\tSTATE_STARTING\t\n"),
		},
	}
	client := gcloud.NewClient(runner)
	ref := gcloud.WorkstationRef{Project: "p", Region: "r", Cluster: "cl", Config: "cfg", Name: "p-user"}
	var errOut bytes.Buffer
	_, err := ensureRunning(client, ref, &errOut)
	if err == nil {
		t.Fatalf("expected a timeout error when the workstation never reaches RUNNING")
	}
	if !strings.Contains(err.Error(), "did not reach RUNNING") {
		t.Errorf("error should describe the timeout, got: %v", err)
	}
}

func TestEnsureRunning_EmptyStateFailsFast(t *testing.T) {
	zeroPollInterval(t)
	// A describe that yields no state (parse/format drift) must fail fast rather
	// than poll for the full timeout — set a long timeout to prove we don't wait.
	prevTimeout := waitRunningTimeout
	waitRunningTimeout = time.Hour
	t.Cleanup(func() { waitRunningTimeout = prevTimeout })
	runner := &testRunner{
		outputSeq: map[string][][]byte{
			"workstations describe p-user --cluster cl --config cfg --region r --project p --format value(name,state,host)": {
				[]byte("p-user\tSTATE_STARTING\t\n"),
				[]byte("p-user\t\t\n"), // RUNNING never comes; state parses empty
			},
		},
	}
	client := gcloud.NewClient(runner)
	ref := gcloud.WorkstationRef{Project: "p", Region: "r", Cluster: "cl", Config: "cfg", Name: "p-user"}
	var errOut bytes.Buffer
	_, err := ensureRunning(client, ref, &errOut)
	if err == nil {
		t.Fatalf("expected an error when the workstation returns no state")
	}
	if !strings.Contains(err.Error(), "no state") {
		t.Errorf("error should describe the missing state, got: %v", err)
	}
}

func TestRunConnect_WaitsForSSHReadyThenSSH(t *testing.T) {
	zeroPollInterval(t)
	runner := &testRunner{
		checks: map[string]bool{
			"workstations describe p-user --cluster cl --config cfg --region r --project p": true,
		},
		// STARTING -> RUNNING so this is the post-start path that actually
		// probes; then sshd not ready on the first probe, ready on the second —
		// runConnect must poll past the empty probe rather than SSHing into the gap.
		outputSeq: map[string][][]byte{
			"workstations describe p-user --cluster cl --config cfg --region r --project p --format value(name,state,host)": {
				[]byte("p-user\tSTATE_STARTING\t\n"),
				[]byte("p-user\tSTATE_RUNNING\thost\n"),
			},
			sshReadyProbeKey: {
				[]byte(""),
				[]byte("ra_42_ready\n"),
			},
		},
	}
	client := gcloud.NewClient(runner)
	var errOut bytes.Buffer
	if err := runConnect(newConnectCfg(), client, "", wsOverride{}, &errOut); err != nil {
		t.Fatalf("runConnect: %v", err)
	}
	foundSSH := false
	for _, call := range runner.runs {
		if strings.HasPrefix(strings.Join(call, " "), "workstations ssh p-user ") {
			foundSSH = true
		}
	}
	if !foundSSH {
		t.Errorf("expected ssh call after SSH became ready, got runs=%v", runner.runs)
	}
	if !strings.Contains(errOut.String(), "Waiting for SSH") {
		t.Errorf("expected a 'Waiting for SSH' notice, got %q", errOut.String())
	}
	if strings.Contains(errOut.String(), "connecting anyway") {
		t.Errorf("should not have hit the timeout fallback, got %q", errOut.String())
	}
}

func TestRunConnect_SSHReadyTimeout_ConnectsAnyway(t *testing.T) {
	zeroPollInterval(t)
	prevTimeout := sshReadyTimeout
	sshReadyTimeout = 0 // no polling window: probe once, then fall through
	t.Cleanup(func() { sshReadyTimeout = prevTimeout })
	runner := &testRunner{
		checks: map[string]bool{
			"workstations describe p-user --cluster cl --config cfg --region r --project p": true,
		},
		// STARTING -> RUNNING (so the probe runs), but the SSH probe never
		// returns the sentinel — connect must fall through anyway.
		outputSeq: map[string][][]byte{
			"workstations describe p-user --cluster cl --config cfg --region r --project p --format value(name,state,host)": {
				[]byte("p-user\tSTATE_STARTING\t\n"),
				[]byte("p-user\tSTATE_RUNNING\thost\n"),
			},
		},
		outputs: map[string][]byte{sshReadyProbeKey: []byte("")},
	}
	client := gcloud.NewClient(runner)
	var errOut bytes.Buffer
	if err := runConnect(newConnectCfg(), client, "", wsOverride{}, &errOut); err != nil {
		t.Fatalf("runConnect: %v", err)
	}
	foundSSH := false
	for _, call := range runner.runs {
		if strings.HasPrefix(strings.Join(call, " "), "workstations ssh p-user ") {
			foundSSH = true
		}
	}
	if !foundSSH {
		t.Errorf("expected ssh to be attempted anyway after the probe timed out, got runs=%v", runner.runs)
	}
	if !strings.Contains(errOut.String(), "connecting anyway") {
		t.Errorf("expected the timeout-fallback notice, got %q", errOut.String())
	}
}

func TestRunConnect_StartError_Propagates(t *testing.T) {
	runner := &testRunner{
		checks: map[string]bool{
			"workstations describe p-user --cluster cl --config cfg --region r --project p": true,
		},
		outputs: map[string][]byte{
			"workstations describe p-user --cluster cl --config cfg --region r --project p --format value(name,state,host)": []byte("p-user\tSTATE_STOPPED\t\n"),
		},
		runErrors: map[string]error{
			"workstations start p-user --cluster cl --config cfg --region r --project p": errors.New("boom"),
		},
	}
	client := gcloud.NewClient(runner)
	var errOut bytes.Buffer
	err := runConnect(newConnectCfg(), client, "", wsOverride{}, &errOut)
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "starting workstation") || !strings.Contains(err.Error(), "boom") {
		t.Errorf("error should wrap start failure, got: %v", err)
	}
	for _, call := range runner.runs {
		if strings.HasPrefix(strings.Join(call, " "), "workstations ssh ") {
			t.Errorf("ssh must not be called after start failure, got: %v", call)
		}
	}
}
