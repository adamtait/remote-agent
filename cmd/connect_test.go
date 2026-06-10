package cmd

import (
	"bytes"
	"errors"
	"strings"
	"testing"

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
	if errOut.Len() != 0 {
		t.Errorf("expected no stderr output when already running, got %q", errOut.String())
	}
}

// zeroPollInterval makes ensureRunning's wait loop spin without sleeping, so
// state-transition tests finish instantly. Restored after the test.
func zeroPollInterval(t *testing.T) {
	t.Helper()
	prev := waitRunningInterval
	waitRunningInterval = 0
	t.Cleanup(func() { waitRunningInterval = prev })
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
	err := ensureRunning(client, ref, &errOut)
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
	err := ensureRunning(client, ref, &errOut)
	if err == nil {
		t.Fatalf("expected a timeout error when the workstation never reaches RUNNING")
	}
	if !strings.Contains(err.Error(), "did not reach RUNNING") {
		t.Errorf("error should describe the timeout, got: %v", err)
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
