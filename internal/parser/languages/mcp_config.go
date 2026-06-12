package languages

import (
	"encoding/json"
	"path/filepath"
	"strings"

	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/modules"
	"github.com/zzet/gortex/internal/parser"
)

// MCPConfigExtractor ingests external MCP-server configuration files —
// a repo-root `.mcp.json`, a `.cursor/mcp.json` / `.kiro/mcp.json`, a
// VS Code `mcp.json`, or a `claude_desktop_config.json` — into the graph
// as first-class nodes so an agent can traverse the supply chain of the
// MCP servers a project (or a developer's machine) wires up.
//
// The canonical shape is:
//
//	{"mcpServers": {"<name>": {"command":"npx","args":["-y","@scope/pkg"],
//	                           "env":{"VAR":"val"},"type":"stdio"}}}
//
// VS Code uses a top-level "servers" key with the same per-server shape;
// Kiro adds tolerated extras (disabled, autoApprove). Both are accepted.
//
// For each server the extractor emits:
//
//   - a KindResource node (resource_type "mcp_server"), EdgeDefines from
//     the file.
//   - when the launch command is a package runner, the package it pulls:
//     npx/npm/pnpm/bunx → an npm KindModule, uvx/pipx/uv → a pypi
//     KindModule (both via modules.ModuleNodeID + EdgeDependsOnModule);
//     docker → a KindImage node (image::<ref>:<tag>, reusing the shared
//     Dockerfile/K8s ID scheme) + EdgeDependsOn.
//   - for every declared env var and every "${VAR}" / "$VAR" arg
//     interpolation: a KindConfigKey node with the shared
//     `cfg::env::<NAME>` ID (so it shares identity with code-side
//     os.Getenv("NAME") reads and infra EdgeUsesEnv declarations) +
//     EdgeUsesEnv from the server.
//
// No new graph node or edge kinds are introduced — every emitted node
// reuses KindResource / KindModule / KindImage / KindConfigKey / KindFile
// and every edge reuses EdgeDefines / EdgeUsesEnv / EdgeDependsOnModule /
// EdgeDependsOn.
type MCPConfigExtractor struct{}

// NewMCPConfigExtractor constructs an MCPConfigExtractor.
func NewMCPConfigExtractor() *MCPConfigExtractor { return &MCPConfigExtractor{} }

func (e *MCPConfigExtractor) Language() string { return "mcp_config" }

// Extensions claims ONLY the specific MCP-config files by name. ".mcp.json"
// is a compound extension (matched against a file's last-two-segment
// suffix, e.g. a repo-root `.mcp.json`); "mcp.json" and
// "claude_desktop_config.json" are exact basenames (matched against the
// file's basename, e.g. `.cursor/mcp.json`, `.kiro/mcp.json`, or a
// `~/Library/.../claude_desktop_config.json`). It deliberately does NOT
// include the bare ".json" extension, so a normal `package.json` /
// `tsconfig.json` still routes to the generic JSON extractor.
func (e *MCPConfigExtractor) Extensions() []string {
	return []string{".mcp.json", "mcp.json", "claude_desktop_config.json"}
}

// mcpServerSpec is the tolerant decode target for a single server entry.
// Unknown keys (disabled, autoApprove, timeout, …) are ignored by
// encoding/json. Every field is optional.
type mcpServerSpec struct {
	Command   string            `json:"command"`
	Args      []string          `json:"args"`
	Env       map[string]string `json:"env"`
	URL       string            `json:"url"`
	Type      string            `json:"type"`
	Transport string            `json:"transport"`
}

// mcpConfigFile decodes the two accepted top-level layouts. Server order
// is recovered from the raw object so emission is deterministic.
type mcpConfigFile struct {
	MCPServers map[string]json.RawMessage `json:"mcpServers"`
	Servers    map[string]json.RawMessage `json:"servers"`
}

