package controller

import (
	"context"
	"strings"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	fake "sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"

	ainselv1alpha1 "github.com/ainsel-project/ainsel/shared/api/api/v1alpha1"
)

func TestTaskJobName(t *testing.T) {
	// The common case: a plain join, deterministic so "does this task already
	// have a Job?" is a name lookup rather than a search.
	got := taskJobName("a-developer-panel", 1689)
	if want := "task-a-developer-panel-1689"; got != want {
		t.Errorf("taskJobName = %q, want %q", got, want)
	}
	if taskJobName("a-developer-panel", 1689) != got {
		t.Error("taskJobName is not deterministic; the dedup by name would leak double dispatches")
	}
	if taskJobName("a-developer-panel", 1690) == got {
		t.Error("two tasks share a job name; one would never be dispatched")
	}

	// A name at the length limit must stay RFC 1123 safe and must keep the task
	// id: the id is the part that carries uniqueness.
	long := "a-" + strings.Repeat("x", 70)
	name := taskJobName(long, 123456789)
	if len(name) > maxK8sNameLen {
		t.Errorf("taskJobName length = %d, must not exceed %d", len(name), maxK8sNameLen)
	}
	if !strings.HasSuffix(name, "-123456789") {
		t.Errorf("truncated name %q dropped the task id", name)
	}
	if taskJobName(long, 123456789) != name {
		t.Error("truncated names are not deterministic")
	}
	// Distinct ids still produce distinct names after truncation.
	if taskJobName(long, 123456788) == name {
		t.Error("truncated names collide across task ids")
	}
}

func TestTaskJobNameTrimsHyphenSeam(t *testing.T) {
	// The cut can land right after a hyphen inside the agent name. The prefix
	// must not keep it: "task-…--<hash>-<id>" reads as a bug and trips
	// stricter downstream name validators even though a DNS-1123 subdomain
	// technically allows it.
	agent := strings.Repeat("a", 48) + "-" + strings.Repeat("b", 12) // hyphen sits at the cut seam
	name := taskJobName(agent, 42)
	if len(name) > maxK8sNameLen {
		t.Errorf("taskJobName length = %d, must not exceed %d", len(name), maxK8sNameLen)
	}
	if strings.Contains(name, "--") {
		t.Errorf("truncated name %q keeps a double hyphen at the cut seam", name)
	}
	if !strings.HasSuffix(name, "-42") {
		t.Errorf("truncated name %q dropped the task id", name)
	}
	if taskJobName(agent, 42) != name {
		t.Error("seam-truncated names are not deterministic")
	}
}

