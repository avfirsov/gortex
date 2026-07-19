package indexer

import (
	"sort"
	"strings"

	"github.com/zzet/gortex/internal/contracts"
	"github.com/zzet/gortex/internal/graph"
)

// ContractBridgeFilePath is the synthetic FilePath every
// KindContractBridge node (and its EdgeBridges edges) carries. Bridge
// nodes are derived state — re-computed from the matcher result on
// every contract reconcile — so they share one virtual "file" and the
// materialisation pass evicts the previous generation with a single
// EvictFile call before re-minting. That makes the pass idempotent
// and self-cleaning: a contract group that disappears (file deleted,
// repo untracked, route renamed) takes its bridge with it on the next
// reconcile.
const ContractBridgeFilePath = "contracts://bridges"

// bridgeGroupKey is the identity a matched contract group materialises
// under. It mirrors the matcher's pairing boundary — Match buckets
// provider/consumer pairs by (EffectiveWorkspace, EffectiveProject,
// ContractID) and never pairs across that boundary, so two unrelated
// workspaces that each serve the same route (`GET /api/users`) produce
// two distinct groups, not one merged bridge. Keying the bridge on the
// bare ContractID alone collapsed them, summing counts and asserting a
// cross-repo blast radius the matcher never produced.
type bridgeGroupKey struct {
	workspace  string
	project    string
	contractID string
}

// bridgeGroup accumulates one matched provider↔consumer contract
// group while the materialisation pass walks the CrossLink list.
type bridgeGroup struct {
	contractType contracts.ContractType
	workspaceID  string
	projectID    string
	providerRepo string
	repos        map[string]struct{}
	// side membership per participating contract node ID.
	providerIDs map[string]struct{}
	consumerIDs map[string]struct{}
	// distinct provider/consumer records (a contract node collapses
	// same-ID records, so counts come from registry identities).
	providerKeys map[string]struct{}
	consumerKeys map[string]struct{}
	crossRepo    bool
	minLine      int
}

