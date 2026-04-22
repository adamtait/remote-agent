package scaffold

import (
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
