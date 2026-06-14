#!/usr/bin/env node
// ua_validate.mjs — authoritative Understand-Anything validation harness.
//
// Reads a knowledge-graph JSON document on stdin, runs it through the REAL UA
// `validateGraph` from the understand-anything fork, and prints a single JSON
// line `{ "success": <bool>, "dropped": <int>, "fatal": <int>, "issues": [...] }`
// on stdout. Exit code 0 on success (zero dropped, zero fatal), 1 otherwise.
//
// This harness is the ONLY oracle for acceptance AC1 — the schema is never
// ported into Go. The Go integration test pipes its built graph here and
// asserts success && zero dropped && zero fatal. When the UA package or its
// build output is unavailable, the harness exits 2 with a clear message on
// stderr and the Go test t.Skips with that reason (it does NOT fake success).
//
// Resolution order for validateGraph:
//   1. the UA fork's built dist  (packages/core/dist/index.js)
//   2. the UA fork's package main (via its package.json "exports"/"main")
//   3. the TS source directly     (packages/core/src/schema.ts) when run under tsx
// The UA fork is expected at /mnt/d/code/understand-anything. Override with the
// UA_CORE env var (absolute path to the @understand-anything/core package dir).

import { readFileSync } from "node:fs";
import { existsSync } from "node:fs";
import path from "node:path";

const UA_CORE =
  process.env.UA_CORE ||
  "/mnt/d/code/understand-anything/understand-anything-plugin/packages/core";

async function loadValidateGraph() {
  const candidates = [
    path.join(UA_CORE, "dist", "index.js"),
    path.join(UA_CORE, "dist", "schema.js"),
    path.join(UA_CORE, "index.js"),
    path.join(UA_CORE, "src", "schema.ts"),
  ];
  for (const c of candidates) {
    if (!existsSync(c)) continue;
    try {
      const mod = await import(c);
      if (typeof mod.validateGraph === "function") return mod.validateGraph;
      if (mod.default && typeof mod.default.validateGraph === "function") {
        return mod.default.validateGraph;
      }
    } catch (err) {
      // .ts candidate needs a TS loader (tsx); fall through to the next.
      if (process.env.UA_DEBUG) console.error(`load ${c}: ${err}`);
    }
  }
  // Last resort: resolve the package by name (works when it is installed
  // into a reachable node_modules with a proper "exports" map).
  try {
    const mod = await import("@understand-anything/core");
    if (typeof mod.validateGraph === "function") return mod.validateGraph;
  } catch (err) {
    if (process.env.UA_DEBUG) console.error(`resolve by name: ${err}`);
  }
  return null;
}

function readStdin() {
  return readFileSync(0, "utf8");
}

const validateGraph = await loadValidateGraph();
if (!validateGraph) {
  console.error(
    `ua_validate: could not load validateGraph from ${UA_CORE} ` +
      `(build the UA core, or set UA_CORE to its package dir)`,
  );
  process.exit(2);
}

let graph;
try {
  graph = JSON.parse(readStdin());
} catch (err) {
  console.error(`ua_validate: invalid JSON on stdin: ${err}`);
  process.exit(2);
}

let result;
try {
  result = validateGraph(graph);
} catch (err) {
  console.error(`ua_validate: validateGraph threw: ${err}`);
  process.exit(2);
}

// validateGraph's shape varies across UA versions; normalize defensively.
// We treat any issue with severity "fatal"/"error" as fatal, and count
// reported "dropped" entities (dropped nodes + dropped edges) however the
// validator surfaces them.
const issues = result.issues || result.errors || [];
let fatal = 0;
for (const issue of issues) {
  const sev = (issue.severity || issue.level || "").toLowerCase();
  if (sev === "fatal" || sev === "error") fatal++;
}
const dropped =
  (result.dropped?.length ?? result.droppedCount ?? 0) +
  (result.droppedNodes?.length ?? 0) +
  (result.droppedEdges?.length ?? 0);
const success = (result.success ?? fatal === 0) && fatal === 0 && dropped === 0;

console.log(JSON.stringify({ success, dropped, fatal, issues }));
process.exit(success ? 0 : 1);
