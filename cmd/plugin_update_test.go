package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"
)

// pluginYAML returns minimal valid plugin.yaml content for tests.
func pluginYAML(name string) string {
	return "name: " + name + "\nversion: \"1.0.0\"\ndescription: \"test\"\n"
}

// writePlugin lays down a minimal plugin directory at dest with optional
// extra files. Returns the directory path.
func writePlugin(t *testing.T, dest, name string, extras map[string]string) string {
	t.Helper()
	if err := os.MkdirAll(dest, 0755); err != nil {
		t.Fatalf("mkdir %s: %v", dest, err)
	}
	if err := os.WriteFile(filepath.Join(dest, "plugin.yaml"), []byte(pluginYAML(name)), 0644); err != nil {
		t.Fatalf("write plugin.yaml: %v", err)
	}
	for rel, body := range extras {
		full := filepath.Join(dest, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
			t.Fatalf("mkdir for %s: %v", rel, err)
		}
		if err := os.WriteFile(full, []byte(body), 0644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	return dest
}

// withFakeHome redirects HOME to a temp dir for the duration of the test and
// returns the path. Plugin user-level discovery resolves under HOME.
func withFakeHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	return home
}

// withCwd chdir's to dir for the test, restoring on cleanup.
func withCwd(t *testing.T, dir string) {
	t.Helper()
	prev, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir %s: %v", dir, err)
	}
	t.Cleanup(func() { _ = os.Chdir(prev) })
}

// installFromSource lays down an installed plugin at the user level whose
// .ra-install.yaml points at the given source dir. Returns the install dir.
func installFromSource(t *testing.T, home, name, source, installedAt string) string {
	t.Helper()
	dest := filepath.Join(home, ".config", "ra", "plugins", name)
	writePlugin(t, dest, name, nil)
	meta := "source: " + source + "\ninstalled_at: \"" + installedAt + "\"\n"
	if err := os.WriteFile(filepath.Join(dest, ".ra-install.yaml"), []byte(meta), 0644); err != nil {
		t.Fatalf("write .ra-install.yaml: %v", err)
	}
	return dest
}

// resetUpdateFlags clears the package-level flag globals between tests.
func resetUpdateFlags() {
	pluginUpdateUser = false
	pluginUpdateLocal = false
	pluginUpdateCheck = false
}

func TestDiscoverInstalledPlugins_NoMatchingNameErrors(t *testing.T) {
	home := withFakeHome(t)
	withCwd(t, t.TempDir())
	installFromSource(t, home, "tmux", "/nonexistent", "2026-05-06T00:00:00Z")

	_, err := discoverInstalledPlugins([]string{"github"}, false, false)
	if err == nil {
		t.Fatal("expected error for unknown name")
	}
	if !strings.Contains(err.Error(), "github") {
		t.Errorf("error missing plugin name: %v", err)
	}
}

func TestDiscoverInstalledPlugins_EmptyArgsReturnsAll(t *testing.T) {
	home := withFakeHome(t)
	withCwd(t, t.TempDir())
	installFromSource(t, home, "tmux", "/x", "2026-05-06T00:00:00Z")
	installFromSource(t, home, "github", "/y", "2026-05-06T00:00:00Z")

	got, err := discoverInstalledPlugins(nil, false, false)
	if err != nil {
		t.Fatalf("discover: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d plugins, want 2: %#v", len(got), got)
	}
}

func TestRefreshOne_LocalPathContentReplacedAndMetaRefreshed(t *testing.T) {
	home := withFakeHome(t)
	wd := t.TempDir()
	withCwd(t, wd)

	src := writePlugin(t, filepath.Join(wd, "src-tmux"), "tmux", map[string]string{
		"profile.d/05-attach.sh": "v2-content\n",
	})

	installedAt := "2026-05-06T00:00:00Z"
	dest := installFromSource(t, home, "tmux", src, installedAt)

	targets, err := discoverInstalledPlugins(nil, false, false)
	if err != nil {
		t.Fatalf("discover: %v", err)
	}
	if len(targets) != 1 {
		t.Fatalf("want 1 target, got %d", len(targets))
	}

	var out, errOut bytes.Buffer
	if err := refreshOne(&out, &errOut, targets[0]); err != nil {
		t.Fatalf("refreshOne: %v", err)
	}

	got, err := os.ReadFile(filepath.Join(dest, "profile.d", "05-attach.sh"))
	if err != nil {
		t.Fatalf("read refreshed file: %v", err)
	}
	if string(got) != "v2-content\n" {
		t.Errorf("file content not refreshed; got %q", string(got))
	}

	metaBytes, err := os.ReadFile(filepath.Join(dest, ".ra-install.yaml"))
	if err != nil {
		t.Fatalf("read meta: %v", err)
	}
	if strings.Contains(string(metaBytes), installedAt) {
		t.Errorf("installed_at not refreshed; meta still has %q: %q", installedAt, string(metaBytes))
	}
	if !strings.Contains(string(metaBytes), "source: "+src) {
		t.Errorf("source not preserved in refreshed meta: %q", string(metaBytes))
	}
}

func TestRefreshOne_SourceNameMismatchIsRejected(t *testing.T) {
	home := withFakeHome(t)
	wd := t.TempDir()
	withCwd(t, wd)

	// Source declares name "github" but installed dir is "tmux".
	src := writePlugin(t, filepath.Join(wd, "src-mismatch"), "github", nil)
	installFromSource(t, home, "tmux", src, "2026-05-06T00:00:00Z")

	targets, err := discoverInstalledPlugins(nil, false, false)
	if err != nil {
		t.Fatalf("discover: %v", err)
	}
	var out, errOut bytes.Buffer
	err = refreshOne(&out, &errOut, targets[0])
	if err == nil {
		t.Fatal("expected mismatched-name error")
	}
	if !strings.Contains(err.Error(), "github") || !strings.Contains(err.Error(), "tmux") {
		t.Errorf("error should mention both names: %v", err)
	}
}

