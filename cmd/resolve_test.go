package cmd

import (
	"io"
	"strings"
	"testing"

	"github.com/adamtait/remote-agent/internal/config"
	"github.com/adamtait/remote-agent/internal/gcloud"
)

// testRunner is a minimal fake for resolve tests.
type testRunner struct {
	outputs   map[string][]byte
	checks    map[string]bool
	runs      [][]string
	runErrors map[string]error
}

func (f *testRunner) key(args []string) string { return strings.Join(args, " ") }
func (f *testRunner) Run(args ...string) error {
	f.runs = append(f.runs, args)
	if f.runErrors != nil {
		if err, ok := f.runErrors[f.key(args)]; ok {
			return err
		}
	}
	return nil
}
func (f *testRunner) RunWithStdin(_ io.Reader, _ ...string) error          { return nil }
func (f *testRunner) Check(args ...string) bool {
	if v, ok := f.checks[f.key(args)]; ok {
		return v
	}
	return false
}
func (f *testRunner) Output(args ...string) ([]byte, error) {
	if out, ok := f.outputs[f.key(args)]; ok {
		return out, nil
	}
	return nil, nil
}

func TestResolveWorkstationName_ExplicitName(t *testing.T) {
	cfg := &config.Config{ProjectID: "p", Region: "r", ClusterName: "cl", ConfigName: "cfg", WorkstationName: "p-user"}
	client := gcloud.NewClient(&testRunner{})
	name, err := resolveWorkstationName(cfg, client, "explicit-name")
	if err != nil {
		t.Fatal(err)
	}
	if name != "explicit-name" {
		t.Errorf("got %q, want explicit-name", name)
	}
}

func TestResolveWorkstationName_UsesDefaultWhenExists(t *testing.T) {
	runner := &testRunner{
		checks: map[string]bool{
			"workstations describe p-user --cluster cl --config cfg --region r --project p": true,
		},
		outputs: map[string][]byte{
			"workstations describe p-user --cluster cl --config cfg --region r --project p --format value(name,state,host)": []byte("p-user\tSTATE_RUNNING\thost\n"),
		},
	}
	cfg := &config.Config{ProjectID: "p", Region: "r", ClusterName: "cl", ConfigName: "cfg", WorkstationName: "p-user"}
	client := gcloud.NewClient(runner)
	name, err := resolveWorkstationName(cfg, client, "")
	if err != nil {
		t.Fatal(err)
	}
	if name != "p-user" {
		t.Errorf("got %q, want p-user", name)
	}
}

func TestResolveWorkstationName_SingleFallback(t *testing.T) {
	runner := &testRunner{
		checks: map[string]bool{
			"workstations describe p-user --cluster cl --config cfg --region r --project p": false,
		},
		outputs: map[string][]byte{
			"workstations list --cluster cl --config cfg --region r --project p --format value(name,state)": []byte("other-ws\tSTATE_STOPPED\n"),
		},
	}
	cfg := &config.Config{ProjectID: "p", Region: "r", ClusterName: "cl", ConfigName: "cfg", WorkstationName: "p-user"}
	client := gcloud.NewClient(runner)
	name, err := resolveWorkstationName(cfg, client, "")
	if err != nil {
		t.Fatal(err)
	}
	if name != "other-ws" {
		t.Errorf("got %q, want other-ws", name)
	}
}
