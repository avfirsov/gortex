// Package hermes implements the Gortex init/install integration for
// NousResearch Hermes (https://github.com/NousResearch/hermes-agent),
// a CLI agent-orchestrator that consumes MCP servers.
//
// Hermes is a user-level agent, not a repo-scoped IDE: it stores all
// state under ~/.hermes/ and the gortex daemon already resolves the
// active workspace per MCP session, so one global server entry serves
// every repo. We therefore write user-level artifacts in both
// `gortex init` (ModeProject) and `gortex install` (ModeGlobal), the
// same as the openclaw / antigravity adapters — the writes are
// idempotent, so running both is harmless.
//
// Three surfaces are configured:
//
//  1. Global ~/.hermes/config.yaml — upsert a `gortex` stdio server
//     under the snake_case `mcp_servers` map, comment-preservingly
//     (the config is hand-edited and comment-rich).
//  2. Every existing ~/.hermes/profiles/<name>/config.yaml — Hermes
//     profiles can re-declare their own `mcp_servers` block rather
//     than inheriting the global one, so we upsert the gortex stanza
//     into each profile config that already exists. This guarantees
//     every profile resolves the gortex tools regardless of the
//     global↔profile merge semantics. (We never create new profiles.)
//  3. A user-level skill at ~/.hermes/skills/gortex/SKILL.md teaching
//     the agent to prefer gortex graph tools — Hermes' equivalent of
//     the Claude Code / Antigravity user-level instruction surface.
package hermes

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/zzet/gortex/internal/agents"
	"github.com/zzet/gortex/internal/agents/internalutil"
	yaml "gopkg.in/yaml.v3"
)

const Name = "hermes"
const DocsURL = "https://hermes-agent.nousresearch.com/docs/user-guide/features/mcp"

type Adapter struct{}

func New() *Adapter                { return &Adapter{} }
func (a *Adapter) Name() string    { return Name }
func (a *Adapter) DocsURL() string { return DocsURL }

// Detect returns true when Hermes is installed or its home directory
// exists. False means "skip", not an error — a machine without Hermes
// gets no ~/.hermes writes.
func (a *Adapter) Detect(env agents.Env) (bool, error) {
	if p, err := exec.LookPath("hermes"); err == nil && p != "" {
		return true, nil
	}
	if env.Home == "" {
		return false, nil
	}
	if _, err := os.Stat(hermesDir(env.Home)); err == nil {
		return true, nil
	}
	return false, nil
}

func (a *Adapter) Plan(env agents.Env) (*agents.Plan, error) {
	if env.Home == "" {
		return &agents.Plan{}, nil
	}
	// The global config carries mcp_servers always, and the gortex
	// pre_tool_call / pre_llm_call hooks when hook installation is on —
	// so `init doctor` (which runs Plan) reports the hooks surface too.
	globalKeys := []string{"mcp_servers"}
	if env.InstallHooks {
		globalKeys = append(globalKeys, "hooks")
	}
	files := []agents.FileAction{
		{Path: globalConfigPath(env.Home), Action: agents.ActionWouldMerge, Keys: globalKeys},
	}
	for _, p := range profileConfigPaths(env.Home) {
		files = append(files, agents.FileAction{Path: p, Action: agents.ActionWouldMerge, Keys: []string{"mcp_servers"}})
	}
	files = append(files, agents.FileAction{Path: skillPath(env.Home, SkillName), Action: agents.ActionWouldCreate})
	for _, name := range RoutingSkillNames() {
		files = append(files, agents.FileAction{Path: skillPath(env.Home, name), Action: agents.ActionWouldCreate})
	}
	return &agents.Plan{Files: files}, nil
}

