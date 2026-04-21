# cws setup Command Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add `cws setup` (interactive) and `cws setup template` (commented templates) subcommands that scaffold `.workstation.yaml` and `~/.config/cws/config.yaml`, probing the environment for sensible defaults.

**Architecture:** Pure logic lives in a new `cws/internal/scaffold` package (atomic writes, probes, prompts, templates, flow orchestration), exposed through a `Prober` interface so tests inject a fake. `cws/cmd/setup.go` wires cobra subcommands, flags, and a `RealProber`. The existing `rootCmd.PersistentPreRunE` loads `.workstation.yaml` and must be short-circuited on the setup subtree.

**Tech Stack:** Go 1.26, cobra, gopkg.in/yaml.v3, standard library (os/exec, bufio, io, regexp). Tests use Go's stdlib `testing`; no external test libraries.

---

## File Structure

Files created:
- `cws/internal/scaffold/atomic.go` — `WriteAtomic(path, data, force) error`
- `cws/internal/scaffold/atomic_test.go`
- `cws/internal/scaffold/probe.go` — `Prober` interface, `StaticProber` fake, `RealProber`
- `cws/internal/scaffold/probe_test.go`
- `cws/internal/scaffold/validate.go` — `ValidateProjectID`, `ValidateGithubRepoURL`
- `cws/internal/scaffold/validate_test.go`
- `cws/internal/scaffold/prompt.go` — `Prompt` primitive + `ErrCancelled`
- `cws/internal/scaffold/prompt_test.go`
- `cws/internal/scaffold/templates.go` — `RenderProjectTemplate`, `RenderGlobalTemplate`, `ProbedValues` struct
- `cws/internal/scaffold/templates_test.go`
- `cws/internal/scaffold/scaffold.go` — `InteractiveScaffold`, `TemplateScaffold`, option structs
- `cws/internal/scaffold/scaffold_test.go`
- `cws/cmd/setup.go` — cobra wiring for `setup` and `setup template`
- `cws/cmd/setup_test.go` — in-process cobra smoke tests

No existing files are modified except implicitly via the new `rootCmd.AddCommand(setupCmd)` call in `setup.go`'s `init()`. Short-circuit of `PersistentPreRunE` is done by defining `PersistentPreRunE: func(...) error { return nil }` on `setupCmd` itself — cobra uses the nearest-defined pre-run hook, so `cmd/root.go` stays untouched.

---

## Task 1: Atomic write primitive

**Files:**
- Create: `cws/internal/scaffold/atomic.go`
- Create: `cws/internal/scaffold/atomic_test.go`

- [ ] **Step 1.1: Write the failing test**

Create `cws/internal/scaffold/atomic_test.go`:

```go
package scaffold_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/adamtait/remote-agent/cws/internal/scaffold"
)

func TestWriteAtomic_CreatesNewFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "out.yaml")
	if err := scaffold.WriteAtomic(path, []byte("hello"), false); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "hello" {
		t.Errorf("got %q, want hello", got)
	}
}

func TestWriteAtomic_RefusesIfExistsWithoutForce(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "out.yaml")
	if err := os.WriteFile(path, []byte("original"), 0644); err != nil {
		t.Fatal(err)
	}
	err := scaffold.WriteAtomic(path, []byte("new"), false)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	got, _ := os.ReadFile(path)
	if string(got) != "original" {
		t.Errorf("file was modified: got %q, want original", got)
	}
}

func TestWriteAtomic_OverwritesWithForce(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "out.yaml")
	if err := os.WriteFile(path, []byte("original"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := scaffold.WriteAtomic(path, []byte("new"), true); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(path)
	if string(got) != "new" {
		t.Errorf("got %q, want new", got)
	}
}

func TestWriteAtomic_CreatesParentDir(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sub", "nested", "out.yaml")
	if err := scaffold.WriteAtomic(path, []byte("hi"), false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
}

func TestWriteAtomic_NoTempFileLeftOnSuccess(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "out.yaml")
	if err := scaffold.WriteAtomic(path, []byte("x"), false); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name() != "out.yaml" {
			t.Errorf("leftover file: %s", e.Name())
		}
	}
}
```

- [ ] **Step 1.2: Run tests to verify they fail**

Run: `cd cws && go test ./internal/scaffold/...`
Expected: build failure — package `scaffold` does not exist yet.

- [ ] **Step 1.3: Write minimal implementation**

Create `cws/internal/scaffold/atomic.go`:

```go
// Package scaffold builds cws config files from probed + prompted values.
package scaffold

import (
	"fmt"
	"os"
	"path/filepath"
)

// WriteAtomic writes data to path. If the file exists and force is false, it
// returns an error without modifying the existing file. Writes go via a temp
// file + rename so partial writes cannot corrupt an existing file.
func WriteAtomic(path string, data []byte, force bool) error {
	if _, err := os.Stat(path); err == nil {
		if !force {
			return fmt.Errorf("%s already exists; re-run with --force to overwrite", path)
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("stat %s: %w", path, err)
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("creating %s: %w", dir, err)
	}

	tmp, err := os.CreateTemp(dir, ".cws-scaffold-*")
	if err != nil {
		return fmt.Errorf("creating temp file: %w", err)
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return fmt.Errorf("writing temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("closing temp file: %w", err)
	}
	if err := os.Chmod(tmpName, 0644); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("chmod temp file: %w", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		os.Remove(tmpName)
		return fmt.Errorf("renaming to %s: %w", path, err)
	}
	return nil
}
```

- [ ] **Step 1.4: Run tests to verify they pass**

Run: `cd cws && go test ./internal/scaffold/...`
Expected: all 5 tests pass.

- [ ] **Step 1.5: Commit**

```bash
git add cws/internal/scaffold/atomic.go cws/internal/scaffold/atomic_test.go
git commit -m "feat(cws/scaffold): add atomic write primitive"
```

---

## Task 2: Prober interface + StaticProber fake

**Files:**
- Create: `cws/internal/scaffold/probe.go`
- Create: `cws/internal/scaffold/probe_test.go`

- [ ] **Step 2.1: Write the failing test**

Create `cws/internal/scaffold/probe_test.go`:

