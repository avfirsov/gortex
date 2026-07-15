package store_sqlite

import (
	"database/sql"
	"errors"
	"sort"
	"strings"

	"github.com/zzet/gortex/internal/graph"
)

// sqliteMutationReceiptState is guarded exclusively by Store.writeMu. Sharing
// that lock with every graph mutation makes receipt boundaries atomic without a
// second gate or lock-ordering edge.
type sqliteMutationReceiptState struct {
	next   graph.MutationReceiptToken
	active map[graph.MutationReceiptToken]*sqliteMutationReceiptAccumulator
}

type sqliteMutationReceiptAccumulator struct {
	complete           bool
	resolutionRelevant bool
	changedFiles       map[string]struct{}
	definitionFiles    map[string]struct{}
	targetNames        map[string]struct{}
	targetIDs          map[string]struct{}
	importCandidates   map[string]struct{}
}

type sqliteMutationNodeIdentity struct {
	kind       string
	name       string
	qualName   string
	filePath   string
	repoPrefix string
}

func newSQLiteMutationReceiptAccumulator() *sqliteMutationReceiptAccumulator {
	return &sqliteMutationReceiptAccumulator{
		complete:         true,
		changedFiles:     make(map[string]struct{}),
		definitionFiles:  make(map[string]struct{}),
		targetNames:      make(map[string]struct{}),
		targetIDs:        make(map[string]struct{}),
		importCandidates: make(map[string]struct{}),
	}
}

func (a *sqliteMutationReceiptAccumulator) receipt() graph.MutationReceipt {
	return graph.MutationReceipt{
		Complete:           a.complete,
		ResolutionRelevant: a.resolutionRelevant,
		ChangedFiles:       sortedSQLiteReceiptKeys(a.changedFiles),
		DefinitionFiles:    sortedSQLiteReceiptKeys(a.definitionFiles),
		TargetNames:        sortedSQLiteReceiptKeys(a.targetNames),
		TargetIDs:          sortedSQLiteReceiptKeys(a.targetIDs),
		ImportCandidates:   sortedSQLiteReceiptKeys(a.importCandidates),
	}
}

func sortedSQLiteReceiptKeys(values map[string]struct{}) []string {
	if len(values) == 0 {
		return nil
	}
	out := make([]string, 0, len(values))
	for value := range values {
		if value != "" {
			out = append(out, value)
		}
	}
	sort.Strings(out)
	return out
}

// BeginMutationReceipt starts an independent observation window. writeMu is
// the receipt boundary: a concurrent writer is wholly before or wholly inside
// the window, never split across it.
func (s *Store) BeginMutationReceipt() graph.MutationReceiptToken {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	s.mutationReceipts.next++
	if s.mutationReceipts.next == 0 {
		s.mutationReceipts.next++
	}
	if s.mutationReceipts.active == nil {
		s.mutationReceipts.active = make(map[graph.MutationReceiptToken]*sqliteMutationReceiptAccumulator)
	}
	token := s.mutationReceipts.next
	s.mutationReceipts.active[token] = newSQLiteMutationReceiptAccumulator()
	return token
}

// EndMutationReceipt closes one observation window. Unknown or already-ended
// tokens fail closed.
func (s *Store) EndMutationReceipt(token graph.MutationReceiptToken) graph.MutationReceipt {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	acc := s.mutationReceipts.active[token]
	if acc == nil {
		return graph.MutationReceipt{Complete: false}
	}
	delete(s.mutationReceipts.active, token)
	return acc.receipt()
}

func (s *Store) hasActiveMutationReceiptsLocked() bool {
	return len(s.mutationReceipts.active) != 0
}

func (s *Store) markMutationReceiptsIncompleteLocked() {
	for _, acc := range s.mutationReceipts.active {
		acc.complete = false
	}
}

func (s *Store) mergeMutationReceiptLocked(delta *sqliteMutationReceiptAccumulator) {
	if delta == nil {
		return
	}
	for _, acc := range s.mutationReceipts.active {
		if !delta.complete {
			acc.complete = false
		}
		acc.resolutionRelevant = acc.resolutionRelevant || delta.resolutionRelevant
		mergeSQLiteReceiptSet(acc.changedFiles, delta.changedFiles)
		mergeSQLiteReceiptSet(acc.definitionFiles, delta.definitionFiles)
		mergeSQLiteReceiptSet(acc.targetNames, delta.targetNames)
		mergeSQLiteReceiptSet(acc.targetIDs, delta.targetIDs)
		mergeSQLiteReceiptSet(acc.importCandidates, delta.importCandidates)
	}
}

func mergeSQLiteReceiptSet(dst, src map[string]struct{}) {
	for value := range src {
		if value != "" {
			dst[value] = struct{}{}
		}
	}
}

func recordSQLiteAddedNode(acc *sqliteMutationReceiptAccumulator, n *graph.Node) {
	if acc == nil || n == nil {
		return
	}
	if n.ID != "" {
		acc.targetIDs[n.ID] = struct{}{}
	}
	if n.Name != "" {
		acc.targetNames[n.Name] = struct{}{}
	}
	if n.QualName != "" {
		acc.targetNames[n.QualName] = struct{}{}
	}
	if n.FilePath != "" {
		acc.changedFiles[n.FilePath] = struct{}{}
	}
	if !graph.IsReferenceableSymbol(n.Kind) {
		return
	}
	acc.resolutionRelevant = true
	if n.FilePath != "" {
		acc.definitionFiles[n.FilePath] = struct{}{}
	} else {
		acc.complete = false
	}
}

