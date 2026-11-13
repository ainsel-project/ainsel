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
