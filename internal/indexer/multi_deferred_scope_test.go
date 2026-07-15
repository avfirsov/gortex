package indexer

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNormalizeDeferredCatchupScope(t *testing.T) {
	t.Run("all repos restores full pass terminal stamping", func(t *testing.T) {
		scope := map[string]struct{}{"alpha": {}, "beta": {}, "gamma": {}}
		assert.Nil(t, normalizeDeferredCatchupScope(scope, true, 3))
	})

	t.Run("strict subset remains scoped", func(t *testing.T) {
		scope := map[string]struct{}{"beta": {}}
		got := normalizeDeferredCatchupScope(scope, true, 3)
		assert.NotNil(t, got)
		assert.Equal(t, scope, got)
	})

	t.Run("unknown prefix restores full pass", func(t *testing.T) {
		scope := map[string]struct{}{"known": {}}
		assert.Nil(t, normalizeDeferredCatchupScope(scope, false, 3))
	})
}
