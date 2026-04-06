package indexer

import (
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	"go.uber.org/zap"

	"github.com/zzet/gortex/internal/config"
)

// ChangeKind describes the type of filesystem change.
type ChangeKind string

const (
	ChangeCreated  ChangeKind = "created"
	ChangeModified ChangeKind = "modified"
	ChangeDeleted  ChangeKind = "deleted"
	ChangeRenamed  ChangeKind = "renamed"
)

// GraphChangeEvent is emitted after a successful graph patch.
type GraphChangeEvent struct {
	FilePath     string     `json:"file_path"`
	Kind         ChangeKind `json:"kind"`
	NodesAdded   int        `json:"nodes_added"`
	NodesRemoved int        `json:"nodes_removed"`
	EdgesAdded   int        `json:"edges_added"`
	EdgesRemoved int        `json:"edges_removed"`
	Timestamp    time.Time  `json:"timestamp"`
	DurationMs   int64      `json:"duration_ms"`
}

// Watcher keeps the knowledge graph in live sync with the filesystem.
type Watcher struct {
	indexer   *Indexer
	fsw       *fsnotify.Watcher
	config    config.WatchConfig
	events    chan GraphChangeEvent
	history   []GraphChangeEvent
	historyMu sync.Mutex
	pending   map[string]*time.Timer
	mu        sync.Mutex
	logger    *zap.Logger
	done      chan struct{}
	stopped   chan struct{}
}

const maxHistory = 1000

// NewWatcher creates a Watcher for the given indexer.
func NewWatcher(idx *Indexer, cfg config.WatchConfig, logger *zap.Logger) (*Watcher, error) {
	fsw, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}

	debounce := cfg.DebounceMs
	if debounce <= 0 {
		debounce = 150
	}
	cfg.DebounceMs = debounce

	return &Watcher{
		indexer: idx,
		fsw:     fsw,
		config:  cfg,
		events:  make(chan GraphChangeEvent, 64),
		pending: make(map[string]*time.Timer),
		logger:  logger,
		done:    make(chan struct{}),
		stopped: make(chan struct{}),
	}, nil
}

// Start begins watching the given paths recursively.
func (w *Watcher) Start(paths []string) error {
	for _, p := range paths {
		absPath, err := filepath.Abs(p)
		if err != nil {
			return err
		}
		if err := w.addRecursive(absPath); err != nil {
			return err
		}
	}

	go w.loop()
	return nil
}

// Stop halts the watcher and cleans up resources.
func (w *Watcher) Stop() error {
	close(w.done)
	err := w.fsw.Close()
	<-w.stopped
	return err
}

// Events returns a read-only channel of graph change events.
func (w *Watcher) Events() <-chan GraphChangeEvent {
	return w.events
}

// History returns recent change events (up to maxHistory).
func (w *Watcher) History() []GraphChangeEvent {
	w.historyMu.Lock()
	defer w.historyMu.Unlock()
	out := make([]GraphChangeEvent, len(w.history))
	copy(out, w.history)
	return out
}

// HistorySince returns change events after the given timestamp.
func (w *Watcher) HistorySince(since time.Time) []GraphChangeEvent {
	w.historyMu.Lock()
	defer w.historyMu.Unlock()
	var out []GraphChangeEvent
	for _, ev := range w.history {
		if ev.Timestamp.After(since) {
			out = append(out, ev)
		}
	}
	return out
}

func (w *Watcher) loop() {
	defer close(w.stopped)
	for {
		select {
		case <-w.done:
			return
		case event, ok := <-w.fsw.Events:
			if !ok {
				return
			}
			w.handleEvent(event)
		case err, ok := <-w.fsw.Errors:
			if !ok {
				return
			}
			w.logger.Warn("watcher error", zap.Error(err))
		}
	}
}

func (w *Watcher) handleEvent(event fsnotify.Event) {
	path := event.Name

	// Skip events from always-excluded directories.
	if inExcludedDir(path) {
		return
	}

	// If a new directory is created, watch it recursively.
	if event.Has(fsnotify.Create) {
		if info, err := os.Stat(path); err == nil && info.IsDir() {
			_ = w.addRecursive(path)
			return
		}
	}

	// Skip excluded paths.
	if w.isExcluded(path) {
		return
	}

	// Only process files with known extensions.
	if _, ok := w.indexer.registry.DetectLanguage(path); !ok {
		// Still handle remove for previously indexed files.
		if !event.Has(fsnotify.Remove) && !event.Has(fsnotify.Rename) {
			return
		}
	}

	// Debounce: reset or start timer for this file.
	w.mu.Lock()
	if timer, exists := w.pending[path]; exists {
		timer.Stop()
	}

	var kind ChangeKind
	switch {
	case event.Has(fsnotify.Create):
		kind = ChangeCreated
	case event.Has(fsnotify.Write):
		kind = ChangeModified
	case event.Has(fsnotify.Remove):
		kind = ChangeDeleted
	case event.Has(fsnotify.Rename):
		kind = ChangeRenamed
	default:
		w.mu.Unlock()
		return
	}

	debounce := time.Duration(w.config.DebounceMs) * time.Millisecond
	w.pending[path] = time.AfterFunc(debounce, func() {
		w.patchGraph(path, kind)
		w.mu.Lock()
		delete(w.pending, path)
		w.mu.Unlock()
	})
	w.mu.Unlock()
}

