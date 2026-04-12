package main

import (
	"compress/gzip"
	"encoding/gob"
	"fmt"
	"os"

	"go.uber.org/zap"

	"github.com/zzet/gortex/internal/daemon"
	"github.com/zzet/gortex/internal/graph"
)

// daemonSnapshot is the on-disk shape we round-trip the graph through.
// Kept minimal — on a cold start we re-index anyway if the snapshot
// is absent, so this carries the nodes/edges plus a schema version tag
// to detect format drift across daemon upgrades.
type daemonSnapshot struct {
	SchemaVersion int
	Version       string
	Nodes         []*graph.Node
	Edges         []*graph.Edge
}

// snapshotSchemaVersion is bumped whenever daemonSnapshot's shape or
// semantics change in a way that older snapshots can no longer be
// interpreted. Mismatching on load: we silently discard the snapshot
// and re-index from scratch.
const snapshotSchemaVersion = 1

// saveSnapshot writes a gob+gzip snapshot of the graph to the daemon's
// snapshot path. Called from the daemon's shutdown hook. Errors are
// logged but never propagated — a failed snapshot write should never
// block clean shutdown.
func saveSnapshot(g *graph.Graph, version string, logger *zap.Logger) {
	if g == nil {
		return
	}
	path := daemon.SnapshotPath()
	if err := daemon.EnsureParentDir(path); err != nil {
		logger.Warn("snapshot: parent dir", zap.Error(err))
		return
	}
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		logger.Warn("snapshot: create tmp", zap.Error(err))
		return
	}

	gz := gzip.NewWriter(f)
	enc := gob.NewEncoder(gz)

	snap := daemonSnapshot{
		SchemaVersion: snapshotSchemaVersion,
		Version:       version,
		Nodes:         g.AllNodes(),
		Edges:         g.AllEdges(),
	}
	if err := enc.Encode(snap); err != nil {
		logger.Warn("snapshot: encode", zap.Error(err))
		_ = gz.Close()
		_ = f.Close()
		_ = os.Remove(tmp)
		return
	}
	if err := gz.Close(); err != nil {
		logger.Warn("snapshot: gzip close", zap.Error(err))
		_ = f.Close()
		_ = os.Remove(tmp)
		return
	}
	if err := f.Close(); err != nil {
		logger.Warn("snapshot: file close", zap.Error(err))
		_ = os.Remove(tmp)
		return
	}
	// Atomic swap so a concurrent crash can never leave a truncated
	// snapshot on disk.
	if err := os.Rename(tmp, path); err != nil {
		logger.Warn("snapshot: rename", zap.Error(err))
		return
	}
	logger.Info("snapshot: wrote",
		zap.String("path", path),
		zap.Int("nodes", len(snap.Nodes)),
		zap.Int("edges", len(snap.Edges)))
}

// loadSnapshot reads the snapshot at daemon.SnapshotPath() into g. Returns
// (loaded=false, nil) when no snapshot exists — that's the expected
// first-run / post-reset case, not an error. Schema mismatches are
// logged and treated as absent so we don't try to interpret bytes we
// don't understand.
func loadSnapshot(g *graph.Graph, logger *zap.Logger) (loaded bool, err error) {
	if g == nil {
		return false, nil
	}
	path := daemon.SnapshotPath()
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, fmt.Errorf("open snapshot: %w", err)
	}
	defer func() { _ = f.Close() }()

	gz, err := gzip.NewReader(f)
	if err != nil {
		return false, fmt.Errorf("gzip reader: %w", err)
	}
	defer func() { _ = gz.Close() }()

	var snap daemonSnapshot
	if err := gob.NewDecoder(gz).Decode(&snap); err != nil {
		return false, fmt.Errorf("decode snapshot: %w", err)
	}
	if snap.SchemaVersion != snapshotSchemaVersion {
		logger.Info("snapshot: schema mismatch, ignoring",
			zap.Int("on_disk", snap.SchemaVersion),
			zap.Int("expected", snapshotSchemaVersion))
		return false, nil
	}

	for _, n := range snap.Nodes {
		g.AddNode(n)
	}
	for _, e := range snap.Edges {
		g.AddEdge(e)
	}
	logger.Info("snapshot: loaded",
		zap.String("path", path),
		zap.Int("nodes", len(snap.Nodes)),
		zap.Int("edges", len(snap.Edges)))
	return true, nil
}
