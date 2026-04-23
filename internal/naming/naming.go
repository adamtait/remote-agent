package naming

import (
	"fmt"
	"math/rand"
	"strconv"
)

const maxNameLen = 63

// GenerateMemorable returns "<base>-<adj>-<noun>", retrying up to 100 times on
// collision with taken. Truncates the base so the full name fits within 63
// chars (GCP workstation name limit) without damaging the adj-noun suffix.
func GenerateMemorable(base string, taken map[string]bool, rng *rand.Rand) (string, error) {
	return generateMemorableWith(base, taken, rng, adjectives, nouns)
}

func generateMemorableWith(base string, taken map[string]bool, rng *rand.Rand, adjs, ns []string) (string, error) {
	const maxAttempts = 100
	for i := 0; i < maxAttempts; i++ {
		adj := adjs[rng.Intn(len(adjs))]
		noun := ns[rng.Intn(len(ns))]
		suffix := "-" + adj + "-" + noun
		name := truncateBase(base, len(suffix)) + suffix
		if !taken[name] {
			return name, nil
		}
	}
	return "", fmt.Errorf("could not generate a unique memorable name after %d attempts", maxAttempts)
}

// UniqueSuffixed returns base if free; otherwise base-1, base-2, ... Truncates
// to fit within 63 chars once the suffix is appended.
func UniqueSuffixed(base string, taken map[string]bool) string {
	bare := truncateBase(base, 0)
	if !taken[bare] {
		return bare
	}
	for i := 1; ; i++ {
		suffix := "-" + strconv.Itoa(i)
		name := truncateBase(base, len(suffix)) + suffix
		if !taken[name] {
			return name
		}
	}
}

// truncateBase shortens base so len(base)+suffixLen <= maxNameLen and strips
// trailing hyphens so we never emit "foo--bar".
func truncateBase(base string, suffixLen int) string {
	limit := maxNameLen - suffixLen
	if len(base) > limit {
		base = base[:limit]
	}
	for len(base) > 0 && base[len(base)-1] == '-' {
		base = base[:len(base)-1]
	}
	return base
}
