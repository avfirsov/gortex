package mcp

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/zzet/gortex/internal/analysis"
	"github.com/zzet/gortex/internal/graph"
	"github.com/zzet/gortex/internal/runtimeactivity"
	"github.com/zzet/gortex/internal/search"
	"go.uber.org/zap"
)

const (
	analysisGenerationFormatVersion uint32 = 1
	analysisGenerationWriteChunk           = 1000
	analysisGenerationPruneBatch           = 1000
	analysisGenerationPruneKeep            = 2
)

func (s *Server) analysisGenerationBackends() (graph.AnalysisGenerationStore, graph.AnalysisQueryStore) {
	backend := s.backendStore()
	writer, _ := backend.(graph.AnalysisGenerationStore)
	query, _ := backend.(graph.AnalysisQueryStore)
	return writer, query
}

type analysisGenerationInput struct {
	header         graph.AnalysisGenerationHeader
	adjacency      analysis.AdjacencyPersistenceSnapshot
	communities    []graph.AnalysisCommunitySummary
	processes      []graph.AnalysisProcessSummary
	concepts       []graph.AnalysisConcept
	conceptRelated map[string][]string
}

// normalizePersistedAnalysis gives cold and lazy materializations the same
// deterministic collection order. Community membership has no semantic order,
// while process Steps deliberately retain their call-tree preorder.
func normalizePersistedAnalysis(candidate *persistedAnalysis) {
	if candidate == nil {
		return
	}
	if candidate.communities != nil {
		for i := range candidate.communities.Communities {
			sort.Strings(candidate.communities.Communities[i].Members)
			sort.Strings(candidate.communities.Communities[i].Files)
		}
		sort.Slice(candidate.communities.Communities, func(i, j int) bool {
			return candidate.communities.Communities[i].ID < candidate.communities.Communities[j].ID
		})
	}
	if candidate.processes != nil {
		for i := range candidate.processes.Processes {
			sort.Strings(candidate.processes.Processes[i].Files)
		}
		sort.Slice(candidate.processes.Processes, func(i, j int) bool {
			return candidate.processes.Processes[i].ID < candidate.processes.Processes[j].ID
		})
		for nodeID := range candidate.processes.NodeToProcs {
			sort.Strings(candidate.processes.NodeToProcs[nodeID])
		}
	}
}