func (a *Adapter) Apply(env agents.Env, opts agents.ApplyOpts) (*agents.Result, error) {
	res := &agents.Result{Name: Name, DocsURL: DocsURL}
	detected, _ := a.Detect(env)
	res.Detected = detected
	if !detected && !opts.ForceDetect {
		internalutil.Logf(env.Stderr, "[gortex init] skip Hermes setup (hermes not detected)")
		return res, nil
	}
	if env.Home == "" {
		return res, fmt.Errorf("hermes: requires a resolved home directory")
	}
	internalutil.Logf(env.Stderr, "[gortex init] setting up Hermes integration...")

	command := resolveGortexCommand()

	// 1. Global config — the entry every profile inherits when it
	//    doesn't re-declare its own server map, plus (when hooks are
	//    enabled) the gortex pre_tool_call / pre_llm_call hooks. Both
	//    ride one comment-preserving merge of ~/.hermes/config.yaml.
	globalAction, err := upsertGlobalConfig(env.Stderr, globalConfigPath(env.Home), command, env.HookMode, env.InstallHooks, opts)
	if err != nil {
		return res, fmt.Errorf("hermes global config: %w", err)
	}
	res.Files = append(res.Files, globalAction)
	if env.InstallHooks {
		internalutil.Logf(env.Stderr, "[gortex init] wired Hermes pre_tool_call + pre_llm_call hooks (posture: %s)", hermesHookModeLabel(env.HookMode))
	} else {
		internalutil.Logf(env.Stderr, "[gortex init] skipping Hermes hook installation (--no-hooks)")
	}

	// 2. Per-profile configs — Hermes profiles may carry their own
	//    mcp_servers block, so upsert into each existing one too. A
	//    failure on one profile is a warning, not fatal: the global
	//    entry still covers profiles that do inherit.
	for _, profilePath := range profileConfigPaths(env.Home) {
		profileAction, perr := upsertGortexServer(env.Stderr, profilePath, command, opts)
		if perr != nil {
			// Non-fatal: the global stanza still covers profiles that
			// inherit. But this profile does NOT inherit, so record the
			// failure on the result — not just stderr — otherwise a
			// Configured=true silently masks a profile left unconfigured.
			internalutil.Warnf(env.Stderr, "hermes profile %s: %v", profilePath, perr)
			res.Warnings = append(res.Warnings, fmt.Sprintf("profile %s not configured: %v", profilePath, perr))
			continue
		}
		res.Files = append(res.Files, profileAction)
	}

	// 3. User-level skills — the master `gortex` guide plus the
	//    per-task routing playbooks (explore / impact / refactor / …),
	//    mirroring the Claude Code user-level skill set. User-authored files are
	//    preserved; exact legacy Gortex vocabulary is migrated in place.
	masterAction, err := writeHermesSkillArtifact(env.Stderr, skillPath(env.Home, SkillName), SkillBody(), opts)
	if err != nil {
		return res, fmt.Errorf("hermes skill: %w", err)
	}
	res.Files = append(res.Files, masterAction)

	routing := RoutingSkills()
	for _, name := range RoutingSkillNames() {
		action, rerr := writeHermesSkillArtifact(env.Stderr, skillPath(env.Home, name), routing[name], opts)
		if rerr != nil {
			internalutil.Warnf(env.Stderr, "hermes skill %s: %v", name, rerr)
			continue
		}
		res.Files = append(res.Files, action)
	}

	res.Configured = true
	return res, nil
}

func writeHermesSkillArtifact(w io.Writer, path, content string, opts agents.ApplyOpts) (agents.FileAction, error) {
	if existing, err := os.ReadFile(path); err == nil && isLegacyHermesSkill(string(existing)) {
		return agents.WriteOwnedFile(w, path, content, opts)
	}
	return agents.WriteIfNotExists(w, path, content, opts)
}

func isLegacyHermesSkill(body string) bool {
	if !strings.HasPrefix(body, "---\nname: gortex") {
		return false
	}
	for _, legacy := range []string{
		"smart_context", "search_symbols", "get_symbol_source", "get_editing_context",
		"find_usages", "get_callers", "verify_change", "detect_changes", "tools_search",
	} {
		if strings.Contains(body, legacy) {
			return true
		}
	}
	return false
}

// upsertGortexServer merges the gortex stdio stanza into the
// `mcp_servers` map of a Hermes YAML config, preserving comments and
// unrelated keys. Used for per-profile configs, which carry only the
// server stanza.
func upsertGortexServer(w io.Writer, path, command string, opts agents.ApplyOpts) (agents.FileAction, error) {
	return agents.MergeYAML(w, path, func(root *yaml.Node, _ bool) (bool, error) {
		return agents.UpsertYAMLMapEntry(root, "mcp_servers", gortexServerName, gortexMCPEntry(command), opts.Force)
	}, opts)
}

