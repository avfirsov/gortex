package mcp

import (
	"fmt"
	"strings"
	"sync"
	"testing"
)

func TestResponseBufferEnforcesByteBudget(t *testing.T) {
	var b responseBuffer
	chunk := strings.Repeat("x", defaultResponseBufferBytes/3+1)
	first := b.capture("first", chunk)
	_ = b.capture("second", chunk)
	latest := b.capture("third", chunk)

	if first == "" || latest == "" {
		t.Fatal("responses within the per-entry budget were not captured")
	}
	if b.totalBytes > defaultResponseBufferBytes {
		t.Fatalf("retained bytes = %d, cap = %d", b.totalBytes, defaultResponseBufferBytes)
	}
	if got := len(b.entries); got != 2 {
		t.Fatalf("retained entries = %d, want 2 after byte eviction", got)
	}
	if _, ok := b.get(first); ok {
		t.Fatal("oldest response survived byte-budget eviction")
	}
	if _, ok := b.get(latest); !ok {
		t.Fatal("latest response was evicted")
	}
}

func TestResponseBufferRejectsOversizedEntry(t *testing.T) {
	var b responseBuffer
	oversized := strings.Repeat("x", defaultResponseBufferBytes+1)
	if id := b.capture("oversized", oversized); id != "" {
		t.Fatalf("oversized capture id = %q, want empty", id)
	}
	if len(b.entries) != 0 || b.totalBytes != 0 {
		t.Fatalf("oversized response retained: entries=%d bytes=%d", len(b.entries), b.totalBytes)
	}
}

func TestResponseBufferCountEvictionUpdatesBytes(t *testing.T) {
	var b responseBuffer
	for i := 0; i < defaultResponseBufferCap+3; i++ {
		b.capture(fmt.Sprintf("tool-%d", i), strings.Repeat("x", 1024+i))
	}
	if got := len(b.entries); got != defaultResponseBufferCap {
		t.Fatalf("retained entries = %d, want %d", got, defaultResponseBufferCap)
	}
	wantBytes := 0
	for _, entry := range b.entries {
		wantBytes += len(entry.Text)
	}
	if b.totalBytes != wantBytes {
		t.Fatalf("tracked bytes = %d, actual = %d", b.totalBytes, wantBytes)
	}
}

func TestResponseBufferConcurrentCapturesStayBounded(t *testing.T) {
	var b responseBuffer
	const callers = 64
	payload := strings.Repeat("x", 256<<10)
	var wg sync.WaitGroup
	wg.Add(callers)
	for i := 0; i < callers; i++ {
		go func(i int) {
			defer wg.Done()
			b.capture(fmt.Sprintf("tool-%d", i), payload)
		}(i)
	}
	wg.Wait()

	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.entries) > defaultResponseBufferCap {
		t.Fatalf("retained entries = %d, cap = %d", len(b.entries), defaultResponseBufferCap)
	}
	if b.totalBytes > defaultResponseBufferBytes {
		t.Fatalf("retained bytes = %d, cap = %d", b.totalBytes, defaultResponseBufferBytes)
	}
}

func BenchmarkResponseBufferByteCap(b *testing.B) {
	for range b.N {
		var buf responseBuffer
		for i := 0; i < 32; i++ {
			payload := strings.Repeat(string(rune('a'+i%26)), 1<<20)
			buf.capture(fmt.Sprintf("tool-%d", i), payload)
		}
		b.ReportMetric(float64(buf.totalBytes)/(1<<20), "retained-MiB")
		b.ReportMetric(float64(len(buf.entries)), "retained-entries")
	}
}
