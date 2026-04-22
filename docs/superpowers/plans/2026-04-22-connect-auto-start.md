# Connect Auto-Start Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make `ws connect` automatically start the target workstation when it is stopped, so users don't get an opaque gcloud SSH failure on a stopped workstation.

**Architecture:** In `cmd/connect.go`, after resolving the workstation name, `Describe` it to read its state. If the state is neither `STATE_RUNNING` nor `STATE_STARTING`, print a one-line notice, call `Start` (which blocks until the workstation is running), then call `SSH`. Logic is extracted into a testable helper `runConnect` that takes an `io.Writer` for the notice, so tests can assert on call sequence and output without touching stdout.

**Tech Stack:** Go 1.26, `github.com/spf13/cobra`, `github.com/adamtait/remote-agent/internal/gcloud`, `github.com/adamtait/remote-agent/internal/config`.

---

## File Map

- **Modify** `cmd/connect.go` — extract body of `RunE` into a new `runConnect(cfg, client, name, errOut)` helper, add state check + auto-start, keep `RunE` as a thin wrapper that passes `cmd.ErrOrStderr()`.
- **Create** `cmd/connect_test.go` — unit tests for `runConnect` covering RUNNING, STARTING, STOPPED, Describe-error, and Start-error paths.
- **Modify** `cmd/resolve_test.go` — extend the existing package-internal `testRunner` fake to record `Run(...)` call args (currently discarded), so `connect_test.go` can assert that Start/SSH were or were not invoked.

The `testRunner` lives in `resolve_test.go` and is shared across `package cmd` tests (internal, not `_test` package). Extending it there keeps test infrastructure in one place. No production file moves.

---

### Task 1: Extend the package-internal `testRunner` fake to record Run calls

**Files:**
- Modify: `cmd/resolve_test.go`

This enables later tasks to assert on Start/SSH invocations. No production code changes.

- [ ] **Step 1: Add a `runs` field and record Run calls in `testRunner`**

In `cmd/resolve_test.go`, locate the `testRunner` struct and its `Run` method:

```go
type testRunner struct {
	outputs map[string][]byte
	checks  map[string]bool
}
```

```go
func (f *testRunner) Run(args ...string) error                             { return nil }
```

Replace with:

```go
type testRunner struct {
	outputs map[string][]byte
	checks  map[string]bool
	runs    [][]string
}
```

```go
func (f *testRunner) Run(args ...string) error {
	f.runs = append(f.runs, args)
	return nil
}
```

- [ ] **Step 2: Run the existing resolve tests to confirm nothing broke**

Run: `cd /home/user/workspace && go test ./cmd/ -run TestResolveWorkstationName -v`

Expected: all three existing `TestResolveWorkstationName_*` tests PASS.

- [ ] **Step 3: Commit**

```bash
cd /home/user/workspace
git add cmd/resolve_test.go
git commit -m "test: record Run calls in cmd package testRunner fake"
```

---

### Task 2: Extract `runConnect` helper; keep behavior unchanged; add first test

This task refactors `connect.go` without changing behavior (no auto-start yet), and lands the first passing test against the extracted helper. Auto-start logic arrives in Task 3.

**Files:**
- Modify: `cmd/connect.go`
- Create: `cmd/connect_test.go`

- [ ] **Step 1: Write the failing test for the RUNNING-state pass-through**

Create `cmd/connect_test.go` with:

```go
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
```

- [ ] **Step 2: Run the test to verify it fails (compile error: `runConnect` undefined)**

Run: `cd /home/user/workspace && go test ./cmd/ -run TestRunConnect_Running_SkipsStart -v`

Expected: FAIL with `undefined: runConnect`.

- [ ] **Step 3: Extract `runConnect` into `cmd/connect.go` preserving current behavior**

Replace the contents of `cmd/connect.go` with:

```go
package cmd

import (
	"io"

	"github.com/adamtait/remote-agent/internal/config"
	"github.com/adamtait/remote-agent/internal/gcloud"
	"github.com/spf13/cobra"
)

var connectCmd = &cobra.Command{
	Use:   "connect [name]",
	Short: "Open an SSH session to a workstation",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		var name string
		if len(args) == 1 {
			name = args[0]
		}
		return runConnect(cfg, client, name, cmd.ErrOrStderr())
	},
}

func runConnect(cfg *config.Config, client *gcloud.Client, name string, errOut io.Writer) error {
	_ = errOut // unused until Task 3 adds the auto-start notice
	resolved, err := resolveWorkstationName(cfg, client, name)
	if err != nil {
		return err
	}
	return client.SSH(cfg.ProjectID, cfg.Region, cfg.ClusterName, cfg.ConfigName, resolved)
}

func init() {
	rootCmd.AddCommand(connectCmd)
}
```

