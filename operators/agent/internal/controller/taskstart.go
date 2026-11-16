package controller

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	ainselv1alpha1 "github.com/ainsel-project/ainsel/shared/api/api/v1alpha1"
)

// A task Job holds one slot of the agent's concurrency cap from the moment it is
// created, and the slot only comes back when the Job finishes. A pod whose
// container never starts — an image tag that does not exist, a secret key the
// pod cannot resolve — never finishes: the kubelet retries the pull forever and
// the Job never gains a completion condition. On an agent with the default cap of
// one that single pod holds the queue shut behind it, indefinitely, with the task
// still reading QUEUED to anyone who asks (issue #342).
//
// This file decides when a pod is never going to start, retires the Job so the
// slot goes back to the queue, and records the reason on Agent status so the hub
// can put it on the task row instead of leaving the reader with "queued".

const (
	// taskStartErrorDeadline is how long a pod may sit in an explicit start
	// error before the operator calls it. The kubelet retries these forever —
	// a missing image tag is not a transient failure — so waiting longer buys
	// nothing, but a few minutes of retrying is fair to a registry that is
	// briefly unreachable.
	taskStartErrorDeadline = 5 * time.Minute

	// taskStartAbsoluteDeadline is the outer bound for a pod that reports no
	// error at all: a huge image that is genuinely still pulling, a slow
	// container create. Nothing here says the pod is doomed, so it gets the
	// long leash — but it does not get an unlimited one, because the cost of
	// being wrong is the whole queue behind it.
	taskStartAbsoluteDeadline = 30 * time.Minute

	// taskStartBlockCooldown is how long a stalled task is kept out of
	// dispatch after its Job is retired. Retrying it immediately is pointless
	// while the image is still broken, and the point of retiring the Job is to
	// let the tasks *behind* it run — which needs the doomed task to stay off
	// the slot, not to take it back a second later.
	taskStartBlockCooldown = 30 * time.Minute

	// taskStartBlockKeep is how long a block record outlives its cooldown. The
	// hub reads the record to put the reason on the task row; dropping it the
	// second the cooldown ends would take the explanation away before the hub's
	// five-minute pass has had a chance to see it.
	taskStartBlockKeep = 2 * taskStartBlockCooldown

	// taskStartBlockCap bounds the record. It is status, not a task log: the
	// tasks that matter are the ones still blocked, and the list is written on
	// every reconcile that touches it.
	taskStartBlockCap = 20
)

// taskStartReasonNeverStarted is the code for a pod that never got a container
// running without ever reporting a fatal waiting reason — the absolute deadline,
// not an error the kubelet named.
const taskStartReasonNeverStarted = "PodNeverStarted"

// fatalStartReasons are the waiting reasons that mean "waiting longer will not
// help". Everything the kubelet can name here is a property of the image or the
// pod spec, not of the cluster's mood: the same pull fails on the next attempt,
// and a container-config error repeats on every one. Reasons that are not on
// this list (Pulling, ContainerCreating) are progress, not failure, and only the
// absolute deadline applies to them.
var fatalStartReasons = map[string]bool{
	"ErrImagePull":               true,
	"ImagePullBackOff":           true,
	"InvalidImageName":           true,
	"ErrImageNeverPull":          true,
	"RegistryUnavailable":        true,
	"CreateContainerConfigError": true,
	"CreateContainerError":       true,
	"RunContainerError":          true,
	"InvalidRegistryImage":       true,
}

// startStall is the verdict on one task Job: this pod is never starting, and
// this is why. Reason is a stable code (a kubelet waiting reason, or
// PodNeverStarted); Message is the kubelet's own line, safe to show a human.
type startStall struct {
	Reason  string
	Message string
}

// String renders the stall for a task row or a log line.
func (s startStall) String() string {
	if s.Message == "" {
		return s.Reason
	}
	return s.Reason + ": " + s.Message
}

