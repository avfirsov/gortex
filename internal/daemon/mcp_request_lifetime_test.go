package daemon

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net"
	"sync/atomic"
	"testing"
	"time"
)

type testMCPResponse struct {
	ID     json.RawMessage `json:"id"`
	Result map[string]any  `json:"result"`
	Error  *struct {
		Code    int            `json:"code"`
		Message string         `json:"message"`
		Data    map[string]any `json:"data"`
	} `json:"error"`
}

type testMCPDispatcherFunc func(context.Context, *Session, []byte) ([]byte, error)

func (f testMCPDispatcherFunc) Dispatch(ctx context.Context, sess *Session, frame []byte) ([]byte, error) {
	return f(ctx, sess, frame)
}

func TestServeMCPConnectionDeadlineCancelsDispatcher(t *testing.T) {
	cancelled := make(chan error, 1)
	conn, reader, _ := startMCPConnectionTest(t, 25*time.Millisecond, func(ctx context.Context, _ []byte) ([]byte, error) {
		<-ctx.Done()
		cancelled <- ctx.Err()
		return nil, ctx.Err()
	})

	writeMCPTestFrame(t, conn, `{"jsonrpc":"2.0","id":"slow-id","method":"tools/call","params":{"name":"search"}}`)
	response := readMCPTestResponse(t, conn, reader)
	if string(response.ID) != `"slow-id"` {
		t.Fatalf("response id = %s, want exact raw string id", response.ID)
	}
	if response.Error == nil || response.Error.Code != mcpRequestTimeoutCode {
		t.Fatalf("response error = %#v, want timeout code %d", response.Error, mcpRequestTimeoutCode)
	}
	if response.Error.Data["outcome"] != "deadline" || response.Error.Data["deadline_ms"] != float64(25) {
		t.Fatalf("timeout data = %#v", response.Error.Data)
	}
	select {
	case err := <-cancelled:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("dispatcher cancellation = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("dispatcher did not observe deadline")
	}
}

func TestServeMCPConnectionProcessesNextRequestAndCancellation(t *testing.T) {
	started := make(chan struct{})
	conn, reader, _ := startMCPConnectionTest(t, time.Second, func(ctx context.Context, frame []byte) ([]byte, error) {
		var envelope mcpRequestEnvelope
		if err := json.Unmarshal(frame, &envelope); err != nil {
			return nil, err
		}
		if string(envelope.ID) == "1" {
			close(started)
			<-ctx.Done()
			return nil, ctx.Err()
		}
		return []byte(`{"jsonrpc":"2.0","id":2,"result":{"ok":true}}`), nil
	})

	writeMCPTestFrame(t, conn, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"search"}}`)
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("blocked request did not start")
	}
	writeMCPTestFrame(t, conn, `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"search"}}`)
	response := readMCPTestResponse(t, conn, reader)
	if string(response.ID) != "2" || response.Error != nil || response.Result["ok"] != true {
		t.Fatalf("next response = %#v", response)
	}

	writeMCPTestFrame(t, conn, `{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":1,"reason":"test"}}`)
	response = readMCPTestResponse(t, conn, reader)
	if string(response.ID) != "1" || response.Error == nil || response.Error.Code != mcpRequestCancelledCode {
		t.Fatalf("cancel response = %#v", response)
	}
}