- [ ] **Step 4: Run the test to verify it passes**

Run: `cd /home/user/workspace && go test ./cmd/ -run TestRunConnect_Running_SkipsStart -v`

Expected: PASS.

- [ ] **Step 5: Run full package tests to confirm nothing else broke**

Run: `cd /home/user/workspace && go test ./...`

Expected: all packages PASS.

- [ ] **Step 6: Commit**

```bash
cd /home/user/workspace
git add cmd/connect.go cmd/connect_test.go
git commit -m "refactor: extract runConnect helper for testability"
```

---

### Task 3: Auto-start the workstation when it is not running

Adds the actual behavior change: check state after name resolution; if not `STATE_RUNNING` or `STATE_STARTING`, print a notice and call `Start` before `SSH`.

**Files:**
- Modify: `cmd/connect.go`
- Modify: `cmd/connect_test.go`

- [ ] **Step 1: Write the failing test for the STOPPED state (start then ssh)**

Append to `cmd/connect_test.go`:

```go
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
```

- [ ] **Step 2: Run the new test to verify it fails**

Run: `cd /home/user/workspace && go test ./cmd/ -run TestRunConnect_Stopped_StartsThenSSH -v`

Expected: FAIL with "expected a start call" (the current `runConnect` never calls Start).

- [ ] **Step 3: Implement the auto-start logic in `runConnect`**

Replace the `runConnect` function body in `cmd/connect.go` with:

```go
func runConnect(cfg *config.Config, client *gcloud.Client, name string, errOut io.Writer) error {
	resolved, err := resolveWorkstationName(cfg, client, name)
	if err != nil {
		return err
	}
	info, err := client.Describe(cfg.ProjectID, cfg.Region, cfg.ClusterName, cfg.ConfigName, resolved)
	if err != nil {
		return fmt.Errorf("describing workstation: %w", err)
	}
	if info.State != "STATE_RUNNING" && info.State != "STATE_STARTING" {
		fmt.Fprintf(errOut, "Workstation %q is %s; starting...\n", resolved, strings.TrimPrefix(info.State, "STATE_"))
		if err := client.Start(cfg.ProjectID, cfg.Region, cfg.ClusterName, cfg.ConfigName, resolved); err != nil {
			return fmt.Errorf("starting workstation: %w", err)
		}
	}
	return client.SSH(cfg.ProjectID, cfg.Region, cfg.ClusterName, cfg.ConfigName, resolved)
}
```

And update the imports at the top of `cmd/connect.go` to:

```go
import (
	"fmt"
	"io"
	"strings"

	"github.com/adamtait/remote-agent/internal/config"
	"github.com/adamtait/remote-agent/internal/gcloud"
	"github.com/spf13/cobra"
)
```

- [ ] **Step 4: Run the stopped-state test to verify it passes**

Run: `cd /home/user/workspace && go test ./cmd/ -run TestRunConnect_Stopped_StartsThenSSH -v`

Expected: PASS.

- [ ] **Step 5: Re-run the RUNNING-state test to confirm it still passes**

Run: `cd /home/user/workspace && go test ./cmd/ -run TestRunConnect_Running_SkipsStart -v`

Expected: PASS.

- [ ] **Step 6: Update the `connectCmd.Short` description to reflect new behavior**

In `cmd/connect.go`, change:

```go
	Short: "Open an SSH session to a workstation",
```

to:

```go
	Short: "Open an SSH session to a workstation (starts it if stopped)",
```

- [ ] **Step 7: Run full package tests to confirm nothing else broke**

Run: `cd /home/user/workspace && go test ./...`

Expected: all packages PASS.

- [ ] **Step 8: Commit**

```bash
cd /home/user/workspace
git add cmd/connect.go cmd/connect_test.go
git commit -m "feat(connect): start workstation automatically if stopped"
```

---

### Task 4: Cover STARTING and error paths

Adds three more targeted tests: `STATE_STARTING` must not re-trigger Start, Describe failures must propagate, and Start failures must propagate without calling SSH.

**Files:**
- Modify: `cmd/connect_test.go`
- Modify: `cmd/resolve_test.go` (minor — testRunner needs to return errors from Run for one test)