// classifyTaskPodStart decides whether this pod is ever going to start.
//
// The clock is the pod's own creation, not the Job's: a Job retry starts a fresh
// pod, and the new pod deserves its own deadline. Two answers, in order of
// confidence — an explicit error reason that has outlasted
// taskStartErrorDeadline, or any pod that has outlasted taskStartAbsoluteDeadline
// without a container running. A pod that is Running is not stalled: the task
// started, and what happens to it from here is the Job's own story.
func classifyTaskPodStart(pod *corev1.Pod, now time.Time) (startStall, bool) {
	if pod.Status.Phase != corev1.PodPending {
		// Running, Succeeded, Failed, or not scheduled yet with no status: this
		// is not the "never started" case. A pod that ran and died is a Job
		// retry, and a Job that ran out of retries is finished.
		return startStall{}, false
	}

	age := now.Sub(pod.CreationTimestamp.Time)
	if age < 0 {
		age = 0
	}

	var waiting []corev1.ContainerStateWaiting
	for _, cs := range pod.Status.InitContainerStatuses {
		if cs.State.Waiting != nil {
			waiting = append(waiting, *cs.State.Waiting)
		}
	}
	for _, cs := range pod.Status.ContainerStatuses {
		if cs.State.Waiting != nil {
			waiting = append(waiting, *cs.State.Waiting)
		}
	}

	// The named error wins over the deadline: it says what is wrong, and it is
	// the reason the pod will still have on the next attempt.
	for _, w := range waiting {
		if fatalStartReasons[w.Reason] && age >= taskStartErrorDeadline {
			return startStall{Reason: w.Reason, Message: w.Message}, true
		}
	}

	if age >= taskStartAbsoluteDeadline {
		// No fatal reason — either nothing is reported yet or something slow is
		// in progress. Say what was last observed without claiming it is fatal.
		msg := "no container started"
		if len(waiting) > 0 {
			msg = fmt.Sprintf("last waiting reason %s: %s", waiting[0].Reason, waiting[0].Message)
		}
		return startStall{Reason: taskStartReasonNeverStarted, Message: strings.TrimSpace(msg)}, true
	}

	return startStall{}, false
}

// taskStartStall lists one Job's pods and reports whether the task's pod is
// never going to start, and — when it is not stalled yet — how long until its
// start deadline expires.
//
// The second answer is what makes the first one reliable: a pod sitting in
// ImagePullBackOff generates no events, so a reconcile that only looks once would
// never notice the deadline passing. The caller requeues for it.
//
// The newest pod decides: a Job retry replaces the pod, and an old attempt's
// failure is not the current attempt's verdict. A listing failure is reported as
// "not stalled" with no deadline — a broken pods RBAC or API read must not retire
// healthy Jobs; the caller's next pass tries again.
func (r *AgentReconciler) taskStartStall(ctx context.Context, j *batchv1.Job, now time.Time) (startStall, bool, time.Duration) {
	var pods corev1.PodList
	if err := r.List(ctx, &pods,
		client.InNamespace(j.Namespace),
		client.MatchingLabels{batchv1.JobNameLabel: j.Name},
	); err != nil {
		logf.FromContext(ctx).Error(err, "listing pods of task job", "job", j.Name)
		return startStall{}, false, 0
	}

	var newest *corev1.Pod
	for i := range pods.Items {
		p := &pods.Items[i]
		if newest == nil || p.CreationTimestamp.After(newest.CreationTimestamp.Time) {
			newest = p
		}
	}
	if newest == nil {
		// No pod yet: the Job controller has not created one, or the cache has not
		// caught up. Nothing to judge, and nothing to wait on — the pod's own
		// creation re-triggers nothing here, so the next dispatch poll looks again.
		return startStall{}, false, 0
	}
	stall, stalled := classifyTaskPodStart(newest, now)
	return stall, stalled, taskStartDeadline(newest, now)
}

// taskStartDeadline is how long this pod has left before the start check should
// be made again: the error deadline if it already reports a fatal reason, the
// absolute deadline otherwise. Zero for a pod that is not pending — nothing here
// is waiting on it.
func taskStartDeadline(pod *corev1.Pod, now time.Time) time.Duration {
	if pod.Status.Phase != corev1.PodPending {
		return 0
	}
	deadline := pod.CreationTimestamp.Add(taskStartAbsoluteDeadline)
	for _, cs := range append(append([]corev1.ContainerStatus{},
		pod.Status.InitContainerStatuses...), pod.Status.ContainerStatuses...) {
		if cs.State.Waiting != nil && fatalStartReasons[cs.State.Waiting.Reason] {
			if d := pod.CreationTimestamp.Add(taskStartErrorDeadline); d.Before(deadline) {
				deadline = d
			}
		}
	}
	if remaining := deadline.Sub(now); remaining > 0 {
		return remaining
	}
	return 0
}

// noteEarlier keeps the sooner of two requeue intervals, where zero means "no
// request yet" — so the first non-zero one wins.
func noteEarlier(cur *time.Duration, next time.Duration) {
	if next > 0 && (*cur == 0 || next < *cur) {
		*cur = next
	}
}