func TestBuildTaskJob(t *testing.T) {
	agent := &ainselv1alpha1.Agent{
		ObjectMeta: metav1.ObjectMeta{Name: "a-dev", Namespace: "ainsel-dev"},
	}
	deploy := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "a-dev",
			Namespace: "ainsel-dev",
			Labels:    map[string]string{"ainsel.dev/agent": "a-dev"},
		},
		Spec: appsv1.DeploymentSpec{
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"ainsel.dev/agent": "a-dev"}},
				Spec: corev1.PodSpec{
					RestartPolicy: corev1.RestartPolicyAlways,
					Containers: []corev1.Container{
						{Name: "agent", Env: []corev1.EnvVar{{Name: "AINSEL_HUB_URL", Value: "http://hub"}}},
						{Name: "chat", Env: []corev1.EnvVar{{Name: "AINSEL_HUB_URL", Value: "http://hub"}}},
					},
				},
			},
		},
	}

	job := buildTaskJob(agent, "a-dev", deploy, 4242)

	if job.Name != "task-a-dev-4242" {
		t.Errorf("job name = %q, want task-a-dev-4242", job.Name)
	}
	if job.Namespace != "ainsel-dev" {
		t.Errorf("job namespace = %q", job.Namespace)
	}
	if job.Labels[TaskIDLabel] != "4242" {
		t.Errorf("job task-id label = %q, want 4242", job.Labels[TaskIDLabel])
	}
	if job.Spec.Template.Labels[TaskIDLabel] != "4242" {
		t.Error("task-id label missing on the pod template; pods cannot be traced to their task")
	}
	// The Deployment's labels are carried over so the per-agent list in
	// syncPerTaskJobs finds the Job.
	if job.Labels["ainsel.dev/agent"] != "a-dev" {
		t.Error("agent label lost; the job would be invisible to the agent's list")
	}

	// One-shot semantics: never restart in place (the Job retry owns retries so
	// the hub sees one nack per attempt), short grace (nothing of its own to
	// drain; a kill leaves the row pending and the Job restarts it).
	if job.Spec.Template.Spec.RestartPolicy != corev1.RestartPolicyNever {
		t.Errorf("restart policy = %q, want Never", job.Spec.Template.Spec.RestartPolicy)
	}
	if ptr.Deref(job.Spec.Template.Spec.TerminationGracePeriodSeconds, 0) != taskJobGracePeriod {
		t.Error("grace period not shortened for task pods")
	}
	if ptr.Deref(job.Spec.Completions, 0) != 1 || ptr.Deref(job.Spec.Parallelism, 0) != 1 {
		t.Error("a task Job must be exactly one pod completing once")
	}
	if ptr.Deref(job.Spec.BackoffLimit, -1) != taskJobBackoffLimit {
		t.Error("backoff limit not set; a failed task pod would never be retried")
	}
	if ptr.Deref(job.Spec.TTLSecondsAfterFinished, 0) != taskJobTTLAfterFinished {
		t.Error("finished Jobs would never be collected")
	}

	// The task id reaches only the runtime container — and only as an addition;
	// the template's existing env must survive untouched.
	agentC := job.Spec.Template.Spec.Containers[0]
	if agentC.Name != "agent" {
		t.Fatalf("first container = %q", agentC.Name)
	}
	if len(agentC.Env) != 3 || agentC.Env[1].Name != "AINSEL_TASK_ID" || agentC.Env[1].Value != "4242" {
		t.Errorf("agent container env = %+v, want the task id appended", agentC.Env)
	}
	if agentC.Env[0].Name != "AINSEL_HUB_URL" {
		t.Error("template env was dropped")
	}
	// The sidecar never learns which task is running — it does not run tasks. It
	// does learn where the runtime announces itself, because ending with the task
	// is its job too (see TestBuildTaskJobEndsWithItsRuntime).
	chatC := job.Spec.Template.Spec.Containers[1]
	if findEnvVar(chatC.Env, "AINSEL_TASK_ID") != nil {
		t.Errorf("chat sidecar received the task id; it does not run tasks: %+v", chatC.Env)
	}
	if ev := findEnvVar(chatC.Env, "AINSEL_TASK_DIR"); ev == nil || ev.Value != taskRunDir {
		t.Errorf("chat sidecar cannot see the task run dir: %+v", chatC.Env)
	}

	// The Deployment's own template must not be mutated — it is shared state
	// across reconciles.
	if len(deploy.Spec.Template.Spec.Containers[0].Env) != 1 {
		t.Error("buildTaskJob mutated the Deployment template")
	}
	if len(deploy.Spec.Template.Spec.Volumes) != 0 {
		t.Error("buildTaskJob added volumes to the Deployment template")
	}
}

func TestBuildTaskJobEndsWithItsRuntime(t *testing.T) {
	// A Job completes only when every container in its pod exits. The chat MCP
	// server copied from the Deployment template is a long-lived HTTP server with
	// no task of its own, so it would outlive the task: the pod stays Running, the
	// Job never gains a completion condition, and the slot it holds in the agent's
	// concurrency ceiling blocks new tasks *and* this task's own retry. Task pods
	// therefore share one PID namespace and one directory, and the runtime writes
	// its PID there for the sidecars to watch.
	deploy := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "a-dev", Namespace: "ainsel-dev"},
		Spec: appsv1.DeploymentSpec{
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{Name: "agent"},
						{Name: "chat"},
						{Name: "custom-sidecar"},
					},
				},
			},
		},
	}

	job := buildTaskJob(&ainselv1alpha1.Agent{
		ObjectMeta: metav1.ObjectMeta{Name: "a-dev", Namespace: "ainsel-dev"},
	}, "a-dev", deploy, 99)

	spec := job.Spec.Template.Spec
	if !ptr.Deref(spec.ShareProcessNamespace, false) {
		t.Fatal("task pods do not share a PID namespace; a sidecar cannot see the runtime's pid")
	}

	var vol *corev1.Volume
	for i := range spec.Volumes {
		if spec.Volumes[i].Name == taskRunVolumeName {
			vol = &spec.Volumes[i]
		}
	}
	if vol == nil || vol.EmptyDir == nil {
		t.Fatalf("no %s emptyDir for the runtime to announce itself in: %+v", taskRunVolumeName, spec.Volumes)
	}

	// Every container, not just the platform's own chat sidecar: an AgentImage's
	// sidecar can adopt the same contract.
	for _, c := range spec.Containers {
		mounted := false
		for _, m := range c.VolumeMounts {
			if m.Name == taskRunVolumeName && m.MountPath == taskRunDir {
				mounted = true
			}
		}
		if !mounted {
			t.Errorf("container %q cannot reach %s", c.Name, taskRunDir)
		}
		if ev := findEnvVar(c.Env, "AINSEL_TASK_DIR"); ev == nil || ev.Value != taskRunDir {
			t.Errorf("container %q has no AINSEL_TASK_DIR: %+v", c.Name, c.Env)
		}
	}
}

