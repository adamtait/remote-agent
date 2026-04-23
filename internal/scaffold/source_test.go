package scaffold

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestResolveSource_UserTemplateTakesPrecedence(t *testing.T) {
	home := t.TempDir()
	projectRoot := t.TempDir()
	tmplDir := filepath.Join(home, ".config", "ws", "template")
	if err := os.MkdirAll(tmplDir, 0755); err != nil {
		t.Fatal(err)
	}
	custom := []byte("FROM my/custom:latest\n")
	if err := os.WriteFile(filepath.Join(tmplDir, "Dockerfile"), custom, 0644); err != nil {
		t.Fatal(err)
	}

	body, label, err := ResolveSource(home, projectRoot, "Dockerfile")
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

func TestResolveSource_ProjectTakesPrecedenceOverUser(t *testing.T) {
	home := t.TempDir()
	projectRoot := t.TempDir()

	// Write user template
	userTmplDir := filepath.Join(home, ".config", "ws", "template")
	if err := os.MkdirAll(userTmplDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(userTmplDir, "Dockerfile"), []byte("FROM user\n"), 0644); err != nil {
		t.Fatal(err)
	}

	// Write project override
	wsDir := filepath.Join(projectRoot, ".ws")
	if err := os.MkdirAll(wsDir, 0755); err != nil {
		t.Fatal(err)
	}
	projectBytes := []byte("FROM project\n")
	if err := os.WriteFile(filepath.Join(wsDir, "Dockerfile"), projectBytes, 0644); err != nil {
		t.Fatal(err)
	}

	body, label, err := ResolveSource(home, projectRoot, "Dockerfile")
	if err != nil {
		t.Fatalf("ResolveSource: %v", err)
	}
	if !bytes.Equal(body, projectBytes) {
		t.Errorf("expected project bytes, got: %q", body)
	}
	if label != "project-override" {
		t.Errorf("label = %q, want project-override", label)
	}
}

func TestResolveSource_MissingBoth_FallsBackToEmbedded(t *testing.T) {
	home := t.TempDir()
	projectRoot := t.TempDir()

	body, label, err := ResolveSource(home, projectRoot, "Dockerfile")
	if err != nil {
		t.Fatalf("expected embedded fallback, got error: %v", err)
	}
	if label != "embedded-default" {
		t.Errorf("label = %q, want %q", label, "embedded-default")
	}
	if len(body) == 0 {
		t.Error("expected non-empty embedded body")
	}
}

func TestResolveSource_UnknownName(t *testing.T) {
	home := t.TempDir()
	_, _, err := ResolveSource(home, "", "unknown-asset")
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

	_, _, err := ResolveSource(home, "", "Dockerfile")
	if err == nil {
		t.Fatal("expected permission error, got nil")
	}
}

func TestEmbeddedSeed_ReturnsBytes(t *testing.T) {
	for _, name := range AssetNames {
		data, err := EmbeddedSeed(name)
		if err != nil {
			t.Errorf("EmbeddedSeed(%q): %v", name, err)
		}
		if len(data) == 0 {
			t.Errorf("EmbeddedSeed(%q) returned empty bytes", name)
		}
	}
}

func TestEmbeddedSeed_UnknownName_ReturnsError(t *testing.T) {
	_, err := EmbeddedSeed("not-a-real-asset")
	if err == nil {
		t.Fatal("expected error for unknown asset name")
	}
}

func TestEmbeddedSeed_ProfileDWaitScript(t *testing.T) {
	body, err := EmbeddedSeed("profile.d/00-ws-wait.sh")
	if err != nil {
		t.Fatalf("EmbeddedSeed: %v", err)
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