func prepareAnalysisGeneration(candidate persistedAnalysis, revision uint64) (analysisGenerationInput, error) {
	normalizePersistedAnalysis(&candidate)
	if candidate.communities == nil || candidate.leiden == nil || candidate.processes == nil ||
		candidate.pageRank == nil || candidate.adjacency == nil || candidate.autoConcepts == nil || candidate.hits == nil {
		return analysisGenerationInput{}, fmt.Errorf("analysis generation: incomplete candidate")
	}

	adjacency := candidate.adjacency.PersistenceSnapshot()
	communities := make([]graph.AnalysisCommunitySummary, 0, len(candidate.communities.Communities))
	for _, community := range candidate.communities.Communities {
		files := append([]string(nil), community.Files...)
		sort.Strings(files)
		communities = append(communities, graph.AnalysisCommunitySummary{
			ID: community.ID, Label: community.Label, Hub: community.Hub, ParentID: community.ParentID,
			Size: community.Size, Cohesion: community.Cohesion, Files: files,
		})
	}
	sort.Slice(communities, func(i, j int) bool { return communities[i].ID < communities[j].ID })

	processes := make([]graph.AnalysisProcessSummary, 0, len(candidate.processes.Processes))
	for _, process := range candidate.processes.Processes {
		files := append([]string(nil), process.Files...)
		sort.Strings(files)
		processes = append(processes, graph.AnalysisProcessSummary{
			ID: process.ID, Name: process.Name, EntryPoint: process.EntryPoint,
			StepCount: process.StepCount, Score: process.Score, Truncated: process.Truncated, Files: files,
		})
	}
	sort.Slice(processes, func(i, j int) bool { return processes[i].ID < processes[j].ID })

	conceptSnapshot := candidate.autoConcepts.PersistenceSnapshot()
	conceptSet := make(map[string]bool, len(conceptSnapshot.Vocab)+len(conceptSnapshot.Related))
	for _, token := range conceptSnapshot.Vocab {
		conceptSet[token] = true
	}
	for token, related := range conceptSnapshot.Related {
		if _, ok := conceptSet[token]; !ok {
			conceptSet[token] = false
		}
		for _, sibling := range related {
			if _, ok := conceptSet[sibling]; !ok {
				conceptSet[sibling] = false
			}
		}
	}
	conceptTokens := make([]string, 0, len(conceptSet))
	for token := range conceptSet {
		conceptTokens = append(conceptTokens, token)
	}
	sort.Strings(conceptTokens)
	concepts := make([]graph.AnalysisConcept, 0, len(conceptTokens))
	for _, token := range conceptTokens {
		concepts = append(concepts, graph.AnalysisConcept{Token: token, InVocabulary: conceptSet[token]})
	}

	return analysisGenerationInput{
		header: graph.AnalysisGenerationHeader{
			FormatVersion: analysisGenerationFormatVersion,
			GraphRevision: revision,
			CreatedAtUnix: time.Now().Unix(),
			NodeCount:     len(adjacency.IDs), CommunityCount: len(communities),
			ProcessCount: len(processes), ConceptCount: len(concepts),
			PageRankMax: candidate.pageRank.Max, AuthorityMax: candidate.hits.MaxAuth,
			HubMax: candidate.hits.MaxHub, Modularity: candidate.communities.Modularity,
			ProcessesTruncated:        candidate.processes.Truncated,
			ProcessesTruncationReason: candidate.processes.TruncationReason,
		},
		adjacency: adjacency, communities: communities, processes: processes,
		concepts: concepts, conceptRelated: conceptSnapshot.Related,
	}, nil
}

