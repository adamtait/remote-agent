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
	"encoding/json"
	"fmt"
	"io"
	"os"
	"regexp"
	"strconv"
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

// DeleteBucket removes a GCS bucket and its contents (caller must confirm
// first). `gcloud storage buckets delete` refuses a non-empty bucket, so the
// objects are cleared first via a best-effort recursive remove (RunSoft, since
// an already-empty bucket makes `storage rm` exit non-zero with "no URLs
// matched"). The reverse of EnsureBucket in `ra create`. Used by `ra teardown`
// only when the user opts in with --delete-buckets, because bucket contents
// (e.g. synced work artifacts) are not recoverable.
func DeleteBucket(r gcloud.Runner, project, name string) error {
	uri := "gs://" + name
	// Best-effort empty: ignore the error so an already-empty bucket (or a
	// missing one) doesn't block the bucket delete, which reports the real
	// outcome.
	_ = r.RunSoft("storage", "rm", "--recursive", uri+"/**", "--project", project)
	if err := r.Run("storage", "buckets", "delete", uri, "--project", project, "--quiet"); err != nil {
		return fmt.Errorf("deleting bucket %s: %w", name, err)
	}
	return nil
}

// LifecycleRule is one GCS Object Lifecycle Management rule in the
// vocabulary ra supports. It is the gcs-package mirror of
// plugins.LifecycleRule (kept separate so internal/gcs need not import
// internal/plugins); cmd/create.go converts between the two. Action is
// "Delete" or "SetStorageClass". AgeDays and Prefixes are the conditions:
// age in days, and an optional set of object-name prefixes the rule is
// scoped to (so a rule can target just one plugin's prefix in a shared
// bucket).
type LifecycleRule struct {
	Action       string
	StorageClass string
	AgeDays      int
	Prefixes     []string
}

// lifecyclePayload is the on-disk JSON shape `gcloud storage buckets
// update --lifecycle-file` expects: {"rule": [{"action": {...},
// "condition": {...}}, ...]}. Marshaled with omitempty so a Delete rule
// doesn't emit an empty storageClass and an unscoped rule doesn't emit an
// empty matchesPrefix (GCS rejects empty arrays in some fields).
type lifecyclePayload struct {
	Rule []lifecycleEntry `json:"rule"`
}

type lifecycleEntry struct {
	Action    lifecycleAction    `json:"action"`
	Condition lifecycleCondition `json:"condition"`
}

type lifecycleAction struct {
	Type         string `json:"type"`
	StorageClass string `json:"storageClass,omitempty"`
}

type lifecycleCondition struct {
	Age           *int     `json:"age,omitempty"`
	MatchesPrefix []string `json:"matchesPrefix,omitempty"`
}

// BuildLifecycleJSON renders rules into the GCS lifecycle-file JSON. It
// validates each rule (known action, storageClass present for
// SetStorageClass, non-negative age) so a typo in plugin.yaml fails fast
// at `ra create` rather than silently shipping a no-op or malformed
// config to GCS. Exposed (not just used by ApplyLifecycle) so it is unit
// testable without a gcloud shell-out.
func BuildLifecycleJSON(rules []LifecycleRule) ([]byte, error) {
	if len(rules) == 0 {
		return nil, fmt.Errorf("no lifecycle rules to apply")
	}
	payload := lifecyclePayload{Rule: make([]lifecycleEntry, 0, len(rules))}
	for i, rule := range rules {
		switch rule.Action {
		case "Delete":
			if rule.StorageClass != "" {
				return nil, fmt.Errorf("lifecycle rule %d: storage_class is meaningless for a Delete action", i)
			}
		case "SetStorageClass":
			if rule.StorageClass == "" {
				return nil, fmt.Errorf("lifecycle rule %d: SetStorageClass requires a storage_class", i)
			}
		default:
			return nil, fmt.Errorf("lifecycle rule %d: unknown action %q (want Delete or SetStorageClass)", i, rule.Action)
		}
		if rule.AgeDays < 0 {
			return nil, fmt.Errorf("lifecycle rule %d: age_days must be >= 0, got %d", i, rule.AgeDays)
		}
		age := rule.AgeDays
		payload.Rule = append(payload.Rule, lifecycleEntry{
			Action: lifecycleAction{Type: rule.Action, StorageClass: rule.StorageClass},
			Condition: lifecycleCondition{
				Age:           &age,
				MatchesPrefix: rule.Prefixes,
			},
		})
	}
	return json.MarshalIndent(payload, "", "  ")
}

// ApplySoftDelete raises the bucket's soft-delete retention to at least
// `days` (7–90, GCS's range). It never lowers or clears it: the current
// policy is read first and a retention already >= days is left alone, so
// a shared bucket an admin set to 30d isn't cut to a plugin's 7d. If the
// current policy can't be read (no storage.buckets.get), the update is
// attempted anyway. Returns (true, nil) when it changed the policy.
// Uses RunSoft: soft delete is a safety net, so callers WARN and carry on
// rather than failing `ra create`.
func ApplySoftDelete(r gcloud.Runner, project, name string, days int) (bool, error) {
	if days < 7 || days > 90 {
		return false, fmt.Errorf("soft_delete_days must be 7-90, got %d", days)
	}
	out, err := r.OutputQuiet("storage", "buckets", "describe", "gs://"+name,
		"--project", project,
		"--format=value(soft_delete_policy.retentionDurationSeconds)")
	if err == nil {
		if cur, perr := strconv.Atoi(strings.TrimSpace(string(out))); perr == nil && cur >= days*86400 {
			return false, nil
		}
	}
	if err := r.RunSoft("storage", "buckets", "update", "gs://"+name,
		"--project", project,
		fmt.Sprintf("--soft-delete-duration=%dd", days)); err != nil {
		return false, err
	}
	return true, nil
}

// ApplyLifecycle writes the rules to a temp lifecycle-file and runs
// `gcloud storage buckets update gs://<name> --lifecycle-file=<path>`.
// Idempotent: GCS replaces the whole lifecycle config with whatever the
// file contains, so re-running `ra create` re-asserts the same policy. A
// no-op when rules is empty (most plugins declare none) so the caller can
// invoke it unconditionally.
func ApplyLifecycle(r gcloud.Runner, project, name string, rules []LifecycleRule) error {
	if len(rules) == 0 {
		return nil
	}
	data, err := BuildLifecycleJSON(rules)
	if err != nil {
		return err
	}
	f, err := os.CreateTemp("", "ra-lifecycle-*.json")
	if err != nil {
		return fmt.Errorf("creating lifecycle temp file: %w", err)
	}
	defer os.Remove(f.Name())
	if _, err := f.Write(data); err != nil {
		f.Close()
		return fmt.Errorf("writing lifecycle temp file: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("closing lifecycle temp file: %w", err)
	}
	return r.Run("storage", "buckets", "update", "gs://"+name,
		"--project", project,
		"--lifecycle-file="+f.Name())
}
