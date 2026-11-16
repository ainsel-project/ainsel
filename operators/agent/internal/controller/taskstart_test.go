package controller

import (
	"context"
	"testing"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	ainselv1alpha1 "github.com/ainsel-project/ainsel/shared/api/api/v1alpha1"
)

// The never-started case (issue #342): a task Job whose pod cannot start holds
// its agent's concurrency slot forever, and every task queued behind it waits
// with it. These cover the two halves of the answer — deciding that a pod is
// never starting, and turning that into a retired Job plus a held-back task so
// the queue behind it moves.

// stalledPod builds the pod of a task Job that has been sitting in one waiting
// reason for the given age.
func stalledPod(jobName string, age time.Duration, reason, message string) *corev1.Pod {
	return stalledPodAt(jobName, time.Now(), age, reason, message)
}

// stalledPodAt is stalledPod with the clock pinned, so a classification test can
// be deterministic about what "10 minutes old" means.
func stalledPodAt(jobName string, now time.Time, age time.Duration, reason, message string) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:              jobName + "-abcde",
			Namespace:         "ns",
			Labels:            map[string]string{batchv1.JobNameLabel: jobName},
			CreationTimestamp: metav1.NewTime(now.Add(-age)),
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodPending,
			ContainerStatuses: []corev1.ContainerStatus{
				{Name: "agent", State: corev1.ContainerState{
					Waiting: &corev1.ContainerStateWaiting{Reason: reason, Message: message},
				}},
			},
		},
	}
}

func TestClassifyTaskPodStart(t *testing.T) {
	now := time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)

	cases := []struct {
		name       string
		pod        *corev1.Pod
		wantStall  bool
		wantReason string
	}{
		{
			// The case from ainsel-dev: an untagged image resolved to a :latest
			// that does not exist. Fatal, and fatal quickly.
			name:      "image pull backoff past the error deadline",
			pod:       stalledPodAt("task-a-1", now, 10*time.Minute, "ImagePullBackOff", "Back-off pulling image"),
			wantStall: true, wantReason: "ImagePullBackOff",
		},
		{
			name: "image pull backoff inside the error deadline",
			pod:  stalledPodAt("task-a-1", now, 2*time.Minute, "ImagePullBackOff", "Back-off pulling image"),
		},
		{
			// A big image that is genuinely still downloading is not a failure.
			name: "still pulling past the error deadline",
			pod:  stalledPodAt("task-a-1", now, 10*time.Minute, "Pulling", "Pulling image"),
		},
		{
			name:      "still pulling past the absolute deadline",
			pod:       stalledPodAt("task-a-1", now, 40*time.Minute, "Pulling", "Pulling image"),
			wantStall: true, wantReason: taskStartReasonNeverStarted,
		},
		{
			name:      "container config error",
			pod:       stalledPodAt("task-a-1", now, 6*time.Minute, "CreateContainerConfigError", "secret \"x\" not found"),
			wantStall: true, wantReason: "CreateContainerConfigError",
		},
		{
			name:      "bad image name",
			pod:       stalledPodAt("task-a-1", now, 6*time.Minute, "InvalidImageName", "invalid reference format"),
			wantStall: true, wantReason: "InvalidImageName",
		},
		{
			// A pod that ran is not a start failure: the Job's own conditions
			// decide what a dead attempt means.
			name: "running pod",
			pod: func() *corev1.Pod {
				p := stalledPodAt("task-a-1", now, 9*time.Hour, "ImagePullBackOff", "")
				p.Status.Phase = corev1.PodRunning
				return p
			}(),
		},
		{
			// Scheduled but nothing reported yet: only the long leash applies.
			name:      "no container status yet, past the absolute deadline",
			pod:       &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "p", Namespace: "ns", CreationTimestamp: metav1.NewTime(now.Add(-35 * time.Minute))}, Status: corev1.PodStatus{Phase: corev1.PodPending}},
			wantStall: true, wantReason: taskStartReasonNeverStarted,
		},
		{
			name: "no container status yet, inside the absolute deadline",
			pod:  &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "p", Namespace: "ns", CreationTimestamp: metav1.NewTime(now.Add(-4 * time.Minute))}, Status: corev1.PodStatus{Phase: corev1.PodPending}},
		},
		{
			// The runtime container is not the only one that can fail to start:
			// an init container that cannot pull blocks the task just the same.
			name: "fatal reason on an init container",
			pod: func() *corev1.Pod {
				p := stalledPodAt("task-a-1", now, 8*time.Minute, "Running", "")
				p.Status.InitContainerStatuses = []corev1.ContainerStatus{
					{Name: "setup", State: corev1.ContainerState{
						Waiting: &corev1.ContainerStateWaiting{Reason: "ErrImagePull", Message: "not found"},
					}},
				}
				return p
			}(),
			wantStall: true, wantReason: "ErrImagePull",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			stall, stalled := classifyTaskPodStart(tc.pod, now)
			if stalled != tc.wantStall {
				t.Fatalf("stalled = %v (%+v), want %v", stalled, stall, tc.wantStall)
			}
			if stalled && stall.Reason != tc.wantReason {
				t.Errorf("reason = %q, want %q", stall.Reason, tc.wantReason)
			}
		})
	}
}

