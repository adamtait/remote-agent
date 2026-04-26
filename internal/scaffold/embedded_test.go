package scaffold

import (
	"bytes"
	"strings"
	"testing"
)

func TestEmbeddedDockerfile_NonEmpty(t *testing.T) {
	if len(embeddedDockerfile) == 0 {
		t.Fatal("embeddedDockerfile is empty")
	}
}

func TestEmbeddedDockerfile_HasEntrypointCopyPath(t *testing.T) {
	s := string(embeddedDockerfile)
	want := "COPY .ra/entrypoint.sh"
	if !strings.Contains(s, want) {
		t.Errorf("embedded Dockerfile missing %q; got:\n%s", want, s)
	}
}

func TestEmbeddedEntrypoint_NonEmpty(t *testing.T) {
	if len(embeddedEntrypoint) == 0 {
		t.Fatal("embeddedEntrypoint is empty")
	}
}

func TestEmbeddedEntrypoint_HasShebangAndStrictMode(t *testing.T) {
	s := string(embeddedEntrypoint)
	if !strings.HasPrefix(s, "#!/bin/bash") {
		t.Errorf("embedded entrypoint.sh should start with '#!/bin/bash', got: %s", strings.SplitN(s, "\n", 2)[0])
	}
	if !strings.Contains(s, "set -euo pipefail") {
		t.Error("embedded entrypoint.sh should contain 'set -euo pipefail'")
	}
}

func TestEmbedded_WaitScriptHasShebangAndTimeout(t *testing.T) {
	if !bytes.HasPrefix(embeddedWaitScript, []byte("#!/")) {
		t.Errorf("expected shebang at start of embeddedWaitScript")
	}
	if !bytes.Contains(embeddedWaitScript, []byte("timeout")) &&
		!bytes.Contains(embeddedWaitScript, []byte("TIMEOUT")) {
		t.Errorf("wait script should mention a timeout")
	}
}

func TestEmbedded_EntrypointWritesRunWsEnv(t *testing.T) {
	if !bytes.Contains(embeddedEntrypoint, []byte("/run/ra/env")) {
		t.Errorf("expected entrypoint to write /run/ra/env")
	}
	if !bytes.Contains(embeddedEntrypoint, []byte("/run/ra/ready")) {
		t.Errorf("expected entrypoint to create /run/ra/ready sentinel")
	}
	if bytes.Contains(embeddedEntrypoint, []byte("/etc/profile.d/claude-auth.sh")) {
		t.Errorf("entrypoint should no longer write directly to /etc/profile.d/claude-auth.sh")
	}
}

func TestEmbedded_EntrypointSentinelIsLast(t *testing.T) {
	idxTouch := bytes.LastIndex(embeddedEntrypoint, []byte(`touch "${RA_READY_SENTINEL}"`))
	idxEnvWrite := bytes.LastIndex(embeddedEntrypoint, []byte(`"${RA_ENV_FILE}"`))
	idxDone := bytes.LastIndex(embeddedEntrypoint, []byte("Entrypoint complete"))
	if idxTouch < 0 || idxEnvWrite < 0 || idxDone < 0 {
		t.Fatalf("markers missing: touch=%d env=%d done=%d", idxTouch, idxEnvWrite, idxDone)
	}
	if idxTouch < idxEnvWrite {
		t.Errorf("sentinel touch (byte %d) must come after last env write (byte %d)", idxTouch, idxEnvWrite)
	}
	if idxTouch > idxDone {
		t.Errorf("sentinel touch (byte %d) must precede 'Entrypoint complete' log (byte %d)", idxTouch, idxDone)
	}
}

func TestEmbedded_DockerfileCopiesStagedProfileD(t *testing.T) {
	if !bytes.Contains(embeddedDockerfile, []byte(".ra/profile.d.staged/")) {
		t.Errorf("Dockerfile should COPY .ra/profile.d.staged/ into /etc/profile.d/")
	}
	if !bytes.Contains(embeddedDockerfile, []byte("/etc/profile.d/")) {
		t.Errorf("Dockerfile should target /etc/profile.d/")
	}
}


