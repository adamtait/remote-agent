// Package gcs wraps the small subset of `gcloud storage` operations needed
// by the claude-sync plugin: creating a bucket and binding IAM to it. It
// mirrors the shell-out pattern in internal/gcloud (no GCP Go SDK).
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

// bucketNameRe matches the GCS bucket-name subset we generate and want to
// accept from user overrides: 3–63 chars, lowercase letters / digits /
// hyphens, must start+end with a letter or digit, no consecutive hyphens.
// GCS allows underscores and dots in legacy bucket names but we reject them
// in overrides to keep the contract tight — if a user needs a dotted name
// we can relax this later.
var bucketNameRe = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{1,61}[a-z0-9])?$`)

// ValidateBucketName returns an error if name is not a valid GCS bucket
// name according to the subset accepted by ra. Used to fail fast on user-
// supplied `plugins.claude-sync.bucket_name_override` before any gcloud
// call attempts (and silently corrupts) a partial provisioning run.
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

// BucketName returns the deterministic bucket name for storing claude state
// for the given (project, user) pair. The 8-hex-char suffix is the prefix of
// sha1(lower(trim(userEmail))) so the same user maps to the same bucket
// regardless of email case while keeping the email itself out of the global
// GCS namespace.
func BucketName(projectID, userEmail string) string {
	h := sha1.Sum([]byte(strings.ToLower(strings.TrimSpace(userEmail))))
	return fmt.Sprintf("ra-claude-%s-%x", projectID, h[:4])
}

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

// GrantObjectAdmin grants roles/storage.objectAdmin on bucket to saEmail.
// Wraps gcloud.RetryOnSANotFound for the IAM eventual-consistency window
// that follows a fresh service-account create.
func GrantObjectAdmin(r gcloud.Runner, attempts int, delay time.Duration, notice io.Writer, bucket, saEmail string) error {
	return gcloud.RetryOnSANotFound(r, attempts, delay, notice,
		"storage", "buckets", "add-iam-policy-binding", "gs://"+bucket,
		"--member=serviceAccount:"+saEmail,
		"--role=roles/storage.objectAdmin",
		"--condition=None")
}
