package cmd

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/adamtait/remote-agent/internal/gcloud"
	"github.com/adamtait/remote-agent/internal/plugins"
	"github.com/spf13/cobra"
)

// writeTestPlugin writes <root>/<name>/plugin.yaml (+ an optional script) and
// returns the plugins loaded from <root> (so Dir() is populated).
func writeTestPlugin(t *testing.T, root, name, manifest, scriptRel, scriptBody string) []*plugins.FilePlugin {
	t.Helper()
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "plugin.yaml"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}
	if scriptRel != "" {
		sp := filepath.Join(dir, scriptRel)
		if err := os.MkdirAll(filepath.Dir(sp), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(sp, []byte(scriptBody), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	ps, err := plugins.LoadAll(root, "")
	if err != nil {
		t.Fatal(err)
	}
	return ps
}

func TestRegisterPluginCommands_BuildsTree(t *testing.T) {
	ps := writeTestPlugin(t, t.TempDir(), "et", `name: et
commands:
  - name: connect
    description: "connect via et"
    run: commands/connect.sh
`, "commands/connect.sh", "#!/bin/sh\n")

	parent := &cobra.Command{Use: "plugin"}
	registerPluginCommands(parent, ps)

	leaf, _, err := parent.Find([]string{"et", "connect"})
	if err != nil || leaf.Name() != "connect" {
		t.Fatalf("expected `plugin et connect`, got name=%q err=%v", leaf.Name(), err)
	}
}

func TestRegisterPluginCommands_SkipsReservedName(t *testing.T) {
	ps := writeTestPlugin(t, t.TempDir(), "install", `name: install
commands:
  - name: x
    run: x.sh
`, "x.sh", "#!/bin/sh\n")

	parent := &cobra.Command{Use: "plugin"}
	parent.AddCommand(&cobra.Command{Use: "install"}) // built-in it would shadow
	registerPluginCommands(parent, ps)

	if leaf, _, _ := parent.Find([]string{"install", "x"}); leaf != nil && leaf.Name() == "x" {
		t.Fatal("a plugin named `install` must be skipped (shadows a built-in subcommand)")
	}
}

// A needs_workstation command resolves the workstation, then execs the script
// with the env contract — RA_LOGIN_COMMAND (from connect.login_command) and
// RA_WORKSTATION reach it.
func TestRunPluginCommand_ExecsScriptWithEnv(t *testing.T) {
	root := t.TempDir()
	outFile := filepath.Join(root, "out.txt")
	ps := writeTestPlugin(t, root, "demo", `name: demo
commands:
  - name: hi
    run: hi.sh
    needs_workstation: true
`, "hi.sh", "#!/bin/sh\nprintf '%s|%s' \"$RA_LOGIN_COMMAND\" \"$RA_WORKSTATION\" > "+outFile+"\n")

	prevCfg, prevClient := cfg, client
	t.Cleanup(func() { cfg, client = prevCfg, prevClient })
	cfg = newConnectCfg() // p-user, RUNNING via runningRunner
	cfg.Connect.LoginCommand = "LC-VALUE"
	client = gcloud.NewClient(runningRunner())

	parent := &cobra.Command{Use: "plugin"}
	registerPluginCommands(parent, ps)
	leaf, _, err := parent.Find([]string{"demo", "hi"})
	if err != nil {
		t.Fatal(err)
	}
	leaf.SetOut(&bytes.Buffer{})
	leaf.SetErr(&bytes.Buffer{})
	if err := leaf.RunE(leaf, nil); err != nil {
		t.Fatalf("RunE: %v", err)
	}
	got, _ := os.ReadFile(outFile)
	if want := "LC-VALUE|p-user"; string(got) != want {
		t.Fatalf("script env: got %q, want %q", got, want)
	}
}

// A command that does NOT need a workstation gets no RA_LOGIN_COMMAND / RA_*
// workstation vars (and never touches gcloud).
func TestRunPluginCommand_NoWorkstation_OmitsLoginCommand(t *testing.T) {
	root := t.TempDir()
	outFile := filepath.Join(root, "out.txt")
	ps := writeTestPlugin(t, root, "demo", `name: demo
commands:
  - name: hi
    run: hi.sh
`, "hi.sh", "#!/bin/sh\nprintf '[%s]' \"${RA_LOGIN_COMMAND-unset}\" > "+outFile+"\n")

	prevCfg := cfg
	t.Cleanup(func() { cfg = prevCfg })
	cfg = newConnectCfg()
	cfg.Connect.LoginCommand = "LC-VALUE"

	parent := &cobra.Command{Use: "plugin"}
	registerPluginCommands(parent, ps)
	leaf, _, err := parent.Find([]string{"demo", "hi"})
	if err != nil {
		t.Fatal(err)
	}
	leaf.SetOut(&bytes.Buffer{})
	leaf.SetErr(&bytes.Buffer{})
	if err := leaf.RunE(leaf, nil); err != nil {
		t.Fatalf("RunE: %v", err)
	}
	got, _ := os.ReadFile(outFile)
	if string(got) != "[unset]" {
		t.Fatalf("RA_LOGIN_COMMAND should be unset for a no-workstation command, got %q", got)
	}
}
