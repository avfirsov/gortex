// Package tsalias resolves TypeScript / JavaScript path-alias imports
// declared in `tsconfig.json` / `jsconfig.json` to repo-relative file
// paths the rest of the indexer can consume.
//
// Recognised shape:
//
//	{
//	  "compilerOptions": {
//	    "baseUrl": "./src",
//	    "paths": {
//	      "@/*": ["lib/*"],
//	      "@components/*": ["src/components/*"],
//	      "$utils": ["src/util/index.ts"]
//	    }
//	  }
//	}
//
// Resolution semantics follow the tsserver / Vite / Webpack consensus:
//
//   - Entries are matched longest-prefix-first so `@components/Button`
//     matches `@components/*` ahead of a hypothetical `@/*`.
//   - A single `*` wildcard splits the pattern into a prefix and a
//     suffix; the substring matched by `*` is slotted into the target
//     at the corresponding `*` position.
//   - Patterns without `*` are exact-match.
//   - Targets are joined with `baseUrl` (if set) and returned without
//     the trailing `.ts/.tsx/.js/.jsx/.mts/.cts` extension — callers
//     reuse the same probing logic as relative imports.
//   - Multi-target arrays (`"@/*": ["a/*", "b/*"]`) take the first
//     entry; resolving by disk existence would require a stat per
//     candidate and the first entry is the documented "primary" path.
//
// The package is intentionally narrow — no JSON-with-comments support,
// no `extends:` chain following, no monorepo `references[]` traversal.
// Those can be layered on without touching the resolver API.
package tsalias

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Alias is one entry in the `paths` map.
type Alias struct {
	// AliasPrefix is the portion of the source pattern before `*`
	// (or the full pattern when HasWildcard is false).
	AliasPrefix string
	// AliasSuffix is the portion after `*`. Usually empty.
	AliasSuffix string
	// TargetPrefix / TargetSuffix split the resolved value the same way.
	TargetPrefix string
	TargetSuffix string
	HasWildcard  bool
}

// Map is the alias set declared by one tsconfig/jsconfig file.
type Map struct {
	Entries []Alias
	// BaseURL is the relative path the targets resolve against. Empty
	// when the config didn't declare one — callers should treat
	// targets as repo-relative in that case.
	BaseURL string
	// DirPrefix is the repo-relative path of the config file's
	// directory. Used by Collection to pick the nearest ancestor scope.
	DirPrefix string
}

// Collection aggregates every alias map found by Load, sorted by
// DirPrefix length descending so nearest-ancestor lookup is a single
// linear scan.
type Collection struct {
	scopes []*Map
}

// Maps returns the underlying scope slice. Test-visibility only.
func (c *Collection) Maps() []*Map { return c.scopes }

// FindForFile returns the alias map for the nearest ancestor scope of
// relPath, or nil when no scope applies.
func (c *Collection) FindForFile(relPath string) *Map {
	if c == nil {
		return nil
	}
	relPath = filepath.ToSlash(relPath)
	for _, m := range c.scopes {
		if m.DirPrefix == "" {
			return m
		}
		if relPath == m.DirPrefix || strings.HasPrefix(relPath, m.DirPrefix+"/") {
			return m
		}
	}
	return nil
}

// Resolve maps modulePath against m's aliases and returns the
// repo-relative target (extension stripped) or "" when no entry
// matches. The returned path is forward-slashed and rooted at the
// repository root.
func Resolve(m *Map, modulePath string) string {
	if m == nil || modulePath == "" {
		return ""
	}
	for _, a := range m.Entries {
		var matched string
		if a.HasWildcard {
			if len(modulePath) < len(a.AliasPrefix)+len(a.AliasSuffix) {
				continue
			}
			if !strings.HasPrefix(modulePath, a.AliasPrefix) {
				continue
			}
			if !strings.HasSuffix(modulePath, a.AliasSuffix) {
				continue
			}
			star := modulePath[len(a.AliasPrefix) : len(modulePath)-len(a.AliasSuffix)]
			matched = a.TargetPrefix + star + a.TargetSuffix
		} else {
			if modulePath != a.AliasPrefix {
				continue
			}
			matched = a.TargetPrefix
		}
		// Join with BaseURL relative to the config's directory.
		joined := matched
		if m.BaseURL != "" {
			joined = filepath.ToSlash(filepath.Join(m.BaseURL, matched))
		}
		if m.DirPrefix != "" {
			joined = filepath.ToSlash(filepath.Join(m.DirPrefix, joined))
		}
		return stripExt(joined)
	}
	return ""
}

