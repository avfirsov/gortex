package platform

import (
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
)

func TestReplaceFileReplacesAnExistingDestination(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src")
	dst := filepath.Join(dir, "dst")
	if err := os.WriteFile(src, []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := ReplaceFile(src, dst); err != nil {
		t.Fatalf("ReplaceFile: %v", err)
	}
	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "new" {
		t.Fatalf("destination = %q, want %q", got, "new")
	}
	if _, err := os.Stat(src); !os.IsNotExist(err) {
		t.Fatalf("source survived the replace: %v", err)
	}
	if err := ReplaceFile(src, dst); err == nil {
		t.Fatal("replacing from a missing source succeeded")
	}
}

// TestReplaceFileSurvivesConcurrentReplacers commits the same destination from
// many goroutines at once. On Windows a replace whose destination another
// goroutine is replacing or reading fails with a sharing violation, so every
// caller has to get its write in.
func TestReplaceFileSurvivesConcurrentReplacers(t *testing.T) {
	dir := t.TempDir()
	dst := filepath.Join(dir, "dst")

	var wg sync.WaitGroup
	for i := range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			src, err := os.CreateTemp(dir, "src-*")
			if err != nil {
				t.Error(err)
				return
			}
			if _, err := src.Write([]byte{byte('a' + i)}); err != nil {
				t.Error(err)
				return
			}
			if err := src.Close(); err != nil {
				t.Error(err)
				return
			}
			if err := ReplaceFile(src.Name(), dst); err != nil {
				t.Errorf("ReplaceFile: %v", err)
			}
		}()
	}
	wg.Wait()

	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("destination = %q, want one committed write", got)
	}
}

func TestClaimFileHasSingleWinner(t *testing.T) {
	path := filepath.Join(t.TempDir(), "claimed")
	if err := os.WriteFile(path, []byte("payload"), 0o600); err != nil {
		t.Fatal(err)
	}

	var winners atomic.Int32
	var wg sync.WaitGroup
	for range 32 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if ClaimFile(path) {
				winners.Add(1)
			}
		}()
	}
	wg.Wait()

	if got := winners.Load(); got != 1 {
		t.Fatalf("claim winners = %d, want 1", got)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("claimed file survived: %v", err)
	}
	if ClaimFile(path) {
		t.Fatal("a consumed path was claimed twice")
	}
	if ClaimFile("") {
		t.Fatal("an empty path was claimed")
	}
	if _, err := os.Stat(path + ClaimMarkerSuffix); !os.IsNotExist(err) {
		t.Fatalf("claim marker survived: %v", err)
	}
}

func TestConsumeFileHandsTheContentToASingleWinner(t *testing.T) {
	path := filepath.Join(t.TempDir(), "consumed")
	if err := os.WriteFile(path, []byte("payload"), 0o600); err != nil {
		t.Fatal(err)
	}

	var winners atomic.Int32
	var wg sync.WaitGroup
	for range 32 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if data, ok := ConsumeFile(path); ok && string(data) == "payload" {
				winners.Add(1)
			}
		}()
	}
	wg.Wait()

	if got := winners.Load(); got != 1 {
		t.Fatalf("consume winners = %d, want 1", got)
	}
	if _, ok := ConsumeFile(path); ok {
		t.Fatal("a consumed path was consumed twice")
	}
	if _, ok := ConsumeFile(""); ok {
		t.Fatal("an empty path was consumed")
	}
}

// TestClaimFileSurvivesConcurrentReaders reads the claimed file from other
// goroutines while it is being claimed. Windows opens files without
// FILE_SHARE_DELETE, so a reader holding the file blocks the winner's delete;
// the winner must still come away with the claim.
func TestClaimFileSurvivesConcurrentReaders(t *testing.T) {
	path := filepath.Join(t.TempDir(), "read-while-claimed")
	if err := os.WriteFile(path, []byte("payload"), 0o600); err != nil {
		t.Fatal(err)
	}

	done := make(chan struct{})
	var readers sync.WaitGroup
	for range 8 {
		readers.Add(1)
		go func() {
			defer readers.Done()
			for {
				select {
				case <-done:
					return
				default:
					_, _ = os.ReadFile(path) //nolint:gosec // test-owned path
				}
			}
		}()
	}

	var winners atomic.Int32
	var claimers sync.WaitGroup
	for range 16 {
		claimers.Add(1)
		go func() {
			defer claimers.Done()
			if ClaimFile(path) {
				winners.Add(1)
			}
		}()
	}
	claimers.Wait()
	close(done)
	readers.Wait()

	if got := winners.Load(); got != 1 {
		t.Fatalf("claim winners = %d, want 1", got)
	}
}

// TestClaimFileRefusesAHeldClaim pins the marker as the arbiter: while one
// claim is outstanding nobody else may consume the file, on every platform.
func TestClaimFileRefusesAHeldClaim(t *testing.T) {
	path := filepath.Join(t.TempDir(), "held")
	if err := os.WriteFile(path, []byte("payload"), 0o600); err != nil {
		t.Fatal(err)
	}
	release, ok := claimMarker(path)
	if !ok {
		t.Fatal("claimMarker failed")
	}
	if ClaimFile(path) {
		t.Fatal("claimed a file whose claim is held")
	}
	if _, ok := ConsumeFile(path); ok {
		t.Fatal("consumed a file whose claim is held")
	}
	release()
	if !ClaimFile(path) {
		t.Fatal("a released claim was not reclaimable")
	}
}