func (e *MCPConfigExtractor) Extract(filePath string, src []byte) (*parser.ExtractionResult, error) {
	result := &parser.ExtractionResult{}

	lineCount := strings.Count(string(src), "\n") + 1
	base := filepath.Base(filePath)
	fileNode := &graph.Node{
		ID: filePath, Kind: graph.KindFile, Name: filePath,
		FilePath: filePath, StartLine: 1, EndLine: lineCount,
		Language: "mcp_config",
	}
	result.Nodes = append(result.Nodes, fileNode)

	var cfg mcpConfigFile
	if err := json.Unmarshal(src, &cfg); err != nil {
		// Malformed JSON — still record the file node so the path is
		// queryable; an extractor must not fail the whole index for one
		// bad config.
		return result, nil
	}

	servers := cfg.MCPServers
	if len(servers) == 0 {
		servers = cfg.Servers
	}

	// Sort server names for deterministic emission order.
	names := make([]string, 0, len(servers))
	for name := range servers {
		names = append(names, name)
	}
	sortStrings(names)

	// Dedup module / image / env nodes within this file — graph.AddNode
	// is idempotent on ID anyway, but we avoid emitting duplicates.
	seenModule := make(map[string]bool)
	seenImage := make(map[string]bool)
	seenEnv := make(map[string]bool)

	for _, name := range names {
		var spec mcpServerSpec
		if err := json.Unmarshal(servers[name], &spec); err != nil {
			continue
		}

		serverID := "mcp::server::" + filePath + "::" + name
		result.Nodes = append(result.Nodes, &graph.Node{
			ID: serverID, Kind: graph.KindResource, Name: name,
			FilePath: filePath, StartLine: 1, EndLine: lineCount,
			Language: "mcp_config",
			Meta: map[string]any{
				"resource_type": "mcp_server",
				"command":       spec.Command,
				"transport":     mcpTransport(spec.Type, spec.Transport, spec.URL),
				"source":        base,
			},
		})
		result.Edges = append(result.Edges, &graph.Edge{
			From: fileNode.ID, To: serverID, Kind: graph.EdgeDefines,
			FilePath: filePath, Line: 1,
		})

		// Launch command implies a package / image.
		switch strings.ToLower(filepath.Base(spec.Command)) {
		case "npx", "npm", "pnpm", "bunx":
			if pkg := firstNonFlagArg(spec.Args); pkg != "" {
				pkgName, version := splitNpmSpec(pkg)
				modID := modules.ModuleNodeID("npm", pkgName, version)
				if !seenModule[modID] {
					seenModule[modID] = true
					result.Nodes = append(result.Nodes, &graph.Node{
						ID: modID, Kind: graph.KindModule, Name: pkgName,
						FilePath: filePath, StartLine: 1, EndLine: lineCount,
						Language: "mcp_config",
						Meta: map[string]any{
							"ecosystem": "npm",
							"path":      pkgName,
							"version":   version,
						},
					})
				}
				result.Edges = append(result.Edges, &graph.Edge{
					From: serverID, To: modID, Kind: graph.EdgeDependsOnModule,
					FilePath: filePath, Line: 1,
				})
			}
		case "uvx", "pipx", "uv":
			if pkg := firstNonFlagArg(spec.Args); pkg != "" {
				pkgName, version := splitPypiSpec(pkg)
				modID := modules.ModuleNodeID("pypi", pkgName, version)
				if !seenModule[modID] {
					seenModule[modID] = true
					result.Nodes = append(result.Nodes, &graph.Node{
						ID: modID, Kind: graph.KindModule, Name: pkgName,
						FilePath: filePath, StartLine: 1, EndLine: lineCount,
						Language: "mcp_config",
						Meta: map[string]any{
							"ecosystem": "pypi",
							"path":      pkgName,
							"version":   version,
						},
					})
				}
				result.Edges = append(result.Edges, &graph.Edge{
					From: serverID, To: modID, Kind: graph.EdgeDependsOnModule,
					FilePath: filePath, Line: 1,
				})
			}
		case "docker":
			if img := dockerImageArg(spec.Args); img != "" {
				imgID := imageNodeID(img)
				ref, tag := splitImageRef(img)
				if !seenImage[imgID] {
					seenImage[imgID] = true
					result.Nodes = append(result.Nodes, &graph.Node{
						ID: imgID, Kind: graph.KindImage, Name: img,
						FilePath: filePath, StartLine: 1, EndLine: lineCount,
						Language: "mcp_config",
						Meta: map[string]any{
							"role": "base",
							"ref":  ref,
							"tag":  tag,
						},
					})
				}
				result.Edges = append(result.Edges, &graph.Edge{
					From: serverID, To: imgID, Kind: graph.EdgeDependsOn,
					FilePath: filePath, Line: 1,
				})
			}
		}

		// Declared env vars.
		envNames := make([]string, 0, len(spec.Env))
		for k := range spec.Env {
			envNames = append(envNames, k)
		}
		sortStrings(envNames)
		for _, key := range envNames {
			emitEnv(result, filePath, lineCount, serverID, key, seenEnv)
		}

		// Env interpolations referenced from args (`${VAR}` / `$VAR`).
		for _, arg := range spec.Args {
			for _, key := range scanEnvInterpolations(arg) {
				emitEnv(result, filePath, lineCount, serverID, key, seenEnv)
			}
		}
	}

	return result, nil
}

