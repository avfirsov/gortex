package search

import "sort"

// AutoConceptsPersistenceSnapshot is the serializable form of the mined
// vocabulary. The maps returned by PersistenceSnapshot alias the immutable
// live structure and must be treated as read-only.
type AutoConceptsPersistenceSnapshot struct {
	Related map[string][]string
	Vocab   []string
}

func (a *AutoConcepts) PersistenceSnapshot() AutoConceptsPersistenceSnapshot {
	if a == nil {
		return AutoConceptsPersistenceSnapshot{}
	}
	vocab := make([]string, 0, len(a.vocab))
	for token := range a.vocab {
		vocab = append(vocab, token)
	}
	sort.Strings(vocab)
	return AutoConceptsPersistenceSnapshot{Related: a.related, Vocab: vocab}
}

// RestoreAutoConcepts takes ownership of snapshot.Related and rebuilds the
// set-shaped vocabulary without touching the graph.
func RestoreAutoConcepts(snapshot AutoConceptsPersistenceSnapshot) *AutoConcepts {
	if snapshot.Related == nil {
		snapshot.Related = map[string][]string{}
	}
	vocab := make(map[string]struct{}, len(snapshot.Vocab))
	for _, token := range snapshot.Vocab {
		if token != "" {
			vocab[token] = struct{}{}
		}
	}
	return &AutoConcepts{related: snapshot.Related, vocab: vocab}
}
