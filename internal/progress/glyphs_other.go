//go:build !windows

package progress

// windowsConsoleUTF8 is only consulted on Windows; supportsUnicode gates the
// call behind a GOOS check, so on every other platform this stub merely keeps
// the build green.
func windowsConsoleUTF8() bool { return true }
