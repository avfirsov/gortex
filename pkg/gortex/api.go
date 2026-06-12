// Package gortex provides a public API for embedding the Gortex code intelligence engine.
package gortex

import (
	"go.uber.org/zap"

	"github.com/zzet/gortex/internal/config"
	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/indexer"
	"github.com/zzet/gortex/internal/parser"
	"github.com/zzet/gortex/internal/parser/languages"
	"github.com/zzet/gortex/internal/query"
)

// Engine is the public entry point for the Gortex code intelligence engine.
type Engine struct {
	graph   *graph.Graph
	indexer *indexer.Indexer
	query   *query.Engine
}

// Option configures an Engine.
type Option func(*config.IndexConfig)

// WithWorkers sets the number of parallel parsing workers.
func WithWorkers(n int) Option {
	return func(c *config.IndexConfig) { c.Workers = n }
}

// WithExclude adds exclude patterns.
func WithExclude(patterns ...string) Option {
	return func(c *config.IndexConfig) { c.Exclude = append(c.Exclude, patterns...) }
}

// New creates a new Gortex Engine with the given options.
func New(opts ...Option) *Engine {
	cfg := config.Default()
	for _, o := range opts {
		o(&cfg.Index)
	}

	g := graph.New()
	reg := parser.NewRegistry()
	languages.RegisterAll(reg)

	idx := indexer.New(g, reg, cfg.Index, zap.NewNop())
	eng := query.NewEngine(g)

	return &Engine{graph: g, indexer: idx, query: eng}
}

// IndexResult is the result of an indexing operation.
type IndexResult = indexer.IndexResult

// Index walks root and populates the knowledge graph.
func (e *Engine) Index(root string) (*IndexResult, error) {
	return e.indexer.Index(root)
}

// IndexFile re-indexes a single file (evict + re-parse).
func (e *Engine) IndexFile(filePath string) error {
	return e.indexer.IndexFile(filePath)
}

// EvictFile removes all data for a file from the graph.
func (e *Engine) EvictFile(filePath string) (int, int) {
	return e.indexer.EvictFile(filePath)
}

// GetSymbol returns a node by ID.
func (e *Engine) GetSymbol(id string) *graph.Node {
	return e.query.GetSymbol(id)
}

// FindSymbols finds nodes matching a name, optionally filtered by kind.
func (e *Engine) FindSymbols(name string, kinds ...graph.NodeKind) []*graph.Node {
	return e.query.FindSymbols(name, kinds...)
}

// SubGraph is a query result.
type SubGraph = query.SubGraph

// GetDependencies returns outgoing dependencies.
func (e *Engine) GetDependencies(nodeID string, depth, limit int) *SubGraph {
	return e.query.GetDependencies(nodeID, query.QueryOptions{Depth: depth, Limit: limit, Detail: "brief"})
}

// GetDependents returns the blast radius for a symbol.
func (e *Engine) GetDependents(nodeID string, depth, limit int) *SubGraph {
	return e.query.GetDependents(nodeID, query.QueryOptions{Depth: depth, Limit: limit, Detail: "brief"})
}

// GetCallChain traces the call graph forward from a function.
func (e *Engine) GetCallChain(funcID string, depth, limit int) *SubGraph {
	return e.query.GetCallChain(funcID, query.QueryOptions{Depth: depth, Limit: limit, Detail: "brief"})
}

// Stats returns summary statistics for the graph.
func (e *Engine) Stats() *graph.GraphStats {
	return e.query.Stats()
}