// upsertGlobalConfig merges the gortex MCP server stanza — and, when
// hook installation is enabled, the gortex pre_tool_call / pre_llm_call
// hook entries — into the global ~/.hermes/config.yaml in a single
// comment-preserving pass. Hooks are global-only: unlike mcp_servers
// (which a profile can re-declare), Hermes documents shell hooks only at
// global scope, so this is the one place they're written.
func upsertGlobalConfig(w io.Writer, path, command, hookMode string, installHooks bool, opts agents.ApplyOpts) (agents.FileAction, error) {
	return agents.MergeYAML(w, path, func(root *yaml.Node, _ bool) (bool, error) {
		serverChanged, err := agents.UpsertYAMLMapEntry(root, "mcp_servers", gortexServerName, gortexMCPEntry(command), opts.Force)
		if err != nil {
			return false, err
		}
		// Enable the gortex CLI platform toolset so the agent can shell the
		// gortex verbs alongside the MCP server.
		toolsetChanged, err := enablePlatformToolsetCLI(root, opts.Force)
		if err != nil {
			return false, err
		}
		if !installHooks {
			return serverChanged || toolsetChanged, nil
		}
		hooksChanged, err := upsertGortexHooks(root, hookMode, opts.Force)
		if err != nil {
			return false, err
		}
		return serverChanged || toolsetChanged || hooksChanged, nil
	}, opts)
}

// enablePlatformToolsetCLI ensures Hermes' `platform_toolsets.cli` toolset is
// turned on, so the gortex CLI verbs the agent can shell out to are available
// alongside the MCP server. force overwrites an explicit user value; otherwise
// an existing entry is left untouched.
func enablePlatformToolsetCLI(root *yaml.Node, force bool) (bool, error) {
	return agents.UpsertYAMLMapEntry(root, "platform_toolsets", "cli",
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!bool", Value: "true"}, force)
}

// hermesHookModeLabel renders the posture name for the install log,
// normalising the empty / unknown default to "deny".
func hermesHookModeLabel(mode string) string {
	m := strings.ToLower(strings.TrimSpace(mode))
	switch m {
	case "enrich", "consult-unlock", "nudge":
		return m
	case "adaptive-nudge":
		return "nudge"
	default:
		return "deny"
	}
}

// resolveGortexCommand returns the command Hermes should launch for the
// gortex MCP server. It prefers a stable absolute path so the entry
// works regardless of how Hermes' subprocess PATH is set up:
//
//  1. os.Executable() — but only when it actually points at an installed
//     `gortex` binary. Under `go run`, os.Executable() is a temp build
//     that is deleted on exit (and may even be *named* gortex), so we
//     additionally reject any path under the temp dir.
//  2. exec.LookPath("gortex") — a stable PATH install (homebrew / go
//     install).
//  3. the bare "gortex" name as a last resort.
func resolveGortexCommand() string {
	if exe, err := os.Executable(); err == nil && exe != "" {
		base := filepath.Base(exe)
		base = strings.TrimSuffix(base, filepath.Ext(base)) // drop .exe on Windows
		underTemp := strings.HasPrefix(exe, filepath.Clean(os.TempDir())+string(os.PathSeparator))
		if base == "gortex" && !underTemp {
			return exe
		}
	}
	if p, err := exec.LookPath("gortex"); err == nil && p != "" {
		return p
	}
	return "gortex"
}

// hermesDir is the ~/.hermes root.
func hermesDir(home string) string { return filepath.Join(home, ".hermes") }

// globalConfigPath is ~/.hermes/config.yaml.
func globalConfigPath(home string) string { return filepath.Join(hermesDir(home), "config.yaml") }

// skillPath is ~/.hermes/skills/<category>/<name>/SKILL.md. Hermes
// discovers SKILL.md files recursively, and its convention is to group
// skills under a category folder rather than at the skills root.
func skillPath(home, name string) string {
	return filepath.Join(hermesDir(home), "skills", skillCategory(name), name, "SKILL.md")
}

// skillCategory returns the ~/.hermes/skills subdirectory a gortex skill
// lives under. We reuse the routing-skill taxonomy so each playbook
// lands in its topical folder (navigation / analysis / debugging / …)
// and the master guide under code-intelligence — keeping the skills root
// uncluttered and matching how Hermes' own skills are organised.
func skillCategory(name string) string {
	if name == SkillName {
		return masterSkillCategory
	}
	_, category := routingSkillTaxonomy(name)
	return category
}

// profileConfigPaths returns the config.yaml of every existing Hermes
// profile under ~/.hermes/profiles/<name>/, sorted for a stable
// install report and deterministic tests. Returns nil when the
// profiles directory is absent.
func profileConfigPaths(home string) []string {
	matches, err := filepath.Glob(filepath.Join(hermesDir(home), "profiles", "*", "config.yaml"))
	if err != nil || len(matches) == 0 {
		return nil
	}
	sort.Strings(matches)
	return matches
}
