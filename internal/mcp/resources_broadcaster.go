package mcp

// resourcesUpdatedNotifier is the slice of *server.MCPServer the
// resource broadcaster needs to push `notifications/resources/updated`
// events to every connected client. Defined here so tests can inject a
// recorder without spinning up a full MCP server.
//
// The MCP spec lets clients call `resources/subscribe` to opt into
// updates for a specific URI. Mark3labs's SDK doesn't track that
// subscription set itself — so we broadcast to all clients and let the
// client-side runtime ignore URIs it didn't subscribe to. That matches
// what the diagnostics broadcaster does for `notifications/diagnostics`,
// minus the per-session filter (resource updates are URI-scoped, not
// session-scoped, and the payload itself is empty — clients re-fetch).
type resourcesUpdatedNotifier interface {
	SendNotificationToAllClients(method string, params map[string]any)
}

// notifyBootstrapResourcesUpdated pushes a
// `notifications/resources/updated` for every bootstrap resource URI.
// Called after each graph re-warm so subscribed clients can stop
// polling `gortex://stats` / `gortex://index-health` / etc.
//
// Prefers an explicit override (`Server.resourcesNotifier`) so tests
// can record broadcasts without spinning up a full MCP server. Falls
// back to the live mcpServer; no-ops cleanly when neither is wired.
func (s *Server) notifyBootstrapResourcesUpdated() {
	notifier := s.effectiveResourcesNotifier()
	if notifier == nil {
		return
	}
	for _, uri := range bootstrapResourceURIs() {
		notifier.SendNotificationToAllClients("notifications/resources/updated", map[string]any{
			"uri": uri,
		})
	}
}

func (s *Server) effectiveResourcesNotifier() resourcesUpdatedNotifier {
	if s.resourcesNotifier != nil {
		return s.resourcesNotifier
	}
	if s.mcpServer == nil {
		return nil
	}
	return s.mcpServer
}
