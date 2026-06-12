# Vendored: github.com/google/renameio

This directory is a vendored copy of
[`github.com/google/renameio`](https://github.com/google/renameio)
**v1.0.1**, licensed under Apache-2.0 (see `LICENSE`).

It is wired into the build via a `replace` directive in the repository
root `go.mod`:

```
replace github.com/google/renameio => ./internal/thirdparty/renameio
```

## Why it is vendored

`renameio` v1.0.1 builds only on non-Windows platforms — `tempfile.go`
and `writefile.go` are tagged `// +build !windows`, so the package
exports nothing for `GOOS=windows`. `github.com/coder/hnsw` (a
transitive dependency of Gortex's `internal/search` vector index)
imports `renameio` unconditionally, which made the whole module
impossible to compile for Windows. Upstream moved Windows support to the
separate `renameio/v2` module and froze the v1 line, so a plain version
bump is not an option.

## Modifications by the Gortex project

The upstream files — `doc.go`, `tempfile.go`, `writefile.go`, `go.mod`,
`LICENSE` — are reproduced **verbatim**. Two files were **added** to
provide the missing Windows implementation:

- `tempfile_windows.go` — `TempDir`, `tempDir`, `PendingFile`,
  `TempFile`, and `Symlink` for Windows, built on `os.CreateTemp` plus
  `os.Rename` (which maps to `MoveFileEx` with
  `MOVEFILE_REPLACE_EXISTING`, an atomic replace).
- `writefile_windows.go` — the Windows half of `WriteFile`.

No upstream file was changed.
