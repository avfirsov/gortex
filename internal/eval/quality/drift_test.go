package quality

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDiffFingerprints_NoPreviousIsNoDrift(t *testing.T) {
	cur := EmbedderFingerprint{Provider: "hugot", Model: "MiniLM-L6-v2", EmbeddingDim: 384}
	w := DiffFingerprints(EmbedderFingerprint{}, cur)
	if w.HasDrift() {
		t.Errorf("no previous fingerprint should not flag drift; got %v", w.Changes)
	}
}

func TestDiffFingerprints_ProviderChangeFlagged(t *testing.T) {
	prev := EmbedderFingerprint{Provider: "hugot", Model: "MiniLM-L6-v2", EmbeddingDim: 384}
	cur := EmbedderFingerprint{Provider: "openai", Model: "text-embedding-3-small", EmbeddingDim: 384}
	w := DiffFingerprints(prev, cur)
	if !w.HasDrift() {
		t.Fatal("provider change should flag drift")
	}
	hasProvider := false
	hasModel := false
	for _, c := range w.Changes {
		if contains(c, "provider:") {
			hasProvider = true
		}
		if contains(c, "model:") {
			hasModel = true
		}
	}
	if !hasProvider || !hasModel {
		t.Errorf("expected provider + model in changes, got %v", w.Changes)
	}
}

func TestDiffFingerprints_DimChangeFlagged(t *testing.T) {
	prev := EmbedderFingerprint{Provider: "hugot", Model: "MiniLM-L6-v2", EmbeddingDim: 384}
	cur := EmbedderFingerprint{Provider: "hugot", Model: "MiniLM-L6-v2", EmbeddingDim: 768}
	w := DiffFingerprints(prev, cur)
	if !w.HasDrift() {
		t.Fatal("dim change should flag drift")
	}
}

func TestDiffFingerprints_IdenticalIsNoDrift(t *testing.T) {
	fp := EmbedderFingerprint{Provider: "hugot", Model: "MiniLM-L6-v2", EmbeddingDim: 384, SampleVecSHA256: "abc"}
	w := DiffFingerprints(fp, fp)
	if w.HasDrift() {
		t.Errorf("identical fingerprints should not flag drift; got %v", w.Changes)
	}
}

func TestDiffFingerprints_SampleVecChangeFlagged(t *testing.T) {
	prev := EmbedderFingerprint{Provider: "hugot", Model: "M", EmbeddingDim: 384, SampleVecSHA256: "abc"}
	cur := EmbedderFingerprint{Provider: "hugot", Model: "M", EmbeddingDim: 384, SampleVecSHA256: "xyz"}
	w := DiffFingerprints(prev, cur)
	if !w.HasDrift() {
		t.Fatal("sample-vec change should flag drift")
	}
}

func TestDriftDetector_RoundTripFingerprint(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "fp.json")
	d := NewDriftDetector(path)

	want := EmbedderFingerprint{
		Provider:        "hugot",
		Model:           "MiniLM-L6-v2",
		ModelRevision:   "fp32",
		EmbeddingDim:    384,
		SampleVecSHA256: "deadbeef",
		RecordedAt:      time.Now().UTC().Round(time.Second),
	}
	if err := d.Save(want); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := d.LoadPrevious()
	if err != nil {
		t.Fatalf("LoadPrevious: %v", err)
	}
	if got.Provider != want.Provider || got.EmbeddingDim != want.EmbeddingDim {
		t.Errorf("round-trip lost data: got %+v want %+v", got, want)
	}
}

func TestDriftDetector_LoadMissingReturnsZero(t *testing.T) {
	d := NewDriftDetector(filepath.Join(t.TempDir(), "missing.json"))
	got, err := d.LoadPrevious()
	if err != nil {
		t.Errorf("missing file should not error, got %v", err)
	}
	if got != (EmbedderFingerprint{}) {
		t.Errorf("missing file should yield zero, got %+v", got)
	}
}

func TestDriftDetector_EmptyPathSilent(t *testing.T) {
	d := NewDriftDetector("")
	if err := d.Save(EmbedderFingerprint{Provider: "x"}); err != nil {
		t.Errorf("empty path Save should be no-op, got %v", err)
	}
	got, err := d.LoadPrevious()
	if err != nil || got != (EmbedderFingerprint{}) {
		t.Errorf("empty path LoadPrevious should return zero / nil, got %+v / %v", got, err)
	}
}

func TestDriftDetector_Compare_EndToEnd(t *testing.T) {
	dir := t.TempDir()
	d := NewDriftDetector(filepath.Join(dir, "fp.json"))

	// First compare: no previous record → no drift.
	cur := EmbedderFingerprint{Provider: "hugot", Model: "A", EmbeddingDim: 384}
	w, err := d.Compare(cur)
	if err != nil {
		t.Fatal(err)
	}
	if w.HasDrift() {
		t.Error("first comparison should report no drift")
	}
	_ = d.Save(cur)

	// Second compare with same fingerprint: still no drift.
	w, _ = d.Compare(cur)
	if w.HasDrift() {
		t.Errorf("identical fingerprint should not drift, got %v", w.Changes)
	}

	// Change the model: should drift.
	cur.Model = "B"
	w, _ = d.Compare(cur)
	if !w.HasDrift() {
		t.Error("model change should drift")
	}
}

func TestDefaultFingerprintPath(t *testing.T) {
	got := DefaultFingerprintPath()
	if got == "" {
		// Acceptable when UserCacheDir is unavailable; just don't panic.
		return
	}
	if filepath.Base(got) != "embedding-fingerprint.json" {
		t.Errorf("default path basename = %q, want embedding-fingerprint.json", filepath.Base(got))
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || stringsHasSubstring(s, sub))
}

func stringsHasSubstring(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// Quiet "imported and not used" when the harness iterates the
// fingerprint file directly without going through DriftDetector.
var _ = os.Stat
