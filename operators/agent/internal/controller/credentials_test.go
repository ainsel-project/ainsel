package controller

import (
	"context"
	"strings"
	"testing"
	"time"

	ainselv1alpha1 "github.com/ainsel-project/ainsel/shared/api/api/v1alpha1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

// signInAgent is an OpenAI agent that authenticates with a ChatGPT sign-in.
func signInAgent() *ainselv1alpha1.Agent {
	a := providerAgent(ainselv1alpha1.AgentLLMProviderOpenAI)
	a.UID = "uid-golden"
	a.Spec.OpenAI = &ainselv1alpha1.AgentOpenAI{AuthMethod: ainselv1alpha1.OpenAIAuthMethodChatGPTSignIn}
	return a
}

func credentialReconciler(objs ...*ainselv1alpha1.Agent) *AgentReconciler {
	b := fake.NewClientBuilder().WithScheme(jobModeScheme())
	for _, o := range objs {
		b = b.WithObjects(o)
	}
	return &AgentReconciler{Client: b.Build(), Scheme: jobModeScheme(), CredentialVolumeSize: resource.MustParse("1Gi")}
}

func getCredentialPVC(t *testing.T, r *AgentReconciler, a *ainselv1alpha1.Agent) (*corev1.PersistentVolumeClaim, error) {
	t.Helper()
	var pvc corev1.PersistentVolumeClaim
	err := r.Get(context.Background(), types.NamespacedName{Namespace: a.Namespace, Name: credentialPVCName("agent-" + a.Name)}, &pvc)
	return &pvc, err
}

func TestCredentialVolumeCreatedForSignIn(t *testing.T) {
	a := signInAgent()
	r := credentialReconciler(a)
	if err := r.reconcileCredentialVolume(context.Background(), a, "agent-"+a.Name); err != nil {
		t.Fatalf("reconcileCredentialVolume: %v", err)
	}
	pvc, err := getCredentialPVC(t, r, a)
	if err != nil {
		t.Fatalf("get PVC: %v", err)
	}
	if pvc.Name != "agent-golden-pi-credentials" {
		t.Errorf("PVC name = %q, want agent-golden-pi-credentials", pvc.Name)
	}
	if len(pvc.Spec.AccessModes) != 1 || pvc.Spec.AccessModes[0] != corev1.ReadWriteOnce {
		t.Errorf("access modes = %v, want [ReadWriteOnce]", pvc.Spec.AccessModes)
	}
	if got := pvc.Spec.Resources.Requests[corev1.ResourceStorage]; got.String() != "1Gi" {
		t.Errorf("size = %s, want 1Gi", got.String())
	}
	if pvc.Spec.StorageClassName != nil {
		t.Errorf("storage class = %q, want unset (cluster default)", *pvc.Spec.StorageClassName)
	}
	if len(pvc.OwnerReferences) != 1 || pvc.OwnerReferences[0].Name != a.Name {
		t.Errorf("owner refs = %+v, want the Agent", pvc.OwnerReferences)
	}
}

func TestCredentialVolumeUsesConfiguredStorageClass(t *testing.T) {
	a := signInAgent()
	r := credentialReconciler(a)
	r.CredentialStorageClass = "fast"
	if err := r.reconcileCredentialVolume(context.Background(), a, "agent-"+a.Name); err != nil {
		t.Fatalf("reconcileCredentialVolume: %v", err)
	}
	pvc, err := getCredentialPVC(t, r, a)
	if err != nil {
		t.Fatalf("get PVC: %v", err)
	}
	if pvc.Spec.StorageClassName == nil || *pvc.Spec.StorageClassName != "fast" {
		t.Errorf("storage class = %v, want fast", pvc.Spec.StorageClassName)
	}
}

func TestCredentialVolumeDeletedOnSwitchToAPIKey(t *testing.T) {
	a := signInAgent()
	r := credentialReconciler(a)
	if err := r.reconcileCredentialVolume(context.Background(), a, "agent-"+a.Name); err != nil {
		t.Fatalf("create: %v", err)
	}
	a.Spec.OpenAI.AuthMethod = ainselv1alpha1.OpenAIAuthMethodAPIKey
	if err := r.reconcileCredentialVolume(context.Background(), a, "agent-"+a.Name); err != nil {
		t.Fatalf("switch: %v", err)
	}
	if _, err := getCredentialPVC(t, r, a); !apierrors.IsNotFound(err) {
		t.Fatalf("PVC after switch to APIKey: err = %v, want NotFound", err)
	}
}

func TestNoCredentialVolumeForAPIKey(t *testing.T) {
	a := providerAgent(ainselv1alpha1.AgentLLMProviderOpenAI)
	r := credentialReconciler(a)
	if err := r.reconcileCredentialVolume(context.Background(), a, "agent-"+a.Name); err != nil {
		t.Fatalf("reconcileCredentialVolume: %v", err)
	}
	if _, err := getCredentialPVC(t, r, a); !apierrors.IsNotFound(err) {
		t.Fatalf("PVC for an API-key agent: err = %v, want NotFound", err)
	}
}

func TestPodPlanSignInMountsCredentials(t *testing.T) {
	plan := newPodPlan(signInAgent(), "agent-golden", &ainselv1alpha1.AgentImage{})
	var vol *corev1.Volume
	for i := range plan.podVolumes {
		if plan.podVolumes[i].Name == credentialVolumeName {
			vol = &plan.podVolumes[i]
		}
	}
	if vol == nil || vol.PersistentVolumeClaim == nil || vol.PersistentVolumeClaim.ClaimName != "agent-golden-pi-credentials" {
		t.Fatalf("credential volume = %+v, want PVC agent-golden-pi-credentials", vol)
	}
	mounted := false
	for _, m := range plan.agentVolumeMounts {
		if m.Name == credentialVolumeName && m.MountPath == "/home/agent/.pi/credentials" && !m.ReadOnly {
			mounted = true
		}
	}
	if !mounted {
		t.Errorf("agent mounts = %+v, want %s writable at /home/agent/.pi/credentials", plan.agentVolumeMounts, credentialVolumeName)
	}
	init := plan.initContainers[0]
	cmd := strings.Join(init.Command, " ")
	if !strings.Contains(cmd, "ln -sf /home/agent/.pi/credentials/auth.json /home/agent/.pi/agent/auth.json") {
		t.Errorf("init command %q does not link auth.json to the credential volume", cmd)
	}
	initMounted := false
	for _, m := range init.VolumeMounts {
		if m.Name == credentialVolumeName {
			initMounted = true
		}
	}
	if !initMounted {
		t.Error("init container does not mount the credential volume, so it cannot create auth.json")
	}
}

func TestPodPlanAPIKeyHasNoCredentials(t *testing.T) {
	plan := newPodPlan(providerAgent(ainselv1alpha1.AgentLLMProviderOpenAI), "agent-golden", &ainselv1alpha1.AgentImage{})
	for _, v := range plan.podVolumes {
		if v.Name == credentialVolumeName {
			t.Fatal("API-key agent got a credential volume")
		}
	}
	if cmd := strings.Join(plan.initContainers[0].Command, " "); strings.Contains(cmd, "credentials") {
		t.Errorf("API-key init command mentions credentials: %q", cmd)
	}
}

func TestSignInEnvOmitsAPIKey(t *testing.T) {
	env := llmProviderEnv(signInAgent())
	if e := envByName(env, "OPENAI_API_KEY"); e != nil {
		t.Errorf("OPENAI_API_KEY = %+v, want absent in sign-in mode", e)
	}
	e := envByName(env, "AINSEL_AUTH_METHOD")
	if e == nil || e.Value != "chatgpt-sign-in" {
		t.Errorf("AINSEL_AUTH_METHOD = %+v, want chatgpt-sign-in", e)
	}
	if e := envByName(llmProviderEnv(providerAgent(ainselv1alpha1.AgentLLMProviderOpenAI)), "AINSEL_AUTH_METHOD"); e != nil {
		t.Errorf("AINSEL_AUTH_METHOD = %+v on an API-key agent, want absent", e)
	}
}

// signInJobModeAgent is the job-mode fixture switched to ChatGPT sign-in.
func signInJobModeAgent(dueIDs ...int64) *ainselv1alpha1.Agent {
	a := jobModeAgent(1, dueIDs...)
	a.Spec.LLM.Provider = ainselv1alpha1.AgentLLMProviderOpenAI
	a.Spec.OpenAI = &ainselv1alpha1.AgentOpenAI{AuthMethod: ainselv1alpha1.OpenAIAuthMethodChatGPTSignIn}
	return a
}

func signedIn(a *ainselv1alpha1.Agent, at time.Time) *ainselv1alpha1.Agent {
	a.Status.ChatGPTSignIn = &ainselv1alpha1.AgentChatGPTSignInStatus{SignedInAt: &metav1.Time{Time: at}}
	return a
}

// failedBySignIn is a task Job whose pod exited 78, as the cluster reports it:
// the Job's podFailurePolicy failed it outright.
func failedBySignIn(t *testing.T, a *ainselv1alpha1.Agent, taskID int64, at time.Time) *batchv1.Job {
	t.Helper()
	j := existingJob(t, a, taskID, false)
	j.Status.Conditions = []batchv1.JobCondition{{
		Type: batchv1.JobFailed, Status: corev1.ConditionTrue, Reason: batchv1.JobReasonPodFailurePolicy,
		LastTransitionTime: metav1.Time{Time: at},
	}}
	return j
}

func TestTaskJobHasPodFailurePolicyInSignInMode(t *testing.T) {
	j := buildTaskJob(signInJobModeAgent(), "agent-worker", jobModeDeployment(), 1)
	p := j.Spec.PodFailurePolicy
	if p == nil || len(p.Rules) != 1 {
		t.Fatalf("podFailurePolicy = %+v, want one rule", p)
	}
	r := p.Rules[0]
	if r.Action != batchv1.PodFailurePolicyActionFailJob || r.OnExitCodes == nil ||
		r.OnExitCodes.ContainerName == nil || *r.OnExitCodes.ContainerName != "agent" ||
		r.OnExitCodes.Operator != batchv1.PodFailurePolicyOnExitCodesOpIn ||
		len(r.OnExitCodes.Values) != 1 || r.OnExitCodes.Values[0] != 78 {
		t.Errorf("rule = %+v, want FailJob on exit 78 of container agent", r)
	}

	if j := buildTaskJob(jobModeAgent(1), "agent-worker", jobModeDeployment(), 1); j.Spec.PodFailurePolicy != nil {
		t.Errorf("API-key agent Job has podFailurePolicy %+v, want none", j.Spec.PodFailurePolicy)
	}
}

func TestNoDispatchWhileSignInRequired(t *testing.T) {
	agent := signInJobModeAgent(7)
	decision, c := runSync(t, agent)
	if jobs := listTaskJobs(t, c); len(jobs) != 0 {
		t.Fatalf("dispatched %d jobs for an agent that never signed in, want 0", len(jobs))
	}
	if !strings.Contains(decision.Message, "sign-in required") {
		t.Errorf("scale message = %q, want it to say sign-in required", decision.Message)
	}
	applySignInStatus(agent)
	cond := apimeta.FindStatusCondition(agent.Status.Conditions, ainselv1alpha1.AgentConditionSignInRequired)
	if cond == nil || cond.Status != metav1.ConditionTrue || cond.Reason != "NotSignedIn" {
		t.Errorf("SignInRequired = %+v, want True/NotSignedIn", cond)
	}
}

func TestSignedInAgentDispatches(t *testing.T) {
	agent := signedIn(signInJobModeAgent(7), time.Now().Add(-time.Hour))
	_, c := runSync(t, agent)
	if jobs := listTaskJobs(t, c); len(jobs) != 1 {
		t.Fatalf("dispatched %d jobs for a signed-in agent, want 1", len(jobs))
	}
	applySignInStatus(agent)
	cond := apimeta.FindStatusCondition(agent.Status.Conditions, ainselv1alpha1.AgentConditionSignInRequired)
	if cond == nil || cond.Status != metav1.ConditionFalse || cond.Reason != "SignedIn" {
		t.Errorf("SignInRequired = %+v, want False/SignedIn", cond)
	}
}

func TestExit78SetsSignInExpired(t *testing.T) {
	now := time.Now()
	agent := signedIn(signInJobModeAgent(7), now.Add(-time.Hour))
	dead := failedBySignIn(t, agent, 7, now.Add(-time.Minute))

	scheme := jobModeScheme()
	c := fake.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects(agent, jobModeDeployment(), dead).Build()
	r := &AgentReconciler{Client: c, Scheme: scheme}
	if _, err := r.syncPerTaskJobs(context.Background(), agent, "agent-worker", jobModeDeployment()); err != nil {
		t.Fatalf("syncPerTaskJobs: %v", err)
	}
	if s := agent.Status.ChatGPTSignIn; s == nil || s.SignInFailedAt == nil {
		t.Fatalf("chatGPTSignIn = %+v, want signInFailedAt recorded", s)
	}
	for _, j := range listTaskJobs(t, c) {
		if j.Name == dead.Name && !jobIsFinished(&j) {
			t.Fatalf("task re-dispatched while the sign-in is dead: %s", j.Name)
		}
	}
	if jobs := listTaskJobs(t, c); len(jobs) != 0 {
		t.Errorf("jobs = %d, want the dead attempt retired and nothing new dispatched", len(jobs))
	}

	// The record is status, not the Job: with the Job gone, a later reconcile
	// still holds the queue.
	_, c2 := runSync(t, agent)
	if jobs := listTaskJobs(t, c2); len(jobs) != 0 {
		t.Errorf("after the failed Job is gone, dispatched %d jobs, want 0", len(jobs))
	}
	applySignInStatus(agent)
	cond := apimeta.FindStatusCondition(agent.Status.Conditions, ainselv1alpha1.AgentConditionSignInRequired)
	if cond == nil || cond.Status != metav1.ConditionTrue || cond.Reason != "SignInExpired" {
		t.Errorf("SignInRequired = %+v, want True/SignInExpired", cond)
	}
}

func TestFailureBeforeSignInIsIgnored(t *testing.T) {
	now := time.Now()
	agent := signedIn(signInJobModeAgent(7), now)
	old := failedBySignIn(t, agent, 7, now.Add(-time.Hour))
	_, c := runSync(t, agent, old)
	if agent.Status.ChatGPTSignIn.SignInFailedAt != nil {
		t.Error("a failure from before the latest sign-in was recorded as expiring it")
	}
	if jobs := listTaskJobs(t, c); len(jobs) != 1 || jobIsFinished(&jobs[0]) {
		t.Errorf("jobs = %+v, want the old attempt retired and a fresh one dispatched", jobs)
	}
}

func TestAPIKeyAgentHasNoSignInCondition(t *testing.T) {
	agent := jobModeAgent(1, 7)
	apimeta.SetStatusCondition(&agent.Status.Conditions, metav1.Condition{
		Type: ainselv1alpha1.AgentConditionSignInRequired, Status: metav1.ConditionTrue, Reason: "NotSignedIn",
	})
	agent.Status.ChatGPTSignIn = &ainselv1alpha1.AgentChatGPTSignInStatus{}
	applySignInStatus(agent)
	if c := apimeta.FindStatusCondition(agent.Status.Conditions, ainselv1alpha1.AgentConditionSignInRequired); c != nil {
		t.Errorf("SignInRequired = %+v on an API-key agent, want absent", c)
	}
	if agent.Status.ChatGPTSignIn != nil {
		t.Errorf("chatGPTSignIn = %+v on an API-key agent, want cleared", agent.Status.ChatGPTSignIn)
	}
}

// With hardening off nothing applies fsGroup, so the init container (root)
// must hand the volume root to UID 1000: Pi takes its auth.json lock as a
// directory next to the resolved file, on the volume.
func TestPodPlanSignInUnhardenedChownsVolumeRoot(t *testing.T) {
	a := signInAgent()
	a.Spec.Runtime.SecurityHardened = ptr.To(false)
	cmd := strings.Join(newPodPlan(a, "agent-golden", &ainselv1alpha1.AgentImage{}).initContainers[0].Command, " ")
	if !strings.Contains(cmd, "chown 1000:1000 /home/agent/.pi/credentials ") {
		t.Errorf("init command %q does not chown the credential volume root", cmd)
	}
}

// A sign-in agent's record must survive a status patch from a reconcile that
// started from a copy without it (an informer cache that has not caught up).
func TestPatchStatusKeepsSignInRecordOfSignInAgent(t *testing.T) {
	stored := signInAgent()
	stored.Status.ChatGPTSignIn = &ainselv1alpha1.AgentChatGPTSignInStatus{SignedInAt: &metav1.Time{Time: time.Now().Truncate(time.Second)}}
	scheme := jobModeScheme()
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(stored).WithStatusSubresource(&ainselv1alpha1.Agent{}).Build()
	r := &AgentReconciler{Client: c, Scheme: scheme}

	stale := stored.DeepCopy()
	stale.Status.ChatGPTSignIn = nil
	if err := r.patchStatus(context.Background(), stale); err != nil {
		t.Fatalf("patchStatus: %v", err)
	}
	var got ainselv1alpha1.Agent
	_ = c.Get(context.Background(), types.NamespacedName{Namespace: stored.Namespace, Name: stored.Name}, &got)
	if got.Status.ChatGPTSignIn == nil || got.Status.ChatGPTSignIn.SignedInAt == nil {
		t.Fatalf("chatGPTSignIn = %+v after a patch from a stale copy, want the record kept", got.Status.ChatGPTSignIn)
	}
}

func TestNoCredentialVolumeDeleteForUnownedPVC(t *testing.T) {
	a := providerAgent(ainselv1alpha1.AgentLLMProviderOpenAI)
	foreign := &corev1.PersistentVolumeClaim{ObjectMeta: metav1.ObjectMeta{Name: "agent-golden-pi-credentials", Namespace: a.Namespace}}
	scheme := jobModeScheme()
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(a, foreign).Build()
	r := &AgentReconciler{Client: c, Scheme: scheme}
	if err := r.reconcileCredentialVolume(context.Background(), a, "agent-"+a.Name); err != nil {
		t.Fatalf("reconcileCredentialVolume: %v", err)
	}
	if _, err := getCredentialPVC(t, r, a); err != nil {
		t.Fatalf("a PVC the agent does not own was deleted (Get err = %v)", err)
	}
}

func TestNoDeleteCallWhenNoCredentialVolume(t *testing.T) {
	a := providerAgent(ainselv1alpha1.AgentLLMProviderOpenAI)
	scheme := jobModeScheme()
	deletes := 0
	c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(a).WithInterceptorFuncs(interceptor.Funcs{
		Delete: func(ctx context.Context, cl client.WithWatch, obj client.Object, opts ...client.DeleteOption) error {
			deletes++
			return cl.Delete(ctx, obj, opts...)
		},
	}).Build()
	r := &AgentReconciler{Client: c, Scheme: scheme}
	if err := r.reconcileCredentialVolume(context.Background(), a, "agent-"+a.Name); err != nil {
		t.Fatalf("reconcileCredentialVolume: %v", err)
	}
	if deletes != 0 {
		t.Errorf("API-key agent with no volume issued %d deletes, want 0", deletes)
	}
}

const testSession = "01JCHATGPTSIGNIN0000ABCDEF"

// requestSignIn marks the agent the way the hub does when a user clicks
// "Sign in with ChatGPT".
func requestSignIn(a *ainselv1alpha1.Agent) *ainselv1alpha1.Agent {
	if a.Annotations == nil {
		a.Annotations = map[string]string{}
	}
	a.Annotations[signInSessionAnnotation] = testSession
	return a
}

func signInJobs(jobs []batchv1.Job) []batchv1.Job {
	var out []batchv1.Job
	for _, j := range jobs {
		if j.Labels[credentialJobLabel] == credentialJobSignIn {
			out = append(out, j)
		}
	}
	return out
}

func taskJobsOf(jobs []batchv1.Job) []batchv1.Job {
	var out []batchv1.Job
	for _, j := range jobs {
		if j.Labels[TaskIDLabel] != "" {
			out = append(out, j)
		}
	}
	return out
}

// finishedSignInJob is a sign-in Job the cluster has finished.
func finishedSignInJob(a *ainselv1alpha1.Agent, ok bool, at time.Time) *batchv1.Job {
	j := buildSignInJob(a, jobModeDeployment(), testSession)
	cond := batchv1.JobCondition{Type: batchv1.JobComplete, Status: corev1.ConditionTrue, LastTransitionTime: metav1.Time{Time: at}}
	if !ok {
		cond.Type = batchv1.JobFailed
	} else {
		j.Status.CompletionTime = &metav1.Time{Time: at}
	}
	j.Status.Conditions = []batchv1.JobCondition{cond}
	return j
}

func TestSignInJobWaitsForRunningTask(t *testing.T) {
	agent := requestSignIn(signedIn(signInJobModeAgent(7), time.Now().Add(-time.Hour)))
	running := existingJob(t, agent, 8, false)
	scheme := jobModeScheme()
	c := fake.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects(agent, jobModeDeployment(), running).Build()
	r := &AgentReconciler{Client: c, Scheme: scheme}
	decision, err := r.syncPerTaskJobs(context.Background(), agent, "agent-worker", jobModeDeployment())
	if err != nil {
		t.Fatal(err)
	}
	jobs := listTaskJobs(t, c)
	if n := len(signInJobs(jobs)); n != 0 {
		t.Fatalf("sign-in Job created while a task Job runs (%d), want it to wait", n)
	}
	if n := len(taskJobsOf(jobs)); n != 1 {
		t.Fatalf("task jobs = %d, want only the running one: no new task while a sign-in waits", n)
	}
	if !strings.Contains(decision.Message, "waiting for the running task") || decision.RequeueAfter == 0 {
		t.Errorf("decision = %+v, want a wait message and a requeue", decision)
	}

	// The task finishes: the sign-in Job starts on the next pass.
	running.Status.Conditions = []batchv1.JobCondition{{Type: batchv1.JobComplete, Status: corev1.ConditionTrue}}
	if err := c.Status().Update(context.Background(), running); err != nil {
		if err := c.Update(context.Background(), running); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := r.syncPerTaskJobs(context.Background(), agent, "agent-worker", jobModeDeployment()); err != nil {
		t.Fatal(err)
	}
	if n := len(signInJobs(listTaskJobs(t, c))); n != 1 {
		t.Fatalf("sign-in jobs after the task finished = %d, want 1", n)
	}
}

func TestSignInJobShape(t *testing.T) {
	agent := requestSignIn(signInJobModeAgent())
	dep := jobModeDeployment()
	dep.Spec.Template.Spec.Containers = append(dep.Spec.Template.Spec.Containers, corev1.Container{Name: "chat", Image: "chat"})
	dep.Spec.Template.Spec.InitContainers = []corev1.Container{{Name: "setup-pi-models", Image: "busybox"}}
	dep.Spec.Template.Spec.Containers[0].Env = []corev1.EnvVar{
		{Name: "HUB_URL", Value: "http://hub"},
		{Name: "HUB_INTERNAL_VALIDATE_SECRET", Value: "s"},
		{Name: "OPENAI_API_KEY", Value: "must-not-copy"},
		{Name: "SOME_IMAGE_ENV", Value: "x"},
	}
	j := buildSignInJob(agent, dep, testSession)

	if j.Spec.ActiveDeadlineSeconds == nil || *j.Spec.ActiveDeadlineSeconds != 900 {
		t.Errorf("activeDeadlineSeconds = %v, want 900", j.Spec.ActiveDeadlineSeconds)
	}
	if j.Spec.BackoffLimit == nil || *j.Spec.BackoffLimit != 0 {
		t.Errorf("backoffLimit = %v, want 0", j.Spec.BackoffLimit)
	}
	pod := j.Spec.Template.Spec
	if len(pod.Containers) != 1 || pod.Containers[0].Name != "agent" || len(pod.InitContainers) != 0 {
		t.Fatalf("containers = %d (init %d), want only the agent container", len(pod.Containers), len(pod.InitContainers))
	}
	if pod.RestartPolicy != corev1.RestartPolicyNever {
		t.Errorf("restartPolicy = %s, want Never", pod.RestartPolicy)
	}
	if len(pod.Volumes) != 1 || pod.Volumes[0].PersistentVolumeClaim == nil || pod.Volumes[0].PersistentVolumeClaim.ClaimName != "agent-worker-pi-credentials" {
		t.Errorf("volumes = %+v, want only the credential PVC", pod.Volumes)
	}
	c := pod.Containers[0]
	if !strings.Contains(strings.Join(c.Command, " "), "ainsel-chatgpt-auth/signin.mjs") {
		t.Errorf("command = %v, want the sign-in script", c.Command)
	}
	env := map[string]string{}
	for _, e := range c.Env {
		env[e.Name] = e.Value
	}
	if env["AINSEL_SIGN_IN_SESSION"] != testSession || env["HUB_URL"] != "http://hub" || env["HUB_INTERNAL_VALIDATE_SECRET"] != "s" || env["AGENT_NAME"] != "worker" {
		t.Errorf("env = %v, want session, hub URL, internal token and agent name", env)
	}
	if _, ok := env["OPENAI_API_KEY"]; ok {
		t.Error("sign-in pod got OPENAI_API_KEY")
	}
	if _, ok := env["SOME_IMAGE_ENV"]; ok {
		t.Error("sign-in pod got image env it does not need")
	}
	if j.Labels[credentialJobLabel] != credentialJobSignIn || j.Labels[AgentNameLabel] != "worker" || j.Labels[signInSessionLabel] != testSession {
		t.Errorf("labels = %v", j.Labels)
	}
}

func TestSignInSuccessClearsCondition(t *testing.T) {
	agent := requestSignIn(signInJobModeAgent(7))
	done := finishedSignInJob(agent, true, time.Now().Add(-time.Minute))
	_, c := runSync(t, agent, done)
	s := agent.Status.ChatGPTSignIn
	if s == nil || s.SignedInAt == nil || s.HandledSession != testSession {
		t.Fatalf("chatGPTSignIn = %+v, want signedInAt and handledSession set", s)
	}
	applySignInStatus(agent)
	cond := apimeta.FindStatusCondition(agent.Status.Conditions, ainselv1alpha1.AgentConditionSignInRequired)
	if cond == nil || cond.Status != metav1.ConditionFalse {
		t.Errorf("SignInRequired = %+v, want False", cond)
	}
	if n := len(taskJobsOf(listTaskJobs(t, c))); n != 1 {
		t.Errorf("task jobs after sign-in = %d, want the queued task dispatched", n)
	}
	if n := len(signInJobs(listTaskJobs(t, c))); n != 1 {
		t.Errorf("sign-in jobs = %d, want the finished one only, not a second", n)
	}
}

func TestSignInFailureKeepsCondition(t *testing.T) {
	agent := requestSignIn(signInJobModeAgent(7))
	failed := finishedSignInJob(agent, false, time.Now().Add(-time.Minute))
	_, c := runSync(t, agent, failed)
	s := agent.Status.ChatGPTSignIn
	if s == nil || s.HandledSession != testSession || s.SignedInAt != nil {
		t.Fatalf("chatGPTSignIn = %+v, want the session handled and no sign-in", s)
	}
	applySignInStatus(agent)
	cond := apimeta.FindStatusCondition(agent.Status.Conditions, ainselv1alpha1.AgentConditionSignInRequired)
	if cond == nil || cond.Status != metav1.ConditionTrue {
		t.Errorf("SignInRequired = %+v, want still True", cond)
	}
	jobs := listTaskJobs(t, c)
	if len(taskJobsOf(jobs)) != 0 || len(signInJobs(jobs)) != 1 {
		t.Errorf("jobs = %d task / %d sign-in, want no task and no second sign-in Job", len(taskJobsOf(jobs)), len(signInJobs(jobs)))
	}
}

func TestNoTaskDispatchWhileSignInJobRuns(t *testing.T) {
	agent := requestSignIn(signedIn(signInJobModeAgent(7), time.Now().Add(-time.Hour)))
	live := buildSignInJob(agent, jobModeDeployment(), testSession)
	_, c := runSync(t, agent, live)
	jobs := listTaskJobs(t, c)
	if len(taskJobsOf(jobs)) != 0 {
		t.Errorf("task dispatched while the sign-in Job runs")
	}
	if len(signInJobs(jobs)) != 1 {
		t.Errorf("sign-in jobs = %d, want the running one only", len(signInJobs(jobs)))
	}
}