func TestEmbeddedEntrypoint_NoClaudeSpecifics(t *testing.T) {
	// Guardrail: the main entrypoint must stay harness-agnostic. Claude-owned
	// helpers live in claude/setup.sh and are sourced at runtime only when
	// WS_CLAUDE_ENABLED=true. If someone reintroduces these symbols into the
	// main entrypoint, toggle semantics break.
	forbidden := []string{
		"seed_claude_onboarding",
		"CLAUDE_SETTINGS_DIR",
		"CLAUDE_SETTINGS_PATH",
		"CLAUDE_CONFIG_PATH",
		"claude-apikey-helper.sh",
	}
	for _, sym := range forbidden {
		if bytes.Contains(embeddedEntrypoint, []byte(sym)) {
			t.Errorf("entrypoint.sh should not reference Claude-specific symbol %q", sym)
		}
	}
}

func TestEmbeddedDockerfile_PutsNpmGlobalOnLoginShellPath(t *testing.T) {
	// Cloud Workstations interactive shells (sshd / IDE terminal) and
	// `sudo -u user` calls don't inherit `ENV PATH=` from the Dockerfile;
	// they build PATH from /etc/profile.d/. Without a profile.d snippet,
	// /home/user/.npm-global/bin is invisible and `claude` is not found.
	s := embeddedDockerfile
	if !bytes.Contains(s, []byte("/etc/profile.d/")) {
		t.Errorf("Dockerfile should write a profile.d snippet that adds /home/user/.npm-global/bin to PATH")
	}
	if !bytes.Contains(s, []byte("/home/user/.npm-global/bin")) {
		t.Errorf("Dockerfile should reference /home/user/.npm-global/bin when extending login-shell PATH")
	}
	// The standalone `ENV PATH=...` directive doesn't reach login shells or
	// sudo'd commands, so it should not be the only mechanism. If it stays,
	// it must be accompanied by a profile.d export — asserted by the checks
	// above. Guard that we don't regress to ENV-only.
	envOnly := bytes.Contains(s, []byte(`ENV PATH="/home/user/.npm-global/bin`)) &&
		!bytes.Contains(s, []byte("profile.d/00-ra-npm-global-path.sh"))
	if envOnly {
		t.Errorf("Dockerfile relies on ENV PATH alone — login shells will not see /home/user/.npm-global/bin")
	}
}

func TestEmbeddedDockerfile_CopiesPluginDirectory(t *testing.T) {
	s := embeddedDockerfile
	for _, want := range []string{
		"COPY .ra/plugins/",
		"/etc/ra/plugins",
	} {
		if !bytes.Contains(s, []byte(want)) {
			t.Errorf("Dockerfile missing %q", want)
		}
	}
	// Old claude-specific paths must not appear.
	for _, banned := range []string{
		"COPY .ra/claude/install.sh",
		"COPY .ra/claude/setup.sh",
		"/etc/ra/claude-install.sh",
		"/etc/ra/claude-setup.sh",
	} {
		if bytes.Contains(s, []byte(banned)) {
			t.Errorf("Dockerfile should not reference old claude path %q", banned)
		}
	}
}

func TestEmbeddedEntrypoint_HasPluginSourcingLoop(t *testing.T) {
	for _, want := range []string{
		"/etc/ra/plugins/",
		"setup.sh",
		"Sourcing plugin:",
	} {
		if !bytes.Contains(embeddedEntrypoint, []byte(want)) {
			t.Errorf("entrypoint.sh missing %q — plugin sourcing loop broken", want)
		}
	}
	// Old claude-specific hooks must not appear.
	for _, banned := range []string{
		"RA_CLAUDE_ENABLED",
		"/etc/ra/claude-setup.sh",
		"ra_claude_handle_secret",
	} {
		if bytes.Contains(embeddedEntrypoint, []byte(banned)) {
			t.Errorf("entrypoint.sh should not reference old claude symbol %q", banned)
		}
	}
}

