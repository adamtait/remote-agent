package etconn

import (
	"slices"
	"testing"
)

func TestBuildETArgs_FullOptions(t *testing.T) {
	got := buildETArgs(ETOptions{
		Host:          "localhost",
		DataPort:      2202,
		BootstrapPort: 2201,
		User:          "user",
		IdentityFile:  "/home/u/.ssh/google_compute_engine",
	})
	want := []string{
		"--ssh-option", "Port=2201",
		"--ssh-option", "IdentityFile=/home/u/.ssh/google_compute_engine",
		"--ssh-option", "StrictHostKeyChecking=no",
		"--ssh-option", "UserKnownHostsFile=/dev/null",
		"-p", "2202",
		"user@localhost",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("buildETArgs mismatch\n got: %q\nwant: %q", got, want)
	}
}

func TestBuildETArgs_Command(t *testing.T) {
	got := buildETArgs(ETOptions{
		Host:          "localhost",
		DataPort:      2202,
		BootstrapPort: 2201,
		User:          "user",
		Command:       "tmux new-session -A -s main",
	})
	// The remote command must be the final token, after the host positional, so
	// et forwards it to the remote shell.
	want := []string{
		"--ssh-option", "Port=2201",
		"--ssh-option", "StrictHostKeyChecking=no",
		"--ssh-option", "UserKnownHostsFile=/dev/null",
		"-p", "2202",
		"user@localhost",
		"tmux new-session -A -s main",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("buildETArgs mismatch\n got: %q\nwant: %q", got, want)
	}
}

func TestBuildETArgs_Keepalive(t *testing.T) {
	got := buildETArgs(ETOptions{
		Host:          "localhost",
		DataPort:      2202,
		BootstrapPort: 2201,
		User:          "user",
		KeepaliveSecs: 3,
	})
	// -k must precede the host positional (like every other flag), and a value of
	// 0 must omit it entirely (covered by the other cases, which leave it unset).
	want := []string{
		"-k", "3",
		"--ssh-option", "Port=2201",
		"--ssh-option", "StrictHostKeyChecking=no",
		"--ssh-option", "UserKnownHostsFile=/dev/null",
		"-p", "2202",
		"user@localhost",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("buildETArgs mismatch\n got: %q\nwant: %q", got, want)
	}
}

func TestBuildETArgs_NoIdentityNoUser(t *testing.T) {
	got := buildETArgs(ETOptions{
		Host:          "localhost",
		DataPort:      40002,
		BootstrapPort: 40001,
	})
	want := []string{
		"--ssh-option", "Port=40001",
		"--ssh-option", "StrictHostKeyChecking=no",
		"--ssh-option", "UserKnownHostsFile=/dev/null",
		"-p", "40002",
		"localhost", // no user → bare host
	}
	if !slices.Equal(got, want) {
		t.Fatalf("buildETArgs mismatch\n got: %q\nwant: %q", got, want)
	}
}

func TestBuildETArgs_ExtraBeforeHost(t *testing.T) {
	got := buildETArgs(ETOptions{
		Host:          "localhost",
		DataPort:      2,
		BootstrapPort: 1,
		User:          "user",
		Extra:         []string{"-v", "9"},
	})
	// Extra must land before the trailing host positional, or et will treat
	// the host as a flag value.
	want := []string{
		"--ssh-option", "Port=1",
		"--ssh-option", "StrictHostKeyChecking=no",
		"--ssh-option", "UserKnownHostsFile=/dev/null",
		"-p", "2",
		"-v", "9",
		"user@localhost",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("buildETArgs mismatch\n got: %q\nwant: %q", got, want)
	}
}
