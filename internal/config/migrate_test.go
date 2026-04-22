package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/adamtait/remote-agent/internal/config"
)

func TestMigrateLegacy_MovesFile(t *testing.T) {
	dir := t.TempDir()
	legacy := filepath.Join(dir, ".ws.yaml")
	if err := os.WriteFile(legacy, []byte("project_id: foo\n"), 0644); err != nil {
		t.Fatal(err)
	}

	migrated, err := config.MigrateLegacyProjectConfig(dir)
	if err != nil {
		t.Fatalf("MigrateLegacyProjectConfig: %v", err)
	}
	if !migrated {
		t.Fatal("expected migrated=true")
	}

	if _, err := os.Stat(legacy); !os.IsNotExist(err) {
		t.Errorf("expected %s removed, err=%v", legacy, err)
	}
	newPath := filepath.Join(dir, ".ws", "config.yaml")
	data, err := os.ReadFile(newPath)
	if err != nil {
		t.Fatalf("reading %s: %v", newPath, err)
	}
	if string(data) != "project_id: foo\n" {
		t.Errorf("content mismatch after migration: %q", data)
	}
}

func TestMigrateLegacy_NoLegacy_NoOp(t *testing.T) {
	dir := t.TempDir()
	migrated, err := config.MigrateLegacyProjectConfig(dir)
	if err != nil {
		t.Fatalf("MigrateLegacyProjectConfig: %v", err)
	}
	if migrated {
		t.Error("expected migrated=false when no legacy file present")
	}
}

func TestMigrateLegacy_BothPresent_Errors(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".ws.yaml"), []byte("x\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, ".ws"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".ws", "config.yaml"), []byte("y\n"), 0644); err != nil {
		t.Fatal(err)
	}

	_, err := config.MigrateLegacyProjectConfig(dir)
	if err == nil {
		t.Fatal("expected error when both files present")
	}
}

func TestMigrateLegacy_CreatesWsDir(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".ws.yaml"), []byte("z\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := config.MigrateLegacyProjectConfig(dir); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(dir, ".ws"))
	if err != nil {
		t.Fatalf("stat .ws: %v", err)
	}
	if !info.IsDir() {
		t.Fatal("expected .ws to be a directory")
	}
}