func persistAnalysisGeneration(
	writer graph.AnalysisGenerationStore,
	expectedRevision uint64,
	candidate persistedAnalysis,
) (graph.AnalysisGenerationHeader, bool, error) {
	input, err := prepareAnalysisGeneration(candidate, expectedRevision)
	if err != nil {
		return graph.AnalysisGenerationHeader{}, false, err
	}
	generationID, accepted, err := writer.BeginAnalysisGeneration(expectedRevision, input.header)
	if err != nil || !accepted {
		return graph.AnalysisGenerationHeader{}, accepted, err
	}
	activated := false
	defer func() {
		if !activated {
			_ = writer.AbortAnalysisGeneration(generationID)
		}
	}()

	// Communities precede nodes because a non-empty node community_id carries
	// an immediate foreign key to the immutable generation's community row.
	for start := 0; start < len(input.communities); start += analysisGenerationWriteChunk {
		end := min(start+analysisGenerationWriteChunk, len(input.communities))
		accepted, err = writer.AppendAnalysisCommunities(expectedRevision, generationID, input.communities[start:end])
		if err != nil || !accepted {
			return graph.AnalysisGenerationHeader{}, accepted, err
		}
	}
	if accepted, err = writer.SealAnalysisComponent(expectedRevision, generationID, graph.AnalysisComponentCommunities, input.header.CommunityCount); err != nil || !accepted {
		return graph.AnalysisGenerationHeader{}, accepted, err
	}

	for start := 0; start < len(input.adjacency.IDs); start += analysisGenerationWriteChunk {
		end := min(start+analysisGenerationWriteChunk, len(input.adjacency.IDs))
		rows := make([]graph.AnalysisNodeMetric, 0, end-start)
		for _, nodeID := range input.adjacency.IDs[start:end] {
			rows = append(rows, graph.AnalysisNodeMetric{
				NodeID: nodeID, CommunityID: candidate.communities.NodeToComm[nodeID],
				PageRank: candidate.pageRank.Scores[nodeID], Authority: candidate.hits.Authorities[nodeID],
				Hub: candidate.hits.Hubs[nodeID],
			})
		}
		accepted, err = writer.AppendAnalysisNodes(expectedRevision, generationID, rows)
		if err != nil || !accepted {
			return graph.AnalysisGenerationHeader{}, accepted, err
		}
	}
	if accepted, err = writer.SealAnalysisComponent(expectedRevision, generationID, graph.AnalysisComponentNodes, input.header.NodeCount); err != nil || !accepted {
		return graph.AnalysisGenerationHeader{}, accepted, err
	}

	for start := 0; start < len(input.processes); start += analysisGenerationWriteChunk {
		end := min(start+analysisGenerationWriteChunk, len(input.processes))
		accepted, err = writer.AppendAnalysisProcesses(expectedRevision, generationID, input.processes[start:end], nil)
		if err != nil || !accepted {
			return graph.AnalysisGenerationHeader{}, accepted, err
		}
	}
	for _, process := range candidate.processes.Processes {
		for start := 0; start < len(process.Steps); start += analysisGenerationWriteChunk {
			end := min(start+analysisGenerationWriteChunk, len(process.Steps))
			steps := make([]graph.AnalysisProcessStep, 0, end-start)
			for ordinal, step := range process.Steps[start:end] {
				steps = append(steps, graph.AnalysisProcessStep{
					ProcessID: process.ID, NodeID: step.ID, Ordinal: start + ordinal, Depth: step.Depth,
				})
			}
			accepted, err = writer.AppendAnalysisProcesses(expectedRevision, generationID, nil, steps)
			if err != nil || !accepted {
				return graph.AnalysisGenerationHeader{}, accepted, err
			}
		}
	}
	if accepted, err = writer.SealAnalysisComponent(expectedRevision, generationID, graph.AnalysisComponentProcesses, input.header.ProcessCount); err != nil || !accepted {
		return graph.AnalysisGenerationHeader{}, accepted, err
	}

	for start := 0; start < len(input.concepts); start += analysisGenerationWriteChunk {
		end := min(start+analysisGenerationWriteChunk, len(input.concepts))
		accepted, err = writer.AppendAnalysisConcepts(expectedRevision, generationID, input.concepts[start:end], nil)
		if err != nil || !accepted {
			return graph.AnalysisGenerationHeader{}, accepted, err
		}
	}
	conceptTokens := make([]string, 0, len(input.conceptRelated))
	for token := range input.conceptRelated {
		conceptTokens = append(conceptTokens, token)
	}
	sort.Strings(conceptTokens)
	relations := make([]graph.AnalysisConceptRelation, 0, analysisGenerationWriteChunk)
	flushRelations := func() (bool, error) {
		if len(relations) == 0 {
			return true, nil
		}
		ok, flushErr := writer.AppendAnalysisConcepts(expectedRevision, generationID, nil, relations)
		relations = relations[:0]
		return ok, flushErr
	}
	for _, token := range conceptTokens {
		for rank, sibling := range input.conceptRelated[token] {
			relations = append(relations, graph.AnalysisConceptRelation{Token: token, RelatedToken: sibling, Rank: rank})
			if len(relations) == cap(relations) {
				accepted, err = flushRelations()
				if err != nil || !accepted {
					return graph.AnalysisGenerationHeader{}, accepted, err
				}
			}
		}
	}
	if accepted, err = flushRelations(); err != nil || !accepted {
		return graph.AnalysisGenerationHeader{}, accepted, err
	}
	if accepted, err = writer.SealAnalysisComponent(expectedRevision, generationID, graph.AnalysisComponentConcepts, input.header.ConceptCount); err != nil || !accepted {
		return graph.AnalysisGenerationHeader{}, accepted, err
	}

	adjacencyPayload, err := encodeAnalysisBlobValue(input.adjacency)
	if err != nil {
		return graph.AnalysisGenerationHeader{}, false, fmt.Errorf("encode adjacency: %w", err)
	}
	if accepted, err = writer.PutAnalysisBlob(expectedRevision, generationID, graph.AnalysisBlob{Component: graph.AnalysisBlobAdjacency, Payload: adjacencyPayload}); err != nil || !accepted {
		return graph.AnalysisGenerationHeader{}, accepted, err
	}
	if accepted, err = writer.SealAnalysisComponent(expectedRevision, generationID, graph.AnalysisComponentAdjacency, 1); err != nil || !accepted {
		return graph.AnalysisGenerationHeader{}, accepted, err
	}

	leidenPayload, err := encodeAnalysisBlobValue(candidate.leiden.PersistenceSnapshot())
	if err != nil {
		return graph.AnalysisGenerationHeader{}, false, fmt.Errorf("encode Leiden state: %w", err)
	}
	if accepted, err = writer.PutAnalysisBlob(expectedRevision, generationID, graph.AnalysisBlob{Component: graph.AnalysisBlobLeiden, Payload: leidenPayload}); err != nil || !accepted {
		return graph.AnalysisGenerationHeader{}, accepted, err
	}
	if accepted, err = writer.SealAnalysisComponent(expectedRevision, generationID, graph.AnalysisComponentLeiden, 1); err != nil || !accepted {
		return graph.AnalysisGenerationHeader{}, accepted, err
	}

	if accepted, err = writer.ActivateAnalysisGeneration(expectedRevision, generationID); err != nil || !accepted {
		return graph.AnalysisGenerationHeader{}, accepted, err
	}
	activated = true
	input.header.GenerationID = generationID
	return input.header, true, nil
}

