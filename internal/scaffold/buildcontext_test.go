package scaffold

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/adamtait/remote-agent/internal/plugins"
)

func TestAssembleBuildContext_CreatesBuildDir(t *testing.T) {
	cwd := t.TempDir()
	home := t.TempDir()

	path, cleanup, err := AssembleBuildContext(BuildContextOptions{
		Cwd:  cwd,
		Home: home,
	})
	if err != nil {
		t.Fatalf("AssembleBuildContext: %v", err)
	}
	defer cleanup()

	want := filepath.Join(cwd, ".ra", ".build")
	if path != want {
		t.Fatalf("path = %q, want %q", path, want)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("expected build dir to exist: %v", err)
	}
}

func TestAssembleBuildContext_DockerfileFromEmbedded(t *testing.T) {
	cwd := t.TempDir()
	home := t.TempDir()

	path, cleanup, err := AssembleBuildContext(BuildContextOptions{Cwd: cwd, Home: home})
	if err != nil {
		t.Fatalf("AssembleBuildContext: %v", err)
	}
	defer cleanup()

	got, err := os.ReadFile(filepath.Join(path, "Dockerfile"))
	if err != nil {
		t.Fatalf("reading Dockerfile: %v", err)
	}
	if len(got) == 0 {
		t.Fatal("Dockerfile is empty; expected embedded default bytes")
	}
}

