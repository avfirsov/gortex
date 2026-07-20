package excludes

import "testing"

func TestBuiltinExcludesVersionControlMetadata(t *testing.T) {
	t.Parallel()

	matcher := New(Builtin)
	for _, path := range []string{
		".git/objects/pack/pack.data",
		".jj/repo/store/type",
		".hg/store/data/file.i",
		".svn/pristine/00/file.svn-base",
		".bzr/repository/pack-names",
		"_darcs/inventories/000000",
		".pijul/changes/ABC.change",
		".fossil-settings/ignore-glob",
		"nested/.jj/repo/store/type",
	} {
		if !matcher.MatchRel(path) {
			t.Errorf("Builtin should exclude VCS metadata path %q", path)
		}
	}
}

func TestBuiltinDoesNotExcludeSimilarSourcePaths(t *testing.T) {
	t.Parallel()

	matcher := New(Builtin)
	for _, path := range []string{
		"jj/repository.go",
		"pijul/client.go",
		"fossil-settings/parser.go",
		"darcs/model.go",
	} {
		if matcher.MatchRel(path) {
			t.Errorf("Builtin unexpectedly excludes source path %q", path)
		}
	}
}
