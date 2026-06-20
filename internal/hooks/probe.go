package hooks

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/zzet/gortex/internal/daemon"
)

func unmarshalResult(raw json.RawMessage, v any) error {
	if len(raw) == 0 {
		return nil
	}
	return json.Unmarshal(raw, v)
}

// probeViaDaemon dials the local daemon over its unix socket and runs one
// search_symbols control RPC. The whole exchange (dial + handshake + RPC)
// must fit inside timeout; otherwise errProbeTimeout is returned and the
// caller falls through to soft guidance.
//
// Returns errDaemonUnreachable when the daemon isn't running — the hook
// distinguishes "no signal" from "probed and missed" so telemetry stays
// clean.
func probeViaDaemon(pattern string, timeout time.Duration) ([]grepSymbolHit, error) {
	deadline := time.Now().Add(timeout)
	type probeResult struct {
		hits []grepSymbolHit
		err  error
	}
	done := make(chan probeResult, 1)

	go func() {
		client, err := daemon.Dial(daemon.Handshake{
			Mode:       daemon.ModeControl,
			ClientName: "gortex-hook",
		})
		if err != nil {
			if errors.Is(err, daemon.ErrDaemonUnavailable) {
				done <- probeResult{err: errDaemonUnreachable}
				return
			}
			done <- probeResult{err: fmt.Errorf("dial daemon: %w", err)}
			return
		}
		defer client.Close()

		// Cap each socket op at the remaining budget so a stuck daemon
		// can't pin the goroutine past timeout.
		_ = client.Conn.SetDeadline(deadline)

		resp, err := client.Control(daemon.ControlSearchSymbols, daemon.SearchSymbolsParams{
			Query: pattern,
			Limit: 10,
		})
		if err != nil {
			done <- probeResult{err: fmt.Errorf("control rpc: %w", err)}
			return
		}
		if !resp.OK {
			done <- probeResult{err: fmt.Errorf("daemon rejected search [%s]: %s", resp.ErrorCode, resp.ErrorMsg)}
			return
		}

		var result daemon.SearchSymbolsResult
		if err := unmarshalResult(resp.Result, &result); err != nil {
			done <- probeResult{err: fmt.Errorf("decode result: %w", err)}
			return
		}
		hits := make([]grepSymbolHit, 0, len(result.Hits))
		for _, h := range result.Hits {
			hits = append(hits, grepSymbolHit{
				Name:     h.Name,
				Kind:     h.Kind,
				FilePath: h.FilePath,
				Line:     h.Line,
			})
		}
		done <- probeResult{hits: hits}
	}()

	select {
	case r := <-done:
		return r.hits, r.err
	case <-time.After(time.Until(deadline)):
		return nil, errProbeTimeout
	}
}
