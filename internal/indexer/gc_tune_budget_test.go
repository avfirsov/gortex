package indexer

import (
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestBoundedIndexMemoryBudget(t *testing.T) {
	tests := []struct {
		name       string
		calculated int64
		current    int64
		want       int64
	}{
		{name: "preserves lower standing limit", calculated: 8 << 30, current: 4 << 30, want: 4 << 30},
		{name: "uses tighter cold budget", calculated: 2 << 30, current: 4 << 30, want: 2 << 30},
		{name: "uses cold budget when runtime is unbounded", calculated: 2 << 30, current: math.MaxInt64, want: 2 << 30},
		{name: "invalid calculated budget leaves runtime untouched", calculated: 0, current: 4 << 30, want: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, boundedIndexMemoryBudget(tt.calculated, tt.current))
		})
	}
}
