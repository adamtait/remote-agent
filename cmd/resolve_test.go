package cmd

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"

	"github.com/adamtait/remote-agent/internal/config"
	"github.com/adamtait/remote-agent/internal/gcloud"
	"github.com/spf13/cobra"
)

// testRunner is a minimal fake for resolve tests.
type testRunner struct {
	outputs   map[string][]byte
	checks    map[string]bool
	runs      [][]string
	runErrors map[string]error

	// outputSeq supplies a different Output result per successive call to the
	// same command, modeling state transitions (e.g. a workstation that goes
	// STARTING -> RUNNING across poll iterations). Each call consumes the next
	// entry; once exhausted, the last entry is returned for all further calls.
	// Takes precedence over outputs when a key is present in both.
	outputSeq map[string][][]byte

	// quietRuns records OutputQuiet calls (the SSH-readiness probe), so tests
	// can assert whether the probe ran — e.g. that the warm path skips it.
	quietRuns [][]string
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
func (f *testRunner) RunContext(_ context.Context, args ...string) error { return f.Run(args...) }
func (f *testRunner) RunWithStdin(_ io.Reader, _ ...string) error        { return nil }
func (f *testRunner) Check(args ...string) bool {
	if v, ok := f.checks[f.key(args)]; ok {
		return v
	}
	return false
}
func (f *testRunner) Output(args ...string) ([]byte, error) {
	k := f.key(args)
	if seq, ok := f.outputSeq[k]; ok && len(seq) > 0 {
		out := seq[0]
		if len(seq) > 1 {
			f.outputSeq[k] = seq[1:]
		}
		return out, nil
	}
	if out, ok := f.outputs[k]; ok {
		return out, nil
	}
	return nil, nil
}
func (f *testRunner) OutputQuiet(args ...string) ([]byte, error) {
	f.quietRuns = append(f.quietRuns, args)
	return f.Output(args...)
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

// listValueKey is the gcloud arg string for List against the test cluster/config.
const listValueKey = "workstations list --cluster cl --config cfg --region r --project p --format value(name,state,createTime)"

// forceTerminal overrides the interactive-picker seams for a test, wiring stdin
// to the given input and capturing menu output, and restores them on cleanup.
func forceTerminal(t *testing.T, input string) *bytes.Buffer {
	t.Helper()
	savedTTY, savedIn, savedOut := stdinIsTerminal, promptIn, promptOut
	out := &bytes.Buffer{}
	stdinIsTerminal = func() bool { return true }
	promptIn = strings.NewReader(input)
	promptOut = out
	t.Cleanup(func() { stdinIsTerminal, promptIn, promptOut = savedTTY, savedIn, savedOut })
	return out
}

func TestPickWorkstation(t *testing.T) {
	cands := []gcloud.WorkstationInfo{
		{Name: "alpha", State: "STATE_RUNNING"},
		{Name: "beta", State: "STATE_STOPPED"},
	}
	cases := []struct {
		name, input, want string
	}{
		{"selects second", "2\n", "beta"},
		{"empty defaults to first", "\n", "alpha"},
		{"out of range defaults to first", "9\n", "alpha"},
		{"garbage defaults to first", "xyz\n", "alpha"},
		{"eof defaults to first", "", "alpha"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := &bytes.Buffer{}
			got, err := pickWorkstation(strings.NewReader(tc.input), out, cands)
			if err != nil {
				t.Fatal(err)
			}
			if got.Name != tc.want {
				t.Errorf("got %q, want %q", got.Name, tc.want)
			}
			// Menu renders NAME + stripped STATE.
			if !strings.Contains(out.String(), "alpha") || !strings.Contains(out.String(), "RUNNING") {
				t.Errorf("menu missing name/state: %q", out.String())
			}
			if strings.Contains(out.String(), "STATE_") {
				t.Errorf("menu should strip STATE_ prefix: %q", out.String())
			}
		})
	}
}

func TestResolveWorkstationRef_InteractivePicksSelection(t *testing.T) {
	out := forceTerminal(t, "2\n")
	runner := &testRunner{
		outputs: map[string][]byte{
			// Unsorted; sortCandidates orders by CreateTime desc (no default-name
			// match), so the menu is [newest, older] → choice "2" is "older".
			listValueKey: []byte("older\tSTATE_STOPPED\t2024-01-10T08:00:00Z\nnewest\tSTATE_RUNNING\t2024-01-15T10:00:00Z\n"),
		},
	}
	ref, err := resolveWorkstationRef(resolveTestCfg(), gcloud.NewClient(runner), "", wsOverride{})
	if err != nil {
		t.Fatal(err)
	}
	if ref.Name != "older" {
		t.Errorf("got %q, want older", ref.Name)
	}
	if !strings.Contains(out.String(), "Multiple workstations found") {
		t.Errorf("expected menu output, got %q", out.String())
	}
}

func TestResolveWorkstationRef_InteractiveSingleAutoUses(t *testing.T) {
	forceTerminal(t, "") // no input needed; single candidate is auto-used
	runner := &testRunner{
		outputs: map[string][]byte{
			listValueKey: []byte("only\tSTATE_RUNNING\t2024-01-10T08:00:00Z\n"),
		},
	}
	ref, err := resolveWorkstationRef(resolveTestCfg(), gcloud.NewClient(runner), "", wsOverride{})
	if err != nil {
		t.Fatal(err)
	}
	if ref.Name != "only" {
		t.Errorf("got %q, want only", ref.Name)
	}
}

func TestResolveWorkstationRef_SelectForcesPickerOverDefault(t *testing.T) {
	// The default-named workstation (p-user) exists and would win non-interactively,
	// but --select must present the menu and honor the choice instead.
	out := forceTerminal(t, "2\n")
	runner := &testRunner{
		checks: map[string]bool{
			"workstations describe p-user --cluster cl --config cfg --region r --project p": true,
		},
		outputs: map[string][]byte{
			// sortCandidates puts p-user (default) first → menu [p-user, other];
			// choice "2" selects "other".
			listValueKey: []byte("other\tSTATE_RUNNING\t2024-01-15T10:00:00Z\np-user\tSTATE_RUNNING\t2024-01-10T08:00:00Z\n"),
		},
	}
	ref, err := resolveWorkstationRef(resolveTestCfg(), gcloud.NewClient(runner), "", wsOverride{Select: true})
	if err != nil {
		t.Fatal(err)
	}
	if ref.Name != "other" {
		t.Errorf("got %q, want other", ref.Name)
	}
	if !strings.Contains(out.String(), "p-user") {
		t.Errorf("menu should list the default workstation: %q", out.String())
	}
}

func TestResolveWorkstationRef_SelectNonTTYErrors(t *testing.T) {
	// stdinIsTerminal defaults to false under `go test` (no terminal).
	_, err := resolveWorkstationRef(resolveTestCfg(), gcloud.NewClient(&testRunner{}), "", wsOverride{Select: true})
	if err == nil || !strings.Contains(err.Error(), "requires an interactive terminal") {
		t.Errorf("expected terminal-required error, got %v", err)
	}
}

func TestResolveWorkstationRef_SelectWithNameErrors(t *testing.T) {
	_, err := resolveWorkstationRef(resolveTestCfg(), gcloud.NewClient(&testRunner{}), "foo", wsOverride{Select: true})
	if err == nil || !strings.Contains(err.Error(), "cannot be combined with an explicit workstation name") {
		t.Errorf("expected select+name conflict error, got %v", err)
	}
}

func TestReadLocationOverrides_ReadsSelect(t *testing.T) {
	c := &cobra.Command{Use: "x", RunE: func(*cobra.Command, []string) error { return nil }}
	addLocationOverrideFlags(c)
	c.SetArgs([]string{"--select", "--cluster", "cl-x"})
	if err := c.Execute(); err != nil {
		t.Fatal(err)
	}
	ov := readLocationOverrides(c)
	if !ov.Select || ov.Cluster != "cl-x" {
		t.Errorf("got %+v, want Select=true Cluster=cl-x", ov)
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

func TestResolveWorkstationRef_PartialOverrideFilters(t *testing.T) {
	// Two workstations with the same short name in different clusters.
	// Passing only --cluster (no --config) should narrow to one match by
	// filtering ListAll results — not skip the search.
	runner := &testRunner{
		outputs: map[string][]byte{
			"workstations list --project p --region r --format json": []byte(`[
  {"name": "projects/p/locations/r/workstationClusters/cl-a/workstationConfigs/cfg/workstations/dup", "state": "STATE_RUNNING"},
  {"name": "projects/p/locations/r/workstationClusters/cl-b/workstationConfigs/cfg/workstations/dup", "state": "STATE_RUNNING"}
]`),
		},
	}
	ref, err := resolveWorkstationRef(resolveTestCfg(), gcloud.NewClient(runner), "dup", wsOverride{Cluster: "cl-b"})
	if err != nil {
		t.Fatal(err)
	}
	if ref.Cluster != "cl-b" || ref.Config != "cfg" {
		t.Errorf("got %+v, want Cluster=cl-b Config=cfg", ref)
	}
}

func TestResolveWorkstationRef_FullURIConflictingOverride(t *testing.T) {
	uri := "projects/p/locations/r/workstationClusters/cl/workstationConfigs/cfg/workstations/x"
	_, err := resolveWorkstationRef(resolveTestCfg(), gcloud.NewClient(&testRunner{}), uri, wsOverride{Cluster: "different"})
	if err == nil || !strings.Contains(err.Error(), "conflicts") {
		t.Errorf("expected conflict error, got %v", err)
	}
}
