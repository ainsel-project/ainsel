package api

import (
	"context"
	"log/slog"
	"fmt"
	"time"

	"sigs.k8s.io/controller-runtime/pkg/client"

	"github.com/ainsel-project/ainsel/services/hub/internal/invocations"
	agentv1alpha1 "github.com/ainsel-project/ainsel/shared/api/api/v1alpha1"
)

// The operator retires a task Job whose pod never starts (an image tag that does
// not exist, a container config the kubelet rejects) so the task cannot hold the
// agent's concurrency slot shut behind it — see
// operators/agent/internal/controller/taskstart.go. It records why on Agent
// status. This file is the other half of that: the task row is what the UI and
// the requester read, and a task whose pod never started is not "queued" — it is
// failed, with the operator's reason on it.
//
// Only blocks the operator still holds are reported. An expired cooldown means
// the operator may have dispatched the task again, and a task that is running is
// not this pass's to fail.

// taskStartFailureMessage renders the operator's stall record into the line the
// task row carries. It names the pod-level cause, which is what a reader needs
// to fix: the image, not the queue.
func taskStartFailureMessage(b agentv1alpha1.AgentTaskStartBlock) string {
	msg := "pod never started: " + b.Reason
	if b.Message != "" {
		msg += ": " + b.Message
	}
	return msg
}

// FailStalledTasks fails the pending tasks the operator gave up on because their
// pod never started, and reports each through the same path a failed task uses, so
// the invocation closes and the requester is told. Called from the hub's reaper
// tick alongside the pickup-deadline pass. Returns how many tasks were failed.
//
// A task that is no longer pending (acked, nacked into backoff, already terminal)
// is left alone by the store's guard: this pass explains a stuck task, it does not
// overrule the task store.
func (s *Server) FailStalledTasks(ctx context.Context) (int, error) {
	if s.eventQueue == nil || s.client == nil {
		return 0, nil
	}
	var agents agentv1alpha1.AgentList
	if err := s.client.List(ctx, &agents, client.InNamespace(s.ns)); err != nil {
		return 0, fmt.Errorf("listing agents for stalled tasks: %w", err)
	}

	now := time.Now()
	failed := 0
	for i := range agents.Items {
		for _, b := range agents.Items[i].Status.TaskStartBlocks {
			if !b.BlockedUntil.After(now) {
				continue // the operator may have dispatched it again
			}
			msg := taskStartFailureMessage(b)
			st, err := s.eventQueue.FailStalledTask(ctx, b.TaskID, msg)
			if err != nil {
				return failed, err
			}
			if st == nil {
				continue // not a pending row any more — nothing to report
			}
			slog.Warn("task failed: pod never started",
				"task_id", st.ID, "agent_name", st.AgentName, "reason", b.Reason)
			s.reportTaskFailure(st.AgentName, st.ID, st.InvocationID, msg, invocations.StatusTimeout)
			failed++
		}
	}
	return failed, nil
}
