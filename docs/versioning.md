# Versioning

Gortex follows [SemVer 2.0.0](https://semver.org/). Every release tag is a SemVer identifier, and the binary carries the full canonical form — including the build-metadata slot — for traceability.

## Format

```
vMAJOR.MINOR.PATCH[-PRERELEASE][+COMMITSHA]
```

- **MAJOR**, **MINOR**, **PATCH** — integers, no leading zeros.
- **PRERELEASE** (optional) — dot-separated identifiers; alphanumeric and hyphens. Common values: `rc.1`, `alpha.2`, `beta`.
- **COMMITSHA** (build slot, optional) — the short git SHA the binary was built from. Injected by the release pipeline; local `make build` also injects it.

Examples:

| String | Meaning |
|---|---|
| `v0.1.0` | Tagged release from a clean commit. Build slot not shown because the tag alone is authoritative. |
| `v0.1.0+abc1234` | A build of `v0.1.0` made from commit `abc1234`. |
| `v0.2.0-rc.1+def5678` | First release candidate for `v0.2.0`, built from `def5678`. |
| `v0.1.0-4-g63d6c43-dirty+63d6c43` | Local dev build 4 commits past `v0.1.0`, with uncommitted changes. `git describe` generates this; the SemVer parser still accepts it. |
| `v0.0.0-dev` | Built with `go build` (no ldflags). Scripts can detect this sentinel. |

Run `gortex version` for the multi-line summary (commit + build date + Go toolchain + os/arch) or `gortex version --short` for just the canonical string.

## When to bump

Gortex exposes two user-visible surfaces — **the MCP tool API** and **the CLI**. SemVer rules apply to both.

**MAJOR** — breaking changes:

- Removing or renaming an MCP tool.
- Removing or renaming a required argument on any MCP tool.
- Changing the shape of a tool's return value in a way that would break an existing agent parsing it.
- Removing or renaming a CLI subcommand or a flag that existed in the previous MAJOR.
- Protocol-breaking daemon changes (e.g., bumping `daemon.ProtocolVersion`).

**MINOR** — additive changes:

- New MCP tools, new optional tool arguments, new fields in a tool's response object.
- New CLI subcommands or new optional flags.
- New config keys with backwards-compatible defaults.
- New backend features that don't alter existing outputs.

**PATCH** — no user-visible API or CLI changes:

- Bug fixes.
- Performance improvements.
- Internal refactors.
- Documentation updates.
- Dependency bumps that don't change observable behavior.

## Tagging a release

Recommended workflow — `gortex version bump` + `make tag-release`:

```bash
# 1. Bump the source-of-truth version in cmd/gortex/main.go
gortex version bump minor                 # or major / patch
# (for a release candidate: gortex version bump minor --pre rc.1)

# 2. Commit the bump
git add cmd/gortex/main.go
git commit -m "Bump version to v0.2.0"

# 3. Create the tag from the freshly-bumped value
make tag-release                          # annotated git tag, name pulled from ./gortex

# 4. Push
git push && git push origin v0.2.0
```

`make tag-release` rebuilds the binary without VERSION ldflags so `gortex version --short` reflects the literal value in `main.go`, strips the `+<commit>` build slot (git tags don't carry it), and creates an annotated tag. It refuses to run on a dev build, a duplicate tag, or a dirty working tree — so a misfire doesn't silently create a broken release.

The push triggers the release workflow at **Actions → Release**. Goreleaser picks up the tag, builds the matrix, publishes to GitHub Releases, and updates the Homebrew tap.

If you want to do the tagging by hand instead (e.g., to sign the tag):

```bash
git tag -s v0.2.0 -m "Release v0.2.0"
git push origin v0.2.0
```

## Programmatic access

The `internal/version` package parses and emits the canonical form. Callers that need structured access:

```go
import "github.com/zzet/gortex/internal/version"

v, err := version.Parse("v1.2.3-rc.1+abc1234")
// v.Major == 1, v.Minor == 2, v.Patch == 3
// v.Prerelease == "rc.1"
// v.Build == "abc1234"
// v.String() == "v1.2.3-rc.1+abc1234"
```

The daemon exposes its running version in the handshake ACK as `DaemonVersion` and on the control surface's `status` response — clients can feature-gate or warn on mismatch.

## Agent-integration migration fingerprints

Exact `v0.60.0` configuration and generated-artifact fingerprints are retained
through the complete `v0.62.x` line so upgrades can replace untouched Gortex
output without overwriting user customizations. Their earliest removal is
`v0.63.0`, and removal is permitted only when both conditions hold:

1. The supported direct-upgrade floor is `v0.61.0` or newer.
2. The `v0.63.0` release notes tell `v0.60.x` users to upgrade through an
   intermediate `v0.61.x` or `v0.62.x` release.

Versioned identifiers such as `v060AlwaysAllow` and `v060GlobalSkillHashes`
make this temporary compatibility scope visible. Long-lived compatibility
types and fields (for example the MCP facade's `Legacy` handler mapping) are
not governed by this retirement gate.

## 0.x caveat

Until Gortex reaches `v1.0.0`, the MAJOR rule relaxes slightly: breaking changes may ship in a MINOR bump when they fall under a clearly-communicated rework (with changelog entry). That's the standard SemVer 0.x behavior. From `v1.0.0` onward, the rules above apply strictly.
