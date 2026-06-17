package gcloud_test

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/adamtait/remote-agent/internal/gcloud"
)

// fakeRunner implements Runner for tests.
type fakeRunner struct {
	calls   [][]string
	outputs map[string][]byte
	checks  map[string]bool
}

func (f *fakeRunner) key(args []string) string { return strings.Join(args, " ") }

func (f *fakeRunner) Run(args ...string) error {
	f.calls = append(f.calls, args)
	return nil
}

func (f *fakeRunner) RunContext(_ context.Context, args ...string) error {
	f.calls = append(f.calls, args)
	return nil
}

func (f *fakeRunner) RunWithStdin(_ io.Reader, args ...string) error {
	f.calls = append(f.calls, args)
	return nil
}

func (f *fakeRunner) Output(args ...string) ([]byte, error) {
	f.calls = append(f.calls, args)
	if out, ok := f.outputs[f.key(args)]; ok {
		return out, nil
	}
	return nil, nil
}

func (f *fakeRunner) OutputQuiet(args ...string) ([]byte, error) {
	return f.Output(args...)
}

func (f *fakeRunner) RunCapture(args ...string) (string, error) {
	return "", f.Run(args...)
}

func (f *fakeRunner) RunSoft(args ...string) error {
	return f.Run(args...)
}

func (f *fakeRunner) Check(args ...string) bool {
	f.calls = append(f.calls, args)
	if v, ok := f.checks[f.key(args)]; ok {
		return v
	}
	return true
}

func TestClient_List_ParsesTabSeparatedOutput(t *testing.T) {
	fake := &fakeRunner{
		outputs: map[string][]byte{
			"workstations list --cluster cl --config cfg --region us-central1 --project proj --format value(name,state,createTime)": []byte(
				"proj-adam\tSTATE_RUNNING\t2024-01-15T10:00:00Z\nproj-bob\tSTATE_STOPPED\t2024-01-14T08:00:00Z\n",
			),
		},
	}
	client := gcloud.NewClient(fake)
	list, err := client.List("proj", "us-central1", "cl", "cfg")
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 {
		t.Fatalf("expected 2 workstations, got %d", len(list))
	}
	if list[0].Name != "proj-adam" || list[0].State != "STATE_RUNNING" || list[0].CreateTime != "2024-01-15T10:00:00Z" {
		t.Errorf("first entry: got %+v", list[0])
	}
	if list[1].Name != "proj-bob" || list[1].State != "STATE_STOPPED" || list[1].CreateTime != "2024-01-14T08:00:00Z" {
		t.Errorf("second entry: got %+v", list[1])
	}
}

func TestClient_List_EmptyOutput(t *testing.T) {
	fake := &fakeRunner{
		outputs: map[string][]byte{
			"workstations list --cluster cl --config cfg --region us-central1 --project proj --format value(name,state,createTime)": []byte(""),
		},
	}
	client := gcloud.NewClient(fake)
	list, err := client.List("proj", "us-central1", "cl", "cfg")
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 0 {
		t.Errorf("expected 0 workstations, got %d", len(list))
	}
}

func TestClient_Describe_ParsesFields(t *testing.T) {
	fake := &fakeRunner{
		outputs: map[string][]byte{
			"workstations describe myws --cluster cl --config cfg --region us-central1 --project proj --format value(name,state,host)": []byte(
				"myws\tSTATE_RUNNING\thostname.workstations.dev\n",
			),
		},
	}
	client := gcloud.NewClient(fake)
	info, err := client.Describe("proj", "us-central1", "cl", "cfg", "myws")
	if err != nil {
		t.Fatal(err)
	}
	if info.Name != "myws" {
		t.Errorf("name: got %q", info.Name)
	}
	if info.State != "STATE_RUNNING" {
		t.Errorf("state: got %q", info.State)
	}
	if info.Host != "hostname.workstations.dev" {
		t.Errorf("host: got %q", info.Host)
	}
}

