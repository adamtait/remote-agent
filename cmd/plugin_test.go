package cmd

import (
	"bufio"
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/adamtait/remote-agent/internal/plugins"
	"gopkg.in/yaml.v3"
)

func TestPluginListCommand_Registered(t *testing.T) {
	var out, errOut bytes.Buffer
	code := ExecuteWithIO(strings.NewReader(""), &out, &errOut, []string{"plugin", "--help"})
	// --help always exits 0 in cobra
	if code != 0 {
		t.Errorf("plugin --help exited %d; stderr: %s", code, errOut.String())
	}
	help := out.String()
	if !strings.Contains(help, "install") {
		t.Errorf("plugin --help missing 'install': %s", help)
	}
	if !strings.Contains(help, "list") {
		t.Errorf("plugin --help missing 'list': %s", help)
	}
	if !strings.Contains(help, "remove") {
		t.Errorf("plugin --help missing 'remove': %s", help)
	}
}

func TestPluginInstall_AcceptsOneOrTwoArgs(t *testing.T) {
	cases := []struct {
		args    []string
		wantErr bool
	}{
		{[]string{}, true},
		{[]string{"./my-plugin"}, false},
		{[]string{"https://x/repo", "github"}, false},
		{[]string{"a", "b", "c"}, true},
	}
	for _, tc := range cases {
		err := pluginInstallCmd.Args(pluginInstallCmd, tc.args)
		if (err != nil) != tc.wantErr {
			t.Errorf("Args(%v) err=%v, wantErr=%v", tc.args, err, tc.wantErr)
		}
	}
}

func TestIsLocalPath(t *testing.T) {
	cases := []struct {
		source string
		want   bool
	}{
		// Absolute paths
		{"/home/user/plugin", true},
		// Relative paths
		{"./my-plugin", true},
		{"../other-plugin", true},
		// Remote URLs
		{"https://github.com/org/repo", false},
		{"http://github.com/org/repo", false},
		// SCP-style git remotes
		{"git@github.com:org/repo", false},
		{"ssh://git@github.com/org/repo", false},
		// Bare non-existing name → remote
		{"some-random-name-that-does-not-exist", false},
	}
	for _, tc := range cases {
		got := isLocalPath(tc.source)
		if got != tc.want {
			t.Errorf("isLocalPath(%q) = %v, want %v", tc.source, got, tc.want)
		}
	}
}

func TestResolvePluginSource_LocalPluginDir(t *testing.T) {
	tmp := t.TempDir()
	oldWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(oldWD); err != nil {
			t.Fatalf("restore cwd: %v", err)
		}
	})
	if err := os.MkdirAll(filepath.Join(tmp, "plugins", "custom"), 0755); err != nil {
		t.Fatalf("mkdir plugin: %v", err)
	}
	if err := os.Chdir(tmp); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	got := resolvePluginSource("custom")
	if got != filepath.Join("plugins", "custom") {
		t.Errorf("resolvePluginSource(custom) = %q, want %q", got, filepath.Join("plugins", "custom"))
	}
}

func TestResolvePluginSource_UnknownBareNameUnchanged(t *testing.T) {
	tmp := t.TempDir()
	oldWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(oldWD); err != nil {
			t.Fatalf("restore cwd: %v", err)
		}
	})
	if err := os.Chdir(tmp); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	got := resolvePluginSource("custom")
	if got != "custom" {
		t.Errorf("resolvePluginSource(custom) = %q, want custom", got)
	}
}

