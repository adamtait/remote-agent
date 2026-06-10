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

// connDropRe matches the transient errors that gcloud / ssh emit when the
// workstation's API state has already flipped to RUNNING but its sshd (and the
// websocket tunnel backend in front of it) is not yet accepting connections.
// The canonical symptom is gcloud's own wrapper around a Python SSL teardown:
//
//	ERROR: Error connecting to workstation: [SSL: UNEXPECTED_EOF_WHILE_READING]
//	EOF occurred in violation of protocol (_ssl.c:1081)
//
// but the same race also surfaces as a refused/reset connection or ssh's
// pre-auth "kex_exchange_identification" failure depending on which layer wins
// the race. All are retryable: the backend just needs a few more seconds.
var connDropRe = regexp.MustCompile(`(?i)error connecting to workstation|unexpected_eof_while_reading|kex_exchange_identification|connection (refused|reset|closed)`)

// RetryOnConnDrop runs the given gcloud args via r.RunCapture, retrying while
// stderr matches a transient connection-drop pattern (see connDropRe). It waits
// delay between attempts and gives up after attempts tries, returning the final
// error. Non-matching failures (auth errors, bad flags, a clean session that
// exits non-zero) return immediately without retrying.
//
// On the first retry it prints a one-line hint to notice so the user
// understands the pause. Pass nil to suppress.
func RetryOnConnDrop(r Runner, attempts int, delay time.Duration, notice io.Writer, args ...string) error {
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
		if !connDropRe.MatchString(stderr) {
			return err
		}
		if i == 0 && notice != nil {
			fmt.Fprintln(notice, "Workstation not accepting connections yet; retrying...")
		}
		if i < attempts-1 {
			time.Sleep(delay)
		}
	}
	return lastErr
}
