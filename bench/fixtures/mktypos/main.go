// mktypos injects realistic single-character typos into a retrieval
// fixture so we can measure typo-tolerant recall independently from
// clean-query recall. One mutation per query — only on tokens ≥4 chars,
// one of {swap adjacent, substitute, insert, delete}. Deterministic via
// a seed so before/after numbers are comparable across runs.
package main

import (
	"flag"
	"fmt"
	"log"
	"math/rand/v2"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

type fixture struct {
	Name  string `yaml:"name"`
	Cases []struct {
		ID                string         `yaml:"id"`
		Tier              string         `yaml:"tier"`
		Query             string         `yaml:"query"`
		Expected          []string       `yaml:"expected"`
		WinnowConstraints map[string]any `yaml:"winnow_constraints,omitempty"`
	} `yaml:"cases"`
}

func main() {
	in := flag.String("in", "bench/fixtures/retrieval.yaml", "source fixture")
	out := flag.String("out", "bench/fixtures/retrieval_typo.yaml", "destination fixture")
	rate := flag.Float64("rate", 1.0, "fraction of queries to mutate (0..1)")
	seed := flag.Int64("seed", 42, "RNG seed for deterministic mutation")
	flag.Parse()

	src, err := os.ReadFile(*in)
	if err != nil {
		log.Fatalf("read: %v", err)
	}
	var fx fixture
	if err := yaml.Unmarshal(src, &fx); err != nil {
		log.Fatalf("unmarshal: %v", err)
	}

	rng := rand.New(rand.NewPCG(uint64(*seed), uint64(*seed)+1))
	fx.Name = fx.Name + "-typo"

	mutated := 0
	for i := range fx.Cases {
		if rng.Float64() > *rate {
			continue
		}
		orig := fx.Cases[i].Query
		mut, ok := injectTypo(orig, rng)
		if !ok {
			continue
		}
		fx.Cases[i].ID = fx.Cases[i].ID + "-typo"
		fx.Cases[i].Query = mut
		mutated++
	}

	outData, err := yaml.Marshal(&fx)
	if err != nil {
		log.Fatalf("marshal: %v", err)
	}
	if err := os.WriteFile(*out, outData, 0o644); err != nil {
		log.Fatalf("write: %v", err)
	}
	fmt.Fprintf(os.Stderr, "mktypos: mutated %d / %d cases → %s\n", mutated, len(fx.Cases), *out)
}

// injectTypo mutates exactly one token (≥4 chars) in q using one of four
// edit operations. Returns the new query and a success flag; success=false
// when no token in q is long enough to mutate.
func injectTypo(q string, rng *rand.Rand) (string, bool) {
	tokens := strings.Fields(q)
	if len(tokens) == 0 {
		return q, false
	}
	// Pick a mutable token. Prefer longer tokens — they're the ones a
	// human is plausibly going to mistype.
	var candidates []int
	for i, t := range tokens {
		if countLetters(t) >= 4 {
			candidates = append(candidates, i)
		}
	}
	if len(candidates) == 0 {
		return q, false
	}
	idx := candidates[rng.IntN(len(candidates))]
	tokens[idx] = mutateWord(tokens[idx], rng)
	return strings.Join(tokens, " "), true
}

func countLetters(s string) int {
	n := 0
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') {
			n++
		}
	}
	return n
}

// mutateWord returns w with a single random edit applied — one of swap,
// substitute, insert, delete. Non-letter prefixes/suffixes (e.g. the "."
// in "graph.Graph") are preserved.
func mutateWord(w string, rng *rand.Rand) string {
	// Locate the contiguous letter run. Everything else stays put.
	start, end := -1, -1
	for i, r := range w {
		letter := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')
		if letter && start < 0 {
			start = i
		}
		if letter {
			end = i + 1
		}
	}
	if start < 0 || end-start < 4 {
		return w
	}
	head := w[:start]
	core := []byte(w[start:end])
	tail := w[end:]

	switch rng.IntN(4) {
	case 0: // swap adjacent
		p := rng.IntN(len(core) - 1)
		core[p], core[p+1] = core[p+1], core[p]
	case 1: // substitute
		p := rng.IntN(len(core))
		sub := byte('a' + rng.IntN(26))
		if isUpper(core[p]) {
			sub = byte('A' + rng.IntN(26))
		}
		if sub == core[p] {
			sub++
		}
		core[p] = sub
	case 2: // insert
		p := rng.IntN(len(core) + 1)
		c := byte('a' + rng.IntN(26))
		core = append(core[:p], append([]byte{c}, core[p:]...)...)
	case 3: // delete
		p := rng.IntN(len(core))
		core = append(core[:p], core[p+1:]...)
	}
	return head + string(core) + tail
}

func isUpper(b byte) bool { return b >= 'A' && b <= 'Z' }
