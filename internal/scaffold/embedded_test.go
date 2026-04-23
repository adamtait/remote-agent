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
