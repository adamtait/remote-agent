// ws/internal/scaffold/validate.go
package scaffold

import (
	"fmt"
	"regexp"
	"strings"
)

var projectIDRe = regexp.MustCompile(`^[a-z][-a-z0-9]{4,28}[a-z0-9]$`)

// sshGithubRe matches the SSH form `git@github.com:owner/repo(.git)?`.
// Captures owner/repo without the optional .git suffix.
var sshGithubRe = regexp.MustCompile(`^git@github\.com:([^/]+/[^/]+?)(?:\.git)?$`)

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

// NormalizeGithubRepoURL rewrites SSH-form github URLs (git@github.com:owner/repo[.git])
// into the HTTPS form (https://github.com/owner/repo.git). The workstation container
// only ships HTTPS credentials (a PAT via the git credential helper) and has no SSH
// keys or known_hosts, so SSH URLs would fail with "Host key verification failed".
// Non-github or already-HTTPS URLs are returned unchanged.
func NormalizeGithubRepoURL(url string) string {
	if m := sshGithubRe.FindStringSubmatch(url); m != nil {
		return "https://github.com/" + m[1] + ".git"
	}
	return url
}

// repoSlugHTTPSRe matches https://github.com/<owner>/<repo>[.git][/] and captures <repo>.
var repoSlugHTTPSRe = regexp.MustCompile(`^https://github\.com/[^/]+/([^/]+?)(?:\.git)?/?$`)

// repoSlugSSHRe matches git@github.com:<owner>/<repo>[.git] and captures <repo>.
var repoSlugSSHRe = regexp.MustCompile(`^git@github\.com:[^/]+/([^/]+?)(?:\.git)?$`)

// RepoSlugFromURL returns the repository name from a GitHub URL, or "" if the
// URL is empty, malformed, or not a GitHub URL. HTTPS and SSH forms are both
// supported; an optional ".git" suffix and trailing slash are stripped.
func RepoSlugFromURL(url string) string {
	if m := repoSlugHTTPSRe.FindStringSubmatch(url); m != nil {
		return m[1]
	}
	if m := repoSlugSSHRe.FindStringSubmatch(url); m != nil {
		return m[1]
	}
	return ""
}
