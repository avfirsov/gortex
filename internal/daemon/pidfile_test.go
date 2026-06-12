package daemon

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

// TestRunningPID covers the four states RunningPID must distinguish: no PID
// file, a live owner, a stale owner (process gone), and a corrupt file. The
// stale case is the load-bearing one — misreading a crashed daemon's leftover
// PID file as "running" would block every subsequent start.
func TestRunningPID(t *testing.T) {
	pidPath := filepath.Join(t.TempDir(), "daemon.pid")
	t.Setenv("GORTEX_DAEMON_PIDFILE", pidPath)

	t.Run("no pid file", func(t *testing.T) {
		if pid, ok := RunningPID(); ok {
			t.Fatalf("want (0,false), got (%d,%v)", pid, ok)
		}
	})

	t.Run("live owner", func(t *testing.T) {
		writePID(t, pidPath, os.Getpid())
		pid, ok := RunningPID()
		if !ok || pid != os.Getpid() {
			t.Fatalf("want (%d,true), got (%d,%v)", os.Getpid(), pid, ok)
		}
	})

	t.Run("live owner with trailing newline", func(t *testing.T) {
		// A pidfile written by `echo`/a process manager ends in "\n". The
		// guard must still detect the live owner — otherwise a restart
		// silently races the store lock again.
		if err := os.WriteFile(pidPath, []byte(strconv.Itoa(os.Getpid())+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if pid, ok := RunningPID(); !ok || pid != os.Getpid() {
			t.Fatalf("want (%d,true), got (%d,%v)", os.Getpid(), pid, ok)
		}
	})

	t.Run("stale owner", func(t *testing.T) {
		// A PID well above any platform's pid_max — guaranteed not live.
		writePID(t, pidPath, 1<<30)
		if pid, ok := RunningPID(); ok {
			t.Fatalf("stale pid must read as not running, got (%d,%v)", pid, ok)
		}
	})

	t.Run("corrupt file", func(t *testing.T) {
		if err := os.WriteFile(pidPath, []byte("not-a-pid"), 0o600); err != nil {
			t.Fatal(err)
		}
		if pid, ok := RunningPID(); ok {
			t.Fatalf("corrupt pid file must read as not running, got (%d,%v)", pid, ok)
		}
	})
}

func writePID(t *testing.T, path string, pid int) {
	t.Helper()
	if err := os.WriteFile(path, []byte(strconv.Itoa(pid)), 0o600); err != nil {
		t.Fatal(err)
	}
}
