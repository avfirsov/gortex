package excludes

import "testing"

func TestIsVendored(t *testing.T) {
	tests := []struct {
		path string
		want bool
	}{
		// Repo-root-relative
		{"vendor/github.com/aws/aws-sdk-go/service.go", true},
		{"node_modules/react/index.js", true},
		{"Pods/AFNetworking/AFNetworking.m", true},
		{".venv/lib/python3.11/site-packages/requests/__init__.py", true},
		{"target/classes/Main.class", true},
		// Repo-prefixed
		{"myrepo/vendor/lib/x.go", true},
		{"tuck_app/Pods/sqlite3.c", true},
		{"daedalus/node_modules/foo/index.ts", true},
		// Absolute
		{"/Users/me/code/proj/vendor/x/y.go", true},
		// Not vendored
		{"internal/mcp/tools_enhancements.go", false},
		{"cmd/gortex/main.go", false},
		{"pkg/server.go", false},
		{"", false},
		// Partial name match must not trigger
		{"vendoring/notes.md", false},
		{"node_modulesx/foo.go", false},
		// Glob-derived entries must not match (*.tmp isn't a dir)
		{"foo.tmp", false},
	}
	for _, tt := range tests {
		if got := IsVendored(tt.path); got != tt.want {
			t.Errorf("IsVendored(%q) = %v, want %v", tt.path, got, tt.want)
		}
	}
}