func recordSQLiteAddedEdge(acc *sqliteMutationReceiptAccumulator, e *graph.Edge, exactFile string) {
	if acc == nil || e == nil {
		return
	}
	if e.To != "" {
		acc.targetIDs[e.To] = struct{}{}
	}
	if name := graph.UnresolvedName(e.To); name != "" {
		acc.targetNames[name] = struct{}{}
	}
	if e.Kind == graph.EdgeImports {
		if name := graph.UnresolvedName(e.To); name != "" {
			acc.importCandidates[name] = struct{}{}
		} else if e.To != "" {
			acc.importCandidates[e.To] = struct{}{}
		}
		if e.Alias != "" {
			acc.importCandidates[e.Alias] = struct{}{}
		}
	}
	if exactFile != "" {
		acc.changedFiles[exactFile] = struct{}{}
	}
	if !graph.IsUnresolvedTarget(e.To) {
		return
	}
	acc.resolutionRelevant = true
	if exactFile == "" {
		acc.complete = false
	}
}

func sqliteIdentityForNode(n *graph.Node) sqliteMutationNodeIdentity {
	return sqliteMutationNodeIdentity{
		kind:       string(n.Kind),
		name:       n.Name,
		qualName:   n.QualName,
		filePath:   n.FilePath,
		repoPrefix: n.RepoPrefix,
	}
}

func (i sqliteMutationNodeIdentity) equalsNode(n *graph.Node) bool {
	return i.kind == string(n.Kind) && i.name == n.Name && i.qualName == n.QualName &&
		i.filePath == n.FilePath && i.repoPrefix == n.RepoPrefix
}

func (s *Store) publishSQLiteNodeWriteLocked(n *graph.Node, old sqliteMutationNodeIdentity, found, exact, changed bool) {
	if !changed || !s.hasActiveMutationReceiptsLocked() {
		return
	}
	if !exact {
		s.markMutationReceiptsIncompleteLocked()
		return
	}
	if found {
		if !old.equalsNode(n) {
			s.markMutationReceiptsIncompleteLocked()
		}
		return
	}
	delta := newSQLiteMutationReceiptAccumulator()
	recordSQLiteAddedNode(delta, n)
	s.mergeMutationReceiptLocked(delta)
}

func (s *Store) publishSQLiteEdgeInsertLocked(e *graph.Edge, inserted bool) {
	if !inserted || !s.hasActiveMutationReceiptsLocked() {
		return
	}
	file := e.FilePath
	delta := newSQLiteMutationReceiptAccumulator()
	if file == "" {
		source, found, err := s.mutationNodeIdentityLocked(e.From)
		if err != nil {
			delta.complete = false
		} else if found {
			file = source.filePath
		}
	}
	recordSQLiteAddedEdge(delta, e, file)
	s.mergeMutationReceiptLocked(delta)
}

func (s *Store) mutationNodeIdentityLocked(id string) (sqliteMutationNodeIdentity, bool, error) {
	var identity sqliteMutationNodeIdentity
	err := s.db.QueryRow(
		`SELECT kind, name, qual_name, file_path, repo_prefix FROM nodes WHERE id = ?`, id,
	).Scan(&identity.kind, &identity.name, &identity.qualName, &identity.filePath, &identity.repoPrefix)
	if errors.Is(err, sql.ErrNoRows) {
		return sqliteMutationNodeIdentity{}, false, nil
	}
	return identity, err == nil, err
}

// mutationNodeIdentitiesTx preloads node identities in bounded batches. It is
// called only while receipts are active, so the steady indexing path pays no
// extra reads.
func mutationNodeIdentitiesTx(tx *sql.Tx, ids []string) (map[string]sqliteMutationNodeIdentity, error) {
	const chunkSize = 900
	unique := make(map[string]struct{}, len(ids))
	ordered := make([]string, 0, len(ids))
	for _, id := range ids {
		if id == "" {
			continue
		}
		if _, exists := unique[id]; exists {
			continue
		}
		unique[id] = struct{}{}
		ordered = append(ordered, id)
	}
	identities := make(map[string]sqliteMutationNodeIdentity, len(ordered))
	for start := 0; start < len(ordered); start += chunkSize {
		end := start + chunkSize
		if end > len(ordered) {
			end = len(ordered)
		}
		chunk := ordered[start:end]
		placeholders := strings.TrimSuffix(strings.Repeat("?,", len(chunk)), ",")
		args := make([]any, len(chunk))
		for i, id := range chunk {
			args[i] = id
		}
		rows, err := tx.Query(
			`SELECT id, kind, name, qual_name, file_path, repo_prefix FROM nodes WHERE id IN (`+placeholders+`)`,
			args...,
		)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var id string
			var identity sqliteMutationNodeIdentity
			if err := rows.Scan(&id, &identity.kind, &identity.name, &identity.qualName, &identity.filePath, &identity.repoPrefix); err != nil {
				_ = rows.Close()
				return nil, err
			}
			identities[id] = identity
		}
		if err := rows.Err(); err != nil {
			_ = rows.Close()
			return nil, err
		}
		_ = rows.Close()
	}
	return identities, nil
}

var _ graph.MutationReceiptStore = (*Store)(nil)
