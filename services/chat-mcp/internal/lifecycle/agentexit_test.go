package lifecycle

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func writePIDFile(t *testing.T, contents string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), PIDFileName)
	// Mode is the runtime's business (the runner has its own test for the
	// world-readable bit); the watcher only reads whatever is there.
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("write pid file: %v", err)
	}
	return path
}

// fastWatcher returns a watcher whose timings are test-sized; the real defaults
// are one second and two minutes, which no unit test should wait out.
func fastWatcher(pidFile string) *Watcher {
	return &Watcher{
		PIDFile:          pidFile,
		Interval:         5 * time.Millisecond,
		StartupGrace:     5 * time.Second,
		OwnsPIDNamespace: func() bool { return false },
	}
}

func TestWaitReturnsWhenRuntimeExits(t *testing.T) {
	w := fastWatcher(writePIDFile(t, "4242\n"))
	live := true
	w.Alive = func(pid int) bool {
		if pid != 4242 {
			t.Errorf("watched the wrong pid: %d", pid)
		}
		return live
	}

	done := make(chan Reason, 1)
	go func() {
		reason, err := w.Wait(context.Background())
		if err != nil {
			t.Errorf("Wait: %v", err)
		}
		done <- reason
	}()

	select {
	case r := <-done:
		t.Fatalf("ended before the runtime did (reason %q); a running task lost its chat tools", r)
	case <-time.After(50 * time.Millisecond):
	}

	live = false
	select {
	case r := <-done:
		if r != AgentExited {
			t.Fatalf("reason = %q, want %q", r, AgentExited)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Wait never returned after the runtime exited; the task pod would hang open")
	}
}

func TestWaitGivesUpWhenRuntimeNeverStarts(t *testing.T) {
	// The pid file never appears: the runtime image is broken, or its entrypoint
	// died before the extension loaded. Without this the sidecar serves forever
	// in a pod that will never do anything.
	w := fastWatcher(filepath.Join(t.TempDir(), PIDFileName))
	w.StartupGrace = 100 * time.Millisecond
	w.Alive = func(int) bool { return true }

	reason, err := w.Wait(context.Background())
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if reason != AgentNeverStarted {
		t.Fatalf("reason = %q, want %q", reason, AgentNeverStarted)
	}
}

func TestWaitStopsOnContextCancel(t *testing.T) {
	w := fastWatcher(writePIDFile(t, "7"))
	w.Alive = func(int) bool { return true }
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() {
		_, err := w.Wait(ctx)
		done <- err
	}()
	cancel()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("err = %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Wait ignored cancellation; the sidecar would ignore SIGTERM")
	}
}

func TestWaitRefusesWithoutSharedPIDNamespace(t *testing.T) {
	// The pid names a process in another container. In our own namespace it does
	// not exist, and "not alive" would end a healthy sidecar mid-task.
	w := fastWatcher(writePIDFile(t, "12"))
	w.OwnsPIDNamespace = func() bool { return true }
	w.Alive = func(int) bool { return false }

	_, err := w.Wait(context.Background())
	if err == nil {
		t.Fatal("watched a pid it cannot see; a running task would lose its chat sidecar")
	}
	if !strings.Contains(err.Error(), "shareProcessNamespace") {
		t.Fatalf("err = %v, want it to name the missing shareProcessNamespace", err)
	}
}

func TestWaitToleratesUnreadablePIDFile(t *testing.T) {
	// A malformed or momentarily unreadable file is not proof the runtime died,
	// so it must not end the pod early — only the grace deadline may.
	path := writePIDFile(t, "not-a-pid")
	w := fastWatcher(path)
	w.StartupGrace = 150 * time.Millisecond
	w.Alive = func(int) bool { return true }

	start := time.Now()
	reason, err := w.Wait(context.Background())
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if reason != AgentNeverStarted {
		t.Fatalf("reason = %q, want %q", reason, AgentNeverStarted)
	}
	if elapsed := time.Since(start); elapsed < 100*time.Millisecond {
		t.Fatalf("returned after %v; a bad read ended the pod instead of waiting out the grace", elapsed)
	}
}

