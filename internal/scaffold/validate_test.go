// ws/internal/scaffold/validate_test.go
package scaffold_test

import (
	"strings"
	"testing"

	"github.com/adamtait/remote-agent/internal/scaffold"
)

func TestValidateProjectID(t *testing.T) {
	valid30 := "a" + strings.Repeat("b", 28) + "c"   // 30 chars
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