func TestClient_ListAll_ParsesJSONResourceName(t *testing.T) {
	fake := &fakeRunner{
		outputs: map[string][]byte{
			"workstations list --project proj --region us-central1 --format json": []byte(`[
  {"name": "projects/proj/locations/us-central1/workstationClusters/cl-x/workstationConfigs/cfg-a/workstations/ws-1", "state": "STATE_RUNNING"},
  {"name": "projects/proj/locations/us-central1/workstationClusters/cl-y/workstationConfigs/cfg-b/workstations/ws-2", "state": "STATE_STOPPED"}
]`),
		},
	}
	client := gcloud.NewClient(fake)
	list, err := client.ListAll("proj", "us-central1")
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 {
		t.Fatalf("expected 2 workstations, got %d", len(list))
	}
	if list[0].ProjectName != "proj" || list[0].Region != "us-central1" || list[0].ClusterName != "cl-x" || list[0].ConfigName != "cfg-a" || list[0].Name != "ws-1" || list[0].State != "STATE_RUNNING" {
		t.Errorf("first entry: got %+v", list[0])
	}
	if list[1].ProjectName != "proj" || list[1].Region != "us-central1" || list[1].ClusterName != "cl-y" || list[1].ConfigName != "cfg-b" || list[1].Name != "ws-2" || list[1].State != "STATE_STOPPED" {
		t.Errorf("second entry: got %+v", list[1])
	}
}

func TestClient_ListAll_RegionWildcardForwarded(t *testing.T) {
	fake := &fakeRunner{
		outputs: map[string][]byte{
			"workstations list --project proj --region - --format json": []byte(`[
  {"name": "projects/proj/locations/us-central1/workstationClusters/cl/workstationConfigs/cfg/workstations/ws-a", "state": "STATE_RUNNING"},
  {"name": "projects/proj/locations/europe-west4/workstationClusters/cl/workstationConfigs/cfg/workstations/ws-b", "state": "STATE_STOPPED"}
]`),
		},
	}
	list, err := gcloud.NewClient(fake).ListAll("proj", "-")
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 {
		t.Fatalf("expected 2 workstations, got %d", len(list))
	}
	if list[0].Region != "us-central1" {
		t.Errorf("first region: got %q, want us-central1", list[0].Region)
	}
	if list[1].Region != "europe-west4" {
		t.Errorf("second region: got %q, want europe-west4", list[1].Region)
	}
}

func TestClient_ListProjects_Parses(t *testing.T) {
	fake := &fakeRunner{
		outputs: map[string][]byte{
			"projects list --format value(projectId)": []byte("proj-a\nproj-b\nproj-c\n"),
		},
	}
	projects, err := gcloud.NewClient(fake).ListProjects()
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"proj-a", "proj-b", "proj-c"}
	if len(projects) != len(want) {
		t.Fatalf("expected %d projects, got %d", len(want), len(projects))
	}
	for i, p := range projects {
		if p != want[i] {
			t.Errorf("projects[%d]: got %q, want %q", i, p, want[i])
		}
	}
}

func TestClient_ListProjects_Empty(t *testing.T) {
	fake := &fakeRunner{
		outputs: map[string][]byte{
			"projects list --format value(projectId)": []byte(""),
		},
	}
	projects, err := gcloud.NewClient(fake).ListProjects()
	if err != nil {
		t.Fatal(err)
	}
	if len(projects) != 0 {
		t.Errorf("expected 0 projects, got %d", len(projects))
	}
}

func TestClient_ListAll_EmptyOutput(t *testing.T) {
	fake := &fakeRunner{
		outputs: map[string][]byte{
			"workstations list --project proj --region us-central1 --format json": []byte("[]"),
		},
	}
	client := gcloud.NewClient(fake)
	list, err := client.ListAll("proj", "us-central1")
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 0 {
		t.Errorf("expected 0 workstations, got %d", len(list))
	}
}

