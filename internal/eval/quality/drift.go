// Package quality provides measurement infrastructure for the
// retrieval pipeline: embedder drift detection, retrieval-confidence
// tracking, query-log replay (top-k churn between two ranker
// configurations), and weight-tuning analysis.
//
// All four are surface-level analyzers — they read substrate already
// shipped (savings store, search index, rerank pipeline) and produce
// markdown / JSON artifacts. No new state in the hot path.
package quality

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/zzet/gortex/internal/platform"
)

// EmbedderFingerprint captures the identity of the active embedder
// at one point in time. Persisting it lets the drift detector flag
// silent provider / model / dimension changes that would otherwise
// produce confusing recall regressions.
type EmbedderFingerprint struct {
	Provider       string    `json:"provider"`
	Model          string    `json:"model"`
	ModelRevision  string    `json:"model_revision,omitempty"`
	EmbeddingDim   int       `json:"embedding_dim"`
	SampleVecSHA256 string   `json:"sample_vec_sha256,omitempty"`
	RecordedAt     time.Time `json:"recorded_at"`
}

// DriftWarning is the structured signal the detector emits when the
// current fingerprint differs from the stored one. Empty Changes
// slice means no drift.
type DriftWarning struct {
	Previous EmbedderFingerprint `json:"previous"`
	Current  EmbedderFingerprint `json:"current"`
	Changes  []string            `json:"changes"`
}

// HasDrift reports whether the warning is non-empty — convenience
// for callers that just want a boolean (e.g. CI gates).
func (w DriftWarning) HasDrift() bool { return len(w.Changes) > 0 }

// DefaultFingerprintPath returns the canonical persistence location.
// An absolute $XDG_CACHE_HOME is honoured; otherwise the file stays
// under os.UserCacheDir() as before. Returns empty when the cache dir
// is unavailable; callers should treat empty as "don't persist, just
// compare in-memory".
func DefaultFingerprintPath() string {
	if v := os.Getenv("XDG_CACHE_HOME"); v == "" || !filepath.IsAbs(v) {
		if base, err := os.UserCacheDir(); err != nil || base == "" {
			return ""
		}
	}
	return filepath.Join(platform.OSCacheDir(), "embedding-fingerprint.json")
}

// DriftDetector wraps the fingerprint persistence and the
// comparison logic. Concurrency: not safe — call from one goroutine
// per detector instance.
type DriftDetector struct {
	Path string
}

// NewDriftDetector returns a detector bound to a persistence path.
// Empty path is allowed — the detector still compares in-memory but
// never writes to disk.
func NewDriftDetector(path string) *DriftDetector {
	return &DriftDetector{Path: path}
}

// LoadPrevious reads the most recent fingerprint, or returns
// (zero-value, nil) when none exists. Real I/O errors propagate so a
// permission problem surfaces.
func (d *DriftDetector) LoadPrevious() (EmbedderFingerprint, error) {
	if d.Path == "" {
		return EmbedderFingerprint{}, nil
	}
	raw, err := os.ReadFile(d.Path)
	if errors.Is(err, os.ErrNotExist) {
		return EmbedderFingerprint{}, nil
	}
	if err != nil {
		return EmbedderFingerprint{}, fmt.Errorf("read fingerprint: %w", err)
	}
	var fp EmbedderFingerprint
	if err := json.Unmarshal(raw, &fp); err != nil {
		return EmbedderFingerprint{}, fmt.Errorf("parse fingerprint: %w", err)
	}
	return fp, nil
}

// Save writes the current fingerprint atomically (temp + rename).
// Silently no-ops when the detector has no persistence path.
func (d *DriftDetector) Save(fp EmbedderFingerprint) error {
	if d.Path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(d.Path), 0o755); err != nil {
		return err
	}
	body, err := json.MarshalIndent(fp, "", "  ")
	if err != nil {
		return err
	}
	tmp := d.Path + ".tmp"
	if err := os.WriteFile(tmp, body, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, d.Path)
}

// Compare returns the drift warning for current vs the stored
// fingerprint. The persistence file is NOT updated by Compare —
// callers decide when to promote (e.g. only after operator
// confirmation, or always in CI).
func (d *DriftDetector) Compare(current EmbedderFingerprint) (DriftWarning, error) {
	prev, err := d.LoadPrevious()
	if err != nil {
		return DriftWarning{}, err
	}
	return DiffFingerprints(prev, current), nil
}

// DiffFingerprints is the pure comparison logic — exported so tests
// and standalone consumers can use it without touching disk.
func DiffFingerprints(prev, current EmbedderFingerprint) DriftWarning {
	w := DriftWarning{Previous: prev, Current: current}
	if prev == (EmbedderFingerprint{}) {
		// No previous record — first run; not drift.
		return w
	}
	if prev.Provider != current.Provider {
		w.Changes = append(w.Changes, fmt.Sprintf("provider: %q → %q", prev.Provider, current.Provider))
	}
	if prev.Model != current.Model {
		w.Changes = append(w.Changes, fmt.Sprintf("model: %q → %q", prev.Model, current.Model))
	}
	if prev.ModelRevision != current.ModelRevision && (prev.ModelRevision != "" || current.ModelRevision != "") {
		w.Changes = append(w.Changes, fmt.Sprintf("model_revision: %q → %q", prev.ModelRevision, current.ModelRevision))
	}
	if prev.EmbeddingDim != current.EmbeddingDim {
		w.Changes = append(w.Changes, fmt.Sprintf("embedding_dim: %d → %d", prev.EmbeddingDim, current.EmbeddingDim))
	}
	if prev.SampleVecSHA256 != current.SampleVecSHA256 && (prev.SampleVecSHA256 != "" || current.SampleVecSHA256 != "") {
		// Vector-shape change without dim change usually means the
		// model was re-quantized or the input pre-processor
		// changed. Worth flagging.
		w.Changes = append(w.Changes, "sample_vec_sha256 changed (re-quantized or pre-processor swap?)")
	}
	return w
}
