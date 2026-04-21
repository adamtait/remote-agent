package scaffold_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/adamtait/remote-agent/cws/internal/scaffold"
)

func TestWriteAtomic_CreatesNewFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "out.yaml")
	if err := scaffold.WriteAtomic(path, []byte("hello"), false); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "hello" {
		t.Errorf("got %q, want hello", got)
	}
}

func TestWriteAtomic_RefusesIfExistsWithoutForce(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "out.yaml")
	if err := os.WriteFile(path, []byte("original"), 0644); err != nil {
		t.Fatal(err)
	}
	err := scaffold.WriteAtomic(path, []byte("new"), false)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	got, _ := os.ReadFile(path)
	if string(got) != "original" {
		t.Errorf("file was modified: got %q, want original", got)
	}
}

func TestWriteAtomic_OverwritesWithForce(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "out.yaml")
	if err := os.WriteFile(path, []byte("original"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := scaffold.WriteAtomic(path, []byte("new"), true); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(path)
	if string(got) != "new" {
		t.Errorf("got %q, want new", got)
	}
}

func TestWriteAtomic_CreatesParentDir(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sub", "nested", "out.yaml")
	if err := scaffold.WriteAtomic(path, []byte("hi"), false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
}

func TestWriteAtomic_NoTempFileLeftOnSuccess(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "out.yaml")
	if err := scaffold.WriteAtomic(path, []byte("x"), false); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name() != "out.yaml" {
			t.Errorf("leftover file: %s", e.Name())
		}
	}
}
