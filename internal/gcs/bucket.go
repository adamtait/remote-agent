// Package gcs wraps the small subset of `gcloud storage` operations needed
// by the claude-sync plugin: creating a bucket and binding IAM to it. It
// mirrors the shell-out pattern in internal/gcloud (no GCP Go SDK).
package gcs

import (
	"crypto/sha1"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/adamtait/remote-agent/internal/gcloud"
)

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
