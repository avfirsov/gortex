//go:build windows

package progress

import "syscall"

// windowsConsoleUTF8 reports whether the active Windows console output codepage
// is UTF-8 (65001). Legacy OEM codepages (437, 850, 866, …) — still the cmd.exe
// default — cannot render the box-drawing / check glyphs, so the caller falls
// back to ASCII. Uses the kernel32 GetConsoleOutputCP syscall directly to avoid
// pulling in golang.org/x/sys.
func windowsConsoleUTF8() bool {
	const cpUTF8 = 65001
	proc := syscall.NewLazyDLL("kernel32.dll").NewProc("GetConsoleOutputCP")
	cp, _, _ := proc.Call()
	return uint32(cp) == cpUTF8
}
