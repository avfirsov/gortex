package eval

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"pgregory.net/rapid"
)

// Feature: eval-framework, Property 6: Cache key determinism and uniqueness

// --- Generators ---

// genRepoCommitPair generates a (repo, commit) pair with non-empty strings
// that don't contain underscores (to avoid ambiguity in the key format).
func genRepoCommitPair() *rapid.Generator[[2]string] {
	return rapid.Custom(func(t *rapid.T) [2]string {
		repo := rapid.StringMatching(`[a-zA-Z0-9\-\.\/]{1,50}`).Draw(t, "repo")
		commit := rapid.StringMatching(`[a-f0-9]{7,40}`).Draw(t, "commit")
		return [2]string{repo, commit}
	})
}

// genDistinctRepoCommitPairs generates two distinct (repo, commit) pairs.
func genDistinctRepoCommitPairs() *rapid.Generator[[2][2]string] {
	return rapid.Custom(func(t *rapid.T) [2][2]string {
		pair1 := genRepoCommitPair().Draw(t, "pair1")
		pair2 := genRepoCommitPair().Draw(t, "pair2")

		// Ensure the pairs are actually distinct
		for pair1[0] == pair2[0] && pair1[1] == pair2[1] {
			pair2 = genRepoCommitPair().Draw(t, "pair2_retry")
		}

		return [2][2]string{pair1, pair2}
	})
}

// --- Property Tests ---

// TestProperty6_CacheKeyDeterminism verifies that CacheKey always returns the
// same result for the same (repo, commit) inputs.
// **Validates: Requirements 4.4**
func TestProperty6_CacheKeyDeterminism(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		pair := genRepoCommitPair().Draw(t, "pair")
		repo, commit := pair[0], pair[1]

		key1 := CacheKey(repo, commit)
		key2 := CacheKey(repo, commit)
		key3 := CacheKey(repo, commit)

		assert.Equal(t, key1, key2, "CacheKey must be deterministic: first and second calls differ")
		assert.Equal(t, key1, key3, "CacheKey must be deterministic: first and third calls differ")
		assert.NotEmpty(t, key1, "CacheKey must produce a non-empty string")
	})
}

// TestProperty6_CacheKeyUniqueness verifies that two distinct (repo, commit)
// pairs always produce different cache keys.
// **Validates: Requirements 4.4**
func TestProperty6_CacheKeyUniqueness(t *testing.T) {
	rapid.Check(t, func(t *rapid.T) {
		pairs := genDistinctRepoCommitPairs().Draw(t, "pairs")
		repo1, commit1 := pairs[0][0], pairs[0][1]
		repo2, commit2 := pairs[1][0], pairs[1][1]

		key1 := CacheKey(repo1, commit1)
		key2 := CacheKey(repo2, commit2)

		assert.NotEqual(t, key1, key2,
			"CacheKey must produce different keys for distinct pairs: (%q, %q) vs (%q, %q)",
			repo1, commit1, repo2, commit2)
	})
}