// The blockage itself: cap 1, task 1's pod is in ImagePullBackOff, task 2 waits.
// Before the fix task 2 never ran; now the doomed Job is retired, its slot goes to
// task 2, and task 1 is held out of dispatch instead of taking the slot back.
func TestSyncPerTaskJobs_RetiresNeverStartedJobAndFreesTheSlot(t *testing.T) {
	agent := jobModeAgent(1, 1, 2)
	doomed := existingJob(t, agent, 1, false)
	pod := stalledPod(doomed.Name, 10*time.Minute, "ImagePullBackOff",
		`Back-off pulling image "dpinsel/ainsel-pi-maui:latest"`)

	decision, c := runSync(t, agent, doomed, pod)

	var live batchv1.JobList
	if err := c.List(context.Background(), &live, client.InNamespace("ns")); err != nil {
		t.Fatalf("list jobs: %v", err)
	}
	if len(live.Items) != 1 || live.Items[0].Labels[TaskIDLabel] != "2" {
		t.Fatalf("live jobs = %s, want only the fresh attempt for task 2", jobLabelIDs(live.Items))
	}
	if decision.Replicas != 1 {
		t.Errorf("decision.Replicas = %d, want 1 (task 2, not the retired pod)", decision.Replicas)
	}
	b := taskStartBlock(agent, 1, time.Now())
	if b == nil {
		t.Fatal("task 1 is not blocked; it would be dispatched into the same broken image")
	}
	if b.Reason != "ImagePullBackOff" {
		t.Errorf("block reason = %q, want ImagePullBackOff", b.Reason)
	}
	if b.Message == "" {
		t.Error("block carries no message; the UI would have nothing to show")
	}
	if !b.BlockedUntil.After(time.Now()) {
		t.Errorf("block expires at %v, want a cooldown still running", b.BlockedUntil)
	}
	if decision.RequeueAfter <= 0 {
		t.Errorf("requeue = %v, want a pass when the cooldown ends", decision.RequeueAfter)
	}
}

// The nine-day variant: the task left status.pendingTaskIds (so nothing below
// the dispatch loop could ever retire it) but its Job is still unfinished. It
// must not hold the slot either.
func TestSyncPerTaskJobs_RetiresNeverStartedJobOfATaskNoLongerDue(t *testing.T) {
	agent := jobModeAgent(1, 2)
	orphan := existingJob(t, agent, 9, false)
	pod := stalledPod(orphan.Name, 45*time.Minute, "ImagePullBackOff", "pull failure")

	decision, c := runSync(t, agent, orphan, pod)

	var live batchv1.JobList
	if err := c.List(context.Background(), &live, client.InNamespace("ns")); err != nil {
		t.Fatalf("list jobs: %v", err)
	}
	if len(live.Items) != 1 || live.Items[0].Labels[TaskIDLabel] != "2" {
		t.Fatalf("live jobs = %s, want the orphan gone and task 2 dispatched", jobLabelIDs(live.Items))
	}
	if decision.Replicas != 1 {
		t.Errorf("decision.Replicas = %d, want 1", decision.Replicas)
	}
	// The reason is recorded for the orphan too: the hub still has the row, and
	// if the published due list was only lagging, the task comes back to an
	// operator that already knows the image is broken.
	if b := taskStartBlock(agent, 9, time.Now()); b == nil {
		t.Error("the stalled orphan recorded no block; a returning task would re-dispatch into the same broken image")
	}
}

// A healthy pod — same Pending phase, but reporting progress rather than an
// error — must not be retired: a slow image pull is not a stalled task.
func TestSyncPerTaskJobs_KeepsJobWhosePodIsStillPulling(t *testing.T) {
	agent := jobModeAgent(1, 1)
	live := existingJob(t, agent, 1, false)
	pod := stalledPod(live.Name, 10*time.Minute, "Pulling", "Pulling image")

	decision, c := runSync(t, agent, live, pod)

	var jobs batchv1.JobList
	if err := c.List(context.Background(), &jobs, client.InNamespace("ns")); err != nil {
		t.Fatalf("list jobs: %v", err)
	}
	if len(jobs.Items) != 1 || jobs.Items[0].Name != live.Name {
		t.Fatalf("jobs = %s, want the still-pulling job left alone", jobLabelIDs(jobs.Items))
	}
	if decision.Replicas != 1 {
		t.Errorf("decision.Replicas = %d, want the pulling job still counted active", decision.Replicas)
	}
	if taskStartBlock(agent, 1, time.Now()) != nil {
		t.Error("a pulling pod is not a stalled task")
	}
}

