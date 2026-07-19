package tstypes

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/zzet/gortex/internal/semantic"
)

func TestEnrichRepoContextCancelledBeforeApplyIsMutationFree(t *testing.T) {
	g, dir := buildFixture(t, map[string]string{
		"a/Svc.java": javaSvc,
		"b/App.java": `package b;
import a.Svc;
public class App { public void main() { new Svc().run(); } }
`,
	})
	p := NewProvider(JavaSpec(), zap.NewNop())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	result, err := p.EnrichRepoContext(ctx, g, "", dir, func(int) time.Duration { return 0 })
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.True(t, result.Partial)
	assert.Equal(t, semantic.EnrichBoundBudget, result.BoundReason)
	assert.Zero(t, result.EdgesAdded)
	assert.Zero(t, result.EdgesConfirmed)
}
