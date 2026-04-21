// cws/internal/scaffold/validate.go
package scaffold

import (
	"fmt"
	"regexp"
	"strings"
)

var projectIDRe = regexp.MustCompile(`^[a-z][-a-z0-9]{4,28}[a-z0-9]$`)

// ValidateProjectID checks that id matches the GCP project ID regex.
// Valid GCP project IDs are 6-30 chars total, lowercase letters/digits/hyphens,
// must start with a lowercase letter, must not end with a hyphen.
func ValidateProjectID(id string) error {
	if !projectIDRe.MatchString(id) {
		return fmt.Errorf("invalid project_id %q: must be 6-30 chars, lowercase letters/digits/hyphens, starting with a letter", id)
	}
	return nil
}

// ValidateGithubRepoURL checks that url starts with "https://" or "git@".
func ValidateGithubRepoURL(url string) error {
	if url == "" || (!strings.HasPrefix(url, "https://") && !strings.HasPrefix(url, "git@")) {
		return fmt.Errorf("invalid github_repo_url %q: must start with https:// or git@", url)
	}
	return nil
}
