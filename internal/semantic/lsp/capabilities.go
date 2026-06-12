package lsp

import (
	"encoding/json"

	"go.uber.org/zap"
)

// Capability tracking — provider-side support for the LSP
// client/registerCapability and client/unregisterCapability flow.
//
// Background. The initialize handshake gives the client a snapshot of
// the server's capabilities, but sophisticated servers (jdtls,
// tsserver, rust-analyzer) lean on dynamic registration: they keep the
// initial handshake cheap and announce extra capabilities lazily, once
// they finish workspace warmup. Without dynamic-registration support,
// any feature the server publishes late is silently unused.
//
// The flow:
//
//   server → client/registerCapability {registrations:[…]}   → reply null
//   server → client/unregisterCapability {unregisterations:[…]} → reply null
//
// We accept both the request form (with an id, expecting an ack) and
// the notification form (no id, no reply). Some servers send one,
// some the other — handle both shapes via the same applyXxx helper.
//
// Supports(method) is the single read API. It consults the static
// initialize-time snapshot AND the dynamic table, returning true if
// either advertises the method. The dynamic table is keyed by
// Registration.ID (the wire handle the server uses for unregister) —
// Supports iterates values; for the small N we expect (~tens of
// dynamic registrations per server, ever) iteration is cheaper than
// maintaining a method index that has to handle multi-registration.

// applyRegistrations records every registration in params on the
// provider's dynamic capability table. Safe to call from both the
// request-form handler (where the LSP id is meaningful) and the
// notification-form handler (no reply expected).
func (p *Provider) applyRegistrations(params json.RawMessage) {
	if len(params) == 0 {
		return
	}
	var rp RegistrationParams
	if err := json.Unmarshal(params, &rp); err != nil {
		if p.logger != nil {
			p.logger.Debug("LSP: malformed registerCapability params", zap.Error(err))
		}
		return
	}
	if len(rp.Registrations) == 0 {
		return
	}
	p.capsMu.Lock()
	for _, r := range rp.Registrations {
		if r.ID == "" || r.Method == "" {
			// Malformed registration — record nothing rather than
			// poison the table with an unkeyable entry.
			continue
		}
		p.dynamicCaps[r.ID] = r
	}
	p.capsMu.Unlock()
}

// applyUnregistrations removes every entry in params from the dynamic
// table. Per the LSP wire format the JSON field is the misspelled
// "unregisterations" — UnregistrationParams keeps that spelling.
// Servers may send an unregister for an id we never recorded
// (e.g., one issued before our last respawn); ignore those silently.
func (p *Provider) applyUnregistrations(params json.RawMessage) {
	if len(params) == 0 {
		return
	}
	var up UnregistrationParams
	if err := json.Unmarshal(params, &up); err != nil {
		if p.logger != nil {
			p.logger.Debug("LSP: malformed unregisterCapability params", zap.Error(err))
		}
		return
	}
	if len(up.Unregisterations) == 0 {
		return
	}
	p.capsMu.Lock()
	for _, u := range up.Unregisterations {
		if u.ID == "" {
			continue
		}
		delete(p.dynamicCaps, u.ID)
	}
	p.capsMu.Unlock()
}

// Supports reports whether the server advertises the given LSP method,
// consulting both the initialize-time snapshot and any dynamic
// registrations the server has issued since.
//
// Convention. The static-side check uses a small switch over the
// methods Provider actually dispatches today (hover, references,
// definition, implementation, codeAction, executeCommand, the call /
// type hierarchy preparators, plus the document-sync notifications).
// Methods outside that set fall through to the dynamic check — if a
// server registers a capability we don't have a static mapping for
// (e.g. textDocument/foldingRange, textDocument/semanticTokens) we
// still record it and report it as supported, so a future feature
// site can flip on once the registration arrives.
//
// Callers may use Supports as a precondition before issuing an LSP
// call, but it is not required: the existing dispatch sites tolerate
// "method not found" errors from the server. Supports is most useful
// for new feature sites that want to short-circuit cleanly when the
// capability was never announced.
func (p *Provider) Supports(method string) bool {
	if method == "" {
		return false
	}
	p.capsMu.RLock()
	defer p.capsMu.RUnlock()
	if staticServerSupports(&p.caps, method) {
		return true
	}
	for _, r := range p.dynamicCaps {
		if r.Method == method {
			return true
		}
	}
	return false
}

// staticServerSupports inspects the initialize-time ServerCapabilities
// snapshot for the methods this provider's dispatch sites use today.
// A nil / zero field means "the server did not advertise this at
// initialize time" — the dynamic table may still flip it on later.
func staticServerSupports(caps *ServerCapabilities, method string) bool {
	if caps == nil {
		return false
	}
	switch method {
	case "textDocument/hover":
		return caps.HoverProvider != nil
	case "textDocument/definition":
		return caps.DefinitionProvider != nil
	case "textDocument/references":
		return caps.ReferencesProvider != nil
	case "textDocument/implementation":
		return caps.ImplementationProvider != nil
	case "textDocument/codeAction":
		return caps.CodeActionProvider != nil
	case "workspace/executeCommand":
		return caps.ExecuteCommandProvider != nil
	case "textDocument/prepareCallHierarchy",
		"callHierarchy/incomingCalls",
		"callHierarchy/outgoingCalls":
		return caps.CallHierarchyProvider != nil
	case "textDocument/prepareTypeHierarchy",
		"typeHierarchy/supertypes",
		"typeHierarchy/subtypes":
		return caps.TypeHierarchyProvider != nil
	case "textDocument/didOpen",
		"textDocument/didChange",
		"textDocument/didClose":
		return caps.TextDocumentSync != nil
	}
	// Method is outside the static mapping. Let the dynamic table
	// have the final say — Supports() iterates dynamicCaps after this
	// returns false.
	return false
}
