package daemon

import (
	"bytes"
	"fmt"
	"sync/atomic"
	"testing"
	"time"
)

func TestSessionDispatchMCPOnceDoesNotSerializeUnrelatedRequests(t *testing.T) {
	t.Parallel()

	sess := &Session{LogicalSessionID: "logical-session"}
	blockedStarted := make(chan struct{})
	releaseBlocked := make(chan struct{})
	defer close(releaseBlocked)
	blockedDone := make(chan struct{})

	go func() {
		defer close(blockedDone)
		_, _, _ = sess.dispatchMCPOnce(
			[]byte(`{"jsonrpc":"2.0","id":"blocked","method":"tools/call"}`),
			func() ([]byte, error) {
				close(blockedStarted)
				<-releaseBlocked
				return []byte(`{"jsonrpc":"2.0","id":"blocked","result":{}}`), nil
			},
		)
	}()
	<-blockedStarted

	unrelatedDone := make(chan error, 1)
	go func() {
		response, _, err := sess.dispatchMCPOnce(
			[]byte(`{"jsonrpc":"2.0","id":"unrelated","method":"tools/call"}`),
			func() ([]byte, error) {
				return []byte(`{"jsonrpc":"2.0","id":"unrelated","result":{}}`), nil
			},
		)
		if err == nil && !bytes.Contains(response, []byte(`"id":"unrelated"`)) {
			err = fmt.Errorf("unexpected response: %s", response)
		}
		unrelatedDone <- err
	}()

	select {
	case err := <-unrelatedDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("unrelated request waited for a blocked dispatch")
	}
}

func TestSessionDispatchMCPOnceSingleflightsIdenticalRequests(t *testing.T) {
	t.Parallel()

	sess := &Session{LogicalSessionID: "logical-session"}
	frame := []byte(`{"jsonrpc":"2.0","id":99,"method":"tools/call","params":{"name":"read"}}`)
	started := make(chan struct{})
	release := make(chan struct{})
	var calls atomic.Int32
	type result struct {
		response []byte
		cached   bool
		err      error
	}
	results := make(chan result, 2)
	dispatch := func() ([]byte, error) {
		if calls.Add(1) == 1 {
			close(started)
		}
		<-release
		return []byte(`{"jsonrpc":"2.0","id":99,"result":{"ok":true}}`), nil
	}

	go func() {
		response, cached, err := sess.dispatchMCPOnce(frame, dispatch)
		results <- result{response: response, cached: cached, err: err}
	}()
	<-started
	go func() {
		response, cached, err := sess.dispatchMCPOnce(frame, dispatch)
		results <- result{response: response, cached: cached, err: err}
	}()
	close(release)

	first := <-results
	second := <-results
	if first.err != nil || second.err != nil {
		t.Fatalf("dispatch errors: first=%v second=%v", first.err, second.err)
	}
	if calls.Load() != 1 {
		t.Fatalf("dispatch calls = %d, want 1", calls.Load())
	}
	if !bytes.Equal(first.response, second.response) {
		t.Fatalf("responses differ: first=%s second=%s", first.response, second.response)
	}
	if first.cached == second.cached {
		t.Fatalf("cached flags = %v and %v, want one original and one shared response", first.cached, second.cached)
	}
}

func TestSessionDispatchMCPOnceRejectsOversizedIDFromCache(t *testing.T) {
	t.Parallel()

	sess := &Session{LogicalSessionID: "logical-session"}
	id := bytes.Repeat([]byte{'x'}, MCPResponseCacheMaxIDBytes+1)
	frame := append([]byte(`{"jsonrpc":"2.0","id":"`), id...)
	frame = append(frame, []byte(`","method":"tools/call"}`)...)
	var calls atomic.Int32
	dispatch := func() ([]byte, error) {
		calls.Add(1)
		return []byte(`{"jsonrpc":"2.0","id":null,"result":{}}`), nil
	}

	for range 2 {
		if _, cached, err := sess.dispatchMCPOnce(frame, dispatch); err != nil {
			t.Fatal(err)
		} else if cached {
			t.Fatal("oversized request ID was cached")
		}
	}
	if calls.Load() != 2 {
		t.Fatalf("dispatch calls = %d, want 2", calls.Load())
	}

	sess.responseMu.Lock()
	defer sess.responseMu.Unlock()
	if len(sess.responseCache) != 0 || len(sess.responseInFlight) != 0 || sess.responseBytes != 0 {
		t.Fatalf("oversized ID retained state: cache=%d in_flight=%d bytes=%d", len(sess.responseCache), len(sess.responseInFlight), sess.responseBytes)
	}
}

func TestSessionResponseCacheAccountsForKeyAndEntryOverhead(t *testing.T) {
	t.Parallel()

	sess := &Session{LogicalSessionID: "logical-session"}
	frame := []byte(`{"jsonrpc":"2.0","id":"accounted-key","method":"tools/call"}`)
	response := []byte(`{"jsonrpc":"2.0","id":"accounted-key","result":{}}`)
	if _, _, err := sess.dispatchMCPOnce(frame, func() ([]byte, error) { return response, nil }); err != nil {
		t.Fatal(err)
	}
	key, _, ok := mcpResponseCacheIdentity(frame)
	if !ok {
		t.Fatal("cache identity unexpectedly rejected")
	}
	want := len(key) + len(response) + sessionResponseCacheEntryOverhead

	sess.responseMu.Lock()
	defer sess.responseMu.Unlock()
	entry, ok := sess.responseCache[key]
	if !ok {
		t.Fatal("response was not cached")
	}
	if entry.size != want || sess.responseBytes != want {
		t.Fatalf("accounted size entry=%d total=%d, want %d", entry.size, sess.responseBytes, want)
	}
}
