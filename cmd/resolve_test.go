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
func (f *testRunner) RunWithStdin(_ io.Reader, _ ...string) error { return nil }
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
func (f *testRunner) RunCapture(args ...string) (string, error) {
	return "", f.Run(args...)
}
func (f *testRunner) RunSoft(args ...string) error { return f.Run(args...) }

func resolveTestCfg() *config.Config {
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

func TestResolveWorkstationRef_NoNameUsesDefault(t *testing.T) {
	runner := &testRunner{
		checks: map[string]bool{
			"workstations describe p-user --cluster cl --config cfg --region r --project p": true,
		},
	}
	ref, err := resolveWorkstationRef(resolveTestCfg(), gcloud.NewClient(runner), "", wsOverride{})
	if err != nil {
		t.Fatal(err)
	}
	if ref.Name != "p-user" || ref.Cluster != "cl" || ref.Config != "cfg" || ref.Project != "p" {
		t.Errorf("got %+v", ref)
	}
}

func TestResolveWorkstationRef_NoNameMostRecent(t *testing.T) {
	runner := &testRunner{
		checks: map[string]bool{"workstations describe p-user --cluster cl --config cfg --region r --project p": false},
		outputs: map[string][]byte{
			"workstations list --cluster cl --config cfg --region r --project p --format value(name,state,createTime)": []byte(
				"older\tSTATE_STOPPED\t2024-01-10T08:00:00Z\nnewest\tSTATE_RUNNING\t2024-01-15T10:00:00Z\n"),
		},
	}
	ref, err := resolveWorkstationRef(resolveTestCfg(), gcloud.NewClient(runner), "", wsOverride{})
	if err != nil {
		t.Fatal(err)
	}
	if ref.Name != "newest" {
		t.Errorf("got %+v, want Name=newest", ref)
	}
}

func TestResolveWorkstationRef_FindsAcrossConfigs(t *testing.T) {
	// six-pages lives in cluster cl-z/cfg-z, but local config points at cl/cfg.
	// Resolver should find it via ListAll and adjust cluster/config.
	runner := &testRunner{
		outputs: map[string][]byte{
			"workstations list --project p --region r --format json": []byte(
				`[{"name": "projects/p/locations/r/workstationClusters/cl-z/workstationConfigs/cfg-z/workstations/six-pages", "state": "STATE_RUNNING"}]`),
		},
	}
	ref, err := resolveWorkstationRef(resolveTestCfg(), gcloud.NewClient(runner), "six-pages", wsOverride{})
	if err != nil {
		t.Fatal(err)
	}
	if ref.Cluster != "cl-z" || ref.Config != "cfg-z" || ref.Name != "six-pages" {
		t.Errorf("got %+v, want cluster=cl-z config=cfg-z name=six-pages", ref)
	}
}

func TestResolveWorkstationRef_AmbiguousNameErrors(t *testing.T) {
	runner := &testRunner{
		outputs: map[string][]byte{
			"workstations list --project p --region r --format json": []byte(`[
  {"name": "projects/p/locations/r/workstationClusters/cl/workstationConfigs/cfg-a/workstations/dup", "state": "STATE_RUNNING"},
  {"name": "projects/p/locations/r/workstationClusters/cl/workstationConfigs/cfg-b/workstations/dup", "state": "STATE_RUNNING"}
]`),
		},
	}
	_, err := resolveWorkstationRef(resolveTestCfg(), gcloud.NewClient(runner), "dup", wsOverride{})
	if err == nil {
		t.Fatal("expected ambiguity error, got nil")
	}
	if !strings.Contains(err.Error(), "ambiguous") || !strings.Contains(err.Error(), "--cluster") {
		t.Errorf("error should mention ambiguity and disambiguation, got %v", err)
	}
}

func TestResolveWorkstationRef_NameNotFound(t *testing.T) {
	runner := &testRunner{
		outputs: map[string][]byte{
			"workstations list --project p --region r --format json": []byte(`[]`),
		},
	}
	_, err := resolveWorkstationRef(resolveTestCfg(), gcloud.NewClient(runner), "ghost", wsOverride{})
	if err == nil {
		t.Fatal("expected not-found error, got nil")
	}
	if !strings.Contains(err.Error(), "no workstation") {
		t.Errorf("error should say not found, got %v", err)
	}
}

func TestResolveWorkstationRef_OverridesSkipSearch(t *testing.T) {
	// With both --cluster and --config explicit, skip ListAll and pre-flight Exists.
	runner := &testRunner{
		checks: map[string]bool{
			"workstations describe foo --cluster ovr-cl --config ovr-cfg --region r --project p": true,
		},
	}
	ref, err := resolveWorkstationRef(resolveTestCfg(), gcloud.NewClient(runner), "foo", wsOverride{Cluster: "ovr-cl", Config: "ovr-cfg"})
	if err != nil {
		t.Fatal(err)
	}
	if ref.Cluster != "ovr-cl" || ref.Config != "ovr-cfg" || ref.Name != "foo" {
		t.Errorf("got %+v", ref)
	}
}

func TestResolveWorkstationRef_OverridesPreflightFails(t *testing.T) {
	runner := &testRunner{} // checks default to false
	_, err := resolveWorkstationRef(resolveTestCfg(), gcloud.NewClient(runner), "ghost", wsOverride{Cluster: "ovr-cl", Config: "ovr-cfg"})
	if err == nil {
		t.Fatal("expected pre-flight error, got nil")
	}
	if !strings.Contains(err.Error(), "no workstation") {
		t.Errorf("expected not-found message, got %v", err)
	}
}

func TestResolveWorkstationRef_FullURI(t *testing.T) {
	uri := "projects/other-proj/locations/us-east1/workstationClusters/other-cl/workstationConfigs/other-cfg/workstations/x"
	ref, err := resolveWorkstationRef(resolveTestCfg(), gcloud.NewClient(&testRunner{}), uri, wsOverride{})
	if err != nil {
		t.Fatal(err)
	}
	if ref.Project != "other-proj" || ref.Region != "us-east1" || ref.Cluster != "other-cl" || ref.Config != "other-cfg" || ref.Name != "x" {
		t.Errorf("got %+v", ref)
	}
}

func TestResolveWorkstationRef_FullURIConflictingOverride(t *testing.T) {
	uri := "projects/p/locations/r/workstationClusters/cl/workstationConfigs/cfg/workstations/x"
	_, err := resolveWorkstationRef(resolveTestCfg(), gcloud.NewClient(&testRunner{}), uri, wsOverride{Cluster: "different"})
	if err == nil || !strings.Contains(err.Error(), "conflicts") {
		t.Errorf("expected conflict error, got %v", err)
	}
}
