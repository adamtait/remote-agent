package cmd

import (
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/adamtait/remote-agent/internal/config"
	"github.com/adamtait/remote-agent/internal/gcloud"
	"github.com/adamtait/remote-agent/internal/plugins"
)

// teardownTestCfg returns a config with a full set of named resources and two
// secrets, for exercising runTeardown.
func teardownTestCfg() *config.Config {
	return &config.Config{
		Gcloud: config.GcloudConfig{
			Project: "proj",
			Workstation: config.WorkstationConfig{
				Region:             "us-central1",
				ClusterName:        "clus",
				ConfigName:         "conf",
				ServiceAccountName: "ra-sa",
				RepoName:           "ra-repo",
			},
		},
		Secrets: map[string]config.SecretEntry{
			"alpha": {SecretName: "sec-alpha", EnvVar: "ALPHA"},
			"beta":  {SecretName: "sec-beta", EnvVar: "BETA"},
		},
	}
}

const teardownSA = "ra-sa@proj.iam.gserviceaccount.com"

// teardownRef is the resolved target matching teardownTestCfg.
func teardownRef() gcloud.WorkstationRef {
	return gcloud.WorkstationRef{Project: "proj", Region: "us-central1", Cluster: "clus", Config: "conf"}
}

// teardownRunner returns a fake runner primed so every ra resource "exists"
// (describe checks pass) and the workstation list returns two instances.
func teardownRunner() *testRunner {
	listKey := "workstations list --cluster clus --config conf --region us-central1 --project proj --format value(name,state,createTime)"
	return &testRunner{
		outputs: map[string][]byte{
			listKey: []byte("ws-a\tSTATE_RUNNING\t2026-01-01T00:00:00Z\nws-b\tSTATE_STOPPED\t2026-01-02T00:00:00Z\n"),
		},
		checks: map[string]bool{
			"workstations configs describe conf --cluster clus --region us-central1 --project proj": true,
			"workstations clusters describe clus --region us-central1 --project proj":               true,
			"iam service-accounts describe " + teardownSA + " --project proj":                       true,
			"artifacts repositories describe ra-repo --location us-central1 --project proj":         true,
			"secrets describe sec-alpha --project proj":                                             true,
			"secrets describe sec-beta --project proj":                                              true,
		},
	}
}

// findRun returns the index of the first recorded run whose joined args contain
// all the given substrings, or -1.
func findRun(runs [][]string, subs ...string) int {
	for i, args := range runs {
		joined := strings.Join(args, " ")
		all := true
		for _, s := range subs {
			if !strings.Contains(joined, s) {
				all = false
				break
			}
		}
		if all {
			return i
		}
	}
	return -1
}

// countInstanceDeletes counts `workstations delete <name>` runs (excluding
// `configs delete` / `clusters delete`).
func countInstanceDeletes(runs [][]string) int {
	n := 0
	for _, args := range runs {
		if len(args) >= 2 && args[0] == "workstations" && args[1] == "delete" {
			n++
		}
	}
	return n
}

func TestRunTeardown_DeletesInDependencyOrder(t *testing.T) {
	r := teardownRunner()
	client := gcloud.NewClient(r)
	reg := plugins.NewRegistry(nil)

	err := runTeardown(client, r, teardownTestCfg(), reg, teardownRef(),
		teardownOpts{Force: true}, io.Discard)
	if err != nil {
		t.Fatalf("runTeardown returned error: %v", err)
	}

	if got := countInstanceDeletes(r.runs); got != 2 {
		t.Errorf("instance deletes = %d, want 2", got)
	}

	cfgIdx := findRun(r.runs, "workstations", "configs", "delete", "conf")
	clusIdx := findRun(r.runs, "workstations", "clusters", "delete", "clus")
	saIdx := findRun(r.runs, "iam", "service-accounts", "delete", teardownSA)
	repoIdx := findRun(r.runs, "artifacts", "repositories", "delete", "ra-repo")
	for name, idx := range map[string]int{"config": cfgIdx, "cluster": clusIdx, "sa": saIdx, "repo": repoIdx} {
		if idx < 0 {
			t.Errorf("expected a %s delete run, found none", name)
		}
	}
	// config before cluster before service account (dependency order).
	if cfgIdx > clusIdx {
		t.Errorf("config delete (%d) must precede cluster delete (%d)", cfgIdx, clusIdx)
	}
	if clusIdx > saIdx {
		t.Errorf("cluster delete (%d) must precede service-account delete (%d)", clusIdx, saIdx)
	}

	// Both project IAM bindings removed.
	if findRun(r.runs, "remove-iam-policy-binding", "roles/logging.logWriter") < 0 {
		t.Error("expected removal of roles/logging.logWriter")
	}
	if findRun(r.runs, "remove-iam-policy-binding", "roles/monitoring.metricWriter") < 0 {
		t.Error("expected removal of roles/monitoring.metricWriter")
	}

	// Both secrets deleted.
	if findRun(r.runs, "secrets", "delete", "sec-alpha") < 0 || findRun(r.runs, "secrets", "delete", "sec-beta") < 0 {
		t.Error("expected both secrets to be deleted")
	}

	// Buckets NOT touched without --delete-buckets.
	if findRun(r.runs, "storage", "buckets", "delete") >= 0 {
		t.Error("buckets must not be deleted without --delete-buckets")
	}
}

