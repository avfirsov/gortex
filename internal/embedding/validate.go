package embedding

import "fmt"

// validateBatch checks that an embedding backend returned exactly one vector per
// input, that no vector is empty, and that every vector has the expected width.
//
// dims is the expected width. Pass a provider's known dimension (e.g. a Hugot
// model's 384) for a strict check; pass 0 when the width is not yet known — an
// API provider learns its width lazily from the first response — and the width
// is taken from the first returned vector so the batch is still checked for
// internal consistency (a ragged batch) without asserting an absolute size.
//
// A violation returns a descriptive error naming the offending index so a
// provider regression that returns nil, short, or mis-counted vectors surfaces
// as a loud, attributable failure instead of a silently empty or mis-dimensioned
// index.
func validateBatch(name string, texts []string, vecs [][]float32, dims int) error {
	if len(vecs) != len(texts) {
		return fmt.Errorf("%s returned %d vectors for %d inputs", name, len(vecs), len(texts))
	}
	expected := dims
	if expected <= 0 && len(vecs) > 0 {
		expected = len(vecs[0])
	}
	for i, v := range vecs {
		if len(v) == 0 {
			return fmt.Errorf("%s returned an empty vector at index %d of %d", name, i, len(vecs))
		}
		if expected > 0 && len(v) != expected {
			return fmt.Errorf("%s returned a width-%d vector at index %d, want %d", name, len(v), i, expected)
		}
	}
	return nil
}