```go
package scaffold_test

import (
	"testing"

	"github.com/adamtait/remote-agent/cws/internal/scaffold"
)

func TestStaticProber_ReturnsCannedValues(t *testing.T) {
	p := &scaffold.StaticProber{
		Project:       "my-proj",
		Region:        "us-central1",
		GitUserName_:  "Alice",
		GitUserEmail_: "alice@example.com",
		GitOriginURL_: "https://github.com/a/b.git",
		HasGcloud_:    true,
		HasAuth_:      true,
		HasDocker_:    false,
		Billing:       map[string]bool{"my-proj": true},
	}

	if p.GcloudProject() != "my-proj" {
		t.Errorf("project: got %q", p.GcloudProject())
	}
	if p.GcloudRegion() != "us-central1" {
		t.Errorf("region: got %q", p.GcloudRegion())
	}
	if p.GitUserName() != "Alice" {
		t.Errorf("git user: got %q", p.GitUserName())
	}
	if !p.HasGcloud() {
		t.Error("HasGcloud: got false, want true")
	}
	if !p.HasGcloudAuth() {
		t.Error("HasGcloudAuth: got false")
	}
	if p.HasDocker() {
		t.Error("HasDocker: got true, want false")
	}
	if !p.BillingEnabled("my-proj") {
		t.Error("billing: got false")
	}
	if p.BillingEnabled("other") {
		t.Error("billing(other): got true")
	}
}
```

- [ ] **Step 2.2: Run tests to verify they fail**

Run: `cd cws && go test ./internal/scaffold/...`
Expected: build failure — `Prober`/`StaticProber` undefined.

- [ ] **Step 2.3: Write minimal implementation**

Create `cws/internal/scaffold/probe.go`:

```go
package scaffold

import (
	"os/exec"
	"strings"
)

// Prober discovers environment values used to pre-fill setup prompts and
// templates. All methods return zero values (empty string, false) when the
// underlying tool is missing or the value is not set — no errors are
// surfaced; setup treats absence as "no default".
type Prober interface {
	GcloudProject() string
	GcloudRegion() string
	GitUserName() string
	GitUserEmail() string
	GitOriginURL() string
	HasGcloud() bool
	HasGcloudAuth() bool
	HasDocker() bool
	BillingEnabled(projectID string) bool
}

// StaticProber is a test double returning canned values.
// Fields whose name would collide with a Prober method are suffixed with `_`.
type StaticProber struct {
	Project       string
	Region        string
	GitUserName_  string
	GitUserEmail_ string
	GitOriginURL_ string
	HasGcloud_    bool
	HasAuth_      bool
	HasDocker_    bool
	Billing       map[string]bool
}

func (p *StaticProber) GcloudProject() string         { return p.Project }
func (p *StaticProber) GcloudRegion() string          { return p.Region }
func (p *StaticProber) GitUserName() string           { return p.GitUserName_ }
func (p *StaticProber) GitUserEmail() string          { return p.GitUserEmail_ }
func (p *StaticProber) GitOriginURL() string          { return p.GitOriginURL_ }
func (p *StaticProber) HasGcloud() bool               { return p.HasGcloud_ }
func (p *StaticProber) HasGcloudAuth() bool           { return p.HasAuth_ }
func (p *StaticProber) HasDocker() bool               { return p.HasDocker_ }
func (p *StaticProber) BillingEnabled(id string) bool { return p.Billing[id] }

// RealProber shells out to gcloud/git/docker via the system PATH. All probes
// are best-effort: any error or non-zero exit yields the zero value.
type RealProber struct{}

func (RealProber) run(name string, args ...string) string {
	out, err := exec.Command(name, args...).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func (RealProber) exists(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

func (r RealProber) GcloudProject() string {
	return r.run("gcloud", "config", "get-value", "project", "--quiet")
}

func (r RealProber) GcloudRegion() string {
	return r.run("gcloud", "config", "get-value", "compute/region", "--quiet")
}

func (r RealProber) GitUserName() string  { return r.run("git", "config", "--get", "user.name") }
func (r RealProber) GitUserEmail() string { return r.run("git", "config", "--get", "user.email") }
func (r RealProber) GitOriginURL() string { return r.run("git", "remote", "get-url", "origin") }

func (r RealProber) HasGcloud() bool { return r.exists("gcloud") }
func (r RealProber) HasDocker() bool { return r.exists("docker") }

func (r RealProber) HasGcloudAuth() bool {
	return r.run("gcloud", "auth", "list", "--filter=status:ACTIVE", "--format=value(account)") != ""
}

func (r RealProber) BillingEnabled(projectID string) bool {
	out := r.run("gcloud", "beta", "billing", "projects", "describe", projectID,
		"--format=value(billingEnabled)")
	return strings.EqualFold(out, "true")
}

// GcloudRegionWithFallback returns the probed region, or "us-central1" if empty.
func GcloudRegionWithFallback(p Prober) string {
	if r := p.GcloudRegion(); r != "" {
		return r
	}
	return "us-central1"
}
```

Note: fields `GitUserName_`, `GitUserEmail_`, `GitOriginURL_` carry trailing underscores because plain `GitUserName` (etc.) would collide with the interface method of the same name. `Project` and `Region` don't collide with `GcloudProject`/`GcloudRegion` so they're plain. The test in Step 2.1 already uses these underscored field names.

- [ ] **Step 2.4: Run tests to verify they pass**

Run: `cd cws && go test ./internal/scaffold/...`
Expected: all tests (including the 5 from Task 1) pass.

- [ ] **Step 2.5: Commit**

```bash
git add cws/internal/scaffold/probe.go cws/internal/scaffold/probe_test.go
git commit -m "feat(cws/scaffold): add Prober interface and StaticProber fake"
```

---

## Task 3: Validators for project_id and github_repo_url

**Files:**
- Create: `cws/internal/scaffold/validate.go`
- Create: `cws/internal/scaffold/validate_test.go`

- [ ] **Step 3.1: Write the failing test**

Create `cws/internal/scaffold/validate_test.go`:

```go
package scaffold_test

import (
	"testing"

	"github.com/adamtait/remote-agent/cws/internal/scaffold"
)

func TestValidateProjectID(t *testing.T) {
	cases := []struct {
		in      string
		wantErr bool
	}{
		{"my-project-123", false},
		{"abc12", true},                       // too short (<6)
		{"my_project", true},                  // underscore disallowed
		{"1my-project", true},                 // must start with lowercase letter
		{"my-project-", true},                 // must end alphanumeric
		{"My-Project", true},                  // uppercase disallowed
		{"a-very-long-valid-project-name-ok", false}, // 33 chars, valid
		{"", true},
	}
	for _, tc := range cases {
		err := scaffold.ValidateProjectID(tc.in)
		if (err != nil) != tc.wantErr {
			t.Errorf("ValidateProjectID(%q): err=%v, wantErr=%v", tc.in, err, tc.wantErr)
		}
	}
}

func TestValidateGithubRepoURL(t *testing.T) {
	cases := []struct {
		in      string
		wantErr bool
	}{
		{"https://github.com/a/b.git", false},
		{"https://github.com/a/b", false},
		{"git@github.com:a/b.git", false},
		{"http://github.com/a/b", true}, // http not https
		{"github.com/a/b", true},        // no scheme
		{"", true},
	}
	for _, tc := range cases {
		err := scaffold.ValidateGithubRepoURL(tc.in)
		if (err != nil) != tc.wantErr {
			t.Errorf("ValidateGithubRepoURL(%q): err=%v, wantErr=%v", tc.in, err, tc.wantErr)
		}
	}
}
```

