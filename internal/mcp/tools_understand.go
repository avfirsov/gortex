package mcp

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"time"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/zzet/gortex/internal/exporter"
)

// registerUnderstandTools wires the L1 Understand-Anything exporter
// (exporter.WriteUnderstandAnything) onto the MCP tool surface as
// export_understand. Registering it here is sufficient to also serve
// POST /v1/tools/export_understand: the daemon's HTTP handler dispatches
// against this same MCP registry (internal/server/handler.go handleToolCall →
// mcpServer.GetTool), so no separate HTTP code is needed. It mirrors
// registerExportTools.
func (s *Server) registerUnderstandTools() {
	s.addTool(
		mcp.NewTool("export_understand",
			mcp.WithDescription("Export the code graph as an Understand-Anything knowledge graph (understand-anything@1, or generic@1). With output_path the daemon writes the file; otherwise it is returned inline."),
			mcp.WithString("granularity", mcp.Description("slim (default, drops high-cardinality kinds) | full (keeps them as concept)")),
			mcp.WithBoolean("generic", mcp.Description("Emit the reduced generic@1 {nodes, edges} projection instead of the full understand-anything@1 envelope")),
			mcp.WithString("repo", mcp.Description("Filter to one repo prefix (default: all)")),
			mcp.WithString("project_name", mcp.Description("Override the UA project name (default: empty)")),
			mcp.WithString("output_path", mcp.Description("Absolute path to write the export to; omit to return inline")),
		),
		s.handleExportUnderstand,
	)
}

// handleExportUnderstand is the Action-layer controller for export_understand.
// It builds exporter.UAOptions from the request arguments — supplying the two
// time-varying inputs (AnalyzedAt and GitCommit) that the pure exporter core
// deliberately refuses to derive itself — and calls WriteUnderstandAnything.
// With output_path it writes the file and returns a stats summary (mirroring
// handleExportGraph); otherwise it returns the rendered JSON inline.
func (s *Server) handleExportUnderstand(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	g := s.graph
	if g == nil {
		return mcp.NewToolResultError("export: graph is not initialised"), nil
	}
	args := req.GetArguments()

	opts := exporter.UAOptions{
		Options:     exporter.Options{Repo: stringArg(args, "repo")},
		Granularity: stringArgOrDefault(args, "granularity", exporter.GranularitySlim),
		Generic:     boolArgValue(args, "generic"),
		ProjectName: stringArg(args, "project_name"),
		// AnalyzedAt and GitCommit live HERE, in the Action layer — never in
		// the pure exporter core (business_requirements §12 MUST NOT).
		AnalyzedAt: time.Now().UTC().Format(time.RFC3339),
		GitCommit:  "", // best-effort: no reachable single-repo commit helper; "" is UA-valid since the gitCommitHash struct-tag fix.
	}

	var buf bytes.Buffer
	st, err := exporter.WriteUnderstandAnything(&buf, g, opts)
	if err != nil {
		return mcp.NewToolResultError(err.Error()), nil
	}

	if outputPath := stringArg(args, "output_path"); outputPath != "" {
		if err := os.WriteFile(outputPath, buf.Bytes(), 0o644); err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("write %q: %v", outputPath, err)), nil
		}
		return s.respondJSONOrTOON(ctx, req, map[string]any{
			"output_path": outputPath,
			"nodes":       st.NodesWritten,
			"edges":       st.EdgesWritten,
			"bytes":       st.BytesWritten,
		})
	}
	// Inline — the rendered UA JSON is the text content.
	return mcp.NewToolResultText(buf.String()), nil
}
