package cmd

import (
	"bytes"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/adamtait/remote-agent/internal/config"
	"github.com/adamtait/remote-agent/internal/gcloud"
	"github.com/adamtait/remote-agent/internal/plugins"
)

func mustLoadPlugin(t *testing.T, yaml string) *plugins.FilePlugin {
	t.Helper()
	fsys := fstest.MapFS{"plugin.yaml": &fstest.MapFile{Data: []byte(yaml)}}
	p, err := plugins.LoadPlugin(fsys)
	if err != nil {
		t.Fatalf("LoadPlugin: %v", err)
	}
	return p
}

func tunnelTestRegistry(t *testing.T) *plugins.Registry {
	t.Helper()
	return plugins.NewRegistry([]*plugins.FilePlugin{
		mustLoadPlugin(t, "name: marimo\nversion: \"1\"\ndescription: d\nport-for-tunnel: 2718\n"),
		mustLoadPlugin(t, "name: github\nversion: \"1\"\ndescription: d\n"),
	})
}

func TestParseTunnelTarget_LocalRemotePair(t *testing.T) {
	r := tunnelTestRegistry(t)
	l, rp, err := parseTunnelTarget("9000:8080", 0, r, nil)
	if err != nil {
		t.Fatalf("parseTunnelTarget: %v", err)
	}
	if l != 9000 || rp != 8080 {
		t.Errorf("got (%d,%d), want (9000,8080)", l, rp)
	}
}

func TestParseTunnelTarget_LocalRemotePair_RejectsOverride(t *testing.T) {
	r := tunnelTestRegistry(t)
	if _, _, err := parseTunnelTarget("9000:8080", 7000, r, nil); err == nil {
		t.Fatal("expected --local-port to conflict with explicit pair")
	}
}

func TestParseTunnelTarget_SinglePort(t *testing.T) {
	r := tunnelTestRegistry(t)
	l, rp, err := parseTunnelTarget("8080", 0, r, nil)
	if err != nil {
		t.Fatalf("parseTunnelTarget: %v", err)
	}
	if l != 8080 || rp != 8080 {
		t.Errorf("got (%d,%d), want (8080,8080)", l, rp)
	}
}

func TestParseTunnelTarget_SinglePort_WithLocalOverride(t *testing.T) {
	r := tunnelTestRegistry(t)
	l, rp, err := parseTunnelTarget("8080", 7000, r, nil)
	if err != nil {
		t.Fatalf("parseTunnelTarget: %v", err)
	}
	if l != 7000 || rp != 8080 {
		t.Errorf("got (%d,%d), want (7000,8080)", l, rp)
	}
}

func TestParseTunnelTarget_NamedPlugin(t *testing.T) {
	r := tunnelTestRegistry(t)
	l, rp, err := parseTunnelTarget("marimo", 0, r, nil)
	if err != nil {
		t.Fatalf("parseTunnelTarget: %v", err)
	}
	if l != 2718 || rp != 2718 {
		t.Errorf("got (%d,%d), want (2718,2718)", l, rp)
	}
}

func TestParseTunnelTarget_NamedPlugin_WithLocalOverride(t *testing.T) {
	r := tunnelTestRegistry(t)
	l, rp, err := parseTunnelTarget("marimo", 8000, r, nil)
	if err != nil {
		t.Fatalf("parseTunnelTarget: %v", err)
	}
	if l != 8000 || rp != 2718 {
		t.Errorf("got (%d,%d), want (8000,2718)", l, rp)
	}
}

func TestParseTunnelTarget_NamedPlugin_ConfigOverridesSchemaDefault(t *testing.T) {
	// When the user customizes plugins.marimo.port-for-tunnel in config.yaml,
	// the tunnel command must read that value — otherwise the tunnel would
	// hit a port the server isn't bound to.
	r := tunnelTestRegistry(t)
	cfg := &config.Config{
		Plugins: map[string]map[string]interface{}{
			"marimo": {"port-for-tunnel": 8000},
		},
	}
	l, rp, err := parseTunnelTarget("marimo", 0, r, cfg)
	if err != nil {
		t.Fatalf("parseTunnelTarget: %v", err)
	}
	if l != 8000 || rp != 8000 {
		t.Errorf("got (%d,%d), want (8000,8000) — user-configured port", l, rp)
	}
}

func TestParseTunnelTarget_UnknownPlugin(t *testing.T) {
	r := tunnelTestRegistry(t)
	_, _, err := parseTunnelTarget("nope", 0, r, nil)
	if err == nil {
		t.Fatal("expected error for unknown plugin")
	}
	// Available plugins should be surfaced in the error.
	if !strings.Contains(err.Error(), "marimo") {
		t.Errorf("error should list available tunnelable plugins, got: %v", err)
	}
}

func TestParseTunnelTarget_PluginWithoutPort(t *testing.T) {
	r := tunnelTestRegistry(t)
	if _, _, err := parseTunnelTarget("github", 0, r, nil); err == nil {
		t.Fatal("expected error for plugin without port:")
	}
}