- [ ] **Step 3.2: Run tests to verify they fail**

Run: `cd cws && go test ./internal/scaffold/...`
Expected: build failure — validators undefined.

- [ ] **Step 3.3: Write minimal implementation**

Create `cws/internal/scaffold/validate.go`:

```go
package scaffold

import (
	"fmt"
	"regexp"
	"strings"
)

// projectIDRegex matches Google Cloud project ID rules: 6-30 chars,
// lowercase letter start, lowercase alphanumerics or hyphens, alphanumeric end.
var projectIDRegex = regexp.MustCompile(`^[a-z][-a-z0-9]{4,28}[a-z0-9]$`)

// ValidateProjectID returns nil iff id is a valid GCP project ID.
func ValidateProjectID(id string) error {
	if !projectIDRegex.MatchString(id) {
		return fmt.Errorf("project_id must be 6-30 lowercase alphanumerics or hyphens, starting with a letter and ending with a letter or digit")
	}
	return nil
}

// ValidateGithubRepoURL accepts https://... or git@... URLs.
func ValidateGithubRepoURL(url string) error {
	if strings.HasPrefix(url, "https://") || strings.HasPrefix(url, "git@") {
		return nil
	}
	return fmt.Errorf("github_repo_url must start with 'https://' or 'git@'")
}
```

- [ ] **Step 3.4: Run tests to verify they pass**

Run: `cd cws && go test ./internal/scaffold/...`
Expected: all tests pass.

- [ ] **Step 3.5: Commit**

```bash
git add cws/internal/scaffold/validate.go cws/internal/scaffold/validate_test.go
git commit -m "feat(cws/scaffold): add project_id and github_repo_url validators"
```

---

## Task 4: Prompt primitive

**Files:**
- Create: `cws/internal/scaffold/prompt.go`
- Create: `cws/internal/scaffold/prompt_test.go`

- [ ] **Step 4.1: Write the failing test**

Create `cws/internal/scaffold/prompt_test.go`:

```go
package scaffold_test

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/adamtait/remote-agent/cws/internal/scaffold"
)

func TestPrompt_ReturnsInput(t *testing.T) {
	in := strings.NewReader("my-value\n")
	var out bytes.Buffer
	got, err := scaffold.Prompt(in, &out, "name", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got != "my-value" {
		t.Errorf("got %q, want my-value", got)
	}
}

func TestPrompt_EmptyInputReturnsDefault(t *testing.T) {
	in := strings.NewReader("\n")
	var out bytes.Buffer
	got, err := scaffold.Prompt(in, &out, "name", "dflt", nil)
	if err != nil {
		t.Fatal(err)
	}
	if got != "dflt" {
		t.Errorf("got %q, want dflt", got)
	}
}

func TestPrompt_RepromptsOnValidationFailure(t *testing.T) {
	in := strings.NewReader("bad\ngood\n")
	var out bytes.Buffer
	validate := func(s string) error {
		if s == "bad" {
			return fmt.Errorf("not good enough")
		}
		return nil
	}
	got, err := scaffold.Prompt(in, &out, "name", "", validate)
	if err != nil {
		t.Fatal(err)
	}
	if got != "good" {
		t.Errorf("got %q, want good", got)
	}
	if !strings.Contains(out.String(), "not good enough") {
		t.Errorf("expected validation error in output, got: %q", out.String())
	}
}

func TestPrompt_EOFReturnsErrCancelled(t *testing.T) {
	in := strings.NewReader("")
	var out bytes.Buffer
	_, err := scaffold.Prompt(in, &out, "name", "", nil)
	if !errors.Is(err, scaffold.ErrCancelled) {
		t.Errorf("got %v, want ErrCancelled", err)
	}
}

func TestPrompt_DefaultShownInPromptString(t *testing.T) {
	in := strings.NewReader("\n")
	var out bytes.Buffer
	_, _ = scaffold.Prompt(in, &out, "region", "us-central1", nil)
	if !strings.Contains(out.String(), "[us-central1]") {
		t.Errorf("expected default shown in prompt, got: %q", out.String())
	}
}
```

- [ ] **Step 4.2: Run tests to verify they fail**

Run: `cd cws && go test ./internal/scaffold/...`
Expected: build failure — `Prompt`/`ErrCancelled` undefined.

- [ ] **Step 4.3: Write minimal implementation**

Create `cws/internal/scaffold/prompt.go`:

```go
package scaffold

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"strings"
)

// ErrCancelled is returned when the user cancels input (EOF / Ctrl-D).
var ErrCancelled = errors.New("cancelled")

// Prompt writes a prompt to out, reads one line from in, and returns the
// trimmed value. Empty input returns defaultValue. If validate is non-nil,
// the prompt is repeated until validate returns nil. EOF returns ErrCancelled.
func Prompt(in io.Reader, out io.Writer, label, defaultValue string, validate func(string) error) (string, error) {
	reader := bufio.NewReader(in)
	for {
		if defaultValue != "" {
			fmt.Fprintf(out, "%s [%s]: ", label, defaultValue)
		} else {
			fmt.Fprintf(out, "%s: ", label)
		}

		line, err := reader.ReadString('\n')
		if err == io.EOF && line == "" {
			return "", ErrCancelled
		}
		// A final line without trailing newline reaches here with err==io.EOF and
		// a non-empty line; treat it as normal input.
		if err != nil && err != io.EOF {
			return "", fmt.Errorf("reading input: %w", err)
		}

		value := strings.TrimSpace(line)
		if value == "" {
			value = defaultValue
		}
		if validate != nil {
			if verr := validate(value); verr != nil {
				fmt.Fprintf(out, "  %v\n", verr)
				continue
			}
		}
		return value, nil
	}
}
```

- [ ] **Step 4.4: Run tests to verify they pass**

Run: `cd cws && go test ./internal/scaffold/...`
Expected: all tests pass.

- [ ] **Step 4.5: Commit**

```bash
git add cws/internal/scaffold/prompt.go cws/internal/scaffold/prompt_test.go
git commit -m "feat(cws/scaffold): add Prompt primitive with validation and cancel"
```

---