// retireStalledJob deletes a Job whose pod is never starting, so its slot goes
// back to the queue, and says so on the Agent (Event + log) because the Job is
// the only place the failure was visible and it is about to be gone.
//
// The delete cascades in the background for the same reason the re-dispatch
// retire path does: a Job delete with no propagation policy defaults to Orphan,
// which would leave the stuck pod behind with nothing owning it — and a pod that
// never started is exactly the pod nobody can clean up afterwards.
func (r *AgentReconciler) retireStalledJob(ctx context.Context, agent *ainselv1alpha1.Agent, taskID int64, j *batchv1.Job, s startStall) error {
	if err := r.Delete(ctx, j, client.PropagationPolicy(metav1.DeletePropagationBackground)); err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("retiring task job %s whose pod never started: %w", j.Name, err)
	}
	logf.FromContext(ctx).Info("retired task job whose pod never started",
		"agent", agent.Name, "task", taskID, "job", j.Name,
		"reason", s.Reason, "message", s.Message,
		"blockedFor", taskStartBlockCooldown)
	if r.Recorder != nil {
		r.Recorder.Eventf(agent, corev1.EventTypeWarning, "TaskPodNeverStarted",
			"task %d: pod never started (%s); job retired, the task is held back for %s",
			taskID, s.String(), taskStartBlockCooldown)
	}
	return nil
}

// taskStartBlock returns the operator's live block on a task, or nil when the
// task may be dispatched. A block whose cooldown has elapsed stops holding the
// task: the image may have been fixed by now, and a task must not be parked
// forever by a status record nobody refreshed.
func taskStartBlock(agent *ainselv1alpha1.Agent, taskID int64, now time.Time) *ainselv1alpha1.AgentTaskStartBlock {
	for i := range agent.Status.TaskStartBlocks {
		b := &agent.Status.TaskStartBlocks[i]
		if b.TaskID != taskID {
			continue
		}
		if !b.BlockedUntil.After(now) {
			return nil
		}
		return b
	}
	return nil
}

// recordTaskStartBlock notes that a task's pod never started, and when the
// operator will let it have a slot again. Re-recording a task that stalled again
// after its cooldown extends the block rather than stacking a second entry for
// it — the record is per task, and the newest reason is the true one.
func recordTaskStartBlock(agent *ainselv1alpha1.Agent, taskID int64, s startStall, now time.Time) {
	blockedUntil := metav1.NewTime(now.Add(taskStartBlockCooldown))
	for i := range agent.Status.TaskStartBlocks {
		b := &agent.Status.TaskStartBlocks[i]
		if b.TaskID != taskID {
			continue
		}
		b.Reason, b.Message = s.Reason, s.Message
		b.BlockedAt = metav1.NewTime(now)
		b.BlockedUntil = blockedUntil
		return
	}
	agent.Status.TaskStartBlocks = append(agent.Status.TaskStartBlocks,
		ainselv1alpha1.AgentTaskStartBlock{
			TaskID:       taskID,
			Reason:       s.Reason,
			Message:      s.Message,
			BlockedAt:    metav1.NewTime(now),
			BlockedUntil: blockedUntil,
		})
}

// pruneTaskStartBlocks drops records whose block ended long enough ago that the
// hub has certainly read them. Keeps the list — and the status patch that
// carries it — from growing with every task that ever had a bad image.
func pruneTaskStartBlocks(agent *ainselv1alpha1.Agent, now time.Time) {
	keep := agent.Status.TaskStartBlocks[:0]
	for _, b := range agent.Status.TaskStartBlocks {
		if b.BlockedUntil.Add(taskStartBlockKeep).After(now) {
			keep = append(keep, b)
		}
	}
	agent.Status.TaskStartBlocks = keep
	if len(keep) <= taskStartBlockCap {
		return
	}
	// Over the cap, the oldest blocks go: they are the ones the hub has most
	// likely already reported, and the newest are the ones still blocking work.
	sort.SliceStable(agent.Status.TaskStartBlocks, func(i, j int) bool {
		return agent.Status.TaskStartBlocks[i].BlockedAt.Before(&agent.Status.TaskStartBlocks[j].BlockedAt)
	})
	agent.Status.TaskStartBlocks = agent.Status.TaskStartBlocks[len(agent.Status.TaskStartBlocks)-taskStartBlockCap:]
}

// nextTaskStartUnblock is how soon the earliest block on this agent expires, for
// the requeue that lets the task have a slot again. Zero means nothing is
// blocked, so no requeue is owed for it.
func nextTaskStartUnblock(agent *ainselv1alpha1.Agent, now time.Time) time.Duration {
	var next time.Duration
	for _, b := range agent.Status.TaskStartBlocks {
		if d := b.BlockedUntil.Sub(now); d > 0 && (next == 0 || d < next) {
			next = d
		}
	}
	return next
}