// Once the cooldown has elapsed the task gets its slot back — the image may have
// been fixed, and a task must not be parked by a stale status record.
func TestTaskStartBlock_ExpiresAndLetsTheTaskThrough(t *testing.T) {
	agent := jobModeAgent(1, 1)
	now := time.Now()
	recordTaskStartBlock(agent, 1, startStall{Reason: "ImagePullBackOff"}, now.Add(-time.Hour))

	if b := taskStartBlock(agent, 1, now); b != nil {
		t.Fatalf("expired block still holds the task: %+v", b)
	}
	decision, c := runSync(t, agent)
	var jobs batchv1.JobList
	if err := c.List(context.Background(), &jobs, client.InNamespace("ns")); err != nil {
		t.Fatalf("list jobs: %v", err)
	}
	if len(jobs.Items) != 1 {
		t.Fatalf("jobs = %s, want task 1 dispatched again once its block expired", jobLabelIDs(jobs.Items))
	}
	// The expired block is gone from the list only after its retention window
	// (the hub reads it to put the reason on the task row); what matters here is
	// that it no longer holds the task.
	if taskStartBlock(agent, 1, time.Now()) != nil {
		t.Error("expired block still holds the task")
	}
	if decision.Replicas != 1 {
		t.Errorf("decision.Replicas = %d, want 1", decision.Replicas)
	}
}

// Re-stalling after a cooldown refreshes the one record rather than stacking a
// second one for the same task.
func TestRecordTaskStartBlock_RefreshesInPlace(t *testing.T) {
	agent := jobModeAgent(1, 1)
	now := time.Now()
	recordTaskStartBlock(agent, 7, startStall{Reason: "ImagePullBackOff", Message: "first"}, now)
	recordTaskStartBlock(agent, 7, startStall{Reason: "ErrImagePull", Message: "second"}, now.Add(time.Minute))

	if len(agent.Status.TaskStartBlocks) != 1 {
		t.Fatalf("%d records, want one per task: %+v", len(agent.Status.TaskStartBlocks), agent.Status.TaskStartBlocks)
	}
	b := agent.Status.TaskStartBlocks[0]
	if b.Reason != "ErrImagePull" || b.Message != "second" {
		t.Errorf("record = %+v, want the newest reason", b)
	}
}

// The record list is status, not a log: it stays bounded and the oldest entries
// go first.
func TestPruneTaskStartBlocks_StaysBounded(t *testing.T) {
	agent := jobModeAgent(1)
	now := time.Now()
	for i := int64(0); i < taskStartBlockCap+5; i++ {
		agent.Status.TaskStartBlocks = append(agent.Status.TaskStartBlocks,
			ainselv1alpha1.AgentTaskStartBlock{
				TaskID:       i,
				Reason:       "ImagePullBackOff",
				BlockedAt:    metav1.NewTime(now.Add(-time.Duration(i) * time.Hour)),
				BlockedUntil: metav1.NewTime(now.Add(time.Hour)),
			})
	}
	pruneTaskStartBlocks(agent, now)

	if len(agent.Status.TaskStartBlocks) != taskStartBlockCap {
		t.Fatalf("%d records, want the cap of %d", len(agent.Status.TaskStartBlocks), taskStartBlockCap)
	}
	for _, b := range agent.Status.TaskStartBlocks {
		if b.TaskID >= taskStartBlockCap {
			t.Errorf("oldest block %d survived; the newest are the ones that matter", b.TaskID)
		}
	}
}

// jobLabelIDs names the jobs by the task they own, for a failure message that
// says what the cluster ended up with.
// A pod that is stuck but not yet past its deadline must still schedule the pass
// that notices: a pod in ImagePullBackOff generates no events, so a reconcile that
// looks once and returns would leave the slot held until something unrelated woke
// the agent.
func TestSyncPerTaskJobs_RequeuesForTheStartDeadline(t *testing.T) {
	agent := jobModeAgent(1, 1)
	live := existingJob(t, agent, 1, false)
	pod := stalledPod(live.Name, 2*time.Minute, "ImagePullBackOff", "pull failure")

	decision, _ := runSync(t, agent, live, pod)

	if decision.RequeueAfter <= 0 || decision.RequeueAfter > 3*time.Minute {
		t.Errorf("requeue = %v, want the pass that reaches the 5-minute deadline (pod is 2m old)", decision.RequeueAfter)
	}
}

func jobLabelIDs(jobs []batchv1.Job) string {
	s := ""
	for _, j := range jobs {
		if s != "" {
			s += ", "
		}
		s += j.Labels[TaskIDLabel] + "/" + j.Name
	}
	if s == "" {
		return "(none)"
	}
	return s
}
