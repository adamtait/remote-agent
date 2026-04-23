package scaffold

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestScaffoldAssets_WritesBothFromEmbedded(t *testing.T) {
	cwd := t.TempDir()
	home := t.TempDir()
	wsDir := filepath.Join(cwd, ".ws")
	var out bytes.Buffer

	res, err := ScaffoldAssets(ScaffoldAssetsOptions{
		TargetDir: wsDir, Home: home, Stdout: &out,
	})
	if err != nil {
		t.Fatalf("ScaffoldAssets: %v", err)
	}

	dockerPath := filepath.Join(wsDir, "Dockerfile")
	entPath := filepath.Join(wsDir, "entrypoint.sh")

	for _, p := range []string{dockerPath, entPath} {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("expected %s to exist: %v", p, err)
		}
	}

	info, _ := os.Stat(dockerPath)
	if info.Mode().Perm() != 0644 {
		t.Errorf("Dockerfile mode = %o, want 0644", info.Mode().Perm())
	}
	info, _ = os.Stat(entPath)
	if info.Mode().Perm() != 0755 {
		t.Errorf("entrypoint.sh mode = %o, want 0755", info.Mode().Perm())
	}

	if !res.Wrote("Dockerfile") || !res.Wrote("entrypoint.sh") {
		t.Errorf("result should record both files written: %+v", res)
	}
	if res.Source("Dockerfile") != "embedded-default" {
		t.Errorf("Dockerfile source = %q, want embedded-default", res.Source("Dockerfile"))
	}
}

func TestScaffoldAssets_RefusesExistingWithoutForce(t *testing.T) {
	cwd := t.TempDir()
	home := t.TempDir()
	wsDir := filepath.Join(cwd, ".ws")
	if err := os.MkdirAll(wsDir, 0755); err != nil {
		t.Fatal(err)
	}
	existing := filepath.Join(wsDir, "Dockerfile")
	if err := os.WriteFile(existing, []byte("old\n"), 0644); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	_, err := ScaffoldAssets(ScaffoldAssetsOptions{
		TargetDir: wsDir, Home: home, Stdout: &out,
	})
	if err == nil {
		t.Fatal("expected error for existing file without --force")
	}
	if !strings.Contains(err.Error(), "already exists") {
		t.Errorf("error = %q, want contains 'already exists'", err.Error())
	}

	data, _ := os.ReadFile(existing)
	if string(data) != "old\n" {
		t.Errorf("existing file was modified: %q", string(data))
	}
}

func TestScaffoldAssets_ForceOverwrites(t *testing.T) {
	cwd := t.TempDir()
	home := t.TempDir()
	wsDir := filepath.Join(cwd, ".ws")
	if err := os.MkdirAll(wsDir, 0755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(wsDir, "Dockerfile")
	if err := os.WriteFile(path, []byte("old\n"), 0644); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	if _, err := ScaffoldAssets(ScaffoldAssetsOptions{
		TargetDir: wsDir, Home: home, Stdout: &out, Force: true,
	}); err != nil {
		t.Fatalf("ScaffoldAssets with --force: %v", err)
	}

	data, _ := os.ReadFile(path)
	if string(data) == "old\n" {
		t.Errorf("expected overwrite, got original content")
	}
}

func TestScaffoldAssets_DryRun_WritesNothing(t *testing.T) {
	cwd := t.TempDir()
	home := t.TempDir()
	wsDir := filepath.Join(cwd, ".ws")
	var out bytes.Buffer
	if _, err := ScaffoldAssets(ScaffoldAssetsOptions{
		TargetDir: wsDir, Home: home, Stdout: &out, DryRun: true,
	}); err != nil {
		t.Fatalf("ScaffoldAssets dry-run: %v", err)
	}

	if _, err := os.Stat(filepath.Join(wsDir, "Dockerfile")); !os.IsNotExist(err) {
		t.Errorf(".ws/Dockerfile should not exist after dry-run, err=%v", err)
	}
	if !strings.Contains(out.String(), "would write") {
		t.Errorf("expected dry-run summary on stdout, got: %q", out.String())
	}
}

func TestScaffoldAssets_AlwaysWritesEmbedded(t *testing.T) {
	cwd := t.TempDir()
	home := t.TempDir()
	wsDir := filepath.Join(cwd, ".ws")
	// Place a user template; ScaffoldAssets should ignore it and use embedded bytes.
	tmplDir := filepath.Join(home, ".config", "ws", "template")
	if err := os.MkdirAll(tmplDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmplDir, "Dockerfile"), []byte("FROM my/custom:latest\n"), 0644); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	res, err := ScaffoldAssets(ScaffoldAssetsOptions{
		TargetDir: wsDir, Home: home, Stdout: &out,
	})
	if err != nil {
		t.Fatalf("ScaffoldAssets: %v", err)
	}

	dockerContents, _ := os.ReadFile(filepath.Join(wsDir, "Dockerfile"))
	if !bytes.Equal(dockerContents, embeddedDockerfile) {
		t.Errorf("Dockerfile bytes differ from embedded; ScaffoldAssets should always use embedded bytes")
	}
	if res.Source("Dockerfile") != "embedded-default" {
		t.Errorf("Dockerfile source = %q, want embedded-default", res.Source("Dockerfile"))
	}
	if res.Source("entrypoint.sh") != "embedded-default" {
		t.Errorf("entrypoint.sh source = %q, want embedded-default", res.Source("entrypoint.sh"))
	}
}

func TestScaffoldAssets_PartialExisting_OnlyScaffoldsMissing(t *testing.T) {
	cwd := t.TempDir()
	home := t.TempDir()
	wsDir := filepath.Join(cwd, ".ws")
	if err := os.MkdirAll(wsDir, 0755); err != nil {
		t.Fatal(err)
	}
	// User already has a custom Dockerfile; entrypoint.sh missing.
	if err := os.WriteFile(filepath.Join(wsDir, "Dockerfile"), []byte("USER\n"), 0644); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	res, err := ScaffoldAssets(ScaffoldAssetsOptions{
		TargetDir: wsDir, Home: home, Stdout: &out, OnlyMissing: true,
	})
	if err != nil {
		t.Fatalf("ScaffoldAssets OnlyMissing: %v", err)
	}
	if res.Wrote("Dockerfile") {
		t.Errorf("should not overwrite existing Dockerfile when OnlyMissing=true")
	}
	if !res.Wrote("entrypoint.sh") {
		t.Errorf("should write missing entrypoint.sh when OnlyMissing=true")
	}

	data, _ := os.ReadFile(filepath.Join(wsDir, "Dockerfile"))
	if string(data) != "USER\n" {
		t.Errorf("existing Dockerfile modified under OnlyMissing, got %q", data)
	}
}
