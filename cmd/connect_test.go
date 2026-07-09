package cmd

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/adamtait/remote-agent/internal/config"
	"github.com/adamtait/remote-agent/internal/gcloud"
)

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
	if err := runConnect(newConnectCfg(), client, "", wsOverride{}, "", &errOut); err != nil {
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

// runningRunner returns a testRunner whose describe reports p-user RUNNING, so
// runConnect proceeds straight to the SSH call (warm path).
func runningRunner() *testRunner {
	return &testRunner{
		checks: map[string]bool{
			"workstations describe p-user --cluster cl --config cfg --region r --project p": true,
		},
		outputs: map[string][]byte{
			"workstations describe p-user --cluster cl --config cfg --region r --project p --format value(name,state,host)": []byte("p-user\tSTATE_RUNNING\thost\n"),
		},
	}
}

func sshCall(t *testing.T, runs [][]string) string {
	t.Helper()
	for _, call := range runs {
		if j := strings.Join(call, " "); strings.HasPrefix(j, "workstations ssh p-user ") {
			return j
		}
	}
	t.Fatalf("no ssh call found in runs=%v", runs)
	return ""
}

// A plugin login-command is threaded into the SSH invocation as `--command`
// plus a forced-PTY flag, so `ra connect` lands directly in (e.g.) tmux.
func TestRunConnect_LoginCommand_PassesCommandAndPTY(t *testing.T) {
	runner := runningRunner()
	client := gcloud.NewClient(runner)
	var errOut bytes.Buffer
	if err := runConnect(newConnectCfg(), client, "", wsOverride{}, "tmux new-session -A -s main", &errOut); err != nil {
		t.Fatalf("runConnect: %v", err)
	}
	ssh := sshCall(t, runner.runs)
	if !strings.Contains(ssh, "--command tmux new-session -A -s main") {
		t.Errorf("ssh call missing rendered --command: %s", ssh)
	}
	if !strings.Contains(ssh, "--ssh-flag=-t") {
		t.Errorf("ssh call missing forced-PTY flag: %s", ssh)
	}
}

// With no login-command the SSH call is exactly today's: no --command, no PTY
// flag, so plain interactive-shell behavior is preserved.
func TestRunConnect_NoLoginCommand_PlainSSH(t *testing.T) {
	runner := runningRunner()
	client := gcloud.NewClient(runner)
	var errOut bytes.Buffer
	if err := runConnect(newConnectCfg(), client, "", wsOverride{}, "", &errOut); err != nil {
		t.Fatalf("runConnect: %v", err)
	}
	ssh := sshCall(t, runner.runs)
	if strings.Contains(ssh, "--command") || strings.Contains(ssh, "--ssh-flag") {
		t.Errorf("expected a plain ssh call with no command/PTY flag, got: %s", ssh)
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
	if err := runConnect(newConnectCfg(), client, "", wsOverride{}, "", &errOut); err != nil {
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
	if err := runConnect(newConnectCfg(), client, "", wsOverride{}, "", &errOut); err != nil {
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
	if err := runConnect(newConnectCfg(), client, "", wsOverride{}, "", &errOut); err != nil {
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
	if err := runConnect(newConnectCfg(), client, "", wsOverride{}, "", &errOut); err != nil {
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
	err := runConnect(newConnectCfg(), client, "", wsOverride{}, "", &errOut)
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
