package quality

import (
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestConfidenceFromScores_Basic(t *testing.T) {
	scores := []float64{0.95, 0.7, 0.5, 0.3}
	r := ConfidenceFromScores("q", scores)
	if r.Top1 != 0.95 || r.Top2 != 0.7 {
		t.Errorf("top1/top2 = %.2f/%.2f, want 0.95/0.7", r.Top1, r.Top2)
	}
	wantRatio := 0.95 / 0.7
	if math.Abs(r.Ratio12-wantRatio) > 1e-9 {
		t.Errorf("Ratio12 = %.4f, want %.4f", r.Ratio12, wantRatio)
	}
	if r.K != 4 {
		t.Errorf("K = %d, want 4", r.K)
	}
}

func TestConfidenceFromScores_Empty(t *testing.T) {
	r := ConfidenceFromScores("q", nil)
	if r.K != 0 || r.Top1 != 0 || r.Top2 != 0 {
		t.Errorf("empty input should yield zero record, got %+v", r)
	}
}

func TestConfidenceFromScores_SingleScore(t *testing.T) {
	r := ConfidenceFromScores("q", []float64{0.5})
	if r.Top1 != 0.5 || r.Top2 != 0 || r.Ratio12 != 0 {
		t.Errorf("single-score = %+v, want top1=0.5 others=0", r)
	}
}

func TestConfidenceTracker_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "conf.jsonl")
	tr := NewConfidenceTracker(path)
	for i := range 3 {
		rec := ConfidenceFromScores(
			"q",
			[]float64{0.9, 0.8 - 0.1*float64(i), 0.5, 0.4},
		)
		if err := tr.Record(rec); err != nil {
			t.Fatal(err)
		}
	}
	got, err := LoadConfidenceLog(path, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Errorf("loaded %d records, want 3", len(got))
	}
}

func TestConfidenceTracker_EmptyPathNoop(t *testing.T) {
	tr := NewConfidenceTracker("")
	if err := tr.Record(ConfidenceFromScores("q", []float64{1})); err != nil {
		t.Errorf("empty-path tracker should no-op, got %v", err)
	}
}

func TestLoadConfidenceLog_FiltersSince(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "conf.jsonl")
	tr := NewConfidenceTracker(path)
	now := time.Now().UTC()

	old := ConfidenceFromScores("old", []float64{0.5})
	old.TS = now.Add(-48 * time.Hour)
	_ = tr.Record(old)

	recent := ConfidenceFromScores("recent", []float64{0.7})
	recent.TS = now
	_ = tr.Record(recent)

	got, _ := LoadConfidenceLog(path, now.Add(-1*time.Hour))
	if len(got) != 1 {
		t.Errorf("since-filter = %d records, want 1", len(got))
	}
	if got[0].Query != "recent" {
		t.Errorf("kept wrong record: %+v", got[0])
	}
}

func TestLoadConfidenceLog_MissingFile(t *testing.T) {
	got, err := LoadConfidenceLog(filepath.Join(t.TempDir(), "missing.jsonl"), time.Time{})
	if err != nil {
		t.Errorf("missing file should not error, got %v", err)
	}
	if len(got) != 0 {
		t.Errorf("missing file should yield empty, got %d", len(got))
	}
}

func TestLoadConfidenceLog_TolerateMalformedLines(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "conf.jsonl")
	body := `{"ts":"2026-05-18T10:00:00Z","query":"good","top1":0.9,"k":1}` + "\n" +
		`{not json}` + "\n" +
		`{"ts":"2026-05-18T11:00:00Z","query":"also-good","top1":0.8,"k":1}` + "\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := LoadConfidenceLog(path, time.Time{})
	if err != nil {
		t.Fatalf("malformed-line tolerance failed: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("got %d records, want 2 (malformed dropped)", len(got))
	}
}

func TestSummarizeConfidence(t *testing.T) {
	records := []ConfidenceRecord{
		{Top1: 0.9, Top2: 0.7, Ratio12: 1.286, StdDev: 0.2},
		{Top1: 0.5, Top2: 0.45, Ratio12: 1.11, StdDev: 0.1}, // low confidence
		{Top1: 0.8, Top2: 0.3, Ratio12: 2.67, StdDev: 0.3},
	}
	s := SummarizeConfidence(records)
	if s.Count != 3 {
		t.Errorf("count = %d, want 3", s.Count)
	}
	if s.LowConfidenceCount != 1 {
		t.Errorf("low confidence = %d, want 1 (Ratio12 < 1.25)", s.LowConfidenceCount)
	}
	if s.MedianTop1 != 0.8 {
		t.Errorf("median top1 = %.2f, want 0.8", s.MedianTop1)
	}
}

func TestSummarizeConfidence_Empty(t *testing.T) {
	s := SummarizeConfidence(nil)
	if s.Count != 0 || s.MedianTop1 != 0 {
		t.Errorf("empty input should yield zero summary, got %+v", s)
	}
}

func TestDefaultConfidenceLogPath(t *testing.T) {
	got := DefaultConfidenceLogPath()
	if got == "" {
		return // UserCacheDir unavailable
	}
	if filepath.Base(got) != "confidence.jsonl" {
		t.Errorf("default path basename = %q, want confidence.jsonl", filepath.Base(got))
	}
}
