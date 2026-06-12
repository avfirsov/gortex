package main

import (
	"bytes"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"text/template"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestLaunchdPlistTemplate_Renders proves the launchd plist template
// is syntactically valid and carries the substitutions it must —
// binary path, log path, and label. A regression here would produce
// an unloadable plist with a cryptic launchctl error, so the check is
// just "did we put the right strings in the right places."
func TestLaunchdPlistTemplate_Renders(t *testing.T) {
	var buf bytes.Buffer
	tmpl := template.Must(template.New("plist").Parse(launchdPlistTemplate))
	require.NoError(t, tmpl.Execute(&buf, map[string]string{
		"Label":   "com.zzet.gortex",
		"Exe":     "/usr/local/bin/gortex",
		"LogPath": "/Users/testuser/.cache/gortex/daemon.log",
	}))

	out := buf.String()
	assert.Contains(t, out, "<key>Label</key>\n    <string>com.zzet.gortex</string>")
	assert.Contains(t, out, "<string>/usr/local/bin/gortex</string>")
	assert.Contains(t, out, "<string>daemon</string>")
	assert.Contains(t, out, "<string>start</string>")
	assert.Contains(t, out, "<key>RunAtLoad</key>")
	assert.Contains(t, out, "<key>KeepAlive</key>")
	assert.Contains(t, out, "/Users/testuser/.cache/gortex/daemon.log")
	// Homebrew paths must be on PATH so launchd can find a Homebrew-
	// installed gortex binary even when the LaunchAgent env is minimal.
	assert.Contains(t, out, "/opt/homebrew/bin")
}

// TestSystemdUnitTemplate_Renders is the analog for Linux. Confirms
// the Type=simple + Restart=on-failure contract the install doc
// promises: simple means the daemon runs in the foreground (no
// --detach), on-failure means crashes restart but clean exits don't
// fight the user if they ran `daemon stop`.
func TestSystemdUnitTemplate_Renders(t *testing.T) {
	var buf bytes.Buffer
	tmpl := template.Must(template.New("unit").Parse(systemdUnitTemplate))
	require.NoError(t, tmpl.Execute(&buf, map[string]string{
		"Exe":     "/home/u/.local/bin/gortex",
		"LogPath": "/home/u/.cache/gortex/daemon.log",
	}))

	out := buf.String()
	assert.Contains(t, out, "ExecStart=/home/u/.local/bin/gortex daemon start")
	assert.Contains(t, out, "Type=simple")
	assert.Contains(t, out, "Restart=on-failure")
	assert.Contains(t, out, "StandardOutput=append:/home/u/.cache/gortex/daemon.log")
	assert.Contains(t, out, "WantedBy=default.target")
}

func TestLaunchdPlistPath_ResolvesUnderHome(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("launchd paths only meaningful on darwin")
	}
	path, err := launchdPlistPath()
	require.NoError(t, err)
	assert.Equal(t, "com.zzet.gortex.plist", filepath.Base(path))
	assert.Contains(t, path, filepath.Join("Library", "LaunchAgents"))
}

func TestSystemdUnitPath_ResolvesUnderHome(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("systemd paths only meaningful on linux")
	}
	path, err := systemdUnitPath()
	require.NoError(t, err)
	assert.Equal(t, "com.zzet.gortex.service", filepath.Base(path))
	assert.Contains(t, path, filepath.Join(".config", "systemd", "user"))
}

// TestServiceCommands_RejectUnsupportedOS keeps the guard
// runDaemonInstallService uses from silently succeeding on Windows or
// other platforms we haven't wired — Phase 4 explicitly excludes
// them for now.
func TestServiceCommands_RejectUnsupportedOS(t *testing.T) {
	if runtime.GOOS == "darwin" || runtime.GOOS == "linux" {
		t.Skip("this test only runs on unsupported platforms")
	}
	err := runDaemonInstallService(daemonInstallServiceCmd, nil)
	require.Error(t, err)
	assert.True(t, strings.Contains(err.Error(), "not supported"),
		"install must refuse on unsupported OS: %v", err)
}