func (s *Server) scheduleAnalysisGenerationPrune(writer graph.AnalysisGenerationStore) {
	if writer == nil || !s.analysisPruneScheduled.CompareAndSwap(false, true) {
		return
	}
	go func() {
		defer s.analysisPruneScheduled.Store(false)
		runtimeactivity.Begin("analysis_generation_gc")
		defer runtimeactivity.End("analysis_generation_gc")
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := writer.PruneAnalysisGenerations(ctx, analysisGenerationPruneKeep, analysisGenerationPruneBatch); err != nil && s.logger != nil {
			s.logger.Warn("mcp: analysis generation prune failed", zap.Error(err))
		}
	}()
}

func restoreAdjacencyBlob(payload []byte) (*analysis.AdjacencySnapshot, error) {
	var snapshot analysis.AdjacencyPersistenceSnapshot
	if err := decodeAnalysisBlobValue(payload, &snapshot); err != nil {
		return nil, err
	}
	return analysis.RestoreAdjacencySnapshot(snapshot)
}

func restoreLeidenBlob(payload []byte, edgeIdentityRevision int) (*analysis.LeidenPartitionCache, error) {
	var snapshot analysis.LeidenPartitionSnapshot
	if err := decodeAnalysisBlobValue(payload, &snapshot); err != nil {
		return nil, err
	}
	return analysis.RestoreLeidenPartitionCache(snapshot, edgeIdentityRevision), nil
}

func conceptsFromQuery(result graph.AnalysisConceptQueryResult) *search.AutoConcepts {
	related := make(map[string][]string, len(result.Concepts))
	vocab := make([]string, 0, len(result.Concepts))
	for _, concept := range result.Concepts {
		if concept.InVocabulary {
			vocab = append(vocab, concept.Token)
		}
	}
	for _, relation := range result.Relations {
		related[relation.Token] = append(related[relation.Token], relation.RelatedToken)
	}
	return search.RestoreAutoConcepts(search.AutoConceptsPersistenceSnapshot{Related: related, Vocab: vocab})
}
