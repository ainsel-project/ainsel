package controller

import (
	"context"
	"strings"
	"testing"

	ainselv1alpha1 "github.com/ainsel-project/ainsel/shared/api/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
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
