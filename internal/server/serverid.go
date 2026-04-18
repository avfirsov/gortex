package server

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
)

// DefaultServerIDPath returns the default persistence path for the
// per-machine server id. Callers that want a process-local id (e.g.
// tests, custom cache dirs) should pass their own dir to
// LoadOrCreateServerID instead of calling this.
func DefaultServerIDPath() (string, error) {
	home, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("resolve user cache dir: %w", err)
	}
	return filepath.Join(home, "gortex", "server.id"), nil
}

// LoadOrCreateServerID returns a stable UUID for this server
// instance. If path already holds a valid UUID, it's returned as-is;
// otherwise a fresh UUID is generated and persisted. Intermediate
// directories are created as needed (0o755).
//
// A malformed existing file is replaced — the file is advisory
// state, not authoritative, so there's no reason to fail loudly
// when it gets corrupted.
func LoadOrCreateServerID(path string) (string, error) {
	if data, err := os.ReadFile(path); err == nil {
		id := strings.TrimSpace(string(data))
		if _, err := uuid.Parse(id); err == nil {
			return id, nil
		}
		// Fall through — we'll regenerate below.
	} else if !os.IsNotExist(err) {
		return "", fmt.Errorf("read server id: %w", err)
	}

	id := uuid.NewString()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", fmt.Errorf("create server id dir: %w", err)
	}
	if err := os.WriteFile(path, []byte(id+"\n"), 0o600); err != nil {
		return "", fmt.Errorf("write server id: %w", err)
	}
	return id, nil
}