- [ ] **Step 1: Extend `testRunner` to optionally return an error from `Run`**

In `cmd/resolve_test.go`, add an optional error map keyed by the same call string. Update the struct:

```go
type testRunner struct {
	outputs   map[string][]byte
	checks    map[string]bool
	runs      [][]string
	runErrors map[string]error
}
```

Replace `Run` with:

```go
func (f *testRunner) Run(args ...string) error {
	f.runs = append(f.runs, args)
	if f.runErrors != nil {
		if err, ok := f.runErrors[f.key(args)]; ok {
			return err
		}
	}
	return nil
}
```

- [ ] **Step 2: Run resolve + connect tests to confirm nothing broke**

Run: `cd /home/user/workspace && go test ./cmd/ -run "TestResolveWorkstationName|TestRunConnect" -v`

Expected: all existing tests PASS.

- [ ] **Step 3: Write the failing test for STARTING state (must not call Start)**

Append to `cmd/connect_test.go`:

```go
func TestRunConnect_Starting_SkipsStart(t *testing.T) {
	runner := &testRunner{
		checks: map[string]bool{
			"workstations describe p-user --cluster cl --config cfg --region r --project p": true,
		},
		outputs: map[string][]byte{
			"workstations describe p-user --cluster cl --config cfg --region r --project p --format value(name,state,host)": []byte("p-user\tSTATE_STARTING\t\n"),
		},
	}
	client := gcloud.NewClient(runner)
	var errOut bytes.Buffer
	if err := runConnect(newConnectCfg(), client, "", &errOut); err != nil {
		t.Fatalf("runConnect: %v", err)
	}
	for _, call := range runner.runs {
		if strings.HasPrefix(strings.Join(call, " "), "workstations start ") {
			t.Errorf("did not expect a start call for STARTING state, got: %v", call)
		}
	}
}
```

- [ ] **Step 4: Run the new test to verify it passes (already covered by Task 3 logic)**

Run: `cd /home/user/workspace && go test ./cmd/ -run TestRunConnect_Starting_SkipsStart -v`

Expected: PASS.

- [ ] **Step 5: Write the failing test for Start-error propagation**

Append to `cmd/connect_test.go`:

```go
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
	err := runConnect(newConnectCfg(), client, "", &errOut)
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
```

Also add `"errors"` to the imports block of `cmd/connect_test.go`.

- [ ] **Step 6: Run the new test to verify it passes**

Run: `cd /home/user/workspace && go test ./cmd/ -run TestRunConnect_StartError_Propagates -v`

Expected: PASS (logic from Task 3 already wraps start errors and returns before SSH).

- [ ] **Step 7: Run the full test suite**

Run: `cd /home/user/workspace && go test ./...`

Expected: all packages PASS.

- [ ] **Step 8: Commit**

```bash
cd /home/user/workspace
git add cmd/connect.go cmd/connect_test.go cmd/resolve_test.go
git commit -m "test(connect): cover STARTING and start-error paths"
```

---

## Self-Review Checklist

- **Spec coverage:** The user's requirement — "connect command should also start the workstation if it has been stopped" — is implemented in Task 3 (`runConnect` calls `Start` when state is neither RUNNING nor STARTING), and verified by `TestRunConnect_Stopped_StartsThenSSH`.
- **No placeholders:** Every code block is concrete. No TBDs, no "similar to above", no "add error handling" hand-waves.
- **Type consistency:** `runConnect(cfg *config.Config, client *gcloud.Client, name string, errOut io.Writer) error` — signature stays identical across Tasks 2, 3, and 4. `testRunner` field names (`runs`, `runErrors`) stay consistent across Tasks 1 and 4.
- **State handling:** Both `STATE_RUNNING` and `STATE_STARTING` are treated as "no start needed", matching the existing pattern at `cmd/create.go:283` — so `ws connect` on a workstation that's mid-start won't issue a redundant Start call.
- **Ordering:** Test always asserts Start happens before SSH when Start is expected; Start-error test asserts SSH is never reached.

---

## Execution Handoff

Plan complete and saved to `docs/superpowers/plans/2026-04-22-connect-auto-start.md`. Two execution options:

1. **Subagent-Driven (recommended)** — I dispatch a fresh subagent per task, review between tasks, fast iteration.
2. **Inline Execution** — Execute tasks in this session using executing-plans, batch execution with checkpoints.

Which approach?
