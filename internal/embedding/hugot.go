package embedding

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/knights-analytics/hugot"
	"github.com/knights-analytics/hugot/pipelines"
)

const hugotModelName = "sentence-transformers/all-MiniLM-L6-v2"

// HugotProvider uses Hugot with the pure Go backend for offline transformer embeddings.
// Model auto-downloads from Hugging Face on first use.
type HugotProvider struct {
	session  *hugot.Session
	pipeline *pipelines.FeatureExtractionPipeline
	dims     int
	mu       sync.Mutex
}

func newHugotProvider() (Provider, error) {
	session, err := hugot.NewGoSession()
	if err != nil {
		return nil, fmt.Errorf("hugot session: %w", err)
	}

	modelPath, err := ensureHugotModel()
	if err != nil {
		_ = session.Destroy()
		return nil, fmt.Errorf("hugot model: %w", err)
	}

	config := hugot.FeatureExtractionConfig{
		ModelPath: modelPath,
		Name:      "gortex-embeddings",
		Options: []hugot.FeatureExtractionOption{
			pipelines.WithNormalization(),
		},
	}

	pipeline, err := hugot.NewPipeline(session, config)
	if err != nil {
		_ = session.Destroy()
		return nil, fmt.Errorf("hugot pipeline: %w", err)
	}

	return &HugotProvider{
		session:  session,
		pipeline: pipeline,
		dims:     384,
	}, nil
}

func (p *HugotProvider) Embed(_ context.Context, text string) ([]float32, error) {
	vecs, err := p.EmbedBatch(context.Background(), []string{text})
	if err != nil {
		return nil, err
	}
	if len(vecs) == 0 {
		return nil, fmt.Errorf("hugot returned no embeddings")
	}
	return vecs[0], nil
}

func (p *HugotProvider) EmbedBatch(_ context.Context, texts []string) ([][]float32, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	output, err := p.pipeline.RunPipeline(texts)
	if err != nil {
		return nil, fmt.Errorf("hugot run: %w", err)
	}
	return output.Embeddings, nil
}

func (p *HugotProvider) Dimensions() int { return p.dims }

func (p *HugotProvider) Close() error {
	if p.session != nil {
		return p.session.Destroy()
	}
	return nil
}

func ensureHugotModel() (string, error) {
	home, _ := os.UserHomeDir()
	dest := filepath.Join(home, ".cache", "gortex", "models")
	modelDir := filepath.Join(dest, "sentence-transformers_all-MiniLM-L6-v2")

	// Check if already downloaded.
	if _, err := os.Stat(filepath.Join(modelDir, "tokenizer.json")); err == nil {
		return modelDir, nil
	}

	// Download from HuggingFace.
	path, err := hugot.DownloadModel(hugotModelName, dest, hugot.NewDownloadOptions())
	if err != nil {
		return "", fmt.Errorf("download model: %w", err)
	}
	return path, nil
}
