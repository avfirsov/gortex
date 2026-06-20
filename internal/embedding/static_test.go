package embedding

import (
	"context"
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// cosine returns the cosine similarity of two equal-length vectors.
// Both inputs from StaticProvider are L2-normalized, so this is just
// the dot product — but the explicit norm keeps the helper correct
// for any caller.
func cosine(a, b []float32) float64 {
	var dot, na, nb float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		na += float64(a[i]) * float64(a[i])
		nb += float64(b[i]) * float64(b[i])
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / (math.Sqrt(na) * math.Sqrt(nb))
}

// TestStaticProvider_LoadsBakedTable asserts the embedded GloVe table
// loads and reports a plausible dimensionality and vocabulary size.
func TestStaticProvider_LoadsBakedTable(t *testing.T) {
	p, err := NewStaticProvider()
	require.NoError(t, err)
	defer p.Close()

	// The baked table is GloVe 6B 50d. Dimensions() must reflect the
	// loaded table, not the 50 hardcoded in the constructor before
	// loadVectors runs.
	assert.Equal(t, 50, p.Dimensions(), "GloVe 6B baked table is 50-dimensional")
	assert.Greater(t, len(p.vectors), 10_000, "baked table should carry the top-~20k vocabulary")
}

// TestStaticProvider_SemanticRanking asserts that semantically related
// code tokens land closer in vector space than unrelated ones — the
// core property that makes static embeddings a useful fusion signal.
//
// The word pairs are drawn from the baked GloVe top-20k vocabulary
// (verb pairs like validate/delete fall below GloVe 6B's frequency
// cutoff and are absent — the tokenizer would lower them to a zero
// vector, so the test uses present near-synonyms instead).
func TestStaticProvider_SemanticRanking(t *testing.T) {
	p, err := NewStaticProvider()
	require.NoError(t, err)
	defer p.Close()
	ctx := context.Background()

	embed := func(s string) []float32 {
		v, err := p.Embed(ctx, s)
		require.NoError(t, err)
		require.NotEmpty(t, v)
		// A non-zero vector confirms the token was found in-vocab; a
		// miss would average to all-zero and make the comparison moot.
		nonZero := false
		for _, x := range v {
			if x != 0 {
				nonZero = true
				break
			}
		}
		require.True(t, nonZero, "token %q must be in the baked vocabulary", s)
		return v
	}

	cases := []struct {
		related, synonym, unrelated string
	}{
		{"check", "verify", "banana"},
		{"request", "response", "banana"},
		{"file", "directory", "mountain"},
	}
	for _, c := range cases {
		base := embed(c.related)
		syn := embed(c.synonym)
		unrel := embed(c.unrelated)
		simRelated := cosine(base, syn)
		simUnrelated := cosine(base, unrel)
		assert.Greater(t, simRelated, simUnrelated,
			"%q should be closer to %q (%.3f) than to %q (%.3f)",
			c.related, c.synonym, simRelated, c.unrelated, simUnrelated)
	}
}

// TestStaticProvider_EmbedBatchRoundTrip asserts EmbedBatch returns one
// vector per input in order, matching the per-item Embed result.
func TestStaticProvider_EmbedBatchRoundTrip(t *testing.T) {
	p, err := NewStaticProvider()
	require.NoError(t, err)
	defer p.Close()
	ctx := context.Background()

	inputs := []string{"validate token", "delete user", "parse json"}
	batch, err := p.EmbedBatch(ctx, inputs)
	require.NoError(t, err)
	require.Len(t, batch, len(inputs))

	for i, in := range inputs {
		want, err := p.Embed(ctx, in)
		require.NoError(t, err)
		require.Len(t, batch[i], p.Dimensions())
		assert.Equal(t, want, batch[i], "EmbedBatch[%d] must match Embed(%q)", i, in)
	}
}

// TestStaticProvider_EmbedBatchEmpty asserts the empty-input edge case
// returns an empty (non-nil-shaped) slice without error.
func TestStaticProvider_EmbedBatchEmpty(t *testing.T) {
	p, err := NewStaticProvider()
	require.NoError(t, err)
	defer p.Close()

	out, err := p.EmbedBatch(context.Background(), nil)
	require.NoError(t, err)
	assert.Empty(t, out)
}

// TestTokenizeForEmbedding_Splitters covers every boundary the
// tokenizer must split on: camelCase, underscore, dot, slash, and
// mixed identifiers — plus the single-char drop rule.
func TestTokenizeForEmbedding_Splitters(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  []string
	}{
		{"camelCase", "validateUserToken", []string{"validate", "user", "token"}},
		{"underscore", "get_user_by_id", []string{"get", "user", "by", "id"}},
		{"dotPath", "config.search.weights", []string{"config", "search", "weights"}},
		{"slashPath", "internal/embedding/static.go", []string{"internal", "embedding", "static", "go"}},
		{"mixed", "internal/auth.checkAccessToken", []string{"internal", "auth", "check", "access", "token"}},
		{"singleCharsDropped", "a b c d", nil},
		{"leadingUpper", "HTTPHandler", []string{"httphandler"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tokenizeForEmbedding(tt.input)
			assert.Equal(t, tt.want, got)
		})
	}
}

// TestStaticProvider_SetVectorsInjection asserts SetVectors swaps the
// table and re-derives Dimensions from the injected vectors — the
// deterministic-injection path used by other tests.
func TestStaticProvider_SetVectorsInjection(t *testing.T) {
	p, err := NewStaticProvider()
	require.NoError(t, err)
	defer p.Close()

	p.SetVectors(map[string][]float32{
		"alpha": {1, 0, 0, 0},
		"beta":  {0, 1, 0, 0},
	})
	assert.Equal(t, 4, p.Dimensions(), "Dimensions must follow the injected vector width")

	vec, err := p.Embed(context.Background(), "alpha")
	require.NoError(t, err)
	require.Len(t, vec, 4)
	// Single in-vocab token: averaging then normalizing a unit basis
	// vector returns it unchanged.
	assert.InDelta(t, 1.0, vec[0], 1e-6)
}
