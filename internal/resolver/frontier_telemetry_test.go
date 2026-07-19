package resolver

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/zzet/gortex/internal/graph"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

type frontierTelemetryStore struct {
	graph.Store
	stats graph.UnresolvedFrontierStats
	err   error
	calls int
}

func (s *frontierTelemetryStore) CountUnresolvedFrontier() (graph.UnresolvedFrontierStats, error) {
	s.calls++
	return s.stats, s.err
}

func TestResolverFrontierTelemetryDisabledDoesNoBackendWork(t *testing.T) {
	t.Setenv(resolverFrontierTelemetryEnv, "")
	backend := &frontierTelemetryStore{Store: graph.New()}
	core, observed := observer.New(zap.InfoLevel)
	resolver := New(backend)
	resolver.SetLogger(zap.New(core))

	resolver.logUnresolvedFrontier("start")
	if backend.calls != 0 {
		t.Fatalf("disabled telemetry made %d backend calls, want zero", backend.calls)
	}
	if observed.Len() != 0 {
		t.Fatalf("disabled telemetry emitted %d logs, want zero", observed.Len())
	}
}

func TestResolverFrontierTelemetryCapsBucketsAndLogsExactTotals(t *testing.T) {
	t.Setenv(resolverFrontierTelemetryEnv, "true")
	stats := graph.UnresolvedFrontierStats{
		Pending:    987654,
		GroupCount: 140,
		QueryCount: 1,
		Buckets:    make([]graph.UnresolvedFrontierBucket, 140),
	}
	for i := range stats.Buckets {
		kind := fmt.Sprintf("kind-%03d", i)
		if i == 0 {
			kind += strings.Repeat("x", resolverFrontierLogTokenByteLimit*2)
		}
		stats.Buckets[i] = graph.UnresolvedFrontierBucket{
			Kind:        graph.EdgeKind(kind),
			TargetClass: graph.UnresolvedTargetBareSymbol,
			Count:       int64(1000 - i),
		}
	}
	backend := &frontierTelemetryStore{Store: graph.New(), stats: stats}
	core, observed := observer.New(zap.InfoLevel)
	resolver := New(backend)
	resolver.SetLogger(zap.New(core))

	resolver.logUnresolvedFrontier("start")
	if backend.calls != 1 {
		t.Fatalf("enabled telemetry made %d backend calls, want one", backend.calls)
	}
	entries := observed.FilterMessage("resolver: unresolved frontier").All()
	if len(entries) != 1 {
		t.Fatalf("frontier logs = %d, want one", len(entries))
	}
	fields := entries[0].ContextMap()
	if fields["phase"] != "start" {
		t.Fatalf("phase = %#v, want start", fields["phase"])
	}
	assertTelemetryNumber(t, fields, "pending", stats.Pending)
	assertTelemetryNumber(t, fields, "group_count", stats.GroupCount)
	assertTelemetryNumber(t, fields, "query_count", stats.QueryCount)
	assertTelemetryNumber(t, fields, "reported_groups", resolverFrontierLogBucketLimit)
	assertTelemetryNumber(t, fields, "truncated_groups", stats.GroupCount-resolverFrontierLogBucketLimit)
	if _, ok := fields["elapsed"]; !ok {
		t.Fatal("elapsed field is missing")
	}

	bucketValue := reflect.ValueOf(fields["buckets"])
	if !bucketValue.IsValid() || bucketValue.Kind() != reflect.Slice {
		t.Fatalf("buckets field = %#v, want a slice", fields["buckets"])
	}
	if bucketValue.Len() != resolverFrontierLogBucketLimit {
		t.Fatalf("logged buckets = %d, want %d", bucketValue.Len(), resolverFrontierLogBucketLimit)
	}
	first := fmt.Sprint(bucketValue.Index(0).Interface())
	if !strings.Contains(first, "...") {
		t.Fatalf("oversized bucket token was not truncated: %q", first)
	}
}

func TestResolverFrontierTelemetryCapabilityAndErrorAreOptional(t *testing.T) {
	t.Setenv(resolverFrontierTelemetryEnv, "on")
	t.Run("backend without capability", func(t *testing.T) {
		core, observed := observer.New(zap.InfoLevel)
		resolver := New(graph.New())
		resolver.SetLogger(zap.New(core))
		resolver.logUnresolvedFrontier("end")
		if observed.Len() != 0 {
			t.Fatalf("unsupported backend emitted %d logs, want zero", observed.Len())
		}
	})

	t.Run("capability error", func(t *testing.T) {
		backend := &frontierTelemetryStore{Store: graph.New(), err: errors.New("boom")}
		core, observed := observer.New(zap.InfoLevel)
		resolver := New(backend)
		resolver.SetLogger(zap.New(core))
		resolver.logUnresolvedFrontier("end")
		if backend.calls != 1 {
			t.Fatalf("backend calls = %d, want one", backend.calls)
		}
		entries := observed.FilterMessage("resolver: unresolved frontier telemetry failed").All()
		if len(entries) != 1 {
			t.Fatalf("error logs = %d, want one", len(entries))
		}
		fields := entries[0].ContextMap()
		if fields["phase"] != "end" {
			t.Fatalf("phase = %#v, want end", fields["phase"])
		}
		if _, ok := fields["elapsed"]; !ok {
			t.Fatal("elapsed field is missing")
		}
	})
}

func TestResolveAllLogsFrontierStartAndEndWhenEnabled(t *testing.T) {
	t.Setenv(resolverFrontierTelemetryEnv, "1")
	backend := &frontierTelemetryStore{
		Store: graph.New(),
		stats: graph.UnresolvedFrontierStats{QueryCount: 1},
	}
	core, observed := observer.New(zap.InfoLevel)
	resolver := New(backend)
	resolver.SetLogger(zap.New(core))

	resolver.ResolveAll()
	if backend.calls != 2 {
		t.Fatalf("frontier backend calls = %d, want start and end", backend.calls)
	}
	entries := observed.FilterMessage("resolver: unresolved frontier").All()
	if len(entries) != 2 {
		t.Fatalf("frontier logs = %d, want start and end", len(entries))
	}
	if phase := entries[0].ContextMap()["phase"]; phase != "start" {
		t.Fatalf("first phase = %#v, want start", phase)
	}
	if phase := entries[1].ContextMap()["phase"]; phase != "end" {
		t.Fatalf("second phase = %#v, want end", phase)
	}
}

func assertTelemetryNumber(t *testing.T, fields map[string]any, key string, want any) {
	t.Helper()
	if fmt.Sprint(fields[key]) != fmt.Sprint(want) {
		t.Fatalf("%s = %#v, want %v", key, fields[key], want)
	}
}
