package resolver

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/zzet/gortex/internal/graph"
	"go.uber.org/zap"
)

const (
	resolverFrontierTelemetryEnv      = "GORTEX_RESOLVER_FRONTIER_TELEMETRY"
	resolverFrontierLogBucketLimit    = 128
	resolverFrontierLogTokenByteLimit = 64
)

func resolverFrontierTelemetryEnabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(resolverFrontierTelemetryEnv))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

// logUnresolvedFrontier emits an opt-in, backend-aggregated snapshot of the
// generic resolver frontier. The environment gate is checked before the
// capability assertion, so normal cold, warm, and partial passes pay neither a
// SQL query nor bucket formatting cost when telemetry is disabled.
func (r *Resolver) logUnresolvedFrontier(phase string) {
	if !resolverFrontierTelemetryEnabled() {
		return
	}
	counter, ok := r.graph.(graph.UnresolvedFrontierCounter)
	if !ok {
		return
	}

	started := time.Now()
	stats, err := counter.CountUnresolvedFrontier()
	elapsed := time.Since(started)
	logger := r.logger
	if logger == nil {
		logger = zap.NewNop()
	}
	if err != nil {
		logger.Warn("resolver: unresolved frontier telemetry failed",
			zap.String("phase", phase),
			zap.Duration("elapsed", elapsed),
			zap.Error(err))
		return
	}

	emitted := len(stats.Buckets)
	if emitted > resolverFrontierLogBucketLimit {
		emitted = resolverFrontierLogBucketLimit
	}
	buckets := make([]string, 0, emitted)
	for _, bucket := range stats.Buckets[:emitted] {
		buckets = append(buckets, fmt.Sprintf("%s/%s=%d",
			boundedFrontierTelemetryToken(string(bucket.Kind)),
			boundedFrontierTelemetryToken(string(bucket.TargetClass)),
			bucket.Count))
	}
	groupCount := stats.GroupCount
	if groupCount < len(stats.Buckets) {
		groupCount = len(stats.Buckets)
	}
	truncated := groupCount - emitted
	if truncated < 0 {
		truncated = 0
	}

	logger.Info("resolver: unresolved frontier",
		zap.String("phase", phase),
		zap.Int64("pending", stats.Pending),
		zap.Int("group_count", groupCount),
		zap.Int("query_count", stats.QueryCount),
		zap.Int("reported_groups", emitted),
		zap.Int("truncated_groups", truncated),
		zap.Strings("buckets", buckets),
		zap.Duration("elapsed", elapsed))
}

func boundedFrontierTelemetryToken(value string) string {
	if len(value) <= resolverFrontierLogTokenByteLimit {
		return value
	}
	return value[:resolverFrontierLogTokenByteLimit-3] + "..."
}
