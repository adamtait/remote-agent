package naming

import (
	"math/rand"
	"strings"
	"testing"
)

func TestUniqueSuffixed_BareFree(t *testing.T) {
	got := UniqueSuffixed("foo", map[string]bool{})
	if got != "foo" {
		t.Fatalf("want foo, got %s", got)
	}
}

func TestUniqueSuffixed_FirstSuffix(t *testing.T) {
	got := UniqueSuffixed("foo", map[string]bool{"foo": true})
	if got != "foo-1" {
		t.Fatalf("want foo-1, got %s", got)
	}
}

func TestUniqueSuffixed_SkipsTaken(t *testing.T) {
	got := UniqueSuffixed("foo", map[string]bool{"foo": true, "foo-1": true, "foo-2": true})
	if got != "foo-3" {
		t.Fatalf("want foo-3, got %s", got)
	}
}

func TestUniqueSuffixed_TruncatesLongBase(t *testing.T) {
	long := strings.Repeat("a", 70)
	got := UniqueSuffixed(long, map[string]bool{})
	if len(got) > maxNameLen {
		t.Fatalf("result too long: %d chars", len(got))
	}
}

func TestGenerateMemorable_Shape(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	got, err := GenerateMemorable("proj-user", map[string]bool{}, rng)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.HasPrefix(got, "proj-user-") {
		t.Fatalf("missing base prefix: %s", got)
	}
	parts := strings.Split(got, "-")
	if len(parts) != 4 {
		t.Fatalf("expected 4 parts, got %d (%s)", len(parts), got)
	}
	if !contains(adjectives, parts[2]) {
		t.Fatalf("adjective %q not in wordlist", parts[2])
	}
	if !contains(nouns, parts[3]) {
		t.Fatalf("noun %q not in wordlist", parts[3])
	}
}

func TestGenerateMemorable_RetriesOnCollision(t *testing.T) {
	adjs := []string{"a1", "a2"}
	ns := []string{"n1", "n2"}
	taken := map[string]bool{
		"b-a1-n1": true,
		"b-a1-n2": true,
		"b-a2-n1": true,
	}
	rng := rand.New(rand.NewSource(1))
	got, err := generateMemorableWith("b", taken, rng, adjs, ns)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "b-a2-n2" {
		t.Fatalf("want b-a2-n2, got %s", got)
	}
}

func TestGenerateMemorable_ErrorsWhenExhausted(t *testing.T) {
	adjs := []string{"a1"}
	ns := []string{"n1"}
	taken := map[string]bool{"b-a1-n1": true}
	rng := rand.New(rand.NewSource(1))
	if _, err := generateMemorableWith("b", taken, rng, adjs, ns); err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestGenerateMemorable_TruncatesLongBase(t *testing.T) {
	long := strings.Repeat("a", 70)
	rng := rand.New(rand.NewSource(42))
	got, err := GenerateMemorable(long, map[string]bool{}, rng)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) > maxNameLen {
		t.Fatalf("result too long: %d chars (%s)", len(got), got)
	}
	parts := strings.Split(got, "-")
	if len(parts) < 3 {
		t.Fatalf("expected adj-noun suffix, got %s", got)
	}
	lastTwo := parts[len(parts)-2:]
	if !contains(adjectives, lastTwo[0]) || !contains(nouns, lastTwo[1]) {
		t.Fatalf("adj-noun suffix damaged: %q, %q", lastTwo[0], lastTwo[1])
	}
}

func contains(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}
