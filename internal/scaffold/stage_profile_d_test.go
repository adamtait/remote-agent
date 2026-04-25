package scaffold

import (
	"os"
	"path/filepath"
	"testing"
)

func writeProfileScript(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0755); err != nil {
		t.Fatal(err)
	}
}

func TestStageProfileD_CopiesProjectOnly(t *testing.T) {
	cwd := t.TempDir()
	home := t.TempDir()
	writeProfileScript(t, filepath.Join(cwd, ".ra", "profile.d"), "10-app.sh", "echo project\n")

	stage, cleanup, err := StageProfileD(cwd, home)
	if err != nil {
		t.Fatalf("StageProfileD: %v", err)
	}
	defer cleanup()

	data, err := os.ReadFile(filepath.Join(stage, "10-app.sh"))
	if err != nil {
		t.Fatalf("reading staged file: %v", err)
	}
	if string(data) != "echo project\n" {
		t.Errorf("staged content = %q, want %q", data, "echo project\n")
	}
}

func TestStageProfileD_CopiesUserGlobalOnly(t *testing.T) {
	cwd := t.TempDir()
	home := t.TempDir()
	writeProfileScript(t, filepath.Join(home, ".config", "ra", "profile.d"), "20-user.sh", "echo user\n")

	stage, cleanup, err := StageProfileD(cwd, home)
	if err != nil {
		t.Fatalf("StageProfileD: %v", err)
	}
	defer cleanup()

	data, err := os.ReadFile(filepath.Join(stage, "20-user.sh"))
	if err != nil {
		t.Fatalf("reading staged file: %v", err)
	}
	if string(data) != "echo user\n" {
		t.Errorf("staged content = %q, want %q", data, "echo user\n")
	}
}

func TestStageProfileD_ProjectOverridesUserGlobalOnCollision(t *testing.T) {
	cwd := t.TempDir()
	home := t.TempDir()
	writeProfileScript(t, filepath.Join(cwd, ".ra", "profile.d"), "50-shared.sh", "echo project\n")
	writeProfileScript(t, filepath.Join(home, ".config", "ra", "profile.d"), "50-shared.sh", "echo user\n")

	stage, cleanup, err := StageProfileD(cwd, home)
	if err != nil {
		t.Fatalf("StageProfileD: %v", err)
	}
	defer cleanup()

	data, err := os.ReadFile(filepath.Join(stage, "50-shared.sh"))
	if err != nil {
		t.Fatalf("reading staged file: %v", err)
	}
	if string(data) != "echo project\n" {
		t.Errorf("collision: staged = %q, want project %q", data, "echo project\n")
	}
}

func TestStageProfileD_CleanupRemovesStagingDir(t *testing.T) {
	cwd := t.TempDir()
	home := t.TempDir()
	writeProfileScript(t, filepath.Join(cwd, ".ra", "profile.d"), "10-app.sh", "echo x\n")

	stage, cleanup, err := StageProfileD(cwd, home)
	if err != nil {
		t.Fatalf("StageProfileD: %v", err)
	}
	cleanup()
	if _, err := os.Stat(stage); !os.IsNotExist(err) {
		t.Errorf("expected staging dir removed, stat err=%v", err)
	}
}

func TestStageProfileD_PreexistingStagingDirReplaced(t *testing.T) {
	cwd := t.TempDir()
	home := t.TempDir()
	stale := filepath.Join(cwd, ".ra", "profile.d.staged")
	if err := os.MkdirAll(stale, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stale, "stale.sh"), []byte("old\n"), 0755); err != nil {
		t.Fatal(err)
	}
	writeProfileScript(t, filepath.Join(cwd, ".ra", "profile.d"), "fresh.sh", "new\n")

	stage, cleanup, err := StageProfileD(cwd, home)
	if err != nil {
		t.Fatalf("StageProfileD: %v", err)
	}
	defer cleanup()

	if _, err := os.Stat(filepath.Join(stage, "stale.sh")); !os.IsNotExist(err) {
		t.Errorf("stale staging file should have been removed")
	}
	if _, err := os.Stat(filepath.Join(stage, "fresh.sh")); err != nil {
		t.Errorf("fresh staging file missing: %v", err)
	}
}

func TestStageProfileD_NoSourcesProducesEmptyStage(t *testing.T) {
	cwd := t.TempDir()
	home := t.TempDir()

	stage, cleanup, err := StageProfileD(cwd, home)
	if err != nil {
		t.Fatalf("StageProfileD: %v", err)
	}
	defer cleanup()

	info, err := os.Stat(stage)
	if err != nil {
		t.Fatalf("staging dir should exist even when empty: %v", err)
	}
	if !info.IsDir() {
		t.Errorf("staging path = %q, not a directory", stage)
	}
	entries, err := os.ReadDir(stage)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("staging dir should be empty, got %d entries", len(entries))
	}
}

func TestStageProfileD_IgnoresNonShellFiles(t *testing.T) {
	cwd := t.TempDir()
	home := t.TempDir()
	dir := filepath.Join(cwd, ".ra", "profile.d")
	writeProfileScript(t, dir, "10-app.sh", "echo x\n")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("notes\n"), 0644); err != nil {
		t.Fatal(err)
	}

	stage, cleanup, err := StageProfileD(cwd, home)
	if err != nil {
		t.Fatalf("StageProfileD: %v", err)
	}
	defer cleanup()

	if _, err := os.Stat(filepath.Join(stage, "README.md")); !os.IsNotExist(err) {
		t.Errorf("non-.sh file should not be staged")
	}
	if _, err := os.Stat(filepath.Join(stage, "10-app.sh")); err != nil {
		t.Errorf(".sh file should be staged: %v", err)
	}
}