// emitEnv emits (once per file) a KindConfigKey node with the shared
// `cfg::env::<NAME>` ID and (once per server+key) an EdgeUsesEnv from the
// server.
func emitEnv(result *parser.ExtractionResult, filePath string, lineCount int, serverID, key string, seen map[string]bool) {
	if key == "" {
		return
	}
	keyID := configKeyEnvID(key)
	if !seen[keyID] {
		seen[keyID] = true
		result.Nodes = append(result.Nodes, &graph.Node{
			ID: keyID, Kind: graph.KindConfigKey, Name: key,
			FilePath: filePath, StartLine: 1, EndLine: lineCount,
			Language: "mcp_config",
			Meta: map[string]any{
				"source": "env",
			},
		})
	}
	edgeKey := serverID + "\x00" + keyID
	if seen[edgeKey] {
		return
	}
	seen[edgeKey] = true
	result.Edges = append(result.Edges, &graph.Edge{
		From: serverID, To: keyID, Kind: graph.EdgeUsesEnv,
		FilePath: filePath, Line: 1,
	})
}

// mcpTransport derives the transport label from the declared type /
// transport keyword or the presence of a URL. Defaults to "stdio" — the
// command-launched local-process transport that the canonical
// mcpServers shape implies.
func mcpTransport(typ, transport, url string) string {
	for _, candidate := range []string{transport, typ} {
		switch strings.ToLower(strings.TrimSpace(candidate)) {
		case "stdio":
			return "stdio"
		case "http", "streamable-http", "streamable_http", "streamablehttp":
			return "http"
		case "sse":
			return "sse"
		}
	}
	if strings.TrimSpace(url) != "" {
		return "http"
	}
	return "stdio"
}

// firstNonFlagArg returns the first argument that is not a flag (does not
// start with "-"). Package runners take leading flags like `-y`, `--yes`,
// `--from`, so the package spec is the first bare token.
func firstNonFlagArg(args []string) string {
	for _, a := range args {
		a = strings.TrimSpace(a)
		if a == "" || strings.HasPrefix(a, "-") {
			continue
		}
		return a
	}
	return ""
}

// dockerImageArg returns the image reference from a `docker run` arg list:
// the first non-flag token that is also not the `run` subcommand. Flags
// that take a separate value token (e.g. `-e KEY=val`, `--env KEY`) would
// otherwise leak their value as the image, so value-taking flags consume
// the next token.
func dockerImageArg(args []string) string {
	skipNext := false
	for _, a := range args {
		a = strings.TrimSpace(a)
		if a == "" {
			continue
		}
		if skipNext {
			skipNext = false
			continue
		}
		if strings.HasPrefix(a, "-") {
			// Long/short flags that take a separate value token. Inline
			// forms (`--env=X`, `-eX`) carry their value already.
			if !strings.Contains(a, "=") && dockerFlagTakesValue(a) {
				skipNext = true
			}
			continue
		}
		if a == "run" || a == "container" {
			continue
		}
		return a
	}
	return ""
}

