#!/usr/bin/env bash
#
# Temporal recall/precision measurement harness.
#
# Answers, on a REAL corpus, whether the input adapter (allow-list / zeroth
# discovery) is needed beyond the greedy heuristic + the output LLM filter.
# See temporal-measure.md for the decision rules.
#
# Usage:
#   measure-temporal.sh [repo ...]          # default: current dir
# Env:
#   GORTEX_MEASURE_OUT   output dir (default ./temporal-measure)
#   GORTEX               gortex binary (default: gortex on PATH)
# Requires: gortex (built from feat/temporal-fork-all), jq. Step 3 needs a
# configured llm.provider.

set -euo pipefail

GORTEX="${GORTEX:-gortex}"
OUT="${GORTEX_MEASURE_OUT:-./temporal-measure}"

command -v "$GORTEX" >/dev/null 2>&1 || { echo "error: '$GORTEX' not on PATH (build from feat/temporal-fork-all)"; exit 1; }
command -v jq >/dev/null 2>&1 || { echo "error: jq not on PATH"; exit 1; }

repos=("$@")
[ ${#repos[@]} -eq 0 ] && repos=(".")
mkdir -p "$OUT"

for repo in "${repos[@]}"; do
	name="$(basename "$(cd "$repo" && pwd)")"
	echo "== measuring $name =="

	# 1. Baseline: heuristic + built-in allow-list, corp allow-list OFF.
	env -u GORTEX_ALLOW_LOCAL_TEMPORAL "$GORTEX" analyze --kind temporal_orphans \
		--path "$repo" --format json > "$OUT/$name.orphans_base.json" || {
		echo "  baseline failed for $name"; continue; }

	# 2. With corp allow-list (only meaningful when the file exists).
	if [ -f "$repo/.gortex/temporal-allowlist.yaml" ]; then
		GORTEX_ALLOW_LOCAL_TEMPORAL=1 "$GORTEX" analyze --kind temporal_orphans \
			--path "$repo" --format json > "$OUT/$name.orphans_allowlist.json" || true
	fi

	# 3. Output LLM filter (needs llm.provider; skipped gracefully otherwise).
	if GORTEX_ALLOW_LOCAL_TEMPORAL=1 "$GORTEX" analyze --kind temporal_verify \
		--path "$repo" --format json > "$OUT/$name.verify.json" 2>"$OUT/$name.verify.err"; then
		:
	else
		echo "  temporal_verify skipped ($(head -1 "$OUT/$name.verify.err"))"
		rm -f "$OUT/$name.verify.json"
	fi
done

echo
printf '%-24s | %-12s | %-15s | %-7s | %-9s | %-8s | %-9s\n' \
	repo "broken(base)" "broken(allowlist)" checked confirmed rejected uncertain
printf -- '---------------------------------------------------------------------------------------------\n'
for repo in "${repos[@]}"; do
	name="$(basename "$(cd "$repo" && pwd)")"
	bb="$(jq -r '.totals.broken_dispatch // 0' "$OUT/$name.orphans_base.json" 2>/dev/null || echo '-')"
	if [ -f "$OUT/$name.orphans_allowlist.json" ]; then
		ba="$(jq -r '.totals.broken_dispatch // 0' "$OUT/$name.orphans_allowlist.json")"
	else ba='-'; fi
	if [ -f "$OUT/$name.verify.json" ]; then
		read -r ck cf rj un < <(jq -r '[.checked,.confirmed,.rejected,.uncertain]|@tsv' "$OUT/$name.verify.json")
	else ck='-'; cf='-'; rj='-'; un='-'; fi
	printf '%-24s | %-12s | %-15s | %-7s | %-9s | %-8s | %-9s\n' "$name" "$bb" "$ba" "$ck" "$cf" "$rj" "$un"
done

echo
echo "== verify verdicts by source (the decision-maker: heuristic FP/TP rate) =="
for repo in "${repos[@]}"; do
	name="$(basename "$(cd "$repo" && pwd)")"
	[ -f "$OUT/$name.verify.json" ] || continue
	echo "-- $name --"
	jq -r '.details | group_by(.source)[] |
		"  \(.[0].source): confirmed=\([.[]|select(.verdict=="confirmed")]|length) rejected=\([.[]|select(.verdict=="rejected")]|length) uncertain=\([.[]|select(.verdict=="uncertain")]|length)"' \
		"$OUT/$name.verify.json"
done

echo
echo "Raw JSON in $OUT/. Read temporal-measure.md §'Как читать' for the decision rules."
