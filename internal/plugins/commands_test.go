package plugins

import (
	"testing"
	"testing/fstest"
)

func loadPluginYAML(t *testing.T, body string) (*FilePlugin, error) {
	t.Helper()
	return LoadPlugin(fstest.MapFS{"plugin.yaml": &fstest.MapFile{Data: []byte(body)}})
}

func TestLoadPlugin_Commands_Valid(t *testing.T) {
	body := `name: et
commands:
  - name: connect
    description: "Connect via Eternal Terminal"
    run: commands/connect.sh
    needs_workstation: true
post_install_message: "hello"
`
	p, err := loadPluginYAML(t, body)
	if err != nil {
		t.Fatalf("LoadPlugin: %v", err)
	}
	cs := p.Schema().Commands
	if len(cs) != 1 || cs[0].Name != "connect" || cs[0].Run != "commands/connect.sh" || !cs[0].NeedsWorkstation {
		t.Fatalf("unexpected commands: %+v", cs)
	}
	if p.Schema().PostInstallMessage != "hello" {
		t.Errorf("post_install_message = %q", p.Schema().PostInstallMessage)
	}
}

func TestLoadPlugin_Commands_Invalid(t *testing.T) {
	cases := map[string]string{
		"missing name": `name: et
commands:
  - run: x.sh
`,
		"missing run": `name: et
commands:
  - name: connect
`,
		"duplicate name": `name: et
commands:
  - name: connect
    run: a.sh
  - name: connect
    run: b.sh
`,
	}
	for label, body := range cases {
		if _, err := loadPluginYAML(t, body); err == nil {
			t.Errorf("%s: expected error, got nil", label)
		}
	}
}
