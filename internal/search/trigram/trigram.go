// Package trigram is a Zoekt-style trigram index over file content —
// an alternative grep backbone. Each document's content contributes
// its set of 3-byte substrings (trigrams) to a posting map; a literal
// query intersects the posting lists of its own trigrams down to a
// small candidate set, which a literal scan then verifies. It turns a
// repo-wide substring search from an O(total bytes) walk into an
// O(candidate bytes) one.
package trigram

import (
	"slices"
	"sync"
)

// trigram is three consecutive content bytes packed into a uint32.
type trigram uint32

// Index maps each trigram to the document IDs whose content contains
// it. It is the candidate-filter half of the search; verification of
// an actual match is the caller's job. Safe for concurrent use.
type Index struct {
	mu     sync.RWMutex
	post   map[trigram][]uint32 // trigram -> docIDs containing it
	perDoc map[uint32][]trigram // docID -> its trigrams, for O(doc) removal
	docs   map[uint32]struct{}  // known docIDs
}

// New returns an empty index.
func New() *Index {
	return &Index{
		post:   make(map[trigram][]uint32),
		perDoc: make(map[uint32][]trigram),
		docs:   make(map[uint32]struct{}),
	}
}

// trigrams returns the distinct trigrams of content. Content shorter
// than three bytes has none.
func trigrams(content []byte) []trigram {
	if len(content) < 3 {
		return nil
	}
	seen := make(map[trigram]struct{})
	for i := 0; i+2 < len(content); i++ {
		tg := trigram(content[i])<<16 | trigram(content[i+1])<<8 | trigram(content[i+2])
		seen[tg] = struct{}{}
	}
	out := make([]trigram, 0, len(seen))
	for tg := range seen {
		out = append(out, tg)
	}
	return out
}

// Add records that document docID's content contains its trigrams.
// Re-adding an existing docID first drops its previous postings, so
// Add doubles as the re-index path for a changed file.
func (ix *Index) Add(docID uint32, content []byte) {
	tgs := trigrams(content)
	ix.mu.Lock()
	defer ix.mu.Unlock()
	ix.removeLocked(docID)
	ix.docs[docID] = struct{}{}
	for _, tg := range tgs {
		ix.post[tg] = append(ix.post[tg], docID)
	}
	ix.perDoc[docID] = tgs
}

// Remove drops docID from every posting list it appears in.
func (ix *Index) Remove(docID uint32) {
	ix.mu.Lock()
	defer ix.mu.Unlock()
	ix.removeLocked(docID)
}

func (ix *Index) removeLocked(docID uint32) {
	tgs, ok := ix.perDoc[docID]
	if !ok {
		return
	}
	for _, tg := range tgs {
		list := ix.post[tg]
		for i, d := range list {
			if d == docID {
				ix.post[tg] = append(list[:i], list[i+1:]...)
				break
			}
		}
		if len(ix.post[tg]) == 0 {
			delete(ix.post, tg)
		}
	}
	delete(ix.perDoc, docID)
	delete(ix.docs, docID)
}

// Candidates returns the docIDs that could contain query: every
// document whose trigram set includes all of the query's trigrams.
// A query shorter than three bytes cannot be trigram-filtered, so
// every known document is returned — the caller verifies regardless.
// The result is sorted ascending.
func (ix *Index) Candidates(query string) []uint32 {
	qtgs := trigrams([]byte(query))
	ix.mu.RLock()
	defer ix.mu.RUnlock()

	if len(qtgs) == 0 {
		out := make([]uint32, 0, len(ix.docs))
		for d := range ix.docs {
			out = append(out, d)
		}
		slices.Sort(out)
		return out
	}

	// A document is a candidate iff it appears under every one of the
	// query's distinct trigrams. Counting appearances avoids needing
	// the posting lists pre-sorted for an intersection.
	hits := make(map[uint32]int)
	for _, tg := range qtgs {
		for _, d := range ix.post[tg] {
			hits[d]++
		}
	}
	out := make([]uint32, 0)
	for d, c := range hits {
		if c == len(qtgs) {
			out = append(out, d)
		}
	}
	slices.Sort(out)
	return out
}

// DocCount returns the number of indexed documents.
func (ix *Index) DocCount() int {
	ix.mu.RLock()
	defer ix.mu.RUnlock()
	return len(ix.docs)
}
