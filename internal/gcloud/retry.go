package gcloud

import (
	"fmt"
	"io"
	"regexp"
	"time"
)

// saNotFoundRe matches gcloud's "Service account ... does not exist" error,
// which appears transiently after a fresh service-account create due to IAM
// eventual consistency. Case-insensitive: gcloud has emitted both "Service
// account" and "service account" variants over time.
var saNotFoundRe = regexp.MustCompile(`(?i)service account .* does not exist`)

// RetryOnSANotFound runs the given gcloud args via r.RunCapture, retrying
// while stderr matches the SA-not-found pattern. It waits delay between
// attempts and gives up after attempts tries, returning the final error.
//
// On the first retry it prints a one-line plain-text hint to notice so the
// user understands the pause. The hint is intentionally indented and
// prefix-less to nest under the enclosing step's "==>" header instead of
// competing with it. Pass nil to suppress.
func RetryOnSANotFound(r Runner, attempts int, delay time.Duration, notice io.Writer, args ...string) error {
	if attempts < 1 {
		attempts = 1
	}
	var lastErr error
	for i := 0; i < attempts; i++ {
		stderr, err := r.RunCapture(args...)
		if err == nil {
			return nil
		}
		lastErr = err
		if !saNotFoundRe.MatchString(stderr) {
			return err
		}
		if i == 0 && notice != nil {
			fmt.Fprintln(notice, "    waiting for service account propagation...")
		}
		if i < attempts-1 {
			time.Sleep(delay)
		}
	}
	return lastErr
}
