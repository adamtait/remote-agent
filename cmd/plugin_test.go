package cmd

import (
	"bytes"
	"strings"
	"testing"
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
