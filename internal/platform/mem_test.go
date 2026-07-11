package platform

import (
	"runtime"
	"testing"
)

func TestHostPhysicalMemoryBytes(t *testing.T) {
	got := HostPhysicalMemoryBytes()

	// Reject an implausibly large figure (a malformed reader masquerading
	// as an exabyte of RAM) on every platform.
	const oneEiB = uint64(1) << 60
	if got > oneEiB {
		t.Fatalf("implausible host RAM: %d bytes", got)
	}

	// linux and darwin carry real readers, so a live host must report a
	// non-zero figure; other platforms deliberately return 0 (unknown).
	switch runtime.GOOS {
	case "linux", "darwin":
		if got == 0 {
			t.Fatalf("expected non-zero host RAM on %s", runtime.GOOS)
		}
	}
}