func (w *Watcher) patchGraph(path string, kind ChangeKind) {
	start := time.Now()
	var nodesAdded, nodesRemoved, edgesAdded, edgesRemoved int

	nodesBefore := w.indexer.graph.NodeCount()
	edgesBefore := w.indexer.graph.EdgeCount()

	switch kind {
	case ChangeCreated:
		if err := w.indexer.IndexFile(path); err != nil {
			w.logger.Warn("index file failed", zap.String("path", path), zap.Error(err))
			return
		}
		nodesAdded = w.indexer.graph.NodeCount() - nodesBefore
		edgesAdded = w.indexer.graph.EdgeCount() - edgesBefore

	case ChangeModified:
		nr, er := w.indexer.EvictFile(path)
		nodesRemoved = nr
		edgesRemoved = er
		if err := w.indexer.IndexFile(path); err != nil {
			w.logger.Warn("reindex file failed", zap.String("path", path), zap.Error(err))
			return
		}
		nodesAdded = w.indexer.graph.NodeCount() - (nodesBefore - nr)
		edgesAdded = w.indexer.graph.EdgeCount() - (edgesBefore - er)

	case ChangeDeleted, ChangeRenamed:
		nr, er := w.indexer.EvictFile(path)
		nodesRemoved = nr
		edgesRemoved = er
	}

	ev := GraphChangeEvent{
		FilePath:     path,
		Kind:         kind,
		NodesAdded:   nodesAdded,
		NodesRemoved: nodesRemoved,
		EdgesAdded:   edgesAdded,
		EdgesRemoved: edgesRemoved,
		Timestamp:    time.Now(),
		DurationMs:   time.Since(start).Milliseconds(),
	}

	w.historyMu.Lock()
	w.history = append(w.history, ev)
	if len(w.history) > maxHistory {
		w.history = w.history[len(w.history)-maxHistory:]
	}
	w.historyMu.Unlock()

	// Non-blocking send.
	select {
	case w.events <- ev:
	default:
	}

	w.logger.Info("graph patch",
		zap.String("kind", string(kind)),
		zap.String("file", path),
		zap.Int("nodes+", nodesAdded),
		zap.Int("nodes-", nodesRemoved),
		zap.Int("edges+", edgesAdded),
		zap.Int("edges-", edgesRemoved),
		zap.Int64("ms", ev.DurationMs),
	)
}

// alwaysExcludeDirs are directories that should never be watched,
// regardless of configuration.
var alwaysExcludeDirs = map[string]bool{
	".git":         true,
	"node_modules": true,
	"vendor":       true,
	".hg":          true,
	".svn":         true,
	"__pycache__":  true,
	".mypy_cache":  true,
	".tox":         true,
	".venv":        true,
}

func (w *Watcher) addRecursive(root string) error {
	return filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if alwaysExcludeDirs[d.Name()] {
				return filepath.SkipDir
			}
			if w.isExcluded(path) {
				return filepath.SkipDir
			}
			return w.fsw.Add(path)
		}
		return nil
	})
}

// inExcludedDir checks if the path contains an always-excluded directory component.
func inExcludedDir(path string) bool {
	dir := path
	for {
		base := filepath.Base(dir)
		if alwaysExcludeDirs[base] {
			return true
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return false
}

func (w *Watcher) isExcluded(path string) bool {
	rel, err := filepath.Rel(w.indexer.rootPath, path)
	if err != nil {
		return false
	}
	rel = filepath.ToSlash(rel)

	for _, pattern := range w.config.Exclude {
		matched, _ := filepath.Match(pattern, filepath.Base(path))
		if matched {
			return true
		}
		// Check directory-based patterns.
		dir := pattern
		for _, prefix := range []string{"**/", "*/"} {
			dir = filepath.ToSlash(dir)
			if len(dir) > len(prefix) && dir[:len(prefix)] == prefix {
				dir = dir[len(prefix):]
			}
		}
		dir = filepath.ToSlash(dir)
		dir = filepath.Clean(dir)
		if dir != "." && (filepath.ToSlash(rel) == dir ||
			len(rel) > len(dir) && rel[:len(dir)+1] == dir+"/") {
			return true
		}
	}
	return false
}