## Task 5: Templates for project + global configs

**Files:**
- Create: `cws/internal/scaffold/templates.go`
- Create: `cws/internal/scaffold/templates_test.go`

- [ ] **Step 5.1: Write the failing test**

Create `cws/internal/scaffold/templates_test.go`:

```go
package scaffold_test

import (
	"strings"
	"testing"

	"github.com/adamtait/remote-agent/cws/internal/scaffold"
)

func TestRenderProjectTemplate_WithProbes(t *testing.T) {
	pv := scaffold.ProbedValues{
		ProjectID:     "my-proj",
		GithubRepoURL: "https://github.com/a/b.git",
	}
	got := scaffold.RenderProjectTemplate(pv)
	if !strings.Contains(got, "project_id: my-proj") {
		t.Errorf("expected project_id line with probed value, got:\n%s", got)
	}
	if !strings.Contains(got, "github_repo_url: https://github.com/a/b.git") {
		t.Errorf("expected github_repo_url line with probed value")
	}
	// commented optional fields present
	for _, frag := range []string{"# region:", "# cluster_name:", "# machine_type:", "# disk_size_gb:"} {
		if !strings.Contains(got, frag) {
			t.Errorf("expected %q in template", frag)
		}
	}
}

func TestRenderProjectTemplate_EmptyProbesShowPlaceholder(t *testing.T) {
	got := scaffold.RenderProjectTemplate(scaffold.ProbedValues{})
	if !strings.Contains(got, "(none — set manually)") {
		t.Errorf("expected 'none — set manually' sentinel, got:\n%s", got)
	}
}

func TestRenderGlobalTemplate_WithProbes(t *testing.T) {
	pv := scaffold.ProbedValues{
		Region:       "us-central1",
		GitUserName:  "Alice",
		GitUserEmail: "alice@example.com",
	}
	got := scaffold.RenderGlobalTemplate(pv)
	for _, frag := range []string{"defaults:", "# region: us-central1", "# git_user_name:", "Alice", "# git_user_email:", "alice@example.com", "# projects:"} {
		if !strings.Contains(got, frag) {
			t.Errorf("expected %q in global template", frag)
		}
	}
}

func TestRenderGlobalTemplate_NoProbesStillRenders(t *testing.T) {
	got := scaffold.RenderGlobalTemplate(scaffold.ProbedValues{})
	if !strings.Contains(got, "defaults:") {
		t.Error("defaults: section missing")
	}
}
```

- [ ] **Step 5.2: Run tests to verify they fail**

Run: `cd cws && go test ./internal/scaffold/...`
Expected: build failure — template functions undefined.

- [ ] **Step 5.3: Write minimal implementation**

Create `cws/internal/scaffold/templates.go`:

```go
package scaffold

import (
	"fmt"
	"strings"
)

// ProbedValues carries values discovered from the user's environment,
// used to pre-fill both templates and interactive defaults.
type ProbedValues struct {
	ProjectID     string
	Region        string
	GitUserName   string
	GitUserEmail  string
	GithubRepoURL string
}

const probedNone = "(none — set manually)"

func probedOr(v string) string {
	if v == "" {
		return probedNone
	}
	return v
}

// RenderProjectTemplate returns a commented .workstation.yaml with required
// fields filled from probes (or left as placeholders) and optional fields
// commented out with their hardcoded defaults shown.
func RenderProjectTemplate(pv ProbedValues) string {
	projectID := pv.ProjectID
	if projectID == "" {
		projectID = "my-gcp-project"
	}
	repoURL := pv.GithubRepoURL
	if repoURL == "" {
		repoURL = "https://github.com/my-org/my-repo.git"
	}

	var b strings.Builder
	fmt.Fprintf(&b, `# cws project config — committed to the repo root
# Required fields have no default; uncomment and fill them in.
# Optional fields show the default cws will apply if left unset.

project_id: %s        # REQUIRED — probed: %s
github_repo_url: %s   # REQUIRED — probed: %s

# region: us-central1             # optional; falls back to global default, then us-central1
# cluster_name: claude-dev-cluster
# config_name:  claude-dev-config
# machine_type: e2-standard-4
# disk_size_gb: 50
`, projectID, probedOr(pv.ProjectID), repoURL, probedOr(pv.GithubRepoURL))
	return b.String()
}

// RenderGlobalTemplate returns a commented ~/.config/cws/config.yaml with
// defaults and per-project overrides sections, each field commented out.
func RenderGlobalTemplate(pv ProbedValues) string {
	var b strings.Builder
	fmt.Fprintf(&b, `# cws global config — user-wide defaults and per-project overrides
# All fields optional; uncomment what you want to set.

defaults:
  # region: %s
  # git_user_name:  %q
  # git_user_email: %q

# projects:
#   my-gcp-project:
#     service_account_name: claude-workstation-sa
#     image_name:           claude-code-workstation
#     image_tag:            latest
#     repo_name:            claude-workstation-images
`, firstNonEmpty(pv.Region, "us-central1"), probedOr(pv.GitUserName), probedOr(pv.GitUserEmail))
	return b.String()
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
```

- [ ] **Step 5.4: Run tests to verify they pass**

Run: `cd cws && go test ./internal/scaffold/...`
Expected: all tests pass.

- [ ] **Step 5.5: Commit**

```bash
git add cws/internal/scaffold/templates.go cws/internal/scaffold/templates_test.go
git commit -m "feat(cws/scaffold): add project + global YAML template renderers"
```

---

## Task 6: Interactive and template flows

**Files:**
- Create: `cws/internal/scaffold/scaffold.go`
- Create: `cws/internal/scaffold/scaffold_test.go`

This task implements the orchestration functions — interactive prompts the user, template writes from probes only. Both share the final "write both files" step.

- [ ] **Step 6.1: Write the failing test (interactive happy path)**

Create `cws/internal/scaffold/scaffold_test.go`:

```go
package scaffold_test

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/adamtait/remote-agent/cws/internal/scaffold"
)

func TestInteractiveScaffold_WritesBothFiles(t *testing.T) {
	cwd := t.TempDir()
	home := t.TempDir()

	// Inputs (in order): project_id, github_repo_url, region, git_user_name,
	// git_user_email, then "y" to confirm.
	stdin := strings.NewReader(strings.Join([]string{
		"my-proj-123",
		"https://github.com/a/b.git",
		"us-central1",
		"Alice",
		"alice@example.com",
		"y",
	}, "\n") + "\n")
	var stdout, stderr bytes.Buffer

	prober := &scaffold.StaticProber{HasGcloud_: true, HasAuth_: true, HasDocker_: true}

	err := scaffold.InteractiveScaffold(scaffold.InteractiveOptions{
		Stdin: stdin, Stdout: &stdout, Stderr: &stderr,
		Cwd: cwd, Home: home, Prober: prober,
	})
	if err != nil {
		t.Fatalf("InteractiveScaffold: %v", err)
	}

	// Project file
	proj, err := os.ReadFile(filepath.Join(cwd, ".workstation.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(proj), "project_id: my-proj-123") {
		t.Errorf(".workstation.yaml missing project_id:\n%s", proj)
	}
	if !strings.Contains(string(proj), "github_repo_url: https://github.com/a/b.git") {
		t.Errorf(".workstation.yaml missing github_repo_url:\n%s", proj)
	}

	// Global file
	global, err := os.ReadFile(filepath.Join(home, ".config", "cws", "config.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(global), "region: us-central1") {
		t.Errorf("global config missing region:\n%s", global)
	}
	if !strings.Contains(string(global), "git_user_name: Alice") {
		t.Errorf("global config missing git_user_name:\n%s", global)
	}
}

func TestInteractiveScaffold_RefusesIfProjectFileExists(t *testing.T) {
	cwd := t.TempDir()
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(cwd, ".workstation.yaml"), []byte("existing: true\n"), 0644); err != nil {
		t.Fatal(err)
	}
	stdin := strings.NewReader(strings.Join([]string{
		"my-proj-123", "https://github.com/a/b.git", "us-central1", "Alice", "alice@example.com", "y",
	}, "\n") + "\n")
	var stdout, stderr bytes.Buffer
	err := scaffold.InteractiveScaffold(scaffold.InteractiveOptions{
		Stdin: stdin, Stdout: &stdout, Stderr: &stderr,
		Cwd: cwd, Home: home, Prober: &scaffold.StaticProber{},
	})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	// File unchanged
	got, _ := os.ReadFile(filepath.Join(cwd, ".workstation.yaml"))
	if string(got) != "existing: true\n" {
		t.Errorf("file modified: %q", got)
	}
}

func TestInteractiveScaffold_ForceOverwrites(t *testing.T) {
	cwd := t.TempDir()
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(cwd, ".workstation.yaml"), []byte("existing: true\n"), 0644); err != nil {
		t.Fatal(err)
	}
	stdin := strings.NewReader(strings.Join([]string{
		"my-proj-123", "https://github.com/a/b.git", "us-central1", "Alice", "alice@example.com", "y",
	}, "\n") + "\n")
	var stdout, stderr bytes.Buffer
	err := scaffold.InteractiveScaffold(scaffold.InteractiveOptions{
		Stdin: stdin, Stdout: &stdout, Stderr: &stderr,
		Cwd: cwd, Home: home, Prober: &scaffold.StaticProber{}, Force: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(filepath.Join(cwd, ".workstation.yaml"))
	if !strings.Contains(string(got), "my-proj-123") {
		t.Errorf("file not overwritten: %q", got)
	}
}

func TestInteractiveScaffold_ProjectOnlySkipsGlobal(t *testing.T) {
	cwd := t.TempDir()
	home := t.TempDir()
	stdin := strings.NewReader(strings.Join([]string{
		"my-proj-123", "https://github.com/a/b.git", "y",
	}, "\n") + "\n")
	var stdout, stderr bytes.Buffer
	err := scaffold.InteractiveScaffold(scaffold.InteractiveOptions{
		Stdin: stdin, Stdout: &stdout, Stderr: &stderr,
		Cwd: cwd, Home: home, Prober: &scaffold.StaticProber{}, ProjectOnly: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(cwd, ".workstation.yaml")); err != nil {
		t.Errorf("project file not written: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, ".config", "cws", "config.yaml")); !os.IsNotExist(err) {
		t.Errorf("global file was written despite --project-only")
	}
}

func TestInteractiveScaffold_MutuallyExclusiveFlags(t *testing.T) {
	err := scaffold.InteractiveScaffold(scaffold.InteractiveOptions{
		Stdin: strings.NewReader(""), Stdout: &bytes.Buffer{}, Stderr: &bytes.Buffer{},
		Cwd: t.TempDir(), Home: t.TempDir(), Prober: &scaffold.StaticProber{},
		ProjectOnly: true, GlobalOnly: true,
	})
	if err == nil {
		t.Fatal("expected error for mutually exclusive flags")
	}
}

func TestTemplateScaffold_WritesBothFiles(t *testing.T) {
	cwd := t.TempDir()
	home := t.TempDir()
	var stdout bytes.Buffer

	prober := &scaffold.StaticProber{
		Project: "probed-proj", Region: "europe-west1",
		GitUserName_: "Bob", GitUserEmail_: "bob@example.com",
	}

	err := scaffold.TemplateScaffold(scaffold.TemplateOptions{
		Stdout: &stdout, Cwd: cwd, Home: home, Prober: prober,
	})
	if err != nil {
		t.Fatal(err)
	}
	proj, _ := os.ReadFile(filepath.Join(cwd, ".workstation.yaml"))
	if !strings.Contains(string(proj), "probed: probed-proj") {
		t.Errorf("template missing probed project id:\n%s", proj)
	}
	global, _ := os.ReadFile(filepath.Join(home, ".config", "cws", "config.yaml"))
	if !strings.Contains(string(global), "europe-west1") {
		t.Errorf("global template missing region:\n%s", global)
	}
}
```

- [ ] **Step 6.2: Run tests to verify they fail**

Run: `cd cws && go test ./internal/scaffold/...`
Expected: build failure — `InteractiveScaffold`, `TemplateScaffold`, options undefined.

- [ ] **Step 6.3: Write minimal implementation**

Create `cws/internal/scaffold/scaffold.go`:

