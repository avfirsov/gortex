package persistence

import (
	"strings"
	"testing"

	"golang.org/x/text/unicode/norm"
)

// nfcDir / nfdDir are the precomposed and decomposed byte forms of the
// same accented repo path. They are derived in code with norm.NFC /
// norm.NFD from a single base so the two byte sequences are guaranteed
// distinct and deterministic — never dependent on how this source file
// happens to be saved. On macOS APFS a repo cloned into such a
// directory is reported in NFD by the OS, while the same path written
// into a config file or read on Linux is typically NFC; a snapshot
// keyed under one form must resolve to the same slot under the other.
//
// The non-ASCII characters in the base strings are written as explicit
// \u / \U escapes so this source file stays pure-ASCII.
var (
	repoDirBase = "/home/dev/café/repo" // .../café/repo
	nfcDir      = norm.NFC.String(repoDirBase)
	nfdDir      = norm.NFD.String(repoDirBase)
)

const (
	cjkBranch  = "機能/日本語"        // 機能/日本語
	cyrBranch  = "функция-ветка" // функция-ветка
	asciiPath  = "/home/dev/plain/repo"
	asciiBr    = "feat/some-branch"
	commitHash = "0123456789abcdef0123456789abcdef01234567"
)

// TestCacheKey_NFCvsNFDPathSameSlot is the core round-trip guarantee:
// the same repo path supplied in decomposed and precomposed Unicode
// forms must hash to one snapshot slot. Without the NFC fold in
// CacheKey the two byte sequences hash differently and the daemon
// loses its cache across an OS / form boundary.
func TestCacheKey_NFCvsNFDPathSameSlot(t *testing.T) {
	if nfcDir == nfdDir {
		t.Fatal("test fixture invalid: NFC and NFD repo paths are byte-identical")
	}
	keyNFC := CacheKey(nfcDir, "main", commitHash)
	keyNFD := CacheKey(nfdDir, "main", commitHash)
	if keyNFC != keyNFD {
		t.Fatalf("CacheKey not normalisation-stable for repo path:\n NFC -> %q\n NFD -> %q", keyNFC, keyNFD)
	}
}

// TestCacheKey_NFCvsNFDBranchSameSlot guards the branch half of the
// key: a non-ASCII branch name in two Unicode forms must land in one
// slot, so the daemon does not split a single branch's snapshot in two.
func TestCacheKey_NFCvsNFDBranchSameSlot(t *testing.T) {
	branchNFD := norm.NFD.String(cjkBranch)
	branchNFC := norm.NFC.String(cjkBranch)
	keyNFD := CacheKey(asciiPath, branchNFD, commitHash)
	keyNFC := CacheKey(asciiPath, branchNFC, commitHash)
	if keyNFD != keyNFC {
		t.Fatalf("CacheKey not normalisation-stable for branch:\n NFD -> %q\n NFC -> %q", keyNFD, keyNFC)
	}
}

// TestCacheKey_DistinctReposDistinctSlots confirms the fold does not
// over-collapse: two genuinely different non-ASCII repo paths must
// still get distinct slots.
func TestCacheKey_DistinctReposDistinctSlots(t *testing.T) {
	a := CacheKey(nfcDir, "main", commitHash)
	b := CacheKey(asciiPath, "main", commitHash)
	if a == b {
		t.Fatalf("CacheKey collided two different repos onto slot %q", a)
	}
}

// TestCacheKey_DistinctBranchesDistinctSlots confirms two different
// non-ASCII branch names on the same repo do not collide — the
// collision-safety hash inside refSlug must survive normalisation.
func TestCacheKey_DistinctBranchesDistinctSlots(t *testing.T) {
	a := CacheKey(asciiPath, cjkBranch, commitHash)
	b := CacheKey(asciiPath, cyrBranch, commitHash)
	if a == b {
		t.Fatalf("CacheKey collided two different branches onto slot %q", a)
	}
}

// TestCacheKey_FilesystemSafe checks the produced key is usable as a
// directory name: no path separators, no NUL, non-empty — true even
// when the branch is entirely non-ASCII (refSlug replaces every such
// rune, leaving the hash suffix to carry identity).
func TestCacheKey_FilesystemSafe(t *testing.T) {
	for _, branch := range []string{cjkBranch, cyrBranch, "main", ""} {
		key := CacheKey(nfdDir, branch, commitHash)
		if key == "" {
			t.Fatalf("CacheKey returned empty string for branch %q", branch)
		}
		if strings.ContainsAny(key, "/\\\x00") {
			t.Fatalf("CacheKey %q for branch %q contains a path-unsafe byte", key, branch)
		}
	}
}

// TestCacheKey_ASCIIUnchangedByFold pins that the NFC fold is a no-op
// for the common all-ASCII case — the keys for ASCII inputs must not
// shift, so existing on-disk snapshot directories stay valid.
func TestCacheKey_ASCIIUnchangedByFold(t *testing.T) {
	// Recomputed twice must be stable, and an ASCII path/branch must
	// survive the fold byte-for-byte (the slug prefix is visible in
	// the key, so a regression here would change the directory name).
	key1 := CacheKey(asciiPath, asciiBr, commitHash)
	key2 := CacheKey(asciiPath, asciiBr, commitHash)
	if key1 != key2 {
		t.Fatalf("CacheKey not deterministic for ASCII input: %q vs %q", key1, key2)
	}
	if !strings.Contains(key1, "feat-some-branch") {
		t.Fatalf("CacheKey %q lost its readable ASCII branch slug", key1)
	}
}

// TestCacheKey_DetachedHeadFallsBackToCommit checks the detached-HEAD
// path still works once the fold is in place: an empty branch keys by
// commit hash.
func TestCacheKey_DetachedHeadFallsBackToCommit(t *testing.T) {
	withBranch := CacheKey(asciiPath, "main", commitHash)
	detached := CacheKey(asciiPath, "", commitHash)
	headLiteral := CacheKey(asciiPath, "HEAD", commitHash)
	if detached == withBranch {
		t.Fatal("detached-HEAD key collided with a real branch key")
	}
	if detached != headLiteral {
		t.Fatalf("empty branch and literal HEAD must key alike: %q vs %q", detached, headLiteral)
	}
}

// TestRepoCacheKey_NFCvsNFDSameSlot mirrors the CacheKey round-trip
// guarantee for the commit-independent feedback key.
func TestRepoCacheKey_NFCvsNFDSameSlot(t *testing.T) {
	if nfcDir == nfdDir {
		t.Fatal("test fixture invalid: NFC and NFD repo paths are byte-identical")
	}
	keyNFC := RepoCacheKey(nfcDir)
	keyNFD := RepoCacheKey(nfdDir)
	if keyNFC != keyNFD {
		t.Fatalf("RepoCacheKey not normalisation-stable:\n NFC -> %q\n NFD -> %q", keyNFC, keyNFD)
	}
}

// TestRepoCacheKey_DistinctReposDistinctSlots confirms the feedback
// key does not over-collapse distinct non-ASCII repos.
func TestRepoCacheKey_DistinctReposDistinctSlots(t *testing.T) {
	a := RepoCacheKey(nfcDir)
	b := RepoCacheKey(asciiPath)
	if a == b {
		t.Fatalf("RepoCacheKey collided two different repos onto slot %q", a)
	}
}
