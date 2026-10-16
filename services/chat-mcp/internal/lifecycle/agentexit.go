// Package lifecycle ends a task pod's sidecars together with the task they
// serve.
//
// A per-task pod exists to run one process: the agent runtime. A sidecar is a
// long-lived HTTP server, so it would happily outlive that process — and while
// it does, the pod stays Running and the Job that owns it never completes. The
// operator therefore gives task pods a shared PID namespace and a shared
// directory, and the runtime announces itself there by writing its PID to
// <AINSEL_TASK_DIR>/agent.pid. This package turns that announcement into the
// signal a sidecar needs: wait for the runtime's PID to disappear, then stand
// down so the pod can finish as a unit.
package lifecycle

import (
	"context"
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// PIDFileName is the file the runtime writes its PID into, relative to the
// task run directory the operator mounts for every container of a task pod.
const PIDFileName = "agent.pid"

// Reason explains why Watcher.Wait returned, so the caller can log it and pick
// an exit code.
type Reason string

const (
	// AgentExited is the normal end: the runtime resolved its task (ack or nack)
	// and exited, so there is nothing left for this sidecar to serve.
	AgentExited Reason = "agent-exited"

	// AgentNeverStarted means the runtime never announced itself within the
	// startup grace. The pod is not doing anything useful, so the caller should
	// fail loudly instead of holding the Job open forever.
	AgentNeverStarted Reason = "agent-never-started"
)

// Watcher reports when the runtime process of this pod is gone.
type Watcher struct {
	// PIDFile is the absolute path of the runtime's pid file, normally
	// filepath.Join(os.Getenv("AINSEL_TASK_DIR"), PIDFileName).
	PIDFile string

	// Interval between polls. Defaults to one second: fast enough that the pod
	// ends promptly, cheap enough to be invisible next to an LLM turn.
	Interval time.Duration

	// StartupGrace is how long to wait for the pid file to appear at all. It
	// covers a slow container start; past it, the runtime is treated as failed
	// to start. Defaults to two minutes.
	StartupGrace time.Duration

	// Alive reports whether a PID is a live, non-zombie process. Defaults to
	// reading /proc. Injected in tests.
	Alive func(pid int) bool

	// OwnsPIDNamespace reports whether this container is PID 1, i.e. the pod is
	// NOT sharing a PID namespace, so another container's PID is meaningless
	// here. Defaults to the real check. Injected in tests.
	OwnsPIDNamespace func() bool

	// Log receives the state changes. May be nil.
	Log *slog.Logger
}

// Wait blocks until the runtime process this pod existed to run is gone. It
// returns AgentExited in that case, AgentNeverStarted if the runtime never
// wrote its pid file within StartupGrace, or ctx.Err() if the caller is
// shutting down for another reason.
//
// The pid file is the handoff, not the signal. Once a PID is in hand, only that
// PID's liveness ends the watch: a file that goes missing or unreadable
// afterwards must not be what decides, or the sidecar ends up watching nothing
// and holding the Job open — the leak this package exists to close.
func (w *Watcher) Wait(ctx context.Context) (Reason, error) {
	interval := w.Interval
	if interval <= 0 {
		interval = time.Second
	}
	grace := w.StartupGrace
	if grace <= 0 {
		grace = 2 * time.Minute
	}
	alive := w.Alive
	if alive == nil {
		alive = ProcessAlive
	}
	ownsPIDNamespace := w.OwnsPIDNamespace
	if ownsPIDNamespace == nil {
		ownsPIDNamespace = func() bool { return os.Getpid() == 1 }
	}

	// Without a shared PID namespace the PID in the file names a process in this
	// container's own namespace, where it does not exist — watching it would end
	// a healthy sidecar the moment the file appeared. Refuse rather than lie.
	if ownsPIDNamespace() {
		return "", errors.New("AINSEL_TASK_DIR is set but this container owns its PID namespace; the pod needs shareProcessNamespace")
	}

	deadline := time.Now().Add(grace)
	var pid int // the runtime's PID once handed over; 0 until then
	var warned bool
	log := w.Log

	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-t.C:
		}

		// The file is only how the PID gets handed over; the PID is what says
		// whether the runtime lives. Once it is in hand, a file that later goes
		// missing or unreadable must not be the thing that decides — watching
		// nothing is exactly how a sidecar comes to hold a Job open forever.
		if next, err := readPID(w.PIDFile); err == nil {
			if pid == 0 && log != nil {
				log.Info("watching runtime process", "pid", next, "path", w.PIDFile)
			}
			pid = next
		} else {
			if pid == 0 {
				// Nothing to watch yet — the runtime has either not announced itself
				// or wrote something unintelligible. Neither is evidence of death, so
				// the startup deadline is what decides.
				if !errors.Is(err, fs.ErrNotExist) && log != nil {
					log.Warn("cannot read runtime pid file", "path", w.PIDFile, "err", err)
				}
				if time.Now().After(deadline) {
					return AgentNeverStarted, nil
				}
				continue
			}
			if !errors.Is(err, fs.ErrNotExist) && !warned && log != nil {
				log.Warn("runtime pid file unreadable; watching the pid already in hand",
					"path", w.PIDFile, "pid", pid, "err", err)
				warned = true
			}
		}

		if !alive(pid) {
			return AgentExited, nil
		}
	}
}

// readPID parses the runtime's pid file. The runtime writes a bare decimal PID;
// trailing whitespace and a trailing comment are tolerated.
func readPID(path string) (int, error) {
	raw, err := os.ReadFile(path) // #nosec G304 -- <AINSEL_TASK_DIR>/agent.pid, a path the operator fixes, not caller input
	if err != nil {
		return 0, err
	}
	field := strings.Fields(string(raw))
	if len(field) == 0 {
		return 0, &fs.PathError{Op: "read", Path: path, Err: errors.New("empty pid file")}
	}
	pid, err := strconv.Atoi(field[0])
	if err != nil {
		return 0, &fs.PathError{Op: "parse", Path: path, Err: err}
	}
	if pid <= 0 {
		return 0, &fs.PathError{Op: "parse", Path: path, Err: errors.New("pid must be positive")}
	}
	return pid, nil
}

// ProcessAlive reports whether pid names a live, non-zombie process in this PID
// namespace. A task runtime that has exited is reaped by the pod's PID 1, but
// can briefly linger as a zombie, which must count as gone.
func ProcessAlive(pid int) bool {
	stat, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "stat"))
	if err != nil {
		return false
	}
	state, ok := statStateField(stat)
	if !ok {
		return true // unparseable, but the process is clearly there
	}
	return state != 'Z'
}

// statStateField picks the process state out of a /proc/<pid>/stat line. Field 2
// is "(comm)" — the command name, which may itself contain spaces and ')', so
// the fields after it are counted from the last ')' rather than by splitting.
func statStateField(stat []byte) (byte, bool) {
	tail := strings.LastIndexByte(string(stat), ')')
	if tail < 0 {
		return 0, false
	}
	rest := strings.TrimLeft(string(stat[tail+1:]), " ")
	if rest == "" {
		return 0, false
	}
	return rest[0], true
}