func TestRunTeardown_KeepSecrets(t *testing.T) {
	r := teardownRunner()
	client := gcloud.NewClient(r)

	err := runTeardown(client, r, teardownTestCfg(), plugins.NewRegistry(nil), teardownRef(),
		teardownOpts{Force: true, KeepSecrets: true}, io.Discard)
	if err != nil {
		t.Fatalf("runTeardown returned error: %v", err)
	}
	if findRun(r.runs, "secrets", "delete") >= 0 {
		t.Error("--keep-secrets must not delete any secret")
	}
	// The cluster is still torn down.
	if findRun(r.runs, "workstations", "clusters", "delete", "clus") < 0 {
		t.Error("cluster should still be deleted with --keep-secrets")
	}
}

func TestRunTeardown_DryRunDeletesNothing(t *testing.T) {
	r := teardownRunner()
	client := gcloud.NewClient(r)

	err := runTeardown(client, r, teardownTestCfg(), plugins.NewRegistry(nil), teardownRef(),
		teardownOpts{DryRun: true}, io.Discard)
	if err != nil {
		t.Fatalf("runTeardown returned error: %v", err)
	}
	if len(r.runs) != 0 {
		t.Errorf("dry run recorded %d gcloud mutations, want 0: %v", len(r.runs), r.runs)
	}
}

func TestRunTeardown_BestEffortContinuesAfterFailure(t *testing.T) {
	r := teardownRunner()
	// Make the config delete fail; teardown must still proceed to the cluster.
	r.runErrors = map[string]error{
		"workstations configs delete conf --cluster clus --region us-central1 --project proj --quiet": errors.New("boom"),
	}
	client := gcloud.NewClient(r)

	err := runTeardown(client, r, teardownTestCfg(), plugins.NewRegistry(nil), teardownRef(),
		teardownOpts{Force: true}, io.Discard)
	if err == nil {
		t.Fatal("expected an aggregated error when a delete fails")
	}
	if !strings.Contains(err.Error(), "conf") {
		t.Errorf("aggregated error should mention the failed config; got %v", err)
	}
	// Later steps still ran despite the earlier failure.
	if findRun(r.runs, "workstations", "clusters", "delete", "clus") < 0 {
		t.Error("cluster delete should still run after an earlier failure")
	}
	if findRun(r.runs, "iam", "service-accounts", "delete", teardownSA) < 0 {
		t.Error("service-account delete should still run after an earlier failure")
	}
}

func TestRunTeardown_SkipsAbsentResources(t *testing.T) {
	// No checks pass and the list is empty → nothing to delete, no error.
	r := &testRunner{}
	client := gcloud.NewClient(r)

	err := runTeardown(client, r, teardownTestCfg(), plugins.NewRegistry(nil), teardownRef(),
		teardownOpts{Force: true}, io.Discard)
	if err != nil {
		t.Fatalf("runTeardown over absent resources should succeed, got %v", err)
	}
	// Only the best-effort project IAM removals (which don't check existence) run.
	for _, args := range r.runs {
		if strings.Contains(strings.Join(args, " "), "delete") {
			t.Errorf("no delete should run when nothing exists; got %v", args)
		}
	}
}
