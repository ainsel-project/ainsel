package controller

import (
	"context"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	ainselv1alpha1 "github.com/DominikPinsel/ainsel/shared/api/api/v1alpha1"
)

// syncPerTaskJobs is where jobPerTask mode lives or dies: the cap, the dedup by
// deterministic name, the replicas:0 veto, and the retirement of finished Jobs
// whose task is still due. These run against the fake client — no envtest
// needed, because the logic is all List/Create/Delete against the cache.

func jobModeScheme() *runtime.Scheme {
	s := runtime.NewScheme()
	utilruntime.Must(corev1.AddToScheme(s))
	utilruntime.Must(appsv1.AddToScheme(s))
	utilruntime.Must(batchv1.AddToScheme(s))
	utilruntime.Must(ainselv1alpha1.AddToScheme(s))
	return s
}

func jobModeAgent(replicas int32, dueIDs ...int64) *ainselv1alpha1.Agent {
	a := scalingAgent(&ainselv1alpha1.AgentScaling{Replicas: ptr.To(replicas)})
	a.Status.PendingTaskIDs = dueIDs
	return a
}

func jobModeDeployment() *appsv1.Deployment {
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "worker",
			Namespace: "ns",
			Labels:    map[string]string{"ainsel.dev/agent": "worker"},
		},
		Spec: appsv1.DeploymentSpec{
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{Name: "agent", Image: pinnedImage}},
				},
			},
		},
	}
}

// existingJob builds a Job as the cluster would have one: labelled, owned, and
// optionally finished (Complete condition set).
func existingJob(t *testing.T, agent *ainselv1alpha1.Agent, taskID int64, finished bool) *batchv1.Job {
	t.Helper()
	j := buildTaskJob(agent, "agent-worker", jobModeDeployment(), taskID)
	if finished {
		j.Status.Conditions = []batchv1.JobCondition{
			{Type: batchv1.JobComplete, Status: corev1.ConditionTrue},
		}
	}
	return j
}

func listTaskJobs(t *testing.T, c client.Client) []batchv1.Job {
	t.Helper()
	var l batchv1.JobList
	if err := c.List(context.Background(), &l, client.InNamespace("ns")); err != nil {
		t.Fatalf("list jobs: %v", err)
	}
	return l.Items
}

func runSync(t *testing.T, agent *ainselv1alpha1.Agent, jobs ...runtime.Object) (ScaleDecision, client.Client) {
	t.Helper()
	scheme := jobModeScheme()
	objs := []runtime.Object{agent, jobModeDeployment()}
	objs = append(objs, jobs...)
	c := fake.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects(objs...).Build()
	r := &AgentReconciler{Client: c, Scheme: scheme}
	decision, err := r.syncPerTaskJobs(context.Background(), agent, "agent-worker", jobModeDeployment())
	if err != nil {
		t.Fatalf("syncPerTaskJobs: %v", err)
	}
	return decision, c
}

func TestSyncPerTaskJobs_CapsAtReplicas(t *testing.T) {
	agent := jobModeAgent(2, 1, 2, 3)
	decision, c := runSync(t, agent)

	jobs := listTaskJobs(t, c)
	if len(jobs) != 2 {
		t.Fatalf("created %d jobs, want the ceiling of 2 (replicas)", len(jobs))
	}
	if decision.Replicas != 2 || decision.Reason != ScaleReasonCapped {
		t.Errorf("decision = %+v, want 2 active and reason %q while a task waits for a slot", decision, ScaleReasonCapped)
	}
	if decision.RequeueAfter != taskJobDispatchPoll {
		t.Errorf("requeue = %v, want %v while a task waits for a slot", decision.RequeueAfter, taskJobDispatchPoll)
	}
}

func TestSyncPerTaskJobs_DedupsByDeterministicName(t *testing.T) {
	agent := jobModeAgent(2, 1, 2)
	live := existingJob(t, agent, 1, false)
	decision, c := runSync(t, agent, live)

	jobs := listTaskJobs(t, c)
	if len(jobs) != 2 {
		t.Fatalf("have %d jobs, want the existing task-1 plus one new task-2", len(jobs))
	}
	owned := 0
	for _, j := range jobs {
		if j.Labels[TaskIDLabel] == "1" {
			owned++
		}
	}
	if owned != 1 {
		t.Errorf("%d jobs own task 1, want exactly 1 — deterministic-name dedup failed", owned)
	}
	if decision.Replicas != 2 {
		t.Errorf("decision.Replicas = %d, want 2 (live job + new dispatch)", decision.Replicas)
	}
}

func TestSyncPerTaskJobs_ZeroReplicasVetoesDispatch(t *testing.T) {
	agent := jobModeAgent(0, 1, 2)
	decision, c := runSync(t, agent)

	if jobs := listTaskJobs(t, c); len(jobs) != 0 {
		t.Fatalf("created %d jobs under a replicas:0 veto", len(jobs))
	}
	if decision.Replicas != 0 {
		t.Errorf("decision.Replicas = %d, want 0", decision.Replicas)
	}
}

// The re-dispatch hole: a Job that finished while its task is still due is a
// dead attempt (pod never started, pull failure, backoffLimit exhausted). The
// deterministic name would make a re-dispatch collide with it until the TTL
// collector runs an hour later, so syncPerTaskJobs must retire it and let a
// fresh attempt through.
func TestSyncPerTaskJobs_RetiresFinishedJobOfStillDueTask(t *testing.T) {
	agent := jobModeAgent(2, 1)
	dead := existingJob(t, agent, 1, true)
	decision, c := runSync(t, agent, dead)

	jobs := listTaskJobs(t, c)
	if len(jobs) != 1 {
		t.Fatalf("have %d jobs, want exactly the fresh replacement", len(jobs))
	}
	if len(jobs[0].Status.Conditions) != 0 {
		t.Error("the surviving job is the finished corpse, not a fresh attempt")
	}
	if jobs[0].Labels[TaskIDLabel] != "1" {
		t.Errorf("replacement job is for task %q, want 1", jobs[0].Labels[TaskIDLabel])
	}
	if decision.Replicas != 1 {
		t.Errorf("decision.Replicas = %d, want the new attempt counted as active", decision.Replicas)
	}
}

// A finished Job whose task is NOT due (acked, or nacked and waiting out its
// backoff) is recent history — the task row is the durable record, but the Job
// is allowed to age out on its own TTL.
func TestSyncPerTaskJobs_KeepsFinishedJobOfNonDueTask(t *testing.T) {
	agent := jobModeAgent(2, 2)
	history := existingJob(t, agent, 1, true)
	decision, c := runSync(t, agent, history)

	jobs := listTaskJobs(t, c)
	if len(jobs) != 2 {
		t.Fatalf("have %d jobs, want the history kept plus one dispatch for task 2", len(jobs))
	}
	if decision.Replicas != 1 {
		t.Errorf("decision.Replicas = %d, want only the live job counted as active", decision.Replicas)
	}
	if decision.RequeueAfter != 0 {
		t.Errorf("requeue = %v, want 0 when nothing waits", decision.RequeueAfter)
	}
}