```go
package scaffold

import (
	"fmt"
	"io"
	"path/filepath"
	"strings"
)

// InteractiveOptions configure InteractiveScaffold.
type InteractiveOptions struct {
	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer

	Cwd    string // used for .workstation.yaml path
	Home   string // used for ~/.config/cws/config.yaml path
	Prober Prober

	Force       bool
	ProjectOnly bool
	GlobalOnly  bool
	Advanced    bool // prompt for optional project fields too
}

// TemplateOptions configure TemplateScaffold.
type TemplateOptions struct {
	Stdout io.Writer
	Cwd    string
	Home   string
	Prober Prober

	Force       bool
	ProjectOnly bool
	GlobalOnly  bool
}

// InteractiveScaffold prompts the user for config values and writes the
// project and/or global config files.
func InteractiveScaffold(opts InteractiveOptions) error {
	if opts.ProjectOnly && opts.GlobalOnly {
		return fmt.Errorf("--project-only and --global-only are mutually exclusive")
	}

	writeProject := !opts.GlobalOnly
	writeGlobal := !opts.ProjectOnly

	warnPrereqs(opts.Stderr, opts.Prober)

	pv := ProbedValues{
		ProjectID:     opts.Prober.GcloudProject(),
		Region:        GcloudRegionWithFallback(opts.Prober),
		GitUserName:   opts.Prober.GitUserName(),
		GitUserEmail:  opts.Prober.GitUserEmail(),
		GithubRepoURL: opts.Prober.GitOriginURL(),
	}

	project := projectConfigYAML{}
	global := globalConfigYAML{}

	if writeProject {
		projectID, err := Prompt(opts.Stdin, opts.Stdout, "project_id", pv.ProjectID, ValidateProjectID)
		if err != nil {
			return err
		}
		project.ProjectID = projectID

		// Billing check runs now that project_id is known.
		if !opts.Prober.BillingEnabled(projectID) {
			fmt.Fprintf(opts.Stderr, "WARN: could not confirm billing is enabled on %s (cws create will fail otherwise)\n", projectID)
		}

		repoURL, err := Prompt(opts.Stdin, opts.Stdout, "github_repo_url", pv.GithubRepoURL, ValidateGithubRepoURL)
		if err != nil {
			return err
		}
		project.GithubRepoURL = repoURL

		if opts.Advanced {
			if project.Region, err = Prompt(opts.Stdin, opts.Stdout, "region", pv.Region, nil); err != nil {
				return err
			}
			if project.ClusterName, err = Prompt(opts.Stdin, opts.Stdout, "cluster_name", "claude-dev-cluster", nil); err != nil {
				return err
			}
			if project.ConfigName, err = Prompt(opts.Stdin, opts.Stdout, "config_name", "claude-dev-config", nil); err != nil {
				return err
			}
			if project.MachineType, err = Prompt(opts.Stdin, opts.Stdout, "machine_type", "e2-standard-4", nil); err != nil {
				return err
			}
			if project.DiskSizeGB, err = Prompt(opts.Stdin, opts.Stdout, "disk_size_gb", "50", nil); err != nil {
				return err
			}
		}
	}

	if writeGlobal {
		var err error
		if global.Region, err = Prompt(opts.Stdin, opts.Stdout, "region", pv.Region, nil); err != nil {
			return err
		}
		if global.GitUserName, err = Prompt(opts.Stdin, opts.Stdout, "git_user_name", pv.GitUserName, nil); err != nil {
			return err
		}
		if global.GitUserEmail, err = Prompt(opts.Stdin, opts.Stdout, "git_user_email", pv.GitUserEmail, nil); err != nil {
			return err
		}
	}

	fmt.Fprintln(opts.Stdout)
	if writeProject {
		fmt.Fprintln(opts.Stdout, "About to write "+filepath.Join(opts.Cwd, ".workstation.yaml")+":")
		fmt.Fprintln(opts.Stdout, indent(project.marshal(), "  "))
	}
	if writeGlobal {
		fmt.Fprintln(opts.Stdout, "About to write "+filepath.Join(opts.Home, ".config", "cws", "config.yaml")+":")
		fmt.Fprintln(opts.Stdout, indent(global.marshal(), "  "))
	}

	confirm, err := Prompt(opts.Stdin, opts.Stdout, "Proceed? [Y/n]", "Y", nil)
	if err != nil {
		return err
	}
	if strings.ToLower(strings.TrimSpace(confirm)) == "n" {
		return fmt.Errorf("cancelled by user")
	}

	if writeProject {
		if err := WriteAtomic(filepath.Join(opts.Cwd, ".workstation.yaml"), []byte(project.marshal()), opts.Force); err != nil {
			return err
		}
	}
	if writeGlobal {
		if err := WriteAtomic(filepath.Join(opts.Home, ".config", "cws", "config.yaml"), []byte(global.marshal()), opts.Force); err != nil {
			return err
		}
	}

	fmt.Fprintln(opts.Stdout, "Run 'cws create' to provision the workstation.")
	return nil
}

// TemplateScaffold writes commented-template versions of both config files
// without prompting.
func TemplateScaffold(opts TemplateOptions) error {
	if opts.ProjectOnly && opts.GlobalOnly {
		return fmt.Errorf("--project-only and --global-only are mutually exclusive")
	}

	pv := ProbedValues{
		ProjectID:     opts.Prober.GcloudProject(),
		Region:        opts.Prober.GcloudRegion(),
		GitUserName:   opts.Prober.GitUserName(),
		GitUserEmail:  opts.Prober.GitUserEmail(),
		GithubRepoURL: opts.Prober.GitOriginURL(),
	}

	if !opts.GlobalOnly {
		path := filepath.Join(opts.Cwd, ".workstation.yaml")
		if err := WriteAtomic(path, []byte(RenderProjectTemplate(pv)), opts.Force); err != nil {
			return err
		}
		fmt.Fprintln(opts.Stdout, "Wrote "+path)
	}
	if !opts.ProjectOnly {
		path := filepath.Join(opts.Home, ".config", "cws", "config.yaml")
		if err := WriteAtomic(path, []byte(RenderGlobalTemplate(pv)), opts.Force); err != nil {
			return err
		}
		fmt.Fprintln(opts.Stdout, "Wrote "+path)
	}
	return nil
}

func warnPrereqs(out io.Writer, p Prober) {
	if !p.HasGcloud() {
		fmt.Fprintln(out, "WARN: gcloud CLI not found on PATH — cws create will fail until installed")
	} else if !p.HasGcloudAuth() {
		fmt.Fprintln(out, "WARN: no active gcloud credentials — run 'gcloud auth login' before 'cws create'")
	}
	if !p.HasDocker() {
		fmt.Fprintln(out, "WARN: docker not found on PATH — cws create needs it to build the workstation image")
	}
}

// projectConfigYAML is a minimal YAML emitter that writes only the fields
// the user actually supplied, matching the keys of config.ProjectConfig.
type projectConfigYAML struct {
	ProjectID     string
	Region        string
	ClusterName   string
	ConfigName    string
	GithubRepoURL string
	MachineType   string
	DiskSizeGB    string // string so "" can signal "don't emit"
}

func (p projectConfigYAML) marshal() string {
	var b strings.Builder
	emit := func(key, val string) {
		if val != "" {
			fmt.Fprintf(&b, "%s: %s\n", key, val)
		}
	}
	emit("project_id", p.ProjectID)
	emit("region", p.Region)
	emit("cluster_name", p.ClusterName)
	emit("config_name", p.ConfigName)
	emit("github_repo_url", p.GithubRepoURL)
	emit("machine_type", p.MachineType)
	emit("disk_size_gb", p.DiskSizeGB)
	return b.String()
}

type globalConfigYAML struct {
	Region       string
	GitUserName  string
	GitUserEmail string
}

func (g globalConfigYAML) marshal() string {
	var b strings.Builder
	b.WriteString("defaults:\n")
	emit := func(key, val string) {
		if val != "" {
			fmt.Fprintf(&b, "  %s: %s\n", key, val)
		}
	}
	emit("region", g.Region)
	emit("git_user_name", g.GitUserName)
	emit("git_user_email", g.GitUserEmail)
	return b.String()
}

func indent(s, prefix string) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	for i, l := range lines {
		lines[i] = prefix + l
	}
	return strings.Join(lines, "\n")
}
```