func TestCopyDir_SkipsGitMetadata(t *testing.T) {
	src := t.TempDir()
	dst := t.TempDir()
	// A plugin file plus a .git dir that must not be copied.
	if err := os.WriteFile(filepath.Join(src, "plugin.yaml"), []byte("name: x\n"), 0644); err != nil {
		t.Fatalf("write plugin.yaml: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(src, ".git", "objects"), 0755); err != nil {
		t.Fatalf("mkdir .git: %v", err)
	}
	if err := os.WriteFile(filepath.Join(src, ".git", "config"), []byte("[core]\n"), 0644); err != nil {
		t.Fatalf("write .git/config: %v", err)
	}

	if err := copyDir(src, dst); err != nil {
		t.Fatalf("copyDir: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dst, "plugin.yaml")); err != nil {
		t.Errorf("plugin.yaml not copied: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dst, ".git")); !os.IsNotExist(err) {
		t.Errorf(".git should not be copied; stat err = %v", err)
	}
}

// ── resolvePluginDir tests ─────────────────────────────────────────────────────

func TestResolvePluginDir_EmptySubpathReturnsRoot(t *testing.T) {
	root := t.TempDir()
	got, err := resolvePluginDir(root, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != root {
		t.Errorf("got %q, want root %q", got, root)
	}
}

func TestResolvePluginDir_Subpath(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "github")
	if err := os.MkdirAll(sub, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	got, err := resolvePluginDir(root, "github")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != sub {
		t.Errorf("got %q, want %q", got, sub)
	}
}

func TestResolvePluginDir_NestedSubpath(t *testing.T) {
	root := t.TempDir()
	sub := filepath.Join(root, "tools", "claude")
	if err := os.MkdirAll(sub, 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	got, err := resolvePluginDir(root, "tools/claude")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != sub {
		t.Errorf("got %q, want %q", got, sub)
	}
}

func TestResolvePluginDir_EscapeRejected(t *testing.T) {
	root := t.TempDir()
	// Sibling dir outside root must not be reachable, even though it exists.
	if err := os.MkdirAll(filepath.Join(filepath.Dir(root), "evil"), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	for _, bad := range []string{"../evil", "/etc", "github/../../evil"} {
		if _, err := resolvePluginDir(root, bad); err == nil {
			t.Errorf("resolvePluginDir(root, %q) = nil error, want escape/not-found error", bad)
		}
	}
}

func TestResolvePluginDir_MissingSubpath(t *testing.T) {
	root := t.TempDir()
	if _, err := resolvePluginDir(root, "does-not-exist"); err == nil {
		t.Fatal("expected error for missing subpath, got nil")
	}
}

// ── installMeta tests ──────────────────────────────────────────────────────────

func TestInstallMeta_DisplaySource(t *testing.T) {
	cases := []struct {
		name string
		meta installMeta
		want string
	}{
		{"repo only", installMeta{Repo: "https://x/repo"}, "https://x/repo"},
		{"repo+path", installMeta{Repo: "https://x/repo", Path: "github"}, "https://x/repo (github)"},
		{"repo+path+ref", installMeta{Repo: "https://x/repo", Path: "github", Ref: "v1"}, "https://x/repo (github) @v1"},
		{"repo+ref", installMeta{Repo: "https://x/repo", Ref: "main"}, "https://x/repo @main"},
		{"legacy source", installMeta{Source: "https://x/old"}, "https://x/old"},
		{"empty", installMeta{}, ""},
	}
	for _, tc := range cases {
		if got := tc.meta.displaySource(); got != tc.want {
			t.Errorf("%s: displaySource() = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestInstallMeta_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	want := installMeta{
		Repo:        "https://github.com/owner/ra-plugins",
		Path:        "github",
		Ref:         "v1.2.0",
		InstalledAt: "2026-06-01T00:00:00Z",
	}
	data, err := yaml.Marshal(want)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".ra-install.yaml"), data, 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
	got := readInstallMeta(dir)
	if got.Repo != want.Repo || got.Path != want.Path || got.Ref != want.Ref || got.InstalledAt != want.InstalledAt {
		t.Errorf("round-trip mismatch: got %+v, want %+v", got, want)
	}
	// Legacy "source" field should not be emitted for new installs.
	if strings.Contains(string(data), "source:") {
		t.Errorf("new install meta should not write a 'source' field; yaml:\n%s", data)
	}
}

func TestReadInstallMeta_LegacySourceOnly(t *testing.T) {
	dir := t.TempDir()
	legacy := "source: https://github.com/owner/old-plugin\ninstalled_at: 2026-01-01T00:00:00Z\n"
	if err := os.WriteFile(filepath.Join(dir, ".ra-install.yaml"), []byte(legacy), 0644); err != nil {
		t.Fatalf("write: %v", err)
	}
	got := readInstallMeta(dir)
	if got.Source != "https://github.com/owner/old-plugin" {
		t.Errorf("legacy Source not parsed: got %+v", got)
	}
	if got.displaySource() != "https://github.com/owner/old-plugin" {
		t.Errorf("legacy displaySource() = %q", got.displaySource())
	}
}

func TestPreparePluginDestination_ExistingRequiresForce(t *testing.T) {
	tmp := t.TempDir()
	dest := filepath.Join(tmp, "codex")
	if err := os.MkdirAll(dest, 0755); err != nil {
		t.Fatalf("mkdir dest: %v", err)
	}
	marker := filepath.Join(dest, "marker")
	if err := os.WriteFile(marker, []byte("keep"), 0644); err != nil {
		t.Fatalf("write marker: %v", err)
	}

	_, err := preparePluginDestination(tmp, "codex", false)
	if err == nil {
		t.Fatal("expected existing plugin to require --force")
	}
	if _, statErr := os.Stat(marker); statErr != nil {
		t.Fatalf("existing plugin was modified without --force: %v", statErr)
	}
}

func TestPreparePluginDestination_ForceRemovesExisting(t *testing.T) {
	tmp := t.TempDir()
	dest := filepath.Join(tmp, "codex")
	if err := os.MkdirAll(dest, 0755); err != nil {
		t.Fatalf("mkdir dest: %v", err)
	}

	got, err := preparePluginDestination(tmp, "codex", true)
	if err != nil {
		t.Fatalf("preparePluginDestination: %v", err)
	}
	if got != dest {
		t.Errorf("dest = %q, want %q", got, dest)
	}
	if _, statErr := os.Stat(dest); !os.IsNotExist(statErr) {
		t.Fatalf("forced prepare should remove destination before copy; stat err = %v", statErr)
	}
}

func TestChooseInstallLevel_UserFlag(t *testing.T) {
	cmd := pluginInstallCmd
	level, err := chooseInstallLevel(cmd, true, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if level != "user" {
		t.Errorf("got %q, want user", level)
	}
}

func TestChooseInstallLevel_LocalFlag(t *testing.T) {
	cmd := pluginInstallCmd
	level, err := chooseInstallLevel(cmd, false, true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if level != "project" {
		t.Errorf("got %q, want project", level)
	}
}

func TestChooseInstallLevel_MutuallyExclusive(t *testing.T) {
	cmd := pluginInstallCmd
	_, err := chooseInstallLevel(cmd, true, true)
	if err == nil {
		t.Fatal("expected error for --user --local, got nil")
	}
}

func TestChooseInstallLevel_Interactive_Default(t *testing.T) {
	// Empty input → user (default)
	pluginInstallCmd.SetIn(strings.NewReader("\n"))
	defer pluginInstallCmd.SetIn(nil)

	var out bytes.Buffer
	pluginInstallCmd.SetOut(&out)
	defer pluginInstallCmd.SetOut(nil)

	level, err := chooseInstallLevel(pluginInstallCmd, false, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if level != "user" {
		t.Errorf("got %q, want user", level)
	}
}

func TestChooseInstallLevel_Interactive_Project(t *testing.T) {
	pluginInstallCmd.SetIn(strings.NewReader("2\n"))
	defer pluginInstallCmd.SetIn(nil)

	var out bytes.Buffer
	pluginInstallCmd.SetOut(&out)
	defer pluginInstallCmd.SetOut(nil)

	level, err := chooseInstallLevel(pluginInstallCmd, false, false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if level != "project" {
		t.Errorf("got %q, want project", level)
	}
}

// ── resolveSecretNames tests ──────────────────────────────────────────────────

func makePluginWithSecret(t *testing.T, name, secretName, defaultSecretName string) *plugins.FilePlugin {
	t.Helper()
	yml := `name: ` + name + `
auth_providers:
  - name: pat
    description: "PAT"
    secrets:
      - name: ` + secretName + `
        env_var: GH_TOKEN
        description: "token"
        default_secret_name: ` + defaultSecretName
	fsys := fstest.MapFS{"plugin.yaml": &fstest.MapFile{Data: []byte(yml)}}
	p, err := plugins.LoadPlugin(fsys)
	if err != nil {
		t.Fatalf("LoadPlugin: %v", err)
	}
	return p
}

func makePromptResult(p *plugins.FilePlugin) *plugins.PromptResult {
	return &plugins.PromptResult{
		Config:       map[string]any{"enabled": true, "auth_provider": "pat"},
		AuthProvider: &plugins.AuthProvider{Name: "pat", Secrets: p.Schema().AuthProviders[0].Secrets},
		SecretValues: map[string]string{"github_pat": "tok"},
	}
}

func TestResolveSecretNames_UserLevel_UsesUsername(t *testing.T) {
	p := makePluginWithSecret(t, "github", "github_pat", "github-pat")
	res := makePromptResult(p)

	var out bytes.Buffer
	r := bufio.NewReader(strings.NewReader("\n")) // accept default
	names := resolveSecretNames(r, &out, p, res, "user", "/home/alice/workspace")

	got := names["github_pat"]
	if !strings.HasPrefix(got, "github-pat-") {
		t.Errorf("user-level name should start with github-pat-, got %q", got)
	}
	// Prompt should show the proposed name.
	if !strings.Contains(out.String(), got) {
		t.Errorf("prompt missing proposed name %q; output: %q", got, out.String())
	}
}

func TestResolveSecretNames_ProjectLevel_UsesProjectDir(t *testing.T) {
	p := makePluginWithSecret(t, "github", "github_pat", "github-pat")
	res := makePromptResult(p)

	var out bytes.Buffer
	r := bufio.NewReader(strings.NewReader("\n")) // accept default
	names := resolveSecretNames(r, &out, p, res, "project", "/home/alice/myproject")

	if got := names["github_pat"]; got != "github-pat-myproject" {
		t.Errorf("project-level: got %q, want github-pat-myproject", got)
	}
}

func TestResolveSecretNames_CustomNameOverridesDefault(t *testing.T) {
	p := makePluginWithSecret(t, "github", "github_pat", "github-pat")
	res := makePromptResult(p)

	var out bytes.Buffer
	r := bufio.NewReader(strings.NewReader("my-custom-pat\n"))
	names := resolveSecretNames(r, &out, p, res, "project", "/home/alice/myproject")

	if got := names["github_pat"]; got != "my-custom-pat" {
		t.Errorf("custom name: got %q, want my-custom-pat", got)
	}
}
