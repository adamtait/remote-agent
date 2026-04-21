// cws/internal/scaffold/validate_test.go
package scaffold_test

import (
	"strings"
	"testing"

	"github.com/adamtait/remote-agent/cws/internal/scaffold"
)

func TestValidateProjectID(t *testing.T) {
	valid30 := "a" + strings.Repeat("b", 28) + "c" // 30 chars
	invalid31 := "a" + strings.Repeat("b", 29) + "c" // 31 chars

	cases := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{name: "typical valid", input: "my-project-123", wantErr: false},
		{name: "6 chars minimum", input: "abc123", wantErr: false},
		{name: "30 chars valid", input: valid30, wantErr: false},
		{name: "5 chars too short", input: "a1234", wantErr: true},
		{name: "31 chars too long", input: invalid31, wantErr: true},
		{name: "empty string", input: "", wantErr: true},
		{name: "uppercase letter", input: "MyProject", wantErr: true},
		{name: "underscore", input: "my_project", wantErr: true},
		{name: "leading hyphen", input: "-project", wantErr: true},
		{name: "trailing hyphen", input: "project-", wantErr: true},
		{name: "starts with digit", input: "1project", wantErr: true},
		{name: "too short ab", input: "ab", wantErr: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := scaffold.ValidateProjectID(tc.input)
			if (err != nil) != tc.wantErr {
				t.Errorf("ValidateProjectID(%q) err = %v, wantErr = %v", tc.input, err, tc.wantErr)
			}
		})
	}
}

func TestValidateGithubRepoURL(t *testing.T) {
	cases := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{name: "https with .git suffix", input: "https://github.com/owner/repo.git", wantErr: false},
		{name: "https without .git suffix", input: "https://github.com/owner/repo", wantErr: false},
		{name: "git@ ssh style", input: "git@github.com:owner/repo.git", wantErr: false},
		{name: "empty string", input: "", wantErr: true},
		{name: "ssh:// prefix rejected", input: "ssh://git@github.com/owner/repo", wantErr: true},
		{name: "bare domain rejected", input: "github.com/owner/repo", wantErr: true},
		{name: "http not https rejected", input: "http://github.com/owner/repo", wantErr: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := scaffold.ValidateGithubRepoURL(tc.input)
			if (err != nil) != tc.wantErr {
				t.Errorf("ValidateGithubRepoURL(%q) err = %v, wantErr = %v", tc.input, err, tc.wantErr)
			}
		})
	}
}

func TestNormalizeGithubRepoURL(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  string
	}{
		{name: "ssh with .git", input: "git@github.com:owner/repo.git", want: "https://github.com/owner/repo.git"},
		{name: "ssh without .git", input: "git@github.com:owner/repo", want: "https://github.com/owner/repo.git"},
		{name: "ssh hyphenated names", input: "git@github.com:my-org/my-repo.git", want: "https://github.com/my-org/my-repo.git"},
		{name: "https passthrough", input: "https://github.com/owner/repo.git", want: "https://github.com/owner/repo.git"},
		{name: "https without .git passthrough", input: "https://github.com/owner/repo", want: "https://github.com/owner/repo"},
		{name: "empty passthrough", input: "", want: ""},
		{name: "non-github ssh passthrough", input: "git@gitlab.com:owner/repo.git", want: "git@gitlab.com:owner/repo.git"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := scaffold.NormalizeGithubRepoURL(tc.input)
			if got != tc.want {
				t.Errorf("NormalizeGithubRepoURL(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}
