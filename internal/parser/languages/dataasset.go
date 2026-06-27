package languages

import (
	"crypto/sha256"
	"encoding/hex"
	"io"

	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/parser"
)

// dataAssetShaCap bounds the file size we stream-hash for a data-asset node;
// larger files (multi-GB datasets) get a size-only node to avoid the full read.
const dataAssetShaCap = 64 << 20

// DataAssetExtractor records pure data / binary assets (columnar stores, array
// dumps, dataset shards) as a single metadata-only KindFile node — never
// parsed. They are data, not knowledge: a node keeps them listable and linkable
// without feeding a binary blob to any grammar. Tagged data_class=data so they
// stay out of the content retrieval profile (which keys on data_class=content).
type DataAssetExtractor struct{}

func NewDataAssetExtractor() *DataAssetExtractor { return &DataAssetExtractor{} }

func (e *DataAssetExtractor) Language() string { return "data" }
func (e *DataAssetExtractor) Extensions() []string {
	return []string{".parquet", ".npy", ".npz", ".lance", ".arrow", ".feather"}
}
func (e *DataAssetExtractor) AssetClass() parser.AssetClass { return parser.AssetData }

var (
	_ parser.StreamingExtractor = (*DataAssetExtractor)(nil)
	_ parser.AssetExtractor     = (*DataAssetExtractor)(nil)
)

func (e *DataAssetExtractor) Extract(filePath string, src []byte) (*parser.ExtractionResult, error) {
	sum := sha256.Sum256(src)
	return &parser.ExtractionResult{Nodes: []*graph.Node{
		dataAssetNode(filePath, int64(len(src)), hex.EncodeToString(sum[:])),
	}}, nil
}

func (e *DataAssetExtractor) ExtractStream(filePath string, r io.ReaderAt, size int64, emit func(*graph.Node, []*graph.Edge)) error {
	sha := ""
	if size <= dataAssetShaCap {
		h := sha256.New()
		if _, err := io.Copy(h, io.NewSectionReader(r, 0, size)); err == nil {
			sha = hex.EncodeToString(h.Sum(nil))
		}
	}
	emit(dataAssetNode(filePath, size, sha), nil)
	return nil
}

func dataAssetNode(filePath string, size int64, sha string) *graph.Node {
	meta := map[string]any{
		"asset_kind": "data",
		"data_class": "data",
		"size_bytes": int(size),
	}
	if sha != "" {
		meta["sha256"] = sha
	}
	return &graph.Node{
		ID: filePath, Kind: graph.KindFile, Name: filePath,
		FilePath: filePath, StartLine: 1, Language: "data", Meta: meta,
	}
}