func TestJobIsFinished(t *testing.T) {
	withCondition := func(condType batchv1.JobConditionType, status corev1.ConditionStatus) *batchv1.Job {
		return &batchv1.Job{
			Status: batchv1.JobStatus{
				Conditions: []batchv1.JobCondition{{Type: condType, Status: status}},
			},
		}
	}
	if !jobIsFinished(withCondition(batchv1.JobComplete, corev1.ConditionTrue)) {
		t.Error("a completed Job must count as finished; its slot is free")
	}
	if !jobIsFinished(withCondition(batchv1.JobFailed, corev1.ConditionTrue)) {
		t.Error("a failed Job (backoff exhausted) must count as finished")
	}
	if jobIsFinished(withCondition(batchv1.JobComplete, corev1.ConditionFalse)) {
		t.Error("a not-yet-complete condition must not count as finished")
	}
	if jobIsFinished(&batchv1.Job{}) {
		t.Error("a job with no conditions is running")
	}
}

// A Job deleted with no propagation policy is deleted as Orphan — batch/v1 is the
// one resource whose default is not Background — and Orphan means the finished
// pod survives with its ownerRef stripped. Nothing owns such a pod, and nothing
// ever will: the TTL belongs to the Job, and the Job is gone. This path runs on
// every re-dispatch of a task whose last attempt died without acking, so an
// option-less delete leaked one ownerless pod per retry, forever.
func TestSyncPerTaskJobs_RetireCascadesToItsPods(t *testing.T) {
	scheme := scalingFixtures()
	agent := scalingAgent(nil)
	agent.Status.PendingTaskIDs = []int64{7}

	dead := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      taskJobName(agent.Name, 7),
			Namespace: agent.Namespace,
			Labels:    map[string]string{AgentNameLabel: agent.Name, TaskIDLabel: "7"},
		},
		Status: batchv1.JobStatus{
			Succeeded:      1,
			CompletionTime: &metav1.Time{Time: time.Now().Add(-time.Minute)},
			Conditions: []batchv1.JobCondition{
				{Type: batchv1.JobComplete, Status: corev1.ConditionTrue},
			},
		},
	}
	// Takes the agent's only concurrency slot, so the reconcile retires the dead
	// attempt without dispatching a replacement in the same pass: what the delete
	// interceptor sees is the retire and nothing else.
	running := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      taskJobName(agent.Name, 8),
			Namespace: agent.Namespace,
			Labels:    map[string]string{AgentNameLabel: agent.Name, TaskIDLabel: "8"},
		},
	}

	var deleteOpts *client.DeleteOptions
	c := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(agent, dead, running).
		WithStatusSubresource(&ainselv1alpha1.Agent{}).
		WithInterceptorFuncs(interceptor.Funcs{
			Delete: func(ctx context.Context, cl client.WithWatch, obj client.Object, opts ...client.DeleteOption) error {
				if _, isJob := obj.(*batchv1.Job); isJob {
					o := &client.DeleteOptions{}
					for _, opt := range opts {
						opt.ApplyToDelete(o)
					}
					deleteOpts = o
				}
				return cl.Delete(ctx, obj, opts...)
			},
		}).
		Build()
	r := &AgentReconciler{Client: c, Scheme: scheme}

	deploy := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "agent-worker", Namespace: agent.Namespace},
	}
	if _, err := r.syncPerTaskJobs(context.Background(), agent, agent.Name, deploy); err != nil {
		t.Fatalf("syncPerTaskJobs: %v", err)
	}

	var still batchv1.Job
	if err := c.Get(context.Background(), client.ObjectKeyFromObject(dead), &still); !apierrors.IsNotFound(err) {
		t.Fatalf("dead attempt still present (Get err = %v), want it retired", err)
	}
	if err := c.Get(context.Background(), client.ObjectKeyFromObject(running), &still); err != nil {
		t.Fatalf("the running attempt was deleted too: %v", err)
	}
	if deleteOpts == nil {
		t.Fatal("the retire path issued no Job delete")
	}
	if deleteOpts.PropagationPolicy == nil {
		t.Fatal("deleted with no propagation policy: batch/v1 defaults that to Orphan, which strands the finished pod with no owner forever")
	}
	if *deleteOpts.PropagationPolicy != metav1.DeletePropagationBackground {
		t.Fatalf("propagation = %q, want %q", *deleteOpts.PropagationPolicy, metav1.DeletePropagationBackground)
	}
}
