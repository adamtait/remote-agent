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

// Exists reports whether a secret already exists in GCP Secret Manager.
func Exists(r gcloud.Runner, projectID, name string) bool {
	return r.Check("secrets", "describe", name, "--project", projectID)
}

// PromptValue reads a secret value from stdin (hidden input). The caller is
// responsible for zeroing the returned slice after use.
func PromptValue(prompt string, stderr io.Writer, stdin *os.File) ([]byte, error) {
	fmt.Fprintf(stderr, "%s: ", prompt)
	value, err := term.ReadPassword(int(stdin.Fd()))
	fmt.Fprintln(stderr)
	if err != nil {
		return nil, fmt.Errorf("reading secret value: %w", err)
	}
	if len(strings.TrimSpace(string(value))) == 0 {
		return nil, fmt.Errorf("secret value cannot be empty")
	}
	return value, nil
}

// CreateFromValue creates a new secret in GCP Secret Manager from the given
// value bytes. Caller is responsible for zeroing value after the call.
func CreateFromValue(r gcloud.Runner, projectID, name string, value []byte) error {
	if err := r.RunWithStdin(bytes.NewReader(value),
		"secrets", "create", name,
		"--replication-policy=automatic",
		"--data-file=-",
		"--project", projectID); err != nil {
		return fmt.Errorf("creating secret %s: %w", name, err)
	}
	return nil
}

// EnsureSecret checks if a secret exists in GCP Secret Manager; if not,
// reads a value from the user (hidden input) and creates the secret.
// Caller is responsible for IAM bindings (done in `ra create`).
//
// Prefer the split Exists/PromptValue/CreateFromValue API in new callers
// so secret prompts can be batched up front instead of inline mid-pipeline.
func EnsureSecret(r gcloud.Runner, projectID, name, prompt string, stderr io.Writer, stdin *os.File) error {
	if Exists(r, projectID, name) {
		fmt.Fprintf(stderr, "Secret %q already exists (use 'gcloud secrets versions add' to rotate)\n", name)
		return nil
	}
	value, err := PromptValue(prompt, stderr, stdin)
	if err != nil {
		return err
	}
	defer func() {
		for i := range value {
			value[i] = 0
		}
	}()
	return CreateFromValue(r, projectID, name, value)
}
