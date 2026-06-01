// Package gcs wraps the subset of `gcloud storage` operations needed by
// the generic plugin-buckets hook in `ra create`: resolving a templated
// bucket name, idempotently creating the bucket, and binding an IAM role
// on it to the workstation service account. It mirrors the shell-out
// pattern in internal/gcloud (no GCP Go SDK) and is parallel in spirit
// to internal/secrets, which wraps Secret Manager. No plugin name is
// hard-coded here — callers (`cmd/create.go`) pass in the bucket name
// and role declared by each plugin's BucketSpec.
package gcs

import (
	"crypto/sha1"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"

	"github.com/adamtait/remote-agent/internal/gcloud"
)

// bucketNameRe matches the GCS bucket-name subset we accept: 3–63 chars,
// lowercase letters / digits / hyphens, starting+ending with a letter or
// digit, no consecutive hyphens. GCS allows underscores and dots in
// legacy bucket names but we reject them in overrides to keep the
// contract tight — if a plugin needs a dotted name we can relax later.
var bucketNameRe = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{1,61}[a-z0-9])?$`)

// ValidateBucketName returns an error if name is not a valid GCS bucket
// name according to the subset accepted by ra. Used to fail fast on
// user-supplied overrides (or on a poorly-formed resolved template)
// before any gcloud call half-provisions a workstation.
func ValidateBucketName(name string) error {
	if name == "" {
		return fmt.Errorf("bucket name is empty")
	}
	if len(name) < 3 || len(name) > 63 {
		return fmt.Errorf("bucket name %q must be 3–63 chars (got %d)", name, len(name))
	}
	if strings.Contains(name, "--") {
		return fmt.Errorf("bucket name %q must not contain consecutive hyphens", name)
	}
	if !bucketNameRe.MatchString(name) {
		return fmt.Errorf("bucket name %q must be lowercase letters/digits/hyphens, starting and ending with a letter or digit", name)
	}
	return nil
}

// userEmailHash returns the first 8 hex characters of sha1(lower(trim(email))).
// Used by ResolveBucketName so the same user maps to the same bucket
// regardless of email case, while keeping the email itself out of the
// global GCS namespace.
func userEmailHash(email string) string {
	h := sha1.Sum([]byte(strings.ToLower(strings.TrimSpace(email))))
	return fmt.Sprintf("%x", h[:4])
}

// ResolveBucketName expands the BucketSpec.NameTemplate placeholders
// against the active gcloud project and git user email. Supported:
//
//	{project_id}       — the gcloud project ID, as-is
//	{user_email_hash}  — 8-hex-char prefix of sha1(lower(trim(email)))
//
// Unknown placeholders (anything matching `{...}` we don't recognize)
// surface as an error so a typo doesn't silently produce a bucket
// named with a literal `{wrong_placeholder}` substring.
func ResolveBucketName(template, projectID, userEmail string) (string, error) {
	out := template
	out = strings.ReplaceAll(out, "{project_id}", projectID)
	out = strings.ReplaceAll(out, "{user_email_hash}", userEmailHash(userEmail))
	if remaining := unresolvedPlaceholderRe.FindString(out); remaining != "" {
		return "", fmt.Errorf("bucket name template %q contains unknown placeholder %q", template, remaining)
	}
	return out, nil
}

var unresolvedPlaceholderRe = regexp.MustCompile(`\{[^{}]+\}`)

// EnsureBucket idempotently creates a GCS bucket. It first runs
// `gcloud storage buckets describe`; on a false return it creates the
// bucket with uniform bucket-level access in the given region.
func EnsureBucket(r gcloud.Runner, project, name, region string) error {
	uri := "gs://" + name
	if r.Check("storage", "buckets", "describe", uri, "--project", project) {
		return nil
	}
	return r.Run("storage", "buckets", "create", uri,
		"--project", project,
		"--location", region,
		"--uniform-bucket-level-access",
		"--quiet")
}

// GrantRole grants the given IAM role on bucket to saEmail. Wraps
// gcloud.RetryOnSANotFound for the IAM eventual-consistency window that
// follows a fresh service-account create. The role is a parameter (not
// hard-coded) so plugins can request whatever they need —
// roles/storage.objectAdmin for read+write plugins,
// roles/storage.objectViewer for read-only, etc.
func GrantRole(r gcloud.Runner, attempts int, delay time.Duration, notice io.Writer, bucket, saEmail, role string) error {
	return gcloud.RetryOnSANotFound(r, attempts, delay, notice,
		"storage", "buckets", "add-iam-policy-binding", "gs://"+bucket,
		"--member=serviceAccount:"+saEmail,
		"--role="+role,
		"--condition=None")
}
