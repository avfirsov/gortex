package hooks

import "testing"

func TestClassifyGrepPattern(t *testing.T) {
	tests := []struct {
		name    string
		pattern string
		want    GrepPatternClass
	}{
		{"bare identifier", "handleFoo", GrepPatternSymbol},
		{"snake_case", "find_usages", GrepPatternSymbol},
		{"dotted qualified", "pkg.Handler", GrepPatternSymbol},
		{"slash path", "internal/hooks", GrepPatternSymbol},
		{"leading underscore", "_internal", GrepPatternSymbol},
		{"mixed camel", "HTTPClient", GrepPatternSymbol},

		{"too short", "ab", GrepPatternSkip},
		{"empty", "", GrepPatternSkip},
		{"three-char identifier", "foo", GrepPatternSymbol},
		{"starts with digit", "3handler", GrepPatternSkip},
		{"regex metachar star", "hand.*", GrepPatternSkip},
		{"regex metachar brackets", "[abc]", GrepPatternSkip},
		{"regex metachar pipe", "foo|bar", GrepPatternSkip},
		{"regex anchor", "^main", GrepPatternSkip},
		{"multi-word prose", "handle foo", GrepPatternSkip},
		{"quoted", `"literal"`, GrepPatternSkip},
		{"parens", "func(", GrepPatternSkip},
		{"colon", "log:error", GrepPatternSkip},
		{"numeric literal", "12345", GrepPatternSkip},
		{"escape sequence", `\\bword`, GrepPatternSkip},
	}
	for _, tt := range tests {
		if got := classifyGrepPattern(tt.pattern); got != tt.want {
			t.Errorf("classifyGrepPattern(%q) = %v, want %v", tt.pattern, got, tt.want)
		}
	}
}
