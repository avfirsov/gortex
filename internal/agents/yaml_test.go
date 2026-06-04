package agents

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	yaml "gopkg.in/yaml.v3"
)

// gortexEntryNode builds the stdio MCP stanza Hermes expects, used
// across the YAML merge tests.
func gortexEntryNode() *yaml.Node {
	entry := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	YAMLSetMapValue(entry, "command", YAMLScalar("gortex"))
	args := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq", Content: []*yaml.Node{YAMLScalar("mcp")}}
	YAMLSetMapValue(entry, "args", args)
	return entry
}

func upsertGortex(force bool) func(root *yaml.Node, existed bool) (bool, error) {
	return func(root *yaml.Node, _ bool) (bool, error) {
		return UpsertYAMLMapEntry(root, "mcp_servers", "gortex", gortexEntryNode(), force)
	}
}

func TestMergeYAML_CreatesFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	action, err := MergeYAML(nil, path, upsertGortex(false), ApplyOpts{})
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	if action.Action != ActionCreate {
		t.Fatalf("want create, got %s", action.Action)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	var root map[string]any
	if err := yaml.Unmarshal(data, &root); err != nil {
		t.Fatalf("written file is not valid YAML: %v\n%s", err, data)
	}
	servers, ok := root["mcp_servers"].(map[string]any)
	if !ok {
		t.Fatalf("mcp_servers missing or wrong type: %#v", root)
	}
	gortex, ok := servers["gortex"].(map[string]any)
	if !ok {
		t.Fatalf("gortex server missing: %#v", servers)
	}
	if gortex["command"] != "gortex" {
		t.Errorf("command = %v, want gortex", gortex["command"])
	}
}

func TestMergeYAML_Idempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	if _, err := MergeYAML(nil, path, upsertGortex(false), ApplyOpts{}); err != nil {
		t.Fatalf("first merge: %v", err)
	}
	action, err := MergeYAML(nil, path, upsertGortex(false), ApplyOpts{})
	if err != nil {
		t.Fatalf("second merge: %v", err)
	}
	if action.Action != ActionSkip {
		t.Fatalf("re-run want skip, got %s", action.Action)
	}
}

func TestMergeYAML_PreservesCommentsAndKeys(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	original := `# Hermes configuration — edit with care
model: hermes-4

# remote tool servers
mcp_servers:
  # GitHub MCP — issues and PRs
  github:
    command: npx
    args: ["-y", "@modelcontextprotocol/server-github"]

# delegation knobs the user tuned by hand
delegation:
  max_depth: 3 # do not raise without measuring
`
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	action, err := MergeYAML(nil, path, upsertGortex(false), ApplyOpts{})
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	if action.Action != ActionMerge {
		t.Fatalf("want merge, got %s", action.Action)
	}

	out, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	got := string(out)

	// Comments must survive the round-trip.
	for _, want := range []string{
		"# Hermes configuration — edit with care",
		"# remote tool servers",
		"# GitHub MCP — issues and PRs",
		"# delegation knobs the user tuned by hand",
		"do not raise without measuring",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("lost comment %q after merge:\n%s", want, got)
		}
	}

	// Existing servers + unrelated keys must survive, gortex must be added.
	var root map[string]any
	if err := yaml.Unmarshal(out, &root); err != nil {
		t.Fatalf("merged file is not valid YAML: %v", err)
	}
	if root["model"] != "hermes-4" {
		t.Errorf("unrelated key model clobbered: %v", root["model"])
	}
	servers := root["mcp_servers"].(map[string]any)
	if _, ok := servers["github"]; !ok {
		t.Errorf("pre-existing github server dropped: %#v", servers)
	}
	if _, ok := servers["gortex"]; !ok {
		t.Errorf("gortex server not added: %#v", servers)
	}
}

func TestMergeYAML_MalformedBacksUp(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	// Tab indentation is a hard YAML parse error.
	bad := "mcp_servers:\n\t\tgithub: bad\n  : : :\n"
	if err := os.WriteFile(path, []byte(bad), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	action, err := MergeYAML(nil, path, upsertGortex(false), ApplyOpts{})
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	if action.Action != ActionMerge {
		t.Fatalf("want merge (file existed), got %s", action.Action)
	}

	if _, err := os.Stat(path + ".bak"); err != nil {
		t.Errorf("expected .bak of malformed input: %v", err)
	}
	out, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	var root map[string]any
	if err := yaml.Unmarshal(out, &root); err != nil {
		t.Fatalf("recovered file is not valid YAML: %v\n%s", err, out)
	}
	if _, ok := root["mcp_servers"].(map[string]any)["gortex"]; !ok {
		t.Errorf("gortex not written into recovered file: %#v", root)
	}
}

func TestMergeYAML_DryRunNoWrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	action, err := MergeYAML(nil, path, upsertGortex(false), ApplyOpts{DryRun: true})
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	if action.Action != ActionWouldCreate {
		t.Fatalf("want would-create, got %s", action.Action)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("dry-run must not write the file")
	}
}