func TestClient_ListAll_CapturesHostAndCreateTime(t *testing.T) {
	fake := &fakeRunner{
		outputs: map[string][]byte{
			"workstations list --project proj --region us-central1 --format json": []byte(`[
  {"name": "projects/proj/locations/us-central1/workstationClusters/cl/workstationConfigs/cfg/workstations/ws", "state": "STATE_RUNNING", "host": "abc.workstations.dev", "createTime": "2024-01-15T10:00:00Z"}
]`),
		},
	}
	list, err := gcloud.NewClient(fake).ListAll("proj", "us-central1")
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 {
		t.Fatalf("expected 1 workstation, got %d", len(list))
	}
	if list[0].Host != "abc.workstations.dev" {
		t.Errorf("Host: got %q, want abc.workstations.dev", list[0].Host)
	}
	if list[0].CreateTime != "2024-01-15T10:00:00Z" {
		t.Errorf("CreateTime: got %q, want 2024-01-15T10:00:00Z", list[0].CreateTime)
	}
	if list[0].Region != "us-central1" {
		t.Errorf("Region: got %q, want us-central1", list[0].Region)
	}
}

func TestClient_ListAll_InvalidJSON(t *testing.T) {
	fake := &fakeRunner{
		outputs: map[string][]byte{
			"workstations list --project proj --region us-central1 --format json": []byte("not json"),
		},
	}
	client := gcloud.NewClient(fake)
	if _, err := client.ListAll("proj", "us-central1"); err == nil {
		t.Fatal("expected JSON parse error, got nil")
	}
}

func TestClient_Stop_CallsCorrectArgs(t *testing.T) {
	fake := &fakeRunner{}
	client := gcloud.NewClient(fake)
	if err := client.Stop("proj", "us-central1", "cl", "cfg", "myws"); err != nil {
		t.Fatal(err)
	}
	if len(fake.calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(fake.calls))
	}
	got := strings.Join(fake.calls[0], " ")
	want := "workstations stop myws --cluster cl --config cfg --region us-central1 --project proj"
	if got != want {
		t.Errorf("got %q\nwant %q", got, want)
	}
}

func TestClient_StartTCPTunnel_CallsCorrectArgs(t *testing.T) {
	fake := &fakeRunner{}
	client := gcloud.NewClient(fake)
	if err := client.StartTCPTunnel("proj", "us-central1", "cl", "cfg", "myws", 8080, 2718); err != nil {
		t.Fatal(err)
	}
	if len(fake.calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(fake.calls))
	}
	got := strings.Join(fake.calls[0], " ")
	want := "workstations start-tcp-tunnel --cluster cl --config cfg --region us-central1 --project proj --local-host-port localhost:8080 myws 2718"
	if got != want {
		t.Errorf("got %q\nwant %q", got, want)
	}
}

func TestClient_StartTCPTunnelContext_CallsCorrectArgs(t *testing.T) {
	fake := &fakeRunner{}
	client := gcloud.NewClient(fake)
	if err := client.StartTCPTunnelContext(context.Background(), "proj", "us-central1", "cl", "cfg", "myws", 8080, 2718); err != nil {
		t.Fatal(err)
	}
	if len(fake.calls) != 1 {
		t.Fatalf("expected 1 call, got %d", len(fake.calls))
	}
	got := strings.Join(fake.calls[0], " ")
	want := "workstations start-tcp-tunnel --cluster cl --config cfg --region us-central1 --project proj --local-host-port localhost:8080 myws 2718"
	if got != want {
		t.Errorf("got %q\nwant %q", got, want)
	}
}

// sshReadyProbeKey is the OutputQuiet command SSHReady runs. Kept here so the
// test breaks loudly if the probe's args (or the sentinel) ever change.
const sshReadyProbeKey = `workstations ssh myws --cluster cl --config cfg --region us-central1 --project proj --command echo "ra_$((6*7))_ready"`

func TestClient_SSHReady_TrueWhenSentinelRoundTrips(t *testing.T) {
	fake := &fakeRunner{
		// gcloud's own noise (port lines, host-key warning) plus the remotely
		// computed sentinel on stdout.
		outputs: map[string][]byte{sshReadyProbeKey: []byte("ra_42_ready\n")},
	}
	client := gcloud.NewClient(fake)
	if !client.SSHReady("proj", "us-central1", "cl", "cfg", "myws") {
		t.Errorf("expected SSHReady true when the sentinel round-trips")
	}
}

