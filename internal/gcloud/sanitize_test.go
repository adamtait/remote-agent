package gcloud

import (
	"testing"
)

func TestSanitizeArgs_ContainerEnv(t *testing.T) {
	args := []string{"--container-env=KEY=val"}
	got := sanitizeArgs(args)
	if len(got) != 1 {
		t.Fatalf("expected 1 arg, got %d", len(got))
	}
	if got[0] != "--container-env=[redacted]" {
		t.Errorf("expected --container-env=[redacted], got %q", got[0])
	}
}

func TestSanitizeArgs_OtherArgs(t *testing.T) {
	args := []string{"--project", "my-project", "workstations", "create"}
	got := sanitizeArgs(args)
	if len(got) != len(args) {
		t.Fatalf("expected %d args, got %d", len(args), len(got))
	}
	for i, want := range args {
		if got[i] != want {
			t.Errorf("arg[%d]: expected %q, got %q", i, want, got[i])
		}
	}
}

func TestSanitizeArgs_Empty(t *testing.T) {
	got := sanitizeArgs([]string{})
	if len(got) != 0 {
		t.Errorf("expected empty slice, got %v", got)
	}
}

func TestSanitizeArgs_NoMutation(t *testing.T) {
	args := []string{"--container-env=SECRET=hunter2", "--project", "p"}
	original := make([]string, len(args))
	copy(original, args)

	sanitizeArgs(args)

	for i, want := range original {
		if args[i] != want {
			t.Errorf("input mutated at index %d: expected %q, got %q", i, want, args[i])
		}
	}
}
