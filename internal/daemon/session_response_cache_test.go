package daemon

import (
	"bytes"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
)

func TestSessionDispatchMCPOnceDeduplicatesStatefulCalls(t *testing.T) {
	session := &Session{LogicalSessionID: "logical-cache", ID: "logical-cache"}
	frames := [][]byte{
		[]byte(`{"jsonrpc":"2.0","id":9007199254740993,"method":"tools/call","params":{"name":"read","arguments":{"operation":"source"}}}`),
		[]byte(`{"jsonrpc":"2.0","id":null,"method":"tools/call","params":{"name":"explore","arguments":{"operation":"localize"}}}`),
	}
	for i, frame := range frames {
		calls := 0
		dispatch := func() ([]byte, error) {
			calls++
			return []byte(fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"result":{"side_effect":%d}}`, i, calls)), nil
		}
		first, cached, err := session.dispatchMCPOnce(frame, dispatch)
		if err != nil || cached {
			t.Fatalf("first dispatch %d: cached=%v err=%v", i, cached, err)
		}
		second, cached, err := session.dispatchMCPOnce(frame, dispatch)
		if err != nil || !cached {
			t.Fatalf("replay %d: cached=%v err=%v", i, cached, err)
		}
		if calls != 1 || !bytes.Equal(first, second) {
			t.Fatalf("frame %d dispatched %d times; first=%s second=%s", i, calls, first, second)
		}
	}
}

func TestSessionDispatchMCPOnceSameIDDifferentPayloadIsNotDeduplicated(t *testing.T) {
	session := &Session{LogicalSessionID: "logical-cache", ID: "logical-cache"}
	firstFrame := []byte(`{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"search","arguments":{"query":"a"}}}`)
	secondFrame := []byte(`{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"search","arguments":{"query":"b"}}}`)
	calls := 0
	dispatch := func() ([]byte, error) {
		calls++
		return []byte(fmt.Sprintf(`{"jsonrpc":"2.0","id":7,"result":%d}`, calls)), nil
	}
	first, _, err := session.dispatchMCPOnce(firstFrame, dispatch)
	if err != nil {
		t.Fatal(err)
	}
	second, cached, err := session.dispatchMCPOnce(secondFrame, dispatch)
	if err != nil || cached {
		t.Fatalf("different payload was cached: cached=%v err=%v", cached, err)
	}
	replay, cached, err := session.dispatchMCPOnce(secondFrame, dispatch)
	if err != nil || !cached {
		t.Fatalf("identical second payload was not cached: cached=%v err=%v", cached, err)
	}
	if calls != 2 || bytes.Equal(first, second) || !bytes.Equal(second, replay) {
		t.Fatalf("calls=%d first=%s second=%s replay=%s", calls, first, second, replay)
	}
}

func TestSessionDispatchMCPOnceDoesNotCacheNotifications(t *testing.T) {
	session := &Session{LogicalSessionID: "logical-cache", ID: "logical-cache"}
	frame := []byte(`{"jsonrpc":"2.0","method":"notifications/initialized"}`)
	calls := 0
	for i := 0; i < 2; i++ {
		_, cached, err := session.dispatchMCPOnce(frame, func() ([]byte, error) {
			calls++
			return nil, nil
		})
		if err != nil || cached {
			t.Fatalf("notification dispatch %d: cached=%v err=%v", i, cached, err)
		}
	}
	if calls != 2 {
		t.Fatalf("notification calls=%d, want 2", calls)
	}
}

func TestSessionDispatchMCPOnceEvictsOldestResponse(t *testing.T) {
	session := &Session{LogicalSessionID: "logical-cache", ID: "logical-cache"}
	calls := 0
	for i := 0; i <= sessionResponseCacheEntries; i++ {
		frame := []byte(fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"method":"ping"}`, i))
		_, cached, err := session.dispatchMCPOnce(frame, func() ([]byte, error) {
			calls++
			return []byte(fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"result":{}}`, i)), nil
		})
		if err != nil || cached {
			t.Fatalf("fill %d: cached=%v err=%v", i, cached, err)
		}
	}
	first := []byte(`{"jsonrpc":"2.0","id":0,"method":"ping"}`)
	_, cached, err := session.dispatchMCPOnce(first, func() ([]byte, error) {
		calls++
		return []byte(`{"jsonrpc":"2.0","id":0,"result":{}}`), nil
	})
	if err != nil || cached {
		t.Fatalf("evicted first response still cached: cached=%v err=%v", cached, err)
	}
	if calls != sessionResponseCacheEntries+2 {
		t.Fatalf("dispatch calls=%d, want %d", calls, sessionResponseCacheEntries+2)
	}
}

func TestSessionDispatchMCPOnceSerializesOverlappingReplay(t *testing.T) {
	session := &Session{LogicalSessionID: "logical-cache", ID: "logical-cache"}
	frame := []byte(`{"jsonrpc":"2.0","id":"same","method":"tools/call","params":{"name":"read"}}`)
	var calls atomic.Int32
	start := make(chan struct{})
	dispatch := func() ([]byte, error) {
		calls.Add(1)
		<-start
		return []byte(`{"jsonrpc":"2.0","id":"same","result":{}}`), nil
	}
	var wg sync.WaitGroup
	wg.Add(2)
	for i := 0; i < 2; i++ {
		go func() {
			defer wg.Done()
			if _, _, err := session.dispatchMCPOnce(frame, dispatch); err != nil {
				t.Error(err)
			}
		}()
	}
	for calls.Load() == 0 {
	}
	close(start)
	wg.Wait()
	if calls.Load() != 1 {
		t.Fatalf("overlapping identical request dispatched %d times", calls.Load())
	}
}
