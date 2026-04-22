package cmd

import (
	"bytes"
	"strings"
	"testing"

	"github.com/adamtait/remote-agent/internal/config"
	"github.com/adamtait/remote-agent/internal/gcloud"
)

func newConnectCfg() *config.Config {
	return &config.Config{
		ProjectID:       "p",
		Region:          "r",
		ClusterName:     "cl",
		ConfigName:      "cfg",
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
	if err := runConnect(newConnectCfg(), client, "", &errOut); err != nil {
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

func TestRunConnect_Stopped_StartsThenSSH(t *testing.T) {
	runner := &testRunner{
		checks: map[string]bool{
			"workstations describe p-user --cluster cl --config cfg --region r --project p": true,
		},
		outputs: map[string][]byte{
			"workstations describe p-user --cluster cl --config cfg --region r --project p --format value(name,state,host)": []byte("p-user\tSTATE_STOPPED\t\n"),
		},
	}
	client := gcloud.NewClient(runner)
	var errOut bytes.Buffer
	if err := runConnect(newConnectCfg(), client, "", &errOut); err != nil {
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