func TestClient_SSHReady_FalseWhenSentinelAbsent(t *testing.T) {
	// The not-yet-ready case: gcloud printed the SSL EOF to (discarded) stderr
	// and exited 0, so stdout carries no sentinel. Exit code is untrustworthy
	// here by design, so SSHReady must key off stdout content alone.
	fake := &fakeRunner{outputs: map[string][]byte{sshReadyProbeKey: []byte("")}}
	client := gcloud.NewClient(fake)
	if client.SSHReady("proj", "us-central1", "cl", "cfg", "myws") {
		t.Errorf("expected SSHReady false when the sentinel is absent from stdout")
	}
}

func TestClient_SSHReady_FalseWhenOnlyCommandTextEchoed(t *testing.T) {
	// Defense against a self-match: if the unevaluated probe command ever leaked
	// into the scanned stream (e.g. gcloud echoing it in a diagnostic), it
	// carries the literal `ra_$((6*7))_ready`, not the computed `ra_42_ready`.
	// The remote shell must actually run for the success token to appear, so
	// this must read as not-ready.
	fake := &fakeRunner{outputs: map[string][]byte{
		sshReadyProbeKey: []byte(`ERROR: failed running command [echo "ra_$((6*7))_ready"]`),
	}}
	client := gcloud.NewClient(fake)
	if client.SSHReady("proj", "us-central1", "cl", "cfg", "myws") {
		t.Errorf("expected SSHReady false when only the unevaluated command text is present")
	}
}

// etReadyProbeKey is the OutputQuiet command ETServerReady runs for port 2022
// (0x07E6). Kept here so the test breaks loudly if the probe's args (or the
// sentinel) ever change.
const etReadyProbeKey = `workstations ssh myws --cluster cl --config cfg --region us-central1 --project proj --command awk -v p=":07E6" '$2 ~ (p"$") && $4 == "0A" {f=1} END {if (f) print "ra_et_" (42*2) "_ready"}' /proc/net/tcp /proc/net/tcp6 2>/dev/null`

func TestClient_ETServerReady_TrueWhenSentinelRoundTrips(t *testing.T) {
	fake := &fakeRunner{outputs: map[string][]byte{etReadyProbeKey: []byte("ra_et_84_ready\n")}}
	client := gcloud.NewClient(fake)
	if !client.ETServerReady("proj", "us-central1", "cl", "cfg", "myws", 2022) {
		t.Errorf("expected ETServerReady true when the sentinel round-trips")
	}
}

func TestClient_ETServerReady_FalseWhenSentinelAbsent(t *testing.T) {
	// etserver not up yet: the /dev/tcp dial fails, so the echo never runs and
	// stdout carries no sentinel.
	fake := &fakeRunner{outputs: map[string][]byte{etReadyProbeKey: []byte("")}}
	client := gcloud.NewClient(fake)
	if client.ETServerReady("proj", "us-central1", "cl", "cfg", "myws", 2022) {
		t.Errorf("expected ETServerReady false when the sentinel is absent from stdout")
	}
}

func TestClient_ETServerReady_FalseWhenOnlyCommandTextEchoed(t *testing.T) {
	// Self-match guard: the unevaluated command carries the awk expression
	// `(42*2)`, not the computed `ra_et_84_ready`, so an echoed command line
	// must not read as ready.
	fake := &fakeRunner{outputs: map[string][]byte{
		etReadyProbeKey: []byte(`ERROR: failed running [awk ... print "ra_et_" (42*2) "_ready" ...]`),
	}}
	client := gcloud.NewClient(fake)
	if client.ETServerReady("proj", "us-central1", "cl", "cfg", "myws", 2022) {
		t.Errorf("expected ETServerReady false when only the unevaluated command text is present")
	}
}
