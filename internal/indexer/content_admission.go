package indexer

import (
	"path/filepath"

	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/parser"
)

// Content-admission skip reasons. They ride on a synthetic file node's
// Meta["skip_reason"] so a dropped asset stays listable and index_health
// rolls it up, mirroring the size / timeout / minified skip telemetry.
const (
	skipReasonLargeDocument = "large_document"   // document over the per-file cap
	skipReasonVectorData    = "vector_data"      // data asset, data indexing off
	skipReasonLargeData     = "large_data_asset" // data asset over the per-file cap
)

// contentAdmissionGate decides, by asset class, whether a non-source artifact
// is admitted into extraction. It is built once before a walk from the
// registry's asset-class map and the resolved config caps, so the hot
// per-file check is a single map lookup. A nil gate is inert — the common
// all-code repo (no asset extractors registered) pays nothing.
type contentAdmissionGate struct {
	classes    map[string]parser.AssetClass
	docLimit   int64
	docCapped  bool
	indexData  bool
	dataLimit  int64
	dataCapped bool
}

// newContentAdmissionGate builds the gate from the indexer's registry and
// config. Returns nil when no asset extractors are registered.
func (idx *Indexer) newContentAdmissionGate() *contentAdmissionGate {
	classes := idx.registry.AssetClasses()
	if len(classes) == 0 {
		return nil
	}
	c := idx.config.Content
	docLimit, docCapped := c.EffectiveMaxDocumentBytes()
	dataLimit, dataCapped := c.EffectiveMaxDataBytes()
	return &contentAdmissionGate{
		classes:    classes,
		docLimit:   docLimit,
		docCapped:  docCapped,
		indexData:  c.IndexData,
		dataLimit:  dataLimit,
		dataCapped: dataCapped,
	}
}

// skip reports whether a file of the given walk-time language and size should
// be dropped before it is read and extracted, and the telemetry reason. A
// non-asset language (the gate has no entry for it) is never gated.
func (g *contentAdmissionGate) skip(lang string, size int64) (string, bool) {
	if g == nil {
		return "", false
	}
	switch g.classes[lang] {
	case parser.AssetDocument:
		if g.docCapped && size > g.docLimit {
			return skipReasonLargeDocument, true
		}
	case parser.AssetData:
		if !g.indexData {
			return skipReasonVectorData, true
		}
		if g.dataCapped && size > g.dataLimit {
			return skipReasonLargeData, true
		}
	}
	return "", false
}

// contentSkipNode builds a synthetic file node for a content / data asset
// dropped by the admission gate, carrying the skip reason and size so the
// file stays visible (queryable, index_health rollup) without being read or
// parsed — the same treatment size-capped files get.
func contentSkipNode(sf skippedFile) *graph.Node {
	return &graph.Node{
		ID:        sf.relPath,
		Kind:      graph.KindFile,
		Name:      filepath.Base(sf.relPath),
		FilePath:  sf.relPath,
		Language:  sf.lang,
		StartLine: 1,
		Meta: map[string]any{
			"skip_reason":            sf.reason,
			"skipped_due_to_content": true,
			"file_size_bytes":        sf.size,
		},
	}
}

// emitContentSkipNodes adds a synthetic file node for every asset dropped by
// the content-admission gate, so the file stays listable with skip telemetry
// instead of vanishing silently.
func (idx *Indexer) emitContentSkipNodes(skipped []skippedFile) {
	if len(skipped) == 0 {
		return
	}
	nodes := make([]*graph.Node, 0, len(skipped))
	for _, sf := range skipped {
		nodes = append(nodes, contentSkipNode(sf))
	}
	idx.applyRepoPrefix(nodes, nil)
	idx.graph.AddBatch(nodes, nil)
}
