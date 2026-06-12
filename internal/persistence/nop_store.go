package persistence

// NopStore is a no-op persistence backend used when caching is disabled.
type NopStore struct{}

func (NopStore) Check(_, _, _ string) bool              { return false }
func (NopStore) Load(_, _, _ string) (*Snapshot, error) { return nil, ErrNotFound }
func (NopStore) Save(_ *Snapshot) error                 { return nil }
func (NopStore) Validate(_, _, _ string) bool           { return false }
func (NopStore) Evict(_, _, _ string) error             { return nil }
func (NopStore) Close() error                           { return nil }