- [ ] **Step 6.4: Run tests to verify they pass**

Run: `cd cws && go test ./internal/scaffold/...`
Expected: all tests pass (including Tasks 1-5 tests).

- [ ] **Step 6.5: Commit**

```bash
git add cws/internal/scaffold/scaffold.go cws/internal/scaffold/scaffold_test.go
git commit -m "feat(cws/scaffold): add InteractiveScaffold and TemplateScaffold orchestrators"
```

---

## Task 7: Cobra wiring for `setup` and `setup template`

**Files:**
- Create: `cws/cmd/setup.go`
- Create: `cws/cmd/setup_test.go`

- [ ] **Step 7.1: Write the failing test**

Create `cws/cmd/setup_test.go`:

```go
package cmd_test

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/adamtait/remote-agent/cws/cmd"
)

func TestSetupTemplate_WritesFiles(t *testing.T) {
	cwd := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)

	origWd, _ := os.Getwd()
	if err := os.Chdir(cwd); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(origWd) })

	var stdout, stderr bytes.Buffer
	code := cmd.ExecuteWithIO(&bytes.Buffer{}, &stdout, &stderr, []string{"setup", "template"})
	if code != 0 {
		t.Fatalf("exit %d, stderr=%q", code, stderr.String())
	}
	if _, err := os.Stat(filepath.Join(cwd, ".workstation.yaml")); err != nil {
		t.Errorf(".workstation.yaml not written: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, ".config", "cws", "config.yaml")); err != nil {
		t.Errorf("global config not written: %v", err)
	}
}

func TestSetupTemplate_RefusesExistingWithoutForce(t *testing.T) {
	cwd := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)
	origWd, _ := os.Getwd()
	if err := os.Chdir(cwd); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(origWd) })

	if err := os.WriteFile(filepath.Join(cwd, ".workstation.yaml"), []byte("old\n"), 0644); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	code := cmd.ExecuteWithIO(&bytes.Buffer{}, &stdout, &stderr, []string{"setup", "template"})
	if code == 0 {
		t.Fatal("expected non-zero exit")
	}
	if !strings.Contains(stderr.String(), "already exists") {
		t.Errorf("expected 'already exists' in stderr, got: %q", stderr.String())
	}
}

func TestSetupTemplate_ForceOverwrites(t *testing.T) {
	cwd := t.TempDir()
	home := t.TempDir()
	t.Setenv("HOME", home)
	origWd, _ := os.Getwd()
	if err := os.Chdir(cwd); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(origWd) })

	if err := os.WriteFile(filepath.Join(cwd, ".workstation.yaml"), []byte("old\n"), 0644); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	code := cmd.ExecuteWithIO(&bytes.Buffer{}, &stdout, &stderr, []string{"setup", "template", "--force"})
	if code != 0 {
		t.Fatalf("exit %d, stderr=%q", code, stderr.String())
	}
	got, _ := os.ReadFile(filepath.Join(cwd, ".workstation.yaml"))
	if strings.Contains(string(got), "old") {
		t.Errorf("file not overwritten: %q", got)
	}
}
```

- [ ] **Step 7.2: Run tests to verify they fail**

Run: `cd cws && go test ./cmd/...`
Expected: build failure — `ExecuteWithIO`, `setup` cmd undefined.

- [ ] **Step 7.3: Add `ExecuteWithIO` helper to `cmd/root.go`**

`cmd/root.go` currently has `Execute()` that writes to `os.Stderr` on error. Add a testable variant at the end of the file (do not modify `Execute`):

```go
// ExecuteWithIO runs the root command with injectable IO and argv, returning
// an exit code. Used by tests; production still calls Execute().
//
// Flag values are reset to their declared defaults across the entire command
// tree before Execute, so repeated invocations (as in tests) can't leak flag
// state from a previous run.
func ExecuteWithIO(stdin io.Reader, stdout, stderr io.Writer, args []string) int {
	resetFlagsRecursive(rootCmd)
	rootCmd.SetIn(stdin)
	rootCmd.SetOut(stdout)
	rootCmd.SetErr(stderr)
	rootCmd.SetArgs(args)
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	return 0
}

func resetFlagsRecursive(c *cobra.Command) {
	c.Flags().VisitAll(func(f *pflag.Flag) {
		_ = f.Value.Set(f.DefValue)
		f.Changed = false
	})
	for _, child := range c.Commands() {
		resetFlagsRecursive(child)
	}
}
```

Add imports `"github.com/spf13/cobra"` and `"github.com/spf13/pflag"` to `cmd/root.go` if they aren't already present (cobra already is; pflag is new).

Add `"io"` to the imports if not already present.

- [ ] **Step 7.4: Create `cmd/setup.go`**

