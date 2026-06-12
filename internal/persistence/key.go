package persistence

import (
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"
	"strings"

	"github.com/zzet/gortex/internal/pathkey"
)

// CacheKey produces a filesystem-safe directory name identifying one
// snapshot slot. Snapshots are keyed by (repo, branch): one slot per
// branch, overwritten as the branch advances. A daemon restart after
// new commits then loads the branch's last snapshot and incrementally
// reconciles, instead of cold-indexing because the commit hash moved.
// A detached HEAD (empty branch) falls back to the commit hash so each
// checked-out commit still gets a stable slot.
//
// Both the repo path and the ref are folded to Unicode NFC before
// hashing: a non-ASCII path or branch name presents with different
// bytes on macOS (NFD) than on Linux / git (NFC), and without the
// fold the same repo would key into two distinct snapshot slots.
func CacheKey(repoPath, branch, commitHash string) string {
	abs, err := filepath.Abs(repoPath)
	if err != nil {
		abs = repoPath
	}
	h := sha256.Sum256([]byte(pathkey.Normalize(abs)))
	pathPart := hex.EncodeToString(h[:6])

	ref := strings.TrimSpace(branch)
	if ref == "" || ref == "HEAD" {
		ref = strings.TrimSpace(commitHash)
	}
	return pathPart + "_" + refSlug(ref)
}

// refSlug renders a git ref — a branch name or a commit hash — as a
// stable, filesystem-safe path segment: a readable sanitized prefix
// plus a short hash of the full ref, so two refs that sanitize or
// truncate to the same prefix (e.g. feature/x vs feature-x) still get
// distinct slots.
func refSlug(ref string) string {
	if ref == "" {
		return "none"
	}
	// Fold to NFC first: a non-ASCII branch name read in decomposed
	// form on one platform and precomposed form on another would
	// otherwise hash to two different slugs for the same branch.
	ref = pathkey.Normalize(ref)
	var b strings.Builder
	for _, r := range ref {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z',
			r >= '0' && r <= '9', r == '.', r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
		if b.Len() >= 32 {
			break
		}
	}
	h := sha256.Sum256([]byte(ref))
	return b.String() + "_" + hex.EncodeToString(h[:4])
}
