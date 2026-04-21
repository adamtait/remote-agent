package gcloud_test

import (
	"io"
	"strings"
	"testing"

	"github.com/adamtait/remote-agent/cws/internal/gcloud"
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
			"workstations list --cluster cl --region us-central1 --project proj --format value(name,state)": []byte(
				"proj-adam\tSTATE_RUNNING\nproj-bob\tSTATE_STOPPED\n",
			),
		},
	}
	client := gcloud.NewClient(fake)
	list, err := client.List("proj", "us-central1", "cl")
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 {
		t.Fatalf("expected 2 workstations, got %d", len(list))
	}
	if list[0].Name != "proj-adam" || list[0].State != "STATE_RUNNING" {
		t.Errorf("first entry: got %+v", list[0])
	}
	if list[1].Name != "proj-bob" || list[1].State != "STATE_STOPPED" {
		t.Errorf("second entry: got %+v", list[1])
	}
}

func TestClient_List_EmptyOutput(t *testing.T) {
	fake := &fakeRunner{
		outputs: map[string][]byte{
			"workstations list --cluster cl --region us-central1 --project proj --format value(name,state)": []byte(""),
		},
	}
	client := gcloud.NewClient(fake)
	list, err := client.List("proj", "us-central1", "cl")
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
