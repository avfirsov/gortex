package main

import (
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"strings"

	"github.com/zzet/gortex/internal/config"
	"github.com/zzet/gortex/internal/embedding"
	"github.com/zzet/gortex/internal/persistence"
	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/indexer"
	gortexmcp "github.com/zzet/gortex/internal/mcp"
	"github.com/zzet/gortex/internal/parser"
	"github.com/zzet/gortex/internal/parser/languages"
	"github.com/zzet/gortex/internal/query"
	"github.com/zzet/gortex/internal/semantic"
	"github.com/zzet/gortex/internal/semantic/goanalysis"
	"github.com/zzet/gortex/internal/semantic/lsp"
	"github.com/zzet/gortex/internal/semantic/scip"
	"github.com/zzet/gortex/internal/server"
	"github.com/zzet/gortex/internal/server/hub"
)

var (
	serverPort       int
	serverBind       string
	serverAuthToken  string
	serverIndex      string
	serverCORSOrigin string
	serverWatch      bool
	serverTrack      []string
	serverProject    string
	serverCacheDir        string
	serverNoCache         bool
	serverEmbeddings      bool
	serverEmbeddingsURL   string
	serverEmbeddingsModel string
	serverSemantic        bool
	serverNoSemantic      bool
	serverSemanticMode    string
)

var serverCmd = &cobra.Command{
	Use:   "server",
	Short: "Start the HTTP server API for external integrations",
	Long:  "Exposes Gortex MCP tools as an HTTP/JSON API under /v1/*: /v1/health, /v1/tools, /v1/tools/{name}, /v1/stats, /v1/graph, /v1/events. The UI is a separate Next.js frontend (see web/) that talks to this server over HTTP.",
	RunE:  runServer,
}

func init() {
	serverCmd.Flags().IntVar(&serverPort, "port", 4747, "HTTP port to listen on")
	serverCmd.Flags().StringVar(&serverBind, "bind", "127.0.0.1", "bind address (e.g. 127.0.0.1, 0.0.0.0); requires --auth-token when not localhost")
	serverCmd.Flags().StringVar(&serverAuthToken, "auth-token", "", "bearer token required on every /v1/* request (fallback: $GORTEX_SERVER_TOKEN)")
	serverCmd.Flags().StringVar(&serverIndex, "index", "", "repository path to index on startup")
	serverCmd.Flags().StringVar(&serverCORSOrigin, "cors-origin", "*", "allowed CORS origin (use '*' for any)")
	serverCmd.Flags().BoolVar(&serverWatch, "watch", false, "keep graph in sync with filesystem changes")
	serverCmd.Flags().StringSliceVar(&serverTrack, "track", nil, "additional repository paths to track")
	serverCmd.Flags().StringVar(&serverProject, "project", "", "active project name")
	serverCmd.Flags().StringVar(&serverCacheDir, "cache-dir", "", "graph cache directory (default ~/.cache/gortex/)")
	serverCmd.Flags().BoolVar(&serverNoCache, "no-cache", false, "disable graph caching")
	serverCmd.Flags().BoolVar(&serverEmbeddings, "embeddings", false, "enable semantic search")
	serverCmd.Flags().StringVar(&serverEmbeddingsURL, "embeddings-url", "", "embedding API URL (e.g. http://localhost:11434 for Ollama)")
	serverCmd.Flags().StringVar(&serverEmbeddingsModel, "embeddings-model", "", "embedding model name")
	serverCmd.Flags().BoolVar(&serverSemantic, "semantic", false, "enable semantic enrichment (SCIP, go/types, LSP)")
	serverCmd.Flags().BoolVar(&serverNoSemantic, "no-semantic", false, "disable semantic enrichment")
	serverCmd.Flags().StringVar(&serverSemanticMode, "semantic-mode", "typecheck", "Go analysis mode: typecheck or callgraph")
	rootCmd.AddCommand(serverCmd)
}

