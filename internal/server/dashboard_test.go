package server

import "testing"

func TestCanonicalContractKey(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"leaves non-http IDs alone", "grpc::user.v1.UserService/GetUser", "grpc::user.v1.UserService/GetUser"},
		{"leaves unparameterised routes alone", "http::GET::/v1/health", "http::GET::/v1/health"},
		{"rewrites single param", "http::DELETE::/v1/tucks/{id}", "http::DELETE::/v1/tucks/{p1}"},
		{
			"collapses differing param names to the same key — the exact provider/consumer pairing bug",
			"http::DELETE::/v1/workspaces/{wid}/tags/{id}",
			"http::DELETE::/v1/workspaces/{p1}/tags/{p2}",
		},
		{
			"consumer variant canonicalises to the same key",
			"http::DELETE::/v1/workspaces/{workspaceId}/tags/{id}",
			"http::DELETE::/v1/workspaces/{p1}/tags/{p2}",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := canonicalContractKey(tt.in)
			if got != tt.want {
				t.Errorf("canonicalContractKey(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestContractScope(t *testing.T) {
	tests := []struct {
		name     string
		rawType  string
		producer string
		want     string
	}{
		{"go.mod dep always external", "dependency", "core-api", "external"},
		{"http with provider is own", "http", "core-api", "own"},
		{"http with no provider is external", "http", "", "external"},
		{"topic with producer is own", "topic", "worker", "own"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := contractScope(tt.rawType, tt.producer); got != tt.want {
				t.Errorf("contractScope(%q, %q) = %q, want %q", tt.rawType, tt.producer, got, tt.want)
			}
		})
	}
}