// MaterializeContractBridges persists the matcher's view of the
// contract surface as a queryable subgraph: one KindContractBridge
// node per matched provider↔consumer contract group (an HTTP route,
// a gRPC/Thrift method, a pub/sub topic), linked to every
// participating KindContract node via EdgeBridges (Meta["side"] =
// provider | consumer | both).
//
// Identity: the bridge node ID is
// `bridge::<workspace>::<project>::<contract-id>`, where contract-id is
// the canonical contract key (`http::GET::/v1/users`,
// `grpc::Users::GetUser`, `topic::kafka::orders`) and workspace/project
// are the matched group's effective slugs. Pinning the bridge to the
// match boundary keeps two unrelated workspaces that each serve the
// same route from collapsing into one bridge — the matcher already
// pairs only inside one (workspace, project), and the bridge identity
// must respect the same boundary. The key is repo-free within a
// boundary, so one bridge spans every repo of that workspace's group;
// the bridge node's RepoPrefix is the lexicographically-smallest
// provider repo (a deterministic owner for per-repo rollups) and
// Meta["repos"] carries the full sorted spread.
//
// The previous bridge generation is always evicted first (see
// ContractBridgeFilePath), even when matched is empty — that is what
// makes re-runs idempotent and removes bridges whose contracts
// disappeared. Returns the number of bridge nodes minted.
func MaterializeContractBridges(g graph.Store, matched []contracts.CrossLink) int {
	if g == nil {
		return 0
	}
	g.EvictFile(ContractBridgeFilePath)
	if len(matched) == 0 {
		return 0
	}

	groups := make(map[bridgeGroupKey]*bridgeGroup)
	for _, m := range matched {
		if m.ContractID == "" {
			continue
		}
		key := bridgeGroupKey{
			workspace:  m.Provider.EffectiveWorkspace(),
			project:    m.Provider.EffectiveProject(),
			contractID: m.ContractID,
		}
		grp, ok := groups[key]
		if !ok {
			grp = &bridgeGroup{
				contractType: m.Provider.Type,
				workspaceID:  key.workspace,
				projectID:    key.project,
				repos:        make(map[string]struct{}),
				providerIDs:  make(map[string]struct{}),
				consumerIDs:  make(map[string]struct{}),
				providerKeys: make(map[string]struct{}),
				consumerKeys: make(map[string]struct{}),
				// minLine starts unset (0) and folds in a true min over
				// every provider line below, so the persisted StartLine is
				// independent of the (map-ordered) match iteration order.
				minLine: 0,
			}
			groups[key] = grp
		}
		if m.Provider.RepoPrefix != "" {
			grp.repos[m.Provider.RepoPrefix] = struct{}{}
			if grp.providerRepo == "" || m.Provider.RepoPrefix < grp.providerRepo {
				grp.providerRepo = m.Provider.RepoPrefix
			}
		}
		if m.Consumer.RepoPrefix != "" {
			grp.repos[m.Consumer.RepoPrefix] = struct{}{}
		}
		// True min over all provider lines so StartLine doesn't flap with
		// the match-iteration order. A zero/negative line (spec-only
		// provider with no resolved line) never lowers a real minimum.
		if m.Provider.Line > 0 && (grp.minLine == 0 || m.Provider.Line < grp.minLine) {
			grp.minLine = m.Provider.Line
		}
		grp.providerIDs[m.Provider.ID] = struct{}{}
		grp.consumerIDs[m.Consumer.ID] = struct{}{}
		grp.providerKeys[contractRecordKey(m.Provider)] = struct{}{}
		grp.consumerKeys[contractRecordKey(m.Consumer)] = struct{}{}
		if m.CrossRepo {
			grp.crossRepo = true
		}
	}

	// Deterministic emit order keeps re-runs byte-stable on ordered
	// backends and makes test assertions reproducible.
	groupKeys := make([]bridgeGroupKey, 0, len(groups))
	for k := range groups {
		groupKeys = append(groupKeys, k)
	}
	sort.Slice(groupKeys, func(i, j int) bool {
		if groupKeys[i].workspace != groupKeys[j].workspace {
			return groupKeys[i].workspace < groupKeys[j].workspace
		}
		if groupKeys[i].project != groupKeys[j].project {
			return groupKeys[i].project < groupKeys[j].project
		}
		return groupKeys[i].contractID < groupKeys[j].contractID
	})

	minted := 0
	bridgeNodes := make([]*graph.Node, 0, len(groupKeys))
	var bridgeEdges []*graph.Edge
	for _, key := range groupKeys {
		grp := groups[key]
		groupID := key.contractID
		bridgeID := bridgeNodeID(key)

		repos := make([]string, 0, len(grp.repos))
		for r := range grp.repos {
			repos = append(repos, r)
		}
		sort.Strings(repos)

		bridgeNodes = append(bridgeNodes, &graph.Node{
			ID:          bridgeID,
			Kind:        graph.KindContractBridge,
			Name:        bridgeCanonicalKey(groupID, grp.contractType),
			FilePath:    ContractBridgeFilePath,
			StartLine:   grp.minLine,
			Language:    "contract",
			RepoPrefix:  grp.providerRepo,
			WorkspaceID: grp.workspaceID,
			ProjectID:   grp.projectID,
			Meta: map[string]any{
				"contract_type":  string(grp.contractType),
				"canonical_key":  bridgeCanonicalKey(groupID, grp.contractType),
				"contract_id":    groupID,
				"workspace":      grp.workspaceID,
				"project":        grp.projectID,
				"repos":          repos,
				"provider_count": len(grp.providerKeys),
				"consumer_count": len(grp.consumerKeys),
				"cross_repo":     grp.crossRepo,
			},
		})
		minted++

		// One EdgeBridges per participating contract node. A contract
		// node that carries records on BOTH sides (exact-ID matches
		// collapse provider and consumer into one node) gets a single
		// edge with side="both" — two same-(from,to,kind) edges would
		// collide in the adjacency dedup anyway.
		contractIDs := make(map[string]struct{}, len(grp.providerIDs)+len(grp.consumerIDs))
		for id := range grp.providerIDs {
			contractIDs[id] = struct{}{}
		}
		for id := range grp.consumerIDs {
			contractIDs[id] = struct{}{}
		}
		ordered := make([]string, 0, len(contractIDs))
		for id := range contractIDs {
			ordered = append(ordered, id)
		}
		sort.Strings(ordered)
		for _, contractID := range ordered {
			_, isProv := grp.providerIDs[contractID]
			_, isCons := grp.consumerIDs[contractID]
			side := "provider"
			switch {
			case isProv && isCons:
				side = "both"
			case isCons:
				side = "consumer"
			}
			bridgeEdges = append(bridgeEdges, &graph.Edge{
				From:            bridgeID,
				To:              contractID,
				Kind:            graph.EdgeBridges,
				FilePath:        ContractBridgeFilePath,
				Confidence:      1.0,
				ConfidenceLabel: "EXTRACTED",
				Origin:          graph.OriginASTResolved,
				CrossRepo:       grp.crossRepo,
				Meta:            map[string]any{"side": side},
			})
		}
	}
	if len(bridgeNodes) > 0 || len(bridgeEdges) > 0 {
		g.AddBatch(bridgeNodes, bridgeEdges)
	}

	return minted
}