func TestParseTunnelTarget_NoTunnelablePlugins(t *testing.T) {
	r := plugins.NewRegistry([]*plugins.FilePlugin{
		mustLoadPlugin(t, "name: github\nversion: \"1\"\ndescription: d\n"),
	})
	_, _, err := parseTunnelTarget("nope", 0, r, nil)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "no plugins declare a port") {
		t.Errorf("unexpected error message: %v", err)
	}
}

func TestParseTunnelTarget_InvalidPair(t *testing.T) {
	r := tunnelTestRegistry(t)
	cases := []string{"abc:def", ":8080", "8080:", "8080:abc", "abc:8080"}
	for _, c := range cases {
		if _, _, err := parseTunnelTarget(c, 0, r, nil); err == nil {
			t.Errorf("expected error for %q", c)
		}
	}
}

func TestParseTunnelTarget_PairOverrideErrorIsClear(t *testing.T) {
	// Regression: the --local-port conflict should be reported even when
	// the explicit pair would otherwise fail port validation. Without the
	// early-exit check, "0:8080" would say "local 0 is out of range".
	r := tunnelTestRegistry(t)
	_, _, err := parseTunnelTarget("0:8080", 7000, r, nil)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "--local-port conflicts") {
		t.Errorf("expected --local-port conflict message, got: %v", err)
	}
}

func TestParseTunnelTarget_OutOfRangePort(t *testing.T) {
	r := tunnelTestRegistry(t)
	if _, _, err := parseTunnelTarget("70000", 0, r, nil); err == nil {
		t.Fatal("expected error for out-of-range port")
	}
	if _, _, err := parseTunnelTarget("0", 0, r, nil); err == nil {
		t.Fatal("expected error for port 0")
	}
}

func TestRunTunnel_Running_CallsStartTCPTunnel(t *testing.T) {
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
	if err := runTunnel(newConnectCfg(), client, "", wsOverride{}, 2718, 2718, &errOut); err != nil {
		t.Fatalf("runTunnel: %v", err)
	}
	found := false
	for _, call := range runner.runs {
		joined := strings.Join(call, " ")
		if strings.HasPrefix(joined, "workstations start-tcp-tunnel ") {
			found = true
			if !strings.Contains(joined, "--local-host-port localhost:2718") {
				t.Errorf("missing --local-host-port flag in: %s", joined)
			}
			if !strings.HasSuffix(joined, " p-user 2718") {
				t.Errorf("trailing args should be `<ws> <remotePort>`, got: %s", joined)
			}
		}
	}
	if !found {
		t.Errorf("expected start-tcp-tunnel call, got runs=%v", runner.runs)
	}
	// Warm path: already RUNNING, so the SSH-readiness probe is skipped.
	if len(runner.quietRuns) != 0 {
		t.Errorf("expected no SSH-readiness probe on the warm path, got %v", runner.quietRuns)
	}
	if !strings.Contains(errOut.String(), "Tunneling localhost:2718") {
		t.Errorf("expected status line in stderr, got %q", errOut.String())
	}
}

func TestRunTunnel_Stopped_StartsFirst(t *testing.T) {
	zeroPollInterval(t)
	runner := &testRunner{
		checks: map[string]bool{
			"workstations describe p-user --cluster cl --config cfg --region r --project p": true,
		},
		// STOPPED initially, then RUNNING once the wait loop polls after Start.
		outputSeq: map[string][][]byte{
			"workstations describe p-user --cluster cl --config cfg --region r --project p --format value(name,state,host)": {
				[]byte("p-user\tSTATE_STOPPED\t\n"),
				[]byte("p-user\tSTATE_RUNNING\thost\n"),
			},
		},
		// Cold path: tunnel gates on SSH readiness too, so the probe must
		// report ready (sentinel round-trips) before start-tcp-tunnel.
		outputs: readyProbeOutputs(),
	}
	client := gcloud.NewClient(runner)
	var errOut bytes.Buffer
	if err := runTunnel(newConnectCfg(), client, "", wsOverride{}, 8080, 8080, &errOut); err != nil {
		t.Fatalf("runTunnel: %v", err)
	}
	if len(runner.quietRuns) == 0 {
		t.Errorf("expected an SSH-readiness probe before tunnelling on the cold path")
	}
	var startIdx, tunnelIdx = -1, -1
	for i, call := range runner.runs {
		joined := strings.Join(call, " ")
		if startIdx == -1 && strings.HasPrefix(joined, "workstations start p-user ") {
			startIdx = i
		}
		if tunnelIdx == -1 && strings.HasPrefix(joined, "workstations start-tcp-tunnel ") {
			tunnelIdx = i
		}
	}
	if startIdx == -1 || tunnelIdx == -1 || startIdx >= tunnelIdx {
		t.Errorf("expected start before start-tcp-tunnel; start=%d tunnel=%d runs=%v", startIdx, tunnelIdx, runner.runs)
	}
}
