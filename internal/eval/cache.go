package eval

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/zzet/gortex/internal/platform"
)

// Cache provides filesystem-based index caching keyed by (repo_name, commit_hash).
// Cache entries are stored under {cacheDir}/{repo_name}_{commit_hash}/ and contain
// a .version file, graph.bin, and search.bleve/ directory.
type Cache struct {
	dir     string // root cache directory
	version string // current gortex version for compatibility checks
}

// NewCache creates a Cache rooted at dir with the given gortex version string.
//
// If dir is empty the location is resolved by env: when $XDG_CACHE_HOME is
// set it is honoured ($XDG_CACHE_HOME/gortex/eval-cache); otherwise the
// historical default ~/.gortex-eval-cache/ is kept so an existing eval
// cache is not orphaned.
func NewCache(dir, version string) (*Cache, error) {
	if dir == "" {
		if v := os.Getenv("XDG_CACHE_HOME"); v != "" && filepath.IsAbs(v) {
			dir = filepath.Join(platform.CacheDir(), "eval-cache")
		} else {
			home, err := os.UserHomeDir()
			if err != nil {
				return nil, fmt.Errorf("cache: resolve home dir: %w", err)
			}
			dir = filepath.Join(home, ".gortex-eval-cache")
		}
	}
	return &Cache{dir: dir, version: version}, nil
}

// CacheKey generates the cache directory name for a (repo, commit) pair.
// The key format is {repo}_{commit}.
func CacheKey(repo, commit string) string {
	return repo + "_" + commit
}

// entryDir returns the full path to the cache entry directory.
func (c *Cache) entryDir(repo, commit string) string {
	return filepath.Join(c.dir, CacheKey(repo, commit))
}

// versionFile returns the path to the .version file inside a cache entry.
func (c *Cache) versionFile(repo, commit string) string {
	return filepath.Join(c.entryDir(repo, commit), ".version")
}

// Check returns true if a cached index exists for the (repo, commit) pair.
// It verifies the entry directory exists and contains the expected files.
func (c *Cache) Check(repo, commit string) bool {
	dir := c.entryDir(repo, commit)
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		return false
	}
	// Verify .version file exists.
	if _, err := os.Stat(c.versionFile(repo, commit)); err != nil {
		return false
	}
	return true
}

// Load returns the path to the cached index directory for the (repo, commit) pair.
// Returns an error if the cache entry does not exist.
func (c *Cache) Load(repo, commit string) (string, error) {
	dir := c.entryDir(repo, commit)
	info, err := os.Stat(dir)
	if err != nil {
		return "", fmt.Errorf("cache: entry not found for %s: %w", CacheKey(repo, commit), err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("cache: entry %s is not a directory", CacheKey(repo, commit))
	}
	return dir, nil
}

// Store persists an index directory into the cache for the (repo, commit) pair.
// It copies the contents of indexPath (graph.bin, search.bleve/) into the cache
// entry directory and writes a .version file with the current gortex version.
func (c *Cache) Store(repo, commit, indexPath string) error {
	dir := c.entryDir(repo, commit)

	// Remove any existing entry to ensure a clean store.
	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("cache: remove existing entry: %w", err)
	}

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("cache: create entry dir: %w", err)
	}

	// Copy contents from indexPath into the cache entry.
	if err := copyDir(indexPath, dir); err != nil {
		// Clean up on failure.
		_ = os.RemoveAll(dir)
		return fmt.Errorf("cache: copy index: %w", err)
	}

	// Write .version file.
	if err := os.WriteFile(c.versionFile(repo, commit), []byte(c.version), 0o644); err != nil {
		_ = os.RemoveAll(dir)
		return fmt.Errorf("cache: write version file: %w", err)
	}

	return nil
}

// Validate checks whether the cached index for (repo, commit) is compatible
// with the current gortex version by comparing the .version file contents.
func (c *Cache) Validate(repo, commit string) bool {
	data, err := os.ReadFile(c.versionFile(repo, commit))
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(data)) == c.version
}

// Evict removes the cached index entry for the (repo, commit) pair.
func (c *Cache) Evict(repo, commit string) error {
	dir := c.entryDir(repo, commit)
	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("cache: evict %s: %w", CacheKey(repo, commit), err)
	}
	return nil
}

// copyDir recursively copies the contents of src into dst.
// dst must already exist. Only regular files and directories are copied.
func copyDir(src, dst string) error {
	entries, err := os.ReadDir(src)
	if err != nil {
		return err
	}

	for _, entry := range entries {
		srcPath := filepath.Join(src, entry.Name())
		dstPath := filepath.Join(dst, entry.Name())

		if entry.IsDir() {
			if err := os.MkdirAll(dstPath, 0o755); err != nil {
				return err
			}
			if err := copyDir(srcPath, dstPath); err != nil {
				return err
			}
		} else {
			if err := copyFile(srcPath, dstPath); err != nil {
				return err
			}
		}
	}
	return nil
}

// copyFile copies a single file from src to dst, preserving permissions.
func copyFile(src, dst string) error {
	srcFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer srcFile.Close()

	srcInfo, err := srcFile.Stat()
	if err != nil {
		return err
	}

	dstFile, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, srcInfo.Mode())
	if err != nil {
		return err
	}
	defer dstFile.Close()

	_, err = io.Copy(dstFile, srcFile)
	return err
}