func TestWaitWatchesThePIDAndNotTheFile(t *testing.T) {
	// The pid file is how the PID is handed over; the PID is what says whether the
	// runtime lives. If losing the file ended the watch, a sidecar could stop
	// watching a running task — or, worse, an unreadable file could leave it
	// watching nothing and holding the Job open for ever, which is the leak this
	// package exists to close.
	clobbered := []struct {
		name    string
		clobber func(t *testing.T, path string)
	}{
		{"removed", func(t *testing.T, path string) {
			if err := os.Remove(path); err != nil {
				t.Fatalf("remove: %v", err)
			}
		}},
		{"truncated", func(t *testing.T, path string) {
			if err := os.WriteFile(path, nil, 0o600); err != nil {
				t.Fatalf("truncate: %v", err)
			}
		}},
		{"unintelligible", func(t *testing.T, path string) {
			if err := os.WriteFile(path, []byte("not-a-pid"), 0o600); err != nil {
				t.Fatalf("clobber: %v", err)
			}
		}},
	}

	for _, tt := range clobbered {
		t.Run(tt.name, func(t *testing.T) {
			path := writePIDFile(t, "99")
			w := fastWatcher(path)
			live := true
			seen := make(chan struct{}, 1)
			w.Alive = func(pid int) bool {
				// Alive is only reached once a PID has been read, so its first call
				// proves the handoff happened.
				select {
				case seen <- struct{}{}:
				default:
				}
				return live
			}

			done := make(chan Reason, 1)
			go func() {
				reason, err := w.Wait(context.Background())
				if err != nil {
					t.Errorf("Wait: %v", err)
				}
				done <- reason
			}()

			select {
			case <-seen:
			case <-time.After(2 * time.Second):
				t.Fatal("watcher never took the pid")
			}

			tt.clobber(t, path)

			select {
			case r := <-done:
				t.Fatalf("ended with %q while the runtime was still alive; the pid file is not the signal", r)
			case <-time.After(100 * time.Millisecond):
			}

			live = false
			select {
			case r := <-done:
				if r != AgentExited {
					t.Fatalf("reason = %q, want %q", r, AgentExited)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("the runtime died and the watch is still open; this is the Job leak")
			}
		})
	}
}

func TestReadPID(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    int
		wantErr bool
	}{
		{"bare pid", "4242", 4242, false},
		{"trailing newline", "4242\n", 4242, false},
		{"trailing comment", "4242 # written by the runtime\n", 4242, false},
		{"empty", "", 0, true},
		{"whitespace only", "  \n", 0, true},
		{"not a number", "agent", 0, true},
		{"zero", "0", 0, true},
		{"negative", "-3", 0, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := writePIDFile(t, tt.content)
			got, err := readPID(path)
			if (err != nil) != tt.wantErr {
				t.Fatalf("readPID(%q) err = %v, wantErr %v", tt.content, err, tt.wantErr)
			}
			if got != tt.want {
				t.Fatalf("readPID(%q) = %d, want %d", tt.content, got, tt.want)
			}
		})
	}

	if _, err := readPID(filepath.Join(t.TempDir(), "missing")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("missing file: err = %v, want fs.ErrNotExist", err)
	}
}

func TestProcessAliveUnknownPIDIsNotAlive(t *testing.T) {
	// A pid that is not in this namespace must read as gone — that is the whole
	// signal. (Non-Linux dev machines have no /proc at all, which also reads
	// false, so this holds everywhere.)
	if ProcessAlive(2147483647) {
		t.Fatal("a pid that does not exist reported as alive")
	}
}

func TestProcessAliveSeesItself(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("liveness is read from /proc, which only Linux has")
	}
	if !ProcessAlive(os.Getpid()) {
		t.Fatalf("this test process (pid %d) read as dead", os.Getpid())
	}
}

func TestStatStateFieldSurvivesMessyCommandNames(t *testing.T) {
	tests := []struct {
		name   string
		stat   string
		want   byte
		wantOK bool
	}{
		{"plain", "1 (pi) S 0 1 1 0", 'S', true},
		{"spaces in command", "42 (node pi --mode rpc) S 0", 'S', true},
		{"closing paren inside command", "7 (weird (comm)) R 0", 'R', true},
		{"zombie", "9 (pi) Z 0 0", 'Z', true},
		{"no command field", "1 S 0", 0, false},
		{"nothing after command", "1 (pi)", 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := statStateField([]byte(tt.stat))
			if ok != tt.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tt.wantOK)
			}
			if got != tt.want {
				t.Fatalf("state = %q, want %q", got, tt.want)
			}
		})
	}
}
