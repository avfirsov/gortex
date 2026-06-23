package graph

import "reflect"

// metaDelta returns the subset of kv whose value differs from (or is
// absent in) existing — i.e. the keys a merge would actually change.
//
// It is a pure calculation: no locks, no I/O, no mutation of either
// argument. Equality is reflect.DeepEqual so the comparison is correct
// for the value shapes that ride on Node.Meta — scalars (string, the
// JSON-decoded float64 for numbers), []string / []any tag slices, and
// nested map[string]any blobs — where a == comparison would either
// panic (slices/maps are not comparable) or compare identity rather
// than contents.
//
// A nil existing is treated as "no keys present", so every entry in kv
// is returned. An empty/nil kv returns nil (nothing to merge). The
// returned map is freshly allocated and shares no structure with kv's
// values beyond the value pointers themselves (callers store them as-is
// — these are caller-owned, JSON-decoded values).
//
// Idempotency falls straight out of this: once a key has been written,
// a second metaDelta over the now-updated map omits it, so the second
// MergeNodeMeta reports changed=false.
func metaDelta(existing, kv map[string]any) map[string]any {
	if len(kv) == 0 {
		return nil
	}
	var delta map[string]any
	for k, v := range kv {
		if existing != nil {
			if cur, ok := existing[k]; ok && reflect.DeepEqual(cur, v) {
				// Key already present with an equal value — nothing to do.
				continue
			}
		}
		if delta == nil {
			delta = make(map[string]any, len(kv))
		}
		delta[k] = v
	}
	return delta
}

// MergeNodeMeta is the in-memory *Graph implementation of the
// Store.MergeNodeMeta contract: additive, idempotent, shard-locked
// merge of kv into the target node's Meta. See the Store interface doc
// for the full (changed, found) semantics.
//
// Locking: the node lives in exactly one shard (shardFor(id)), so a
// single shard write lock is sufficient and necessary — necessary
// because GetNode reads, and a concurrent AddNode/AddEdge writes, the
// same sharded maps; sufficient because the merge touches only this one
// node's Meta map and no cross-shard index. We reuse lockTwoWrite(id,
// id), which collapses to a single Lock on that shard (its a==b branch),
// rather than hand-rolling shardFor(id).mu.Lock() so this method stays
// in lock-step with the one sanctioned shard-locking helper the rest of
// the mutators use.
//
// The compare-before-write (metaDelta) runs inside the lock: it must
// observe the same Meta it is about to mutate, and computing it outside
// the lock would race a concurrent merge to the same node.
func (g *Graph) MergeNodeMeta(id string, kv map[string]any) (changed bool, found bool) {
	unlock := g.lockTwoWrite(id, id)
	defer unlock()

	s := g.shardFor(id)
	n := s.nodes[id]
	if n == nil {
		// Unknown id — recorded as a not-found skip by the caller; the
		// batch continues. No structural state was touched.
		return false, false
	}

	delta := metaDelta(n.Meta, kv)
	if len(delta) == 0 {
		// Found, but every provided key already equals the stored value:
		// the merge is a no-op, so the call is idempotent.
		return false, true
	}

	if n.Meta == nil {
		n.Meta = make(map[string]any, len(delta))
	}
	for k, v := range delta {
		n.Meta[k] = v
	}
	return true, true
}
