package quality

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/zzet/gortex/internal/platform"
)

// ConfidenceRecord captures the candidate-score distribution for
// one search call. The distribution shape (top-1 vs top-2 gap,
// std-dev, ratio) tells you how confident the ranker was — a sharp
// top-1 with a long tail is a high-confidence answer; a flat
// distribution is the ranker shrugging.
type ConfidenceRecord struct {
	TS       time.Time `json:"ts"`
	Query    string    `json:"query"`
	Top1     float64   `json:"top1"`
	Top2     float64   `json:"top2,omitempty"`
	Mean     float64   `json:"mean"`
	StdDev   float64   `json:"std_dev"`
	Ratio12  float64   `json:"ratio_top1_top2,omitempty"` // top1 / top2; >>1 = confident
	K        int       `json:"k"`                          // number of scored candidates
}

// ConfidenceFromScores derives a ConfidenceRecord from a slice of
// per-candidate scores. Negative / zero K returns a zero record so
// the caller can opt out by passing an empty slice.
func ConfidenceFromScores(query string, scores []float64) ConfidenceRecord {
	r := ConfidenceRecord{
		TS:    time.Now().UTC(),
		Query: query,
		K:     len(scores),
	}
	if len(scores) == 0 {
		return r
	}
	sorted := make([]float64, len(scores))
	copy(sorted, scores)
	sort.Sort(sort.Reverse(sort.Float64Slice(sorted)))
	r.Top1 = sorted[0]
	if len(sorted) > 1 {
		r.Top2 = sorted[1]
		if r.Top2 != 0 {
			r.Ratio12 = r.Top1 / r.Top2
		}
	}
	var sum float64
	for _, v := range scores {
		sum += v
	}
	r.Mean = sum / float64(len(scores))
	var ss float64
	for _, v := range scores {
		ss += (v - r.Mean) * (v - r.Mean)
	}
	r.StdDev = math.Sqrt(ss / float64(len(scores)))
	return r
}

// ConfidenceTracker appends records to a JSONL log on disk. Safe
// for in-process concurrent appends because the file is opened
// with O_APPEND.
type ConfidenceTracker struct {
	Path string
}

// NewConfidenceTracker returns a tracker bound to a log path.
// Empty path is allowed — Record then no-ops without erroring.
func NewConfidenceTracker(path string) *ConfidenceTracker {
	return &ConfidenceTracker{Path: path}
}

// DefaultConfidenceLogPath is the canonical persistence location.
// An absolute $XDG_CACHE_HOME is honoured; otherwise the log stays
// under os.UserCacheDir() as before. Returns empty when no cache dir
// can be resolved.
func DefaultConfidenceLogPath() string {
	if v := os.Getenv("XDG_CACHE_HOME"); v == "" || !filepath.IsAbs(v) {
		if base, err := os.UserCacheDir(); err != nil || base == "" {
			return ""
		}
	}
	return filepath.Join(platform.OSCacheDir(), "confidence.jsonl")
}

// Record appends one record to the log. No-op when Path is empty.
func (t *ConfidenceTracker) Record(r ConfidenceRecord) error {
	if t.Path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(t.Path), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(t.Path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	body, err := json.Marshal(r)
	if err != nil {
		return err
	}
	body = append(body, '\n')
	_, err = f.Write(body)
	return err
}

// LoadConfidenceLog reads the log at path and returns records with
// ts >= since (zero since returns all). Malformed lines are
// skipped so a previous crash mid-write doesn't break readers.
func LoadConfidenceLog(path string, since time.Time) ([]ConfidenceRecord, error) {
	if path == "" {
		return nil, nil
	}
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("open confidence log: %w", err)
	}
	defer func() { _ = f.Close() }()
	r := bufio.NewReaderSize(f, 64*1024)
	out := []ConfidenceRecord{}
	for {
		line, err := r.ReadBytes('\n')
		if len(line) > 0 {
			if n := len(line); n > 0 && line[n-1] == '\n' {
				line = line[:n-1]
			}
			if len(line) > 0 {
				var rec ConfidenceRecord
				if json.Unmarshal(line, &rec) == nil {
					if since.IsZero() || !rec.TS.Before(since) {
						out = append(out, rec)
					}
				}
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return out, fmt.Errorf("read confidence log: %w", err)
		}
	}
	return out, nil
}

// ConfidenceSummary aggregates a slice of records into the headline
// statistics the `gortex eval quality confidence` subcommand emits.
type ConfidenceSummary struct {
	Count            int     `json:"count"`
	MedianTop1       float64 `json:"median_top1"`
	MedianRatio12    float64 `json:"median_ratio_top1_top2"`
	MedianStdDev     float64 `json:"median_std_dev"`
	LowConfidenceCount int   `json:"low_confidence_count"` // records with Ratio12 < 1.25
}

// SummarizeConfidence reduces a slice of records into the summary.
// Median across records; "low confidence" = Ratio12 < 1.25 (the
// ranker barely separated top-1 from top-2).
func SummarizeConfidence(records []ConfidenceRecord) ConfidenceSummary {
	s := ConfidenceSummary{Count: len(records)}
	if len(records) == 0 {
		return s
	}
	top1s := make([]float64, 0, len(records))
	ratios := make([]float64, 0, len(records))
	stds := make([]float64, 0, len(records))
	for _, r := range records {
		top1s = append(top1s, r.Top1)
		stds = append(stds, r.StdDev)
		if r.Ratio12 > 0 {
			ratios = append(ratios, r.Ratio12)
		}
		if r.Ratio12 > 0 && r.Ratio12 < 1.25 {
			s.LowConfidenceCount++
		}
	}
	s.MedianTop1 = medianFloats(top1s)
	s.MedianRatio12 = medianFloats(ratios)
	s.MedianStdDev = medianFloats(stds)
	return s
}

func medianFloats(vs []float64) float64 {
	if len(vs) == 0 {
		return 0
	}
	c := make([]float64, len(vs))
	copy(c, vs)
	sort.Float64s(c)
	return c[len(c)/2]
}
