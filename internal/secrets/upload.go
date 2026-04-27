// Package secrets centralizes GCP Secret Manager interactions used by
// `ra create` and `ra plugin install`.
package secrets

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/adamtait/remote-agent/internal/gcloud"
	"golang.org/x/term"
)

// EnsureSecret checks if a secret exists in GCP Secret Manager; if not,
// reads a value from the user (hidden input) and creates the secret.
// Caller is responsible for IAM bindings (done in `ra create`).
func EnsureSecret(r gcloud.Runner, projectID, name, prompt string, stderr io.Writer, stdin *os.File) error {
	if r.Check("secrets", "describe", name, "--project", projectID) {
		fmt.Fprintf(stderr, "Secret %q already exists (use 'gcloud secrets versions add' to rotate)\n", name)
		return nil
	}

	fmt.Fprintf(stderr, "%s: ", prompt)
	value, err := term.ReadPassword(int(stdin.Fd()))
	fmt.Fprintln(stderr)
	if err != nil {
		return fmt.Errorf("reading secret value: %w", err)
	}
	if len(strings.TrimSpace(string(value))) == 0 {
		return fmt.Errorf("secret value cannot be empty")
	}
	defer func() {
		for i := range value {
			value[i] = 0
		}
	}()

	if err := r.RunWithStdin(bytes.NewReader(value),
		"secrets", "create", name,
		"--replication-policy=automatic",
		"--data-file=-",
		"--project", projectID); err != nil {
		return fmt.Errorf("creating secret %s: %w", name, err)
	}
	return nil
}