// TestMergeYAML_DryRunMalformedNoBackup guards that --dry-run never
// mutates disk even when the existing file is malformed: the .bak of a
// malformed input is deferred to a real apply, so a plan-only run leaves
// both the file and its sibling untouched.
func TestMergeYAML_DryRunMalformedNoBackup(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	bad := "mcp_servers:\n\t\tgithub: bad\n  : : :\n"
	if err := os.WriteFile(path, []byte(bad), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}

	action, err := MergeYAML(nil, path, upsertGortex(false), ApplyOpts{DryRun: true})
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	if action.Action != ActionWouldMerge {
		t.Fatalf("want would-merge, got %s", action.Action)
	}
	if _, err := os.Stat(path + ".bak"); !os.IsNotExist(err) {
		t.Errorf("dry-run must not write a .bak (stat err = %v)", err)
	}
	out, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if string(out) != bad {
		t.Errorf("dry-run mutated the original file:\n%q", out)
	}
}

// TestMergeYAML_NonMapOuterErrors guards that a non-null, non-mapping
// value under the outer key (a stray scalar or sequence) is treated as
// schema-invalid and refused — never silently swapped for a fresh map
// that drops the user's data.
func TestMergeYAML_NonMapOuterErrors(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
	}{
		{"scalar", "mcp_servers: nonsense\n"},
		{"sequence", "mcp_servers:\n  - a\n  - b\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.yaml")
			if err := os.WriteFile(path, []byte(tc.body), 0o644); err != nil {
				t.Fatalf("seed: %v", err)
			}
			if _, err := MergeYAML(nil, path, upsertGortex(false), ApplyOpts{}); err == nil {
				t.Fatal("expected an error for non-mapping mcp_servers, got nil")
			}
			out, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read back: %v", err)
			}
			if string(out) != tc.body {
				t.Errorf("file mutated despite refusal:\n%q", out)
			}
			if _, err := os.Stat(path + ".bak"); !os.IsNotExist(err) {
				t.Errorf("refusal must not write a .bak")
			}
		})
	}
}

// TestMergeYAML_NullOuterPopulated covers the benign case: an empty /
// null `mcp_servers:` (e.g. every server commented out) is populated in
// place rather than erroring, and the key's leading comment survives.
func TestMergeYAML_NullOuterPopulated(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	original := "# servers (all disabled for now)\nmcp_servers:\n"
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	action, err := MergeYAML(nil, path, upsertGortex(false), ApplyOpts{})
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	if action.Action != ActionMerge {
		t.Fatalf("want merge, got %s", action.Action)
	}
	out, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if !strings.Contains(string(out), "# servers (all disabled for now)") {
		t.Errorf("lost leading comment on null key:\n%s", out)
	}
	var root map[string]any
	if err := yaml.Unmarshal(out, &root); err != nil {
		t.Fatalf("not valid YAML: %v\n%s", err, out)
	}
	servers, ok := root["mcp_servers"].(map[string]any)
	if !ok {
		t.Fatalf("mcp_servers not populated: %#v", root)
	}
	if _, ok := servers["gortex"]; !ok {
		t.Errorf("gortex not added: %#v", servers)
	}
}

// TestMergeYAML_PreservesIndentWidth verifies a 4-space config is
// re-emitted at 4 spaces rather than forced to 2 (detectYAMLIndent).
func TestMergeYAML_PreservesIndentWidth(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	original := "mcp_servers:\n    github:\n        command: npx\n"
	if err := os.WriteFile(path, []byte(original), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, err := MergeYAML(nil, path, upsertGortex(false), ApplyOpts{}); err != nil {
		t.Fatalf("merge: %v", err)
	}
	out, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	got := string(out)
	if !strings.Contains(got, "\n    github:") {
		t.Errorf("4-space indent not preserved:\n%s", got)
	}
	if strings.Contains(got, "\n  github:") {
		t.Errorf("indent collapsed to 2 spaces:\n%s", got)
	}
}
