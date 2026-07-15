package store_sqlite

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
)

func TestGetNodeContextHonorsCancellation(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "graph.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := s.GetNodeContext(ctx, "missing"); !errors.Is(err, context.Canceled) {
		t.Fatalf("GetNodeContext error = %v, want context.Canceled", err)
	}
}

func TestGetNodesByIDsContextHonorsCancellation(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "graph.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := s.GetNodesByIDsContext(ctx, []string{"missing"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("GetNodesByIDsContext error = %v, want context.Canceled", err)
	}
}

func TestGetOutEdgesByNodeIDsContextHonorsCancellation(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "graph.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, _, err := s.GetOutEdgesByNodeIDsContext(ctx, []string{"missing"}, 1); !errors.Is(err, context.Canceled) {
		t.Fatalf("GetOutEdgesByNodeIDsContext error = %v, want context.Canceled", err)
	}
}
