package audit

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// DefaultConfigPaths returns filenames and directories probed by default
// when no explicit file list is passed. Directory entries are expanded
// to any `*.md`/`*.mdc`/`*.txt` files they contain.
func DefaultConfigPaths() []string {
	return []string{
		"CLAUDE.md",
		"CLAUDE.local.md",
		"AGENTS.md",
		".cursorrules",
		".cursor/rules",
		".github/copilot-instructions.md",
		".windsurfrules",
		".windsurf/rules",
		".antigravity/rules",
		".aider.conf.yml",
	}
}

// DiscoverConfigFiles walks the default probe locations under root and
// returns the existing config files (relative to root).
func DiscoverConfigFiles(root string) []string {
	var out []string
	for _, entry := range DefaultConfigPaths() {
		abs := entry
		if !filepath.IsAbs(abs) {
			abs = filepath.Join(root, entry)
		}
		info, err := os.Stat(abs)
		if err != nil {
			continue
		}
		if !info.IsDir() {
			out = append(out, entry)
			continue
		}
		// Directory: collect supported config extensions.
		_ = filepath.WalkDir(abs, func(p string, d os.DirEntry, werr error) error {
			if werr != nil {
				return nil
			}
			if d.IsDir() {
				return nil
			}
			ext := strings.ToLower(filepath.Ext(p))
			switch ext {
			case ".md", ".mdc", ".txt", ".rules":
			default:
				return nil
			}
			rel, rerr := filepath.Rel(root, p)
			if rerr != nil {
				rel = p
			}
			out = append(out, rel)
			return nil
		})
	}

	sort.Strings(out)
	return uniqueStrings(out)
}

func uniqueStrings(xs []string) []string {
	seen := make(map[string]bool, len(xs))
	out := xs[:0]
	for _, x := range xs {
		if seen[x] {
			continue
		}
		seen[x] = true
		out = append(out, x)
	}
	return out
}