// buildContractBridgeBatch constructs the deterministic bridge generation
// without evicting or writing graph state. The incremental reconciler combines
// this batch with exact match/topic replacement in one backend transaction.
func buildContractBridgeBatch(matched []contracts.CrossLink) ([]*graph.Node, []*graph.Edge) {
	groups := make(map[bridgeGroupKey]*bridgeGroup)
	for _, match := range matched {
		if match.ContractID == "" {
			continue
		}
		key := bridgeGroupKey{
			workspace:  match.Provider.EffectiveWorkspace(),
			project:    match.Provider.EffectiveProject(),
			contractID: match.ContractID,
		}
		group := groups[key]
		if group == nil {
			group = &bridgeGroup{
				contractType: match.Provider.Type,
				workspaceID:  key.workspace,
				projectID:    key.project,
				repos:        make(map[string]struct{}),
				providerIDs:  make(map[string]struct{}),
				consumerIDs:  make(map[string]struct{}),
				providerKeys: make(map[string]struct{}),
				consumerKeys: make(map[string]struct{}),
			}
			groups[key] = group
		}
		if match.Provider.RepoPrefix != "" {
			group.repos[match.Provider.RepoPrefix] = struct{}{}
			if group.providerRepo == "" || match.Provider.RepoPrefix < group.providerRepo {
				group.providerRepo = match.Provider.RepoPrefix
			}
		}
		if match.Consumer.RepoPrefix != "" {
			group.repos[match.Consumer.RepoPrefix] = struct{}{}
		}
		if match.Provider.Line > 0 && (group.minLine == 0 || match.Provider.Line < group.minLine) {
			group.minLine = match.Provider.Line
		}
		group.providerIDs[match.Provider.ID] = struct{}{}
		group.consumerIDs[match.Consumer.ID] = struct{}{}
		group.providerKeys[contractRecordKey(match.Provider)] = struct{}{}
		group.consumerKeys[contractRecordKey(match.Consumer)] = struct{}{}
		group.crossRepo = group.crossRepo || match.CrossRepo
	}

	keys := make([]bridgeGroupKey, 0, len(groups))
	for key := range groups {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].workspace != keys[j].workspace {
			return keys[i].workspace < keys[j].workspace
		}
		if keys[i].project != keys[j].project {
			return keys[i].project < keys[j].project
		}
		return keys[i].contractID < keys[j].contractID
	})

	nodes := make([]*graph.Node, 0, len(keys))
	var edges []*graph.Edge
	for _, key := range keys {
		group := groups[key]
		bridgeID := bridgeNodeID(key)
		repos := make([]string, 0, len(group.repos))
		for repo := range group.repos {
			repos = append(repos, repo)
		}
		sort.Strings(repos)
		nodes = append(nodes, &graph.Node{
			ID:          bridgeID,
			Kind:        graph.KindContractBridge,
			Name:        bridgeCanonicalKey(key.contractID, group.contractType),
			FilePath:    ContractBridgeFilePath,
			StartLine:   group.minLine,
			Language:    "contract",
			RepoPrefix:  group.providerRepo,
			WorkspaceID: group.workspaceID,
			ProjectID:   group.projectID,
			Meta: map[string]any{
				"contract_type":  string(group.contractType),
				"canonical_key":  bridgeCanonicalKey(key.contractID, group.contractType),
				"contract_id":    key.contractID,
				"workspace":      group.workspaceID,
				"project":        group.projectID,
				"repos":          repos,
				"provider_count": len(group.providerKeys),
				"consumer_count": len(group.consumerKeys),
				"cross_repo":     group.crossRepo,
			},
		})

		contractIDs := make(map[string]struct{}, len(group.providerIDs)+len(group.consumerIDs))
		for id := range group.providerIDs {
			contractIDs[id] = struct{}{}
		}
		for id := range group.consumerIDs {
			contractIDs[id] = struct{}{}
		}
		ordered := make([]string, 0, len(contractIDs))
		for id := range contractIDs {
			ordered = append(ordered, id)
		}
		sort.Strings(ordered)
		for _, contractID := range ordered {
			_, provider := group.providerIDs[contractID]
			_, consumer := group.consumerIDs[contractID]
			side := "provider"
			switch {
			case provider && consumer:
				side = "both"
			case consumer:
				side = "consumer"
			}
			edges = append(edges, &graph.Edge{
				From:            bridgeID,
				To:              contractID,
				Kind:            graph.EdgeBridges,
				FilePath:        ContractBridgeFilePath,
				Confidence:      1.0,
				ConfidenceLabel: "EXTRACTED",
				Origin:          graph.OriginASTResolved,
				CrossRepo:       group.crossRepo,
				Meta:            map[string]any{"side": side},
			})
		}
	}
	return nodes, edges
}