func runServer(_ *cobra.Command, _ []string) error {
	logger := newLogger()
	defer func() { _ = logger.Sync() }()

	// Resolve auth token: flag wins, env var fallback.
	authToken := serverAuthToken
	if authToken == "" {
		authToken = os.Getenv("GORTEX_SERVER_TOKEN")
	}

	// Bind/auth policy. Without a token we force the listener onto
	// localhost; binding to any external interface without auth is a
	// foot-gun (anyone on the network could invoke arbitrary MCP
	// tools), so reject that combination up front.
	if authToken == "" {
		if !isLocalhostBind(serverBind) {
			return fmt.Errorf("--bind %q requires --auth-token (or $GORTEX_SERVER_TOKEN); refusing to expose unauthenticated server on external interface", serverBind)
		}
		fmt.Fprintln(os.Stderr, "[gortex] server: unauthenticated mode; localhost only")
	}

	// Resolve a stable server id. We only fail when a token path is
	// set but not writable — without one, a future daemon client just
	// can't detect reconnects, which is non-fatal.
	serverID, err := resolveServerID(serverCacheDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[gortex] server: id persistence disabled: %v\n", err)
	}

	cfg, err := config.Load(cfgFile)
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	// Build graph/parser/indexer/query/MCP stack.
	g := graph.New()
	reg := parser.NewRegistry()
	languages.RegisterAll(reg)
	idx := indexer.New(g, reg, cfg.Index, logger)

	// Set up embedding provider for semantic search. Kept local so it
	// can be handed off to MultiIndexer below; otherwise per-repo
	// indexers built inside TrackRepoCtx have embedder=nil.
	var embedder embedding.Provider
	if serverEmbeddingsURL != "" {
		embedder = embedding.NewAPIProvider(serverEmbeddingsURL, serverEmbeddingsModel)
		fmt.Fprintf(os.Stderr, "[gortex] server: semantic search enabled (API: %s)\n", serverEmbeddingsURL)
	} else if serverEmbeddings {
		e, err := embedding.NewLocalProvider()
		if err != nil {
			fmt.Fprintf(os.Stderr, "[gortex] server: embeddings disabled: %v\n", err)
		} else {
			embedder = e
			fmt.Fprintf(os.Stderr, "[gortex] server: semantic search enabled (local)\n")
		}
	}
	if embedder != nil {
		idx.SetEmbedder(embedder)
	}

	// Set up semantic enrichment.
	if !serverNoSemantic && (serverSemantic || cfg.Semantic.Enabled) {
		semCfg := cfg.Semantic
		semCfg.Enabled = true

		semInternalCfg := semantic.Config{
			Enabled:           semCfg.Enabled,
			TimeoutSeconds:    semCfg.TimeoutSeconds,
			EnrichOnWatch:     semCfg.EnrichOnWatch,
			WatchDebounceMs:   semCfg.WatchDebounceMs,
			RefuteUnconfirmed: semCfg.RefuteUnconfirmed,
		}
		for _, pc := range semCfg.Providers {
			semInternalCfg.Providers = append(semInternalCfg.Providers, semantic.ProviderConfig{
				Name:        pc.Name,
				Command:     pc.Command,
				Args:        pc.Args,
				Languages:   pc.Languages,
				Priority:    pc.Priority,
				Enabled:     pc.Enabled,
				Mode:        pc.Mode,
				Daemon:      pc.Daemon,
				MaxParallel: pc.MaxParallel,
			})
		}

		semMgr := semantic.NewManager(semInternalCfg, logger)

		mode := goanalysis.ModeTypeCheck
		if serverSemanticMode == "callgraph" {
			mode = goanalysis.ModeCallGraph
		}
		semMgr.RegisterProvider(goanalysis.NewProvider(mode, false, logger))

		for _, pc := range semCfg.Providers {
			if !pc.Enabled {
				continue
			}
			switch {
			case strings.HasPrefix(pc.Name, "scip-") && pc.Command != "":
				semMgr.RegisterProvider(scip.NewProvider(pc.Command, pc.Args, pc.Languages, semCfg.TimeoutSeconds, logger))
			case strings.HasPrefix(pc.Name, "gopls") || pc.Daemon:
				semMgr.RegisterProvider(lsp.NewProvider(pc.Command, pc.Args, pc.Languages, pc.Daemon, pc.MaxParallel, logger))
			}
		}

		idx.SetSemanticManager(semMgr)
		fmt.Fprintf(os.Stderr, "[gortex] server: semantic enrichment enabled (mode: %s)\n", serverSemanticMode)
	}

	// Multi-repo support.
	cm, err := config.NewConfigManager("")
	if err != nil {
		fmt.Fprintf(os.Stderr, "[gortex] warning: could not load global config: %v\n", err)
	}

	if cm != nil && len(serverTrack) > 0 {
		for _, trackPath := range serverTrack {
			absPath, err := filepath.Abs(trackPath)
			if err != nil {
				fmt.Fprintf(os.Stderr, "[gortex] warning: could not resolve --track path %s: %v\n", trackPath, err)
				continue
			}
			if err := cm.Global().AddRepo(config.RepoEntry{Path: absPath}); err != nil {
				fmt.Fprintf(os.Stderr, "[gortex] warning: could not add --track repo %s: %v\n", absPath, err)
			}
		}
	}

	activeProject := serverProject
	if activeProject == "" && cm != nil {
		activeProject = cm.Global().ActiveProject
	}
	if cm != nil {
		cm.Global().ActiveProject = activeProject
	}

	var mi *indexer.MultiIndexer
	if cm != nil {
		mi = indexer.NewMultiIndexer(g, reg, idx.Search(), cm, logger)
		if embedder != nil {
			mi.SetEmbedder(embedder)
		}
	}

	var multiOpts []gortexmcp.MultiRepoOptions
	if mi != nil || cm != nil {
		multiOpts = append(multiOpts, gortexmcp.MultiRepoOptions{
			MultiIndexer:  mi,
			ConfigManager: cm,
			ActiveProject: activeProject,
		})
	}

	eng := query.NewEngine(g)
	eng.SetSearchProvider(idx.Search)
	gortexmcp.Version = version
	srv := gortexmcp.NewServer(eng, g, idx, nil, logger, cfg.Guards.Rules, multiOpts...)

	if semMgr := idx.SemanticManager(); semMgr != nil {
		srv.SetSemanticManager(semMgr)
	}

	// Create persistence store.
	var store persistence.Store
	if serverNoCache {
		store = persistence.NopStore{}
	} else {
		var err error
		store, err = persistence.NewFileStore(serverCacheDir, version)
		if err != nil {
			fmt.Fprintf(os.Stderr, "[gortex] server: cache disabled: %v\n", err)
			store = persistence.NopStore{}
		}
	}

	// Build the HTTP handler — start serving immediately, index in background.
	serverHandler := server.NewHandler(srv.MCPServer(), g, version, logger)
	if cm != nil {
		serverHandler.SetConfigManager(cm)
	}
	if serverID != "" {
		serverHandler.SetServerID(serverID)
	}

	// Watch mode: set up the event hub so /v1/events has a source.
	if serverWatch {
		wcfg := cfg.Watch
		wcfg.Enabled = true
		watcher, err := indexer.NewWatcher(idx, wcfg, logger)
		if err != nil {
			fmt.Fprintf(os.Stderr, "[gortex] server: watcher setup failed: %v\n", err)
		} else {
			watchPaths := wcfg.Paths
			if len(watchPaths) == 0 && serverIndex != "" {
				watchPaths = []string{serverIndex}
			}
			if len(watchPaths) == 0 {
				watchPaths = []string{"."}
			}
			if err := watcher.Start(watchPaths); err != nil {
				fmt.Fprintf(os.Stderr, "[gortex] server: watcher start failed: %v\n", err)
			} else {
				srv.SetWatcher(watcher)
				eventHub := hub.New()
				go eventHub.Run(watcher.Events())
				srv.WatchForReanalysis(eventHub, 500)
				serverHandler.SetEventHub(eventHub)
				fmt.Fprintf(os.Stderr, "[gortex] server: watch mode active\n")
			}
		}
	}

	// Wrap with auth (no-op when authToken is empty), then CORS.
	handler := server.WithAuth(serverHandler, authToken)
	corsOpts := server.CORSOptions{AllowOrigins: []string{serverCORSOrigin}}
	handler = server.WithCORS(handler, corsOpts)

	addr := fmt.Sprintf("%s:%d", serverBind, serverPort)
	httpServer := &http.Server{
		Addr:    addr,
		Handler: handler,
	}

	fmt.Fprintf(os.Stderr, "[gortex] server listening on http://%s\n", addr)

	errCh := make(chan error, 1)
	go func() {
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
	}()

	// Background: index, multi-repo, analyze — graph populates while HTTP is live.
	go func() {
		// When MultiIndexer is available (global config has repos), use it exclusively.
		// Single --index flag is only used when no multi-repo config exists.
		if mi != nil {
			fmt.Fprintf(os.Stderr, "[gortex] server: multi-repo indexing...\n")
			if _, err := mi.IndexAll(); err != nil {
				fmt.Fprintf(os.Stderr, "[gortex] server: multi-repo indexing error: %v\n", err)
			}
		} else if serverIndex != "" {
			commitHash := gitCommitHash(serverIndex)
			cached := false

			if commitHash != "" && store.Check(serverIndex, commitHash) && store.Validate(serverIndex, commitHash) {
				snap, err := store.Load(serverIndex, commitHash)
				if err == nil {
					for _, n := range snap.Nodes {
						g.AddNode(n)
					}
					for _, e := range snap.Edges {
						g.AddEdge(e)
					}
					idx.SetFileMtimes(snap.FileMtimes)
					idx.SetRootPath(serverIndex)

					if len(snap.VectorIndex) > 0 && snap.VectorDims > 0 {
						if err := idx.ImportVectorIndex(snap.VectorIndex, snap.VectorDims, snap.VectorCount); err != nil {
							fmt.Fprintf(os.Stderr, "[gortex] server: vector index restore failed: %v\n", err)
						}
					}

					result, err := idx.IncrementalReindex(serverIndex)
					if err != nil {
						fmt.Fprintf(os.Stderr, "[gortex] server: incremental reindex failed: %v\n", err)
					} else {
						fmt.Fprintf(os.Stderr, "[gortex] server: restored graph (%d nodes, %d edges), re-indexed %d stale files in %dms\n",
							result.NodeCount, result.EdgeCount, result.FileCount, result.DurationMs)
					}
					cached = true
				} else {
					fmt.Fprintf(os.Stderr, "[gortex] server: cache load failed, will re-index: %v\n", err)
				}
			}

			if !cached {
				fmt.Fprintf(os.Stderr, "[gortex] server: indexing %s...\n", serverIndex)
				result, err := idx.Index(serverIndex)
				if err != nil {
					fmt.Fprintf(os.Stderr, "[gortex] server: indexing failed: %v\n", err)
					return
				}
				fmt.Fprintf(os.Stderr, "[gortex] server: indexed %d files (%d nodes, %d edges) in %dms\n",
					result.FileCount, result.NodeCount, result.EdgeCount, result.DurationMs)
			}
		}

		// Search backend is auto-updated via SearchProvider (idx.Search)

		// Set contract registry: in multi-repo mode, merge all per-repo registries.
		if mi != nil {
			srv.SetContractRegistry(mi.MergedContractRegistry())
		} else if cr := idx.ContractRegistry(); cr != nil {
			srv.SetContractRegistry(cr)
		}

		srv.RunAnalysis()
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	select {
	case err := <-errCh:
		return fmt.Errorf("server: %w", err)
	case sig := <-sigCh:
		fmt.Fprintf(os.Stderr, "\n[gortex] server: received %s, shutting down\n", sig)

		if serverIndex != "" {
			commitHash := gitCommitHash(serverIndex)
			if commitHash != "" {
				snap := &persistence.Snapshot{
					Version:    version,
					RepoPath:   serverIndex,
					CommitHash: commitHash,
					IndexedAt:  time.Now(),
					Nodes:      g.AllNodes(),
					Edges:      g.AllEdges(),
					FileMtimes: idx.FileMtimes(),
				}
				snap.VectorIndex, snap.VectorDims, snap.VectorCount = idx.ExportVectorIndex()
				if err := store.Save(snap); err != nil {
					fmt.Fprintf(os.Stderr, "[gortex] server: cache save failed: %v\n", err)
				} else {
					fmt.Fprintf(os.Stderr, "[gortex] server: saved graph snapshot (%d nodes, %d edges)\n",
						len(snap.Nodes), len(snap.Edges))
				}
			}
		}

		return httpServer.Close()
	}
}

// isLocalhostBind reports whether bind resolves to the loopback
// interface. An empty string means "all interfaces" and is not safe.
func isLocalhostBind(bind string) bool {
	switch bind {
	case "127.0.0.1", "::1", "localhost":
		return true
	}
	return false
}

// resolveServerID loads or creates the per-machine server id. When
// cacheDir is empty the id lives alongside other gortex cache files
// (~/.cache/gortex/server.id); otherwise cacheDir/server.id.
func resolveServerID(cacheDir string) (string, error) {
	path := filepath.Join(cacheDir, "server.id")
	if cacheDir == "" {
		def, err := server.DefaultServerIDPath()
		if err != nil {
			return "", err
		}
		path = def
	}
	return server.LoadOrCreateServerID(path)
}