// dockerFlagTakesValue reports whether a docker-run flag expects a
// following value token. Conservative: the common value-taking flags
// used in MCP server configs.
func dockerFlagTakesValue(flag string) bool {
	switch flag {
	case "-e", "--env", "-v", "--volume", "-p", "--publish",
		"--name", "-w", "--workdir", "--network", "--mount",
		"--env-file", "-u", "--user", "--entrypoint", "--label", "-l":
		return true
	}
	return false
}

// splitNpmSpec splits an npm package spec into name and version, scope-
// aware: `@scope/name@1.2.3` → ("@scope/name", "1.2.3"); `lodash@^4` →
// ("lodash", "^4"); `pkg` → ("pkg", "").
func splitNpmSpec(spec string) (name, version string) {
	spec = strings.TrimSpace(spec)
	var atIdx int
	if strings.HasPrefix(spec, "@") {
		// Scoped: skip the leading scope @, find the version @.
		atIdx = strings.Index(spec[1:], "@")
		if atIdx >= 0 {
			atIdx++
		}
	} else {
		atIdx = strings.Index(spec, "@")
	}
	if atIdx <= 0 {
		return spec, ""
	}
	return spec[:atIdx], spec[atIdx+1:]
}

// splitPypiSpec splits a PyPI package spec into name and version. PyPI
// specs are never scoped, and the version separator is `==` (`pkg==1.2`),
// `@` (PEP 508 direct refs), or a comparator (`pkg>=1.0`). We keep it
// simple: split on the first version-introducing delimiter.
func splitPypiSpec(spec string) (name, version string) {
	spec = strings.TrimSpace(spec)
	if i := strings.Index(spec, "=="); i >= 0 {
		return spec[:i], spec[i+2:]
	}
	if i := strings.IndexAny(spec, "@<>=~!"); i > 0 {
		return spec[:i], strings.TrimLeft(spec[i:], "@<>=~!")
	}
	return spec, ""
}

// scanEnvInterpolations extracts env-var names referenced in a string via
// `${VAR}` or `$VAR` interpolation. Returns names in order of appearance.
func scanEnvInterpolations(s string) []string {
	var out []string
	i := 0
	n := len(s)
	for i < n {
		if s[i] != '$' {
			i++
			continue
		}
		i++ // past '$'
		if i >= n {
			break
		}
		if s[i] == '{' {
			i++ // past '{'
			start := i
			for i < n && s[i] != '}' {
				i++
			}
			name := s[start:i]
			if i < n {
				i++ // past '}'
			}
			// Strip default-value / modifier suffixes (`${VAR:-x}`).
			if c := strings.IndexAny(name, ":-/"); c >= 0 {
				name = name[:c]
			}
			if isEnvName(name) {
				out = append(out, name)
			}
			continue
		}
		// `$VAR` — consume a run of identifier chars.
		start := i
		for i < n && isEnvNameChar(s[i]) {
			i++
		}
		name := s[start:i]
		if isEnvName(name) {
			out = append(out, name)
		}
	}
	return out
}

func isEnvName(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if !isEnvNameChar(s[i]) {
			return false
		}
	}
	// Leading digit is not a valid env-var name.
	return s[0] < '0' || s[0] > '9'
}

func isEnvNameChar(c byte) bool {
	return c == '_' ||
		(c >= 'A' && c <= 'Z') ||
		(c >= 'a' && c <= 'z') ||
		(c >= '0' && c <= '9')
}

// sortStrings sorts a string slice in place (ascending) via insertion
// sort — kept local so the file doesn't pull `sort` for one call over
// the tiny server / env-name slices.
func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j-1] > s[j]; j-- {
			s[j-1], s[j] = s[j], s[j-1]
		}
	}
}

var _ parser.Extractor = (*MCPConfigExtractor)(nil)
