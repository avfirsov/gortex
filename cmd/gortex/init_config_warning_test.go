package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWarnIfWorkspaceConfigIgnored(t *testing.T) {
	root := t.TempDir()
	cfgPath := filepath.Join(root, ".gortex.yaml")

	// No file → silent.
	var buf bytes.Buffer
	warnIfWorkspaceConfigIgnored(&buf, root)
	assert.Empty(t, buf.String())

	// Malformed → loud warning with path and parse error.
	require.NoError(t, os.WriteFile(cfgPath, []byte("exclude: [\"broken\n"), 0644))
	buf.Reset()
	warnIfWorkspaceConfigIgnored(&buf, root)
	assert.Contains(t, buf.String(), cfgPath)
	assert.Contains(t, buf.String(), "failed to parse")

	// Valid file with the issue-#3 typo → unknown-keys warning.
	require.NoError(t, os.WriteFile(cfgPath, []byte("index:\n  ignore:\n    - build/**\n"), 0644))
	buf.Reset()
	warnIfWorkspaceConfigIgnored(&buf, root)
	assert.Contains(t, buf.String(), "keys gortex does not recognize")
	assert.Contains(t, buf.String(), "index.ignore")

	// Fully valid file → silent.
	require.NoError(t, os.WriteFile(cfgPath, []byte("exclude:\n  - ok/**\n"), 0644))
	buf.Reset()
	warnIfWorkspaceConfigIgnored(&buf, root)
	assert.Empty(t, buf.String())
}
