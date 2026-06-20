package embedding

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestNewProviderFromConfig_DefaultIsStatic asserts that an empty
// provider name — the zero value, which is what an unconfigured
// `embedding:` block yields — selects the static GloVe provider. This
// is the default-on path: semantic search works with no setup.
func TestNewProviderFromConfig_DefaultIsStatic(t *testing.T) {
	p, err := NewProviderFromConfig(ProviderConfig{})
	require.NoError(t, err)
	defer p.Close()

	_, isStatic := p.(*StaticProvider)
	assert.True(t, isStatic, "empty provider name must select the static GloVe provider")
	assert.Equal(t, 50, p.Dimensions(), "static GloVe is 50-dimensional")
}

// TestNewProviderFromConfig_ExplicitStatic asserts that the explicit
// "static" name also selects the static provider.
func TestNewProviderFromConfig_ExplicitStatic(t *testing.T) {
	p, err := NewProviderFromConfig(ProviderConfig{Provider: "static"})
	require.NoError(t, err)
	defer p.Close()

	_, isStatic := p.(*StaticProvider)
	assert.True(t, isStatic)
}

// TestNewProviderFromConfig_API asserts that the "api" provider
// constructs an APIProvider against the configured URL, and that a
// missing URL is a hard error rather than a silent fallback.
func TestNewProviderFromConfig_API(t *testing.T) {
	p, err := NewProviderFromConfig(ProviderConfig{
		Provider: "api",
		APIURL:   "http://localhost:11434",
		APIModel: "nomic-embed-text",
	})
	require.NoError(t, err)
	defer p.Close()

	api, isAPI := p.(*APIProvider)
	require.True(t, isAPI, "the api provider must construct an APIProvider")
	assert.Equal(t, "nomic-embed-text", api.model)

	_, err = NewProviderFromConfig(ProviderConfig{Provider: "api"})
	require.Error(t, err, "the api provider without a URL must be an error")
}

// TestNewProviderFromConfig_UnknownProviderErrors asserts that a typo
// in the provider name fails loudly instead of degrading silently.
func TestNewProviderFromConfig_UnknownProviderErrors(t *testing.T) {
	_, err := NewProviderFromConfig(ProviderConfig{Provider: "transfromer"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "transfromer")
}