func TestAssembleBuildContext_DockerfileFromUser(t *testing.T) {
	cwd := t.TempDir()
	home := t.TempDir()
	userPath := filepath.Join(home, ".config", "ra", "Dockerfile")
	if err := os.MkdirAll(filepath.Dir(userPath), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(userPath, []byte("USER-DOCKERFILE\n"), 0644); err != nil {
		t.Fatal(err)
	}

	path, cleanup, err := AssembleBuildContext(BuildContextOptions{Cwd: cwd, Home: home})
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()

	got, err := os.ReadFile(filepath.Join(path, "Dockerfile"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "USER-DOCKERFILE\n" {
		t.Fatalf("Dockerfile = %q, want USER-DOCKERFILE", got)
	}
}

func TestAssembleBuildContext_DockerfileFromProject(t *testing.T) {
	cwd := t.TempDir()
	home := t.TempDir()
	userPath := filepath.Join(home, ".config", "ra", "Dockerfile")
	projPath := filepath.Join(cwd, ".ra", "Dockerfile")
	for _, p := range []string{userPath, projPath} {
		if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(userPath, []byte("USER\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(projPath, []byte("PROJECT\n"), 0644); err != nil {
		t.Fatal(err)
	}

	path, cleanup, err := AssembleBuildContext(BuildContextOptions{Cwd: cwd, Home: home})
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()

	got, err := os.ReadFile(filepath.Join(path, "Dockerfile"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "PROJECT\n" {
		t.Fatalf("Dockerfile = %q, want PROJECT", got)
	}
}

func TestAssembleBuildContext_MergesProfileD(t *testing.T) {
	cwd := t.TempDir()
	home := t.TempDir()

	// User-global has a.sh; project has a.sh (override) and b.sh
	mustWrite(t, filepath.Join(home, ".config", "ra", "profile.d", "a.sh"), "USER-A")
	mustWrite(t, filepath.Join(cwd, ".ra", "profile.d", "a.sh"), "PROJ-A")
	mustWrite(t, filepath.Join(cwd, ".ra", "profile.d", "b.sh"), "PROJ-B")

	path, cleanup, err := AssembleBuildContext(BuildContextOptions{Cwd: cwd, Home: home})
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()

	a, _ := os.ReadFile(filepath.Join(path, "profile.d", "a.sh"))
	b, _ := os.ReadFile(filepath.Join(path, "profile.d", "b.sh"))
	if string(a) != "PROJ-A" {
		t.Errorf("a.sh = %q, want PROJ-A (project shadows user)", a)
	}
	if string(b) != "PROJ-B" {
		t.Errorf("b.sh = %q, want PROJ-B", b)
	}
}

func TestAssembleBuildContext_EmptyProfileDGetsKeep(t *testing.T) {
	cwd := t.TempDir()
	home := t.TempDir()

	path, cleanup, err := AssembleBuildContext(BuildContextOptions{Cwd: cwd, Home: home})
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()

	for _, dir := range []string{"profile.d", "workstation-startup.d", "Dockerfile.d"} {
		if _, err := os.Stat(filepath.Join(path, dir, ".ra-keep")); err != nil {
			t.Errorf("%s/.ra-keep missing: %v", dir, err)
		}
	}
}

func TestAssembleBuildContext_MergesPluginDirs(t *testing.T) {
	cwd := t.TempDir()
	home := t.TempDir()
	pluginDir := filepath.Join(home, ".config", "ra", "plugins", "myplug")

	mustWrite(t, filepath.Join(pluginDir, "plugin.yaml"), "name: myplug\nversion: '0.0.1'\ndescription: x\n")
	mustWrite(t, filepath.Join(pluginDir, "profile.d", "01-myplug.sh"), "PLUG-PROFILE")
	mustWrite(t, filepath.Join(pluginDir, "workstation-startup.d", "300_myplug.sh"), "PLUG-START")
	mustWrite(t, filepath.Join(pluginDir, "Dockerfile.d", "200_myplug.sh"), "PLUG-DOCK")

	plug, err := plugins.LoadPlugin(os.DirFS(pluginDir))
	if err != nil {
		t.Fatal(err)
	}

	path, cleanup, err := AssembleBuildContext(BuildContextOptions{
		Cwd: cwd, Home: home,
		Plugins: []*plugins.FilePlugin{plug},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()

	cases := []struct{ rel, want string }{
		{"profile.d/01-myplug.sh", "PLUG-PROFILE"},
		{"workstation-startup.d/300_myplug.sh", "PLUG-START"},
		{"Dockerfile.d/200_myplug.sh", "PLUG-DOCK"},
	}
	for _, c := range cases {
		got, err := os.ReadFile(filepath.Join(path, c.rel))
		if err != nil {
			t.Errorf("read %s: %v", c.rel, err)
			continue
		}
		if string(got) != c.want {
			t.Errorf("%s = %q, want %q", c.rel, got, c.want)
		}
	}
}

func TestAssembleBuildContext_ProjectShadowsPlugin(t *testing.T) {
	cwd := t.TempDir()
	home := t.TempDir()
	pluginDir := filepath.Join(home, ".config", "ra", "plugins", "myplug")

	mustWrite(t, filepath.Join(pluginDir, "plugin.yaml"), "name: myplug\nversion: '0.0.1'\ndescription: x\n")
	mustWrite(t, filepath.Join(pluginDir, "profile.d", "01-myplug.sh"), "PLUG")
	mustWrite(t, filepath.Join(cwd, ".ra", "profile.d", "01-myplug.sh"), "PROJECT")

	plug, _ := plugins.LoadPlugin(os.DirFS(pluginDir))

	path, cleanup, _ := AssembleBuildContext(BuildContextOptions{
		Cwd: cwd, Home: home, Plugins: []*plugins.FilePlugin{plug},
	})
	defer cleanup()

	got, _ := os.ReadFile(filepath.Join(path, "profile.d", "01-myplug.sh"))
	if string(got) != "PROJECT" {
		t.Errorf("got %q, want PROJECT (project must shadow plugin)", got)
	}
}

func TestAssembleBuildContext_StagesPluginTree(t *testing.T) {
	cwd := t.TempDir()
	home := t.TempDir()
	pluginDir := filepath.Join(home, ".config", "ra", "plugins", "myplug")

	mustWrite(t, filepath.Join(pluginDir, "plugin.yaml"), "name: myplug\nversion: '0.0.1'\ndescription: x\n")
	mustWrite(t, filepath.Join(pluginDir, "workstation-startup.d", "300_myplug.sh"), "X")
	mustWrite(t, filepath.Join(pluginDir, ".ra-install.yaml"), "source: foo\n")

	plug, _ := plugins.LoadPlugin(os.DirFS(pluginDir))

	path, cleanup, err := AssembleBuildContext(BuildContextOptions{
		Cwd: cwd, Home: home, Plugins: []*plugins.FilePlugin{plug},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()

	// plugin.yaml + scripts copied into build/plugins/<name>/
	if _, err := os.Stat(filepath.Join(path, "plugins", "myplug", "plugin.yaml")); err != nil {
		t.Errorf("plugin.yaml not staged: %v", err)
	}
	if _, err := os.Stat(filepath.Join(path, "plugins", "myplug", "workstation-startup.d", "300_myplug.sh")); err != nil {
		t.Errorf("plugin script not staged: %v", err)
	}

	// .ra-install.yaml is install metadata; do NOT copy into image
	if _, err := os.Stat(filepath.Join(path, "plugins", "myplug", ".ra-install.yaml")); !os.IsNotExist(err) {
		t.Errorf(".ra-install.yaml should NOT be staged into the image, but Stat returned: %v", err)
	}
}

// mustWrite is a test helper that writes content with parent-dir mkdir.
func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}