// bridgeNodeID renders the persisted node ID for a contract-bridge
// group: `bridge::<workspace>::<project>::<contract-id>`. The boundary
// slugs are part of the identity so two unrelated workspaces serving
// the same contract never share a bridge node (see bridgeGroupKey).
func bridgeNodeID(key bridgeGroupKey) string {
	return "bridge::" + key.workspace + "::" + key.project + "::" + key.contractID
}

// contractRecordKey identifies one registry record (the same dedupe
// fields Registry.All uses) so provider/consumer counts reflect
// distinct call sites rather than distinct contract node IDs.
func contractRecordKey(c contracts.Contract) string {
	return c.ID + "|" + c.FilePath + "|" + c.SymbolID + "|" + c.RepoPrefix
}

// bridgeCanonicalKey renders the human-facing canonical key for a
// contract group ID: the `<type>::` prefix is dropped and the
// remaining segments joined per protocol convention — "GET /v1/users"
// for HTTP, "Users.GetUser" for RPC, "kafka::orders" for topics.
func bridgeCanonicalKey(groupID string, t contracts.ContractType) string {
	rest := groupID
	if i := strings.Index(rest, "::"); i >= 0 {
		rest = rest[i+2:]
	}
	switch t {
	case contracts.ContractHTTP, contracts.ContractOpenAPI:
		return strings.Replace(rest, "::", " ", 1)
	case contracts.ContractGRPC, contracts.ContractThrift, contracts.ContractGraphQL:
		return strings.Replace(rest, "::", ".", 1)
	}
	return rest
}
