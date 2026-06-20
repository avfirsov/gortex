package embedding

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"strings"
	"sync"
)

// StaticProvider computes embeddings by averaging pre-trained word vectors.
// It provides basic semantic search — understands "validate" ≈ "check" but
// has no contextual understanding. Always available, zero external dependencies.
type StaticProvider struct {
	vectors map[string][]float32
	dims    int
	mu      sync.RWMutex
}

// NewStaticProvider creates a provider using built-in word vectors.
func NewStaticProvider() (*StaticProvider, error) {
	p := &StaticProvider{
		vectors: make(map[string][]float32),
		dims:    50, // default GloVe 50d; overridden by loadVectors
	}
	if err := p.loadVectors(); err != nil {
		return nil, err
	}
	return p, nil
}

func (p *StaticProvider) Embed(_ context.Context, text string) ([]float32, error) {
	tokens := tokenizeForEmbedding(text)
	return p.averageVectors(tokens), nil
}

func (p *StaticProvider) EmbedBatch(_ context.Context, texts []string) ([][]float32, error) {
	results := make([][]float32, len(texts))
	for i, text := range texts {
		tokens := tokenizeForEmbedding(text)
		results[i] = p.averageVectors(tokens)
	}
	return results, nil
}

func (p *StaticProvider) Dimensions() int { return p.dims }
func (p *StaticProvider) Close() error    { return nil }

func (p *StaticProvider) averageVectors(tokens []string) []float32 {
	result := make([]float32, p.dims)
	count := 0

	p.mu.RLock()
	defer p.mu.RUnlock()

	for _, tok := range tokens {
		vec, ok := p.vectors[tok]
		if !ok {
			continue
		}
		for i, v := range vec {
			result[i] += v
		}
		count++
	}

	if count == 0 {
		return result // zero vector
	}

	// Average and normalize.
	for i := range result {
		result[i] /= float32(count)
	}
	return normalize(result)
}

func normalize(v []float32) []float32 {
	var norm float64
	for _, x := range v {
		norm += float64(x) * float64(x)
	}
	norm = math.Sqrt(norm)
	if norm < 1e-10 {
		return v
	}
	for i := range v {
		v[i] /= float32(norm)
	}
	return v
}

// tokenizeForEmbedding splits text into lowercase tokens suitable for
// word vector lookup. Splits on camelCase, underscores, dots, slashes.
func tokenizeForEmbedding(text string) []string {
	var tokens []string
	var current strings.Builder

	flush := func() {
		if current.Len() >= 2 {
			tokens = append(tokens, current.String())
		}
		current.Reset()
	}

	prev := rune(0)
	for _, r := range text {
		switch {
		case r >= 'A' && r <= 'Z':
			// camelCase boundary: flush before uppercase
			if prev >= 'a' && prev <= 'z' {
				flush()
			}
			current.WriteRune(r + 32) // toLower
		case r >= 'a' && r <= 'z':
			current.WriteRune(r)
		case r >= '0' && r <= '9':
			current.WriteRune(r)
		default:
			flush()
		}
		prev = r
	}
	flush()

	return tokens
}

// loadVectors loads GloVe word vectors from the embedded data file.
func (p *StaticProvider) loadVectors() error {
	if len(vectorData) == 0 {
		return nil // no embedded vectors, empty vocabulary
	}

	gz, err := gzip.NewReader(bytes.NewReader(vectorData))
	if err != nil {
		return fmt.Errorf("decompress vectors: %w", err)
	}
	defer gz.Close()

	data, err := io.ReadAll(gz)
	if err != nil {
		return fmt.Errorf("read vectors: %w", err)
	}

	if len(data) < 8 {
		return fmt.Errorf("vector data too short")
	}

	wordCount := binary.LittleEndian.Uint32(data[0:4])
	dims := binary.LittleEndian.Uint32(data[4:8])
	p.dims = int(dims)

	offset := 8
	for i := uint32(0); i < wordCount; i++ {
		if offset+2 > len(data) {
			break
		}
		wordLen := int(binary.LittleEndian.Uint16(data[offset : offset+2]))
		offset += 2

		if offset+wordLen > len(data) {
			break
		}
		word := string(data[offset : offset+wordLen])
		offset += wordLen

		vecBytes := int(dims) * 4
		if offset+vecBytes > len(data) {
			break
		}
		vec := make([]float32, dims)
		for j := range vec {
			vec[j] = math.Float32frombits(binary.LittleEndian.Uint32(data[offset+j*4 : offset+j*4+4]))
		}
		offset += vecBytes

		p.vectors[word] = vec
	}

	return nil
}

// SetVectors allows injecting word vectors for testing.
func (p *StaticProvider) SetVectors(vecs map[string][]float32) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.vectors = vecs
	if len(vecs) > 0 {
		for _, v := range vecs {
			p.dims = len(v)
			break
		}
	}
}