```go
package cmd

import (
	"fmt"
	"os"

	"github.com/adamtait/remote-agent/cws/internal/scaffold"
	"github.com/spf13/cobra"
)

// readFlags pulls bool flags from cmd via cmd.Flags().GetBool so values are
// scoped to this invocation — no package-level vars to leak between tests.
func readFlags(cmd *cobra.Command, names ...string) map[string]bool {
	out := map[string]bool{}
	for _, n := range names {
		v, _ := cmd.Flags().GetBool(n)
		out[n] = v
	}
	return out
}

var setupCmd = &cobra.Command{
	Use:   "setup",
	Short: "Scaffold cws config files (.workstation.yaml and ~/.config/cws/config.yaml)",
	Long: `Interactive setup: probes the environment for defaults (gcloud project/region,
git user, git origin) and prompts for each required field. Writes both
project- and user-level config files unless --project-only or --global-only
is set.`,
	// Override root's PersistentPreRunE — setup must work when config files
	// don't exist yet.
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error { return nil },
	RunE: func(cmd *cobra.Command, args []string) error {
		home, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("determining home directory: %w", err)
		}
		cwd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("determining working directory: %w", err)
		}
		f := readFlags(cmd, "force", "project-only", "global-only", "advanced")
		return scaffold.InteractiveScaffold(scaffold.InteractiveOptions{
			Stdin: cmd.InOrStdin(), Stdout: cmd.OutOrStdout(), Stderr: cmd.ErrOrStderr(),
			Cwd: cwd, Home: home, Prober: scaffold.RealProber{},
			Force:       f["force"],
			ProjectOnly: f["project-only"],
			GlobalOnly:  f["global-only"],
			Advanced:    f["advanced"],
		})
	},
}

var setupTemplateCmd = &cobra.Command{
	Use:   "template",
	Short: "Write commented YAML templates without prompting",
	RunE: func(cmd *cobra.Command, args []string) error {
		home, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("determining home directory: %w", err)
		}
		cwd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("determining working directory: %w", err)
		}
		f := readFlags(cmd, "force", "project-only", "global-only")
		return scaffold.TemplateScaffold(scaffold.TemplateOptions{
			Stdout: cmd.OutOrStdout(),
			Cwd:    cwd, Home: home, Prober: scaffold.RealProber{},
			Force:       f["force"],
			ProjectOnly: f["project-only"],
			GlobalOnly:  f["global-only"],
		})
	},
}

func init() {
	setupCmd.Flags().Bool("force", false, "overwrite existing config files")
	setupCmd.Flags().Bool("project-only", false, "scaffold only .workstation.yaml")
	setupCmd.Flags().Bool("global-only", false, "scaffold only ~/.config/cws/config.yaml")
	setupCmd.Flags().Bool("advanced", false, "also prompt for cluster/config/machine/disk fields")

	setupTemplateCmd.Flags().Bool("force", false, "overwrite existing config files")
	setupTemplateCmd.Flags().Bool("project-only", false, "scaffold only .workstation.yaml")
	setupTemplateCmd.Flags().Bool("global-only", false, "scaffold only ~/.config/cws/config.yaml")

	setupCmd.AddCommand(setupTemplateCmd)
	rootCmd.AddCommand(setupCmd)
}
```

Note: `setupTemplateCmd` inherits `PersistentPreRunE` from `setupCmd` (the no-op), so config loading is skipped for it too.

Note on flag state: flags are read via `cmd.Flags().GetBool()` inside `RunE`, not from package-level vars. Cobra resets flag values back to their declared defaults on each `Execute()` call, so tests that re-invoke `ExecuteWithIO` see clean state even when they share the rootCmd instance.

- [ ] **Step 7.5: Run tests to verify they pass**

Run: `cd cws && go test ./...`
Expected: all tests pass, including the 3 new setup cmd tests.

- [ ] **Step 7.6: Commit**

```bash
git add cws/cmd/setup.go cws/cmd/setup_test.go cws/cmd/root.go
git commit -m "feat(cws): add 'setup' and 'setup template' commands"
```

---

## Task 8: End-to-end verification

**Files:** none created; this is a manual + automated smoke test.

- [ ] **Step 8.1: Run the full test suite**

Run: `cd cws && go test ./...`
Expected: every package passes.

- [ ] **Step 8.2: Build the binary**

Run: `cd cws && make build`
Expected: `cws/cws` produced, no errors.

- [ ] **Step 8.3: Smoke test `setup template` in a throwaway dir**

```bash
tmpdir=$(mktemp -d)
HOME=$tmpdir ./cws/cws setup template --project-only
cat $tmpdir/.workstation.yaml 2>/dev/null  # should not exist (we chdir'd elsewhere, but --project-only points to cwd)
```

Actually, run from the tmpdir:
```bash
tmpdir=$(mktemp -d)
(cd $tmpdir && HOME=$tmpdir /path/to/cws setup template)
ls -la $tmpdir/.workstation.yaml $tmpdir/.config/cws/config.yaml
cat $tmpdir/.workstation.yaml
```
Expected: both files exist, commented YAML with probed values from the host's `gcloud`/`git` if present.

- [ ] **Step 8.4: Smoke test refuse-if-exists**

```bash
(cd $tmpdir && HOME=$tmpdir /path/to/cws setup template)
```
Expected: exit non-zero, stderr contains "already exists".

- [ ] **Step 8.5: Smoke test --force**

```bash
(cd $tmpdir && HOME=$tmpdir /path/to/cws setup template --force)
```
Expected: exit 0, files overwritten.

- [ ] **Step 8.6: No commit — this task is verification only**

If any smoke test fails, go back to the relevant task and fix.

---

## Self-review notes (written by plan author)

**Spec coverage:**
- Command structure (`setup` + `setup template`) → Task 7
- `--force`, `--project-only`, `--global-only`, `--advanced` flags → Task 7 (flag registration) + Task 6 (option plumbing) + Task 6 (mutual-exclusion check)
- Prereq probe (gcloud/docker/auth, warn-only) → Task 6 (`warnPrereqs`) + Task 2 (`RealProber.HasGcloud/HasDocker/HasGcloudAuth`)
- Billing check after `project_id` known → Task 6 (post-project_id warn in `InteractiveScaffold`)
- Probe for defaults (project, region, git identity, origin URL) → Task 2 (RealProber)
- Required prompts with validation (project_id regex, github_repo_url scheme) → Task 3 + Task 6
- Advanced fields behind `--advanced` → Task 6
- Global defaults prompts (region, git name, git email) → Task 6
- Confirm + summary before write → Task 6 (`indent` + `Proceed?` prompt)
- Atomic writes via temp file + rename → Task 1
- Refuse-if-exists unless `--force` → Task 1 (`WriteAtomic`)
- `PersistentPreRunE` short-circuit → Task 7 (`setupCmd.PersistentPreRunE = no-op`)
- Template mode with probed values in comments → Task 5 + Task 6 (`TemplateScaffold`)
- "Run 'cws create' ..." next-steps hint → Task 6

**Placeholder scan:** no TBDs, every code step contains the code. ✓

**Type consistency:**
- `ProbedValues` defined in Task 5, used in Tasks 5+6. ✓
- `StaticProber` fields use `_` suffix to avoid method-name collision — Task 2 step 3 calls this out and corrects the test from step 1. ✓
- `InteractiveOptions` / `TemplateOptions` defined in Task 6, used in Task 7. ✓
- `projectConfigYAML.DiskSizeGB` is a `string` (not `int`) so the empty-string sentinel "don't emit" works; tests don't rely on it being numeric. ✓
- `ExecuteWithIO` signature added in Task 7 matches the call sites in `cmd/setup_test.go`. ✓
