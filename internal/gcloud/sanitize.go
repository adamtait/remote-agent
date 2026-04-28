package gcloud

import "strings"

// sanitizeArgs returns a copy of args with sensitive flag values redacted.
// Currently redacts --container-env=<value> → "--container-env=[redacted]"
// All other args pass through unchanged.
func sanitizeArgs(args []string) []string {
	out := make([]string, len(args))
	for i, a := range args {
		if strings.HasPrefix(a, "--container-env=") {
			out[i] = "--container-env=[redacted]"
		} else {
			out[i] = a
		}
	}
	return out
}