func TestServeMCPConnectionCancellationUsesExactRawIDAndTeardownCancelsAll(t *testing.T) {
	numericStarted := make(chan struct{})
	stringStarted := make(chan struct{})
	numericDone := make(chan error, 1)
	stringDone := make(chan error, 1)
	conn, reader, done := startMCPConnectionTest(t, time.Second, func(ctx context.Context, frame []byte) ([]byte, error) {
		var envelope mcpRequestEnvelope
		if err := json.Unmarshal(frame, &envelope); err != nil {
			return nil, err
		}
		switch string(envelope.ID) {
		case "7":
			close(numericStarted)
			<-ctx.Done()
			numericDone <- ctx.Err()
		case `"7"`:
			close(stringStarted)
			<-ctx.Done()
			stringDone <- ctx.Err()
		}
		return nil, ctx.Err()
	})

	writeMCPTestFrame(t, conn, `{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"search"}}`)
	writeMCPTestFrame(t, conn, `{"jsonrpc":"2.0","id":"7","method":"tools/call","params":{"name":"search"}}`)
	awaitMCPTestSignal(t, numericStarted, "numeric request did not start")
	awaitMCPTestSignal(t, stringStarted, "string request did not start")

	writeMCPTestFrame(t, conn, `{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":7}}`)
	response := readMCPTestResponse(t, conn, reader)
	if string(response.ID) != "7" || response.Error == nil || response.Error.Code != mcpRequestCancelledCode {
		t.Fatalf("numeric cancel response = %#v", response)
	}
	select {
	case err := <-numericDone:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("numeric cancellation = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("numeric request was not cancelled")
	}
	select {
	case err := <-stringDone:
		t.Fatalf("string request with distinct raw id was cancelled: %v", err)
	case <-time.After(30 * time.Millisecond):
	}

	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-stringDone:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("teardown cancellation = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("connection teardown did not cancel in-flight request")
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("connection did not wait for in-flight request cleanup")
	}
}

func TestServeMCPDeadlineIsNotStoredAsReplayResponse(t *testing.T) {
	var calls atomic.Int32
	dispatcher := testMCPDispatcherFunc(func(ctx context.Context, _ *Session, _ []byte) ([]byte, error) {
		if calls.Add(1) == 1 {
			<-ctx.Done()
			return []byte(`{"jsonrpc":"2.0","id":9,"result":{"late":true}}`), nil
		}
		return []byte(`{"jsonrpc":"2.0","id":9,"result":{"retried":true}}`), nil
	})
	server := &Server{
		MCPDispatcher:      dispatcher,
		MCPToolCallTimeout: 20 * time.Millisecond,
	}
	sess := &Session{LogicalSessionID: "deadline-retry"}
	serverConn, clientConn := net.Pipe()
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer serverConn.Close()
		server.serveMCP(serverConn, bufio.NewReader(serverConn), sess)
	}()
	t.Cleanup(func() {
		_ = clientConn.Close()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatal("serveMCP did not stop")
		}
	})
	reader := bufio.NewReader(clientConn)
	request := `{"jsonrpc":"2.0","id":9,"method":"tools/call","params":{"name":"search"}}`

	writeMCPTestFrame(t, clientConn, request)
	first := readMCPTestResponse(t, clientConn, reader)
	if first.Error == nil || first.Error.Code != mcpRequestTimeoutCode {
		t.Fatalf("first response = %#v", first)
	}
	writeMCPTestFrame(t, clientConn, request)
	second := readMCPTestResponse(t, clientConn, reader)
	if second.Error != nil || second.Result["retried"] != true {
		t.Fatalf("retry response = %#v", second)
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("dispatch calls = %d, want 2", got)
	}
}

func startMCPConnectionTest(t *testing.T, timeout time.Duration, dispatch mcpFrameDispatch) (net.Conn, *bufio.Reader, <-chan struct{}) {
	t.Helper()
	serverConn, clientConn := net.Pipe()
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer serverConn.Close()
		serveMCPConnection(serverConn, bufio.NewReader(serverConn), timeout, func(ctx context.Context, frame []byte) ([]byte, error) {
			return dispatchMCPWithContext(ctx, func() ([]byte, error) { return dispatch(ctx, frame) })
		})
	}()
	t.Cleanup(func() {
		_ = clientConn.Close()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatal("MCP connection did not stop")
		}
	})
	return clientConn, bufio.NewReader(clientConn), done
}

func writeMCPTestFrame(t *testing.T, conn net.Conn, frame string) {
	t.Helper()
	if err := conn.SetWriteDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	payload := append([]byte(frame), '\n')
	for len(payload) > 0 {
		n, err := conn.Write(payload)
		if err != nil {
			t.Fatal(err)
		}
		payload = payload[n:]
	}
}

func readMCPTestResponse(t *testing.T, conn net.Conn, reader *bufio.Reader) testMCPResponse {
	t.Helper()
	if err := conn.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	frame, err := reader.ReadBytes('\n')
	if err != nil {
		t.Fatal(err)
	}
	var response testMCPResponse
	if err := json.Unmarshal(frame, &response); err != nil {
		t.Fatalf("decode response %q: %v", frame, err)
	}
	return response
}

func awaitMCPTestSignal(t *testing.T, signal <-chan struct{}, message string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(time.Second):
		t.Fatal(message)
	}
}
