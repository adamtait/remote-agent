package scaffold

import (
	"strings"
	"testing"
)

// funcBody returns the body of a shell function defined as `name() {` in src,
// spanning from the header line up to and including the first line that is a
// bare `}` at column 0. Fails the test if the function is not found.
func funcBody(t *testing.T, src, name string) string {
	t.Helper()
	header := name + "() {"
	start := strings.Index(src, header)
	if start < 0 {
		t.Fatalf("function %s not defined in embedded setup script", name)
	}
	rest := src[start:]
	end := strings.Index(rest, "\n}\n")
	if end < 0 {
		t.Fatalf("could not find end of function %s", name)
	}
	return rest[:end]
}

// The embedded ra-env helper library must keep fetched secrets out of the
// world-readable /etc/environment: ra_env_set_secret writes only /run/ra/env,
// and the boot secret-fetch loop must route through it (not ra_env_set).
func TestEmbeddedSetup_SecretsNotWrittenToEtcEnvironment(t *testing.T) {
	src := string(embeddedRemoteAgentSetup)

	secret := funcBody(t, src, "ra_env_set_secret")
	if strings.Contains(secret, ">> /etc/environment") {
		t.Errorf("ra_env_set_secret must never append to /etc/environment:\n%s", secret)
	}
	if !strings.Contains(secret, "_ra_env_write_run") {
		t.Errorf("ra_env_set_secret must write /run/ra/env via _ra_env_write_run:\n%s", secret)
	}

	// The public setter is still expected to write /etc/environment (non-secret
	// config relies on shell-agnostic pam_env delivery).
	pub := funcBody(t, src, "ra_env_set")
	if !strings.Contains(pub, ">> /etc/environment") {
		t.Errorf("ra_env_set must still write /etc/environment for non-secret config:\n%s", pub)
	}

	// The boot loop that fetches secrets from Secret Manager must use the
	// secret-only setter, or secrets leak back into /etc/environment.
	if !strings.Contains(src, `ra_env_set_secret "${target_env_var}"`) {
		t.Errorf("secret-fetch loop must call ra_env_set_secret for fetched values")
	}
	if strings.Contains(src, `ra_env_set "${target_env_var}"`) {
		t.Errorf("secret-fetch loop must not call the both-files ra_env_set for fetched values")
	}
}
