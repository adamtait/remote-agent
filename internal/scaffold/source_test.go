package scaffold

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestResolveSource_EmbeddedWhenNoUserTemplate(t *testing.T) {
	home := t.TempDir()
	body, label, err := ResolveSource(home, "Dockerfile")
	if err != nil {
		t.Fatalf("ResolveSource: %v", err)
	}
	if !bytes.Equal(body, embeddedDockerfile) {
		t.Errorf("expected embedded bytes, got different content (%d vs %d bytes)", len(body), len(embeddedDockerfile))
	}
	if label != "embedded-default" {
		t.Errorf("label = %q, want %q", label, "embedded-default")
	}
}

func TestResolveSource_UserTemplateTakesPrecedence(t *testing.T) {
	home := t.TempDir()
	tmplDir := filepath.Join(home, ".config", "ws", "template")
	if err := os.MkdirAll(tmplDir, 0755); err != nil {
		t.Fatal(err)
	}
	custom := []byte("FROM my/custom:latest\n")
	if err := os.WriteFile(filepath.Join(tmplDir, "Dockerfile"), custom, 0644); err != nil {
		t.Fatal(err)
	}

	body, label, err := ResolveSource(home, "Dockerfile")
	if err != nil {
		t.Fatalf("ResolveSource: %v", err)
	}
	if !bytes.Equal(body, custom) {
		t.Errorf("expected user-template bytes, got: %q", body)
	}
	if label != "user-template" {
		t.Errorf("label = %q, want %q", label, "user-template")
	}
}

func TestResolveSource_PartialTemplate_FallsBackPerFile(t *testing.T) {
	home := t.TempDir()
	tmplDir := filepath.Join(home, ".config", "ws", "template")
	if err := os.MkdirAll(tmplDir, 0755); err != nil {
		t.Fatal(err)
	}
	// Only Dockerfile present; entrypoint.sh should fall back.
	if err := os.WriteFile(filepath.Join(tmplDir, "Dockerfile"), []byte("FROM custom\n"), 0644); err != nil {
		t.Fatal(err)
	}

	_, dockerLabel, err := ResolveSource(home, "Dockerfile")
	if err != nil {
		t.Fatal(err)
	}
	if dockerLabel != "user-template" {
		t.Errorf("Dockerfile label = %q, want user-template", dockerLabel)
	}

	_, entLabel, err := ResolveSource(home, "entrypoint.sh")
	if err != nil {
		t.Fatal(err)
	}
	if entLabel != "embedded-default" {
		t.Errorf("entrypoint.sh label = %q, want embedded-default", entLabel)
	}
}

func TestResolveSource_UnknownName(t *testing.T) {
	home := t.TempDir()
	_, _, err := ResolveSource(home, "unknown-asset")
	if err == nil {
		t.Fatal("expected error for unknown asset name, got nil")
	}
}

func TestResolveSource_UserTemplateUnreadable_Propagates(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("root bypasses file mode permissions")
	}
	home := t.TempDir()
	tmplDir := filepath.Join(home, ".config", "ws", "template")
	if err := os.MkdirAll(tmplDir, 0755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(tmplDir, "Dockerfile")
	if err := os.WriteFile(path, []byte("x"), 0000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(path, 0644) })

	_, _, err := ResolveSource(home, "Dockerfile")
	if err == nil {
		t.Fatal("expected permission error, got nil")
	}
}

func TestResolveSource_ProfileDWaitAsset(t *testing.T) {
	home := t.TempDir()
	body, label, err := ResolveSource(home, "profile.d/00-ws-wait.sh")
	if err != nil {
		t.Fatalf("ResolveSource: %v", err)
	}
	if label != "embedded-default" {
		t.Errorf("label = %q, want embedded-default", label)
	}
	if len(body) == 0 {
		t.Errorf("expected non-empty embedded body for profile.d/00-ws-wait.sh")
	}
	if !bytes.Contains(body, []byte("/run/ws/ready")) {
		t.Errorf("expected body to reference sentinel path /run/ws/ready")
	}
	if !bytes.Contains(body, []byte("/run/ws/env")) {
		t.Errorf("expected body to source /run/ws/env")
	}
}
