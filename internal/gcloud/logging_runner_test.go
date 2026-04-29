package gcloud

import (
	"testing"
)

func TestGcloudSubcmd(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{
			name: "two plain args",
			args: []string{"services", "enable", "foo.api"},
			want: "services enable",
		},
		{
			name: "flags before subcommands",
			args: []string{"--project", "p", "workstations", "configs", "create"},
			want: "workstations configs",
		},
		{
			name: "empty",
			args: []string{},
			want: "",
		},
		{
			name: "single non-flag arg",
			args: []string{"version"},
			want: "version",
		},
		{
			name: "only flags",
			args: []string{"--help", "--verbosity=debug"},
			want: "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := gcloudSubcmd(tc.args)
			if got != tc.want {
				t.Errorf("gcloudSubcmd(%v) = %q, want %q", tc.args, got, tc.want)
			}
		})
	}
}
