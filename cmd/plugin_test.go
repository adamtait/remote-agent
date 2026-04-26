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
