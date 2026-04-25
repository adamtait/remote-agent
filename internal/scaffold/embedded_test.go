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
	want := "COPY .ws/entrypoint.sh"
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
	if !bytes.Contains(embeddedEntrypoint, []byte("/run/ws/env")) {
		t.Errorf("expected entrypoint to write /run/ws/env")
	}
	if !bytes.Contains(embeddedEntrypoint, []byte("/run/ws/ready")) {
		t.Errorf("expected entrypoint to create /run/ws/ready sentinel")
	}
	if bytes.Contains(embeddedEntrypoint, []byte("/etc/profile.d/claude-auth.sh")) {
		t.Errorf("entrypoint should no longer write directly to /etc/profile.d/claude-auth.sh")
	}
}

func TestEmbedded_EntrypointSentinelIsLast(t *testing.T) {
	idxTouch := bytes.LastIndex(embeddedEntrypoint, []byte(`touch "${WS_READY_SENTINEL}"`))
	idxEnvWrite := bytes.LastIndex(embeddedEntrypoint, []byte(`"${WS_ENV_FILE}"`))
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
	if !bytes.Contains(embeddedDockerfile, []byte(".ws/profile.d.staged/")) {
		t.Errorf("Dockerfile should COPY .ws/profile.d.staged/ into /etc/profile.d/")
	}
	if !bytes.Contains(embeddedDockerfile, []byte("/etc/profile.d/")) {
		t.Errorf("Dockerfile should target /etc/profile.d/")
	}
}

func TestEmbeddedClaudeInstall_HasNpmPackage(t *testing.T) {
	if len(embeddedClaudeInstall) == 0 {
		t.Fatal("embeddedClaudeInstall is empty")
	}
	if !bytes.HasPrefix(embeddedClaudeInstall, []byte("#!/bin/bash")) {
		t.Errorf("install.sh should start with '#!/bin/bash'")
	}
	if !bytes.Contains(embeddedClaudeInstall, []byte("@anthropic-ai/claude-code")) {
		t.Errorf("install.sh should install @anthropic-ai/claude-code")
	}
	if !bytes.Contains(embeddedClaudeInstall, []byte("set -euo pipefail")) {
		t.Errorf("install.sh should use strict mode")
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
		!bytes.Contains(s, []byte("profile.d/00-ws-npm-global-path.sh"))
	if envOnly {
		t.Errorf("Dockerfile relies on ENV PATH alone — login shells will not see /home/user/.npm-global/bin")
	}
}

func TestEmbeddedDockerfile_CopiesClaudeScripts(t *testing.T) {
	s := embeddedDockerfile
	for _, want := range []string{
		"COPY .ws/claude/install.sh",
		"COPY .ws/claude/setup.sh",
		"/etc/ws/claude-install.sh",
		"/etc/ws/claude-setup.sh",
	} {
		if !bytes.Contains(s, []byte(want)) {
			t.Errorf("Dockerfile missing %q", want)
		}
	}
	// Claude install moved to runtime (the 'user' account does not exist
	// in /etc/passwd at docker build time). The Dockerfile must not invoke
	// the npm install at build time, nor rely on a build arg to gate it.
	if bytes.Contains(s, []byte("npm install -g @anthropic-ai/claude-code")) {
		t.Errorf("Dockerfile should delegate Claude npm install to runtime, not inline it")
	}
	// The runtime gate may be mentioned in comments, but the Dockerfile must
	// not declare it as a build arg or branch on it inside a RUN.
	if bytes.Contains(s, []byte("ARG WS_CLAUDE_ENABLED")) {
		t.Errorf("Dockerfile should not declare ARG WS_CLAUDE_ENABLED — install is runtime-gated, not build-gated")
	}
	if bytes.Contains(s, []byte("$WS_CLAUDE_ENABLED")) {
		t.Errorf("Dockerfile should not branch on $WS_CLAUDE_ENABLED — install is runtime-gated")
	}
}

func TestEmbeddedEntrypoint_HasClaudeSourceHook(t *testing.T) {
	for _, want := range []string{
		"WS_CLAUDE_ENABLED",
		"/etc/ws/claude-setup.sh",
		"ws_claude_handle_secret",
	} {
		if !bytes.Contains(embeddedEntrypoint, []byte(want)) {
			t.Errorf("entrypoint.sh missing %q — Claude harness hook broken", want)
		}
	}
}

func TestEmbeddedClaudeSetup_InstallsOnFirstBoot(t *testing.T) {
	for _, want := range []string{
		"_ws_claude_install_if_missing",
		"/etc/ws/claude-install.sh",
	} {
		if !bytes.Contains(embeddedClaudeSetup, []byte(want)) {
			t.Errorf("setup.sh missing %q — first-boot install hook broken", want)
		}
	}
}

func TestEmbeddedClaudeSetup_UsesAbsolutePathForVersionAndSmokeTest(t *testing.T) {
	// `sudo -u user` resets PATH to its secure_path default and strips
	// /home/user/.npm-global/bin, so bare `claude` is not found. The version
	// detection and the smoke test must invoke claude by absolute path
	// (CLAUDE_BIN_PATH). Without this fix, both calls fail silently and the
	// onboarding seed proceeds without a version string while the smoke test
	// reports "token or account may be stale" on every fresh boot.
	s := embeddedClaudeSetup
	if bytes.Contains(s, []byte(`sudo -u "${USER_NAME}" claude --version`)) {
		t.Errorf("setup.sh: version check must use ${CLAUDE_BIN_PATH}, not bare `claude` (sudo strips PATH)")
	}
	if bytes.Contains(s, []byte(`claude -p "respond with OK"`)) {
		t.Errorf("setup.sh: smoke test must invoke ${CLAUDE_BIN_PATH}, not bare `claude` (sudo strips PATH)")
	}
	if !bytes.Contains(s, []byte(`"${CLAUDE_BIN_PATH}" --version`)) {
		t.Errorf(`setup.sh: expected '"${CLAUDE_BIN_PATH}" --version' for the version check`)
	}
	if !bytes.Contains(s, []byte(`"${CLAUDE_BIN_PATH}" -p "respond with OK"`)) {
		t.Errorf(`setup.sh: expected '"${CLAUDE_BIN_PATH}" -p "respond with OK"' for the smoke test`)
	}
}

func TestEmbeddedClaudeSetup_HasHandlerFunction(t *testing.T) {
	if len(embeddedClaudeSetup) == 0 {
		t.Fatal("embeddedClaudeSetup is empty")
	}
	if !bytes.Contains(embeddedClaudeSetup, []byte("ws_claude_handle_secret")) {
		t.Errorf("setup.sh must define ws_claude_handle_secret() for the main dispatch hook")
	}
	for _, want := range []string{"claude_oauth", "claude_apikey", "apiKeyHelper"} {
		if !bytes.Contains(embeddedClaudeSetup, []byte(want)) {
			t.Errorf("setup.sh missing expected marker %q", want)
		}
	}
}
