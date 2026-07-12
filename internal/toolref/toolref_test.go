package toolref

import (
	"strings"
	"testing"
)

func TestCLIFallbackUsesCompactPublicSurface(t *testing.T) {
	t.Parallel()

	tests := map[string]string{
		"read_file":         "gortex call read --arg target=",
		"get_symbol_source": "gortex call read --arg target=",
		"search_symbols":    "gortex call search --arg operation=symbols",
		"find_usages":       "gortex call relations --arg operation=usages",
		"smart_context":     "gortex call explore --arg operation=context",
		"edit_file":         "gortex call edit --arg target=",
		"index_repository":  "gortex call workspace_admin --arg operation=index",
	}
	for internal, want := range tests {
		internal, want := internal, want
		t.Run(internal, func(t *testing.T) {
			t.Parallel()
			got := CLIFallback(internal)
			if !strings.Contains(got, want) {
				t.Fatalf("CLIFallback(%q) = %q, want compact call containing %q", internal, got, want)
			}
			if strings.Contains(got, "gortex call "+internal+" ") {
				t.Fatalf("CLIFallback(%q) leaked the internal tool name: %q", internal, got)
			}
		})
	}
}

func TestFallbackLineIsConcreteAndRejectsBareVerbShape(t *testing.T) {
	t.Parallel()

	got := FallbackLine("get_repo_outline")
	for _, want := range []string{
		"gortex call explore --arg operation=outline",
		`--arg options='{"path_prefix":"<dir>/"}'`,
		"There is no bare `gortex <tool>` verb",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("FallbackLine() = %q, want %q", got, want)
		}
	}
}

func TestCLIFallbackPlaceholdersAreShellQuoted(t *testing.T) {
	t.Parallel()
	for name, example := range cliExample {
		if strings.Contains(example, "=<") {
			t.Errorf("cliExample[%q] contains an unquoted shell-redirection placeholder: %s", name, example)
		}
	}
}
