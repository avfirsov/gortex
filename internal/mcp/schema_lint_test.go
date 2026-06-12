package mcp

import (
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
)

// TestToolSchemas_SpecCompliant is the release gate: it lints every
// tool Gortex registers against the MCP schema conventions. A failure
// here means a tool's name / description / input schema would be
// rejected (or render badly) in a strict MCP client — catch it before
// it ships. Lazy registration is disabled so the full surface is live
// and linted in a single pass.
func TestToolSchemas_SpecCompliant(t *testing.T) {
	t.Setenv("GORTEX_LAZY_TOOLS", "0")
	srv := newFullTestServer(t)

	violations := LintAllTools(srv)
	if len(violations) > 0 {
		for _, v := range violations {
			t.Errorf("schema violation: %s", v)
		}
		t.Fatalf("%d MCP tool-schema violation(s) — fix before release", len(violations))
	}

	if n := len(srv.mcpServer.ListTools()); n < 50 {
		t.Fatalf("only %d tools linted — the surface looks truncated", n)
	}
}

func TestLintToolSchema_CatchesViolations(t *testing.T) {
	cases := []struct {
		name string
		tool mcp.Tool
		rule string
	}{
		{
			name: "empty description",
			tool: mcp.NewTool("good_name"),
			rule: "description",
		},
		{
			name: "control char in description",
			tool: mcp.NewTool("good_name", mcp.WithDescription("bad\x00desc")),
			rule: "description",
		},
		{
			name: "uppercase name",
			tool: mcp.NewTool("BadName", mcp.WithDescription("fine")),
			rule: "name",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			vs := LintToolSchema(c.tool)
			found := false
			for _, v := range vs {
				if v.Rule == c.rule {
					found = true
				}
			}
			if !found {
				t.Errorf("expected a %q violation, got %v", c.rule, vs)
			}
		})
	}
}

func TestLintToolSchema_CleanToolPasses(t *testing.T) {
	tool := mcp.NewTool("clean_tool",
		mcp.WithDescription("A perfectly fine tool description."),
		mcp.WithString("arg", mcp.Description("a parameter"), mcp.Required()),
	)
	if vs := LintToolSchema(tool); len(vs) != 0 {
		t.Errorf("clean tool produced violations: %v", vs)
	}
}

func TestLintToolSchema_RequiredMustBeDeclared(t *testing.T) {
	tool := mcp.NewTool("probe", mcp.WithDescription("desc"))
	// Inject a required entry with no matching property.
	tool.InputSchema.Required = []string{"ghost"}
	vs := LintToolSchema(tool)
	found := false
	for _, v := range vs {
		if v.Rule == "required" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected a 'required' violation for an undeclared property, got %v", vs)
	}
}