func stripExt(p string) string {
	switch ext := filepath.Ext(p); ext {
	case ".ts", ".tsx", ".js", ".jsx", ".mts", ".cts", ".mjs", ".cjs":
		return strings.TrimSuffix(p, ext)
	}
	return p
}

// Load walks repoRoot for tsconfig.json / jsconfig.json files and
// returns a Collection ready for FindForFile. Returns nil when the
// walk finds no usable configs. Walk errors on individual files are
// logged-by-skipping — a malformed tsconfig must not stop indexing.
//
// The walk respects a small allowlist of skip-dirs (node_modules, .git,
// vendor, build, dist) to keep cost bounded on large monorepos.
func Load(repoRoot string) *Collection {
	if repoRoot == "" {
		return nil
	}
	var scopes []*Map
	skipDirs := map[string]struct{}{
		"node_modules": {},
		".git":         {},
		".hg":          {},
		".svn":         {},
		"vendor":       {},
		"build":        {},
		"dist":         {},
		"target":       {},
		".next":        {},
		".nuxt":        {},
	}

	err := filepath.WalkDir(repoRoot, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, fs.ErrPermission) {
				if d != nil && d.IsDir() {
					return filepath.SkipDir
				}
				return nil
			}
			return nil
		}
		if d.IsDir() {
			if _, skip := skipDirs[d.Name()]; skip {
				return filepath.SkipDir
			}
			return nil
		}
		name := d.Name()
		if name != "tsconfig.json" && name != "jsconfig.json" {
			return nil
		}
		rel, relErr := filepath.Rel(repoRoot, p)
		if relErr != nil {
			return nil
		}
		dirRel := filepath.ToSlash(filepath.Dir(rel))
		if dirRel == "." {
			dirRel = ""
		}
		if m := parseConfigFile(p, dirRel); m != nil {
			scopes = append(scopes, m)
		}
		return nil
	})
	if err != nil || len(scopes) == 0 {
		return nil
	}
	sort.SliceStable(scopes, func(i, j int) bool {
		return len(scopes[i].DirPrefix) > len(scopes[j].DirPrefix)
	})
	return &Collection{scopes: scopes}
}

func parseConfigFile(absPath, dirPrefix string) *Map {
	data, err := os.ReadFile(absPath)
	if err != nil {
		return nil
	}
	var raw struct {
		CompilerOptions struct {
			BaseURL string              `json:"baseUrl"`
			Paths   map[string][]string `json:"paths"`
		} `json:"compilerOptions"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		// Quietly skip JSON-with-comments / malformed configs. A real
		// parser would be ~200 LOC; we accept the trade-off until a
		// user reports a Next.js / Nuxt config that fails this path.
		return nil
	}
	co := raw.CompilerOptions
	if co.BaseURL == "" && len(co.Paths) == 0 {
		return nil
	}
	m := &Map{
		BaseURL:   filepath.ToSlash(strings.TrimSpace(co.BaseURL)),
		DirPrefix: dirPrefix,
	}
	for pattern, targets := range co.Paths {
		if len(targets) == 0 {
			continue
		}
		entry, ok := splitAlias(pattern, targets[0])
		if !ok {
			continue
		}
		m.Entries = append(m.Entries, entry)
	}
	sort.SliceStable(m.Entries, func(i, j int) bool {
		return len(m.Entries[i].AliasPrefix) > len(m.Entries[j].AliasPrefix)
	})
	return m
}

func splitAlias(pattern, target string) (Alias, bool) {
	pStar := strings.Index(pattern, "*")
	tStar := strings.Index(target, "*")
	if pStar == -1 && tStar == -1 {
		return Alias{
			AliasPrefix:  pattern,
			TargetPrefix: target,
			HasWildcard:  false,
		}, true
	}
	if pStar == -1 || tStar == -1 {
		// Mismatched wildcards — tsserver rejects these.
		return Alias{}, false
	}
	return Alias{
		AliasPrefix:  pattern[:pStar],
		AliasSuffix:  pattern[pStar+1:],
		TargetPrefix: target[:tStar],
		TargetSuffix: target[tStar+1:],
		HasWildcard:  true,
	}, true
}