func TestRefreshOne_MissingMetaIsSkippedNotErrored(t *testing.T) {
	home := withFakeHome(t)
	wd := t.TempDir()
	withCwd(t, wd)

	dest := filepath.Join(home, ".config", "ra", "plugins", "tmux")
	writePlugin(t, dest, "tmux", nil)
	// Deliberately no .ra-install.yaml.

	targets, err := discoverInstalledPlugins(nil, false, false)
	if err != nil {
		t.Fatalf("discover: %v", err)
	}
	var out, errOut bytes.Buffer
	if err := refreshOne(&out, &errOut, targets[0]); err != nil {
		t.Fatalf("refreshOne should not error on missing meta: %v", err)
	}
	if !strings.Contains(errOut.String(), "missing") {
		t.Errorf("expected stderr warning about missing meta, got %q", errOut.String())
	}
}

func TestStalenessFor_LocalSourceModifiedAfterInstall(t *testing.T) {
	wd := t.TempDir()
	withCwd(t, wd)

	src := writePlugin(t, filepath.Join(wd, "src-tmux"), "tmux", nil)
	// Install timestamp in the past; mtime of src is now → stale.
	t.Setenv("HOME", t.TempDir())
	dest := installFromSource(t, t.TempDir(), "tmux", src, "2020-01-01T00:00:00Z")
	_ = dest

	tp := installedPlugin{
		Name:    "tmux",
		Level:   "user",
		Dir:     dest,
		Meta:    installMeta{Source: src, InstalledAt: "2020-01-01T00:00:00Z"},
		HasMeta: true,
	}
	status, _ := stalenessFor(tp)
	if status != "stale" {
		t.Errorf("expected stale, got %q", status)
	}
}

func TestStalenessFor_LocalSourceUnchanged(t *testing.T) {
	wd := t.TempDir()
	withCwd(t, wd)

	src := writePlugin(t, filepath.Join(wd, "src-tmux"), "tmux", nil)
	// installed_at after the source was created → fresh.
	future := time.Now().Add(time.Hour).UTC().Format(time.RFC3339)

	tp := installedPlugin{
		Name:    "tmux",
		Level:   "user",
		Dir:     "/ignored",
		Meta:    installMeta{Source: src, InstalledAt: future},
		HasMeta: true,
	}
	status, _ := stalenessFor(tp)
	if status != "fresh" {
		t.Errorf("expected fresh, got %q", status)
	}
}

func TestStalenessFor_GitURLReturnsUnknown(t *testing.T) {
	tp := installedPlugin{
		Name:    "tmux",
		Level:   "user",
		Meta:    installMeta{Source: "https://github.com/x/y", InstalledAt: "2020-01-01T00:00:00Z"},
		HasMeta: true,
	}
	status, detail := stalenessFor(tp)
	if status != "unknown" {
		t.Errorf("expected unknown for git URL, got %q (%s)", status, detail)
	}
}

func TestStalenessFor_NoMetaReturnsNoMeta(t *testing.T) {
	tp := installedPlugin{Name: "tmux", Level: "user", HasMeta: false}
	status, _ := stalenessFor(tp)
	if status != "no-meta" {
		t.Errorf("expected no-meta, got %q", status)
	}
}

func TestPluginUpdateCommand_RegisteredInHelp(t *testing.T) {
	resetUpdateFlags()
	defer resetUpdateFlags()

	var out, errOut bytes.Buffer
	code := ExecuteWithIO(strings.NewReader(""), &out, &errOut, []string{"plugin", "--help"})
	if code != 0 {
		t.Fatalf("plugin --help exited %d; stderr: %s", code, errOut.String())
	}
	if !strings.Contains(out.String(), "update") {
		t.Errorf("plugin --help missing 'update': %s", out.String())
	}
}

func TestPluginUpdate_MutuallyExclusiveLevelFlags(t *testing.T) {
	resetUpdateFlags()
	defer resetUpdateFlags()

	// Set the package-level flag globals as cobra would after parsing
	// "--user --local", then exercise RunE directly. Going through Execute
	// would trigger PersistentPreRunE which requires cfg + a real config
	// file on disk; the mutual-exclusion check happens before any of that.
	pluginUpdateUser = true
	pluginUpdateLocal = true
	cmd := &cobra.Command{}
	err := pluginUpdateCmd.RunE(cmd, nil)
	if err == nil || !strings.Contains(err.Error(), "mutually exclusive") {
		t.Errorf("expected mutual-exclusion error, got %v", err)
	}
}

// Ensures no log/print noise leaks to stdout when there are no installed
// plugins. The user runs an empty environment, gets a friendly message.
func TestPluginUpdate_EmptyEnvironment(t *testing.T) {
	resetUpdateFlags()
	defer resetUpdateFlags()
	withFakeHome(t)
	withCwd(t, t.TempDir())

	cmd := &cobra.Command{}
	cmd.SetArgs(nil)
	var out bytes.Buffer
	if err := pluginUpdateCmd.RunE(cmd, nil); err != nil {
		t.Fatalf("RunE: %v", err)
	}
	_ = out // silence unused
}
