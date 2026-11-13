package controller

import (
	"context"
	"fmt"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	ainselv1alpha1 "github.com/ainsel-project/ainsel/shared/api/api/v1alpha1"
)

// ChatGPT sign-in credential volume. An agent that authenticates with a
// ChatGPT sign-in keeps Pi's auth.json (and the device id its sign-in was
// registered under) on a persistent volume of its own: the access token is
// refreshed by whichever pod runs next and the rotated refresh token must
// survive that pod. A Secret cannot do this — it is read-only in the pod.
//
// Only the credential lives here. Pi's agent directory stays an emptyDir; the
// setup init container links <agentDir>/auth.json to the file on this volume.
// Pi writes auth.json in place (never by rename), so writes follow the link,
// and its lock directory lands next to the link in the per-pod emptyDir — safe
// because at most one pod uses the volume at a time.

const (
	// credentialVolumeName is the pod volume backed by the agent's PVC.
	credentialVolumeName = "pi-credentials"

	// signInMountPath is where the runtime sees the credential volume.
	signInMountPath = "/home/agent/.pi/credentials"

	// authMethodEnvValue tells the runtime to check the sign-in before a task.
	authMethodEnvValue = "chatgpt-sign-in"
)

//+kubebuilder:rbac:groups="",resources=persistentvolumeclaims,verbs=get;list;watch;create;delete

// credentialPVCName names an agent's credential volume claim.
func credentialPVCName(agentName string) string {
	return agentName + "-pi-credentials"
}

// reconcileCredentialVolume keeps the credential PVC in step with the auth
// method: created when the agent uses ChatGPT sign-in, deleted when it does
// not. Deleting on a switch back to an API key is deliberate — the tokens go
// with it, and signing in again starts from a fresh volume.
func (r *AgentReconciler) reconcileCredentialVolume(ctx context.Context, agent *ainselv1alpha1.Agent, agentName string) error {
	pvc := &corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{Name: credentialPVCName(agentName), Namespace: agent.Namespace},
	}
	if !agent.UsesChatGPTSignIn() {
		if err := r.Delete(ctx, pvc); err != nil && !apierrors.IsNotFound(err) {
			return fmt.Errorf("deleting credential volume: %w", err)
		}
		return nil
	}

	err := r.Get(ctx, client.ObjectKeyFromObject(pvc), pvc)
	if err == nil {
		return nil // a PVC's spec is immutable once bound; nothing to update
	}
	if !apierrors.IsNotFound(err) {
		return fmt.Errorf("reading credential volume: %w", err)
	}
	pvc.Labels = map[string]string{"ainsel.dev/agent": agent.Name}
	pvc.Spec = corev1.PersistentVolumeClaimSpec{
		AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
		Resources: corev1.VolumeResourceRequirements{
			Requests: corev1.ResourceList{corev1.ResourceStorage: r.CredentialVolumeSize},
		},
	}
	if r.CredentialStorageClass != "" {
		pvc.Spec.StorageClassName = &r.CredentialStorageClass
	}
	if err := controllerutil.SetControllerReference(agent, pvc, r.Scheme); err != nil {
		return fmt.Errorf("setting owner reference on credential volume: %w", err)
	}
	if err := r.Create(ctx, pvc); err != nil && !apierrors.IsAlreadyExists(err) {
		return fmt.Errorf("creating credential volume: %w", err)
	}
	return nil
}

// Sign-in required gate. A task pod checks the sign-in before it fetches its
// task and exits exitSignInRequired when the sign-in is missing or dead. The
// task Job's podFailurePolicy fails the Job on that exit code instead of
// retrying it, and the operator records the failure in status and dispatches
// nothing until a new sign-in succeeds. The task row was never fetched or
// nacked, so it stays pending and runs after the sign-in.

const (
	// exitSignInRequired is the runtime's "sign-in required" exit code
	// (EX_CONFIG), shared with pi/pi-extensions/ainsel-chatgpt-auth.
	exitSignInRequired int32 = 78

	signInReasonNotSignedIn = "NotSignedIn"
	signInReasonExpired     = "SignInExpired"
	signInReasonSignedIn    = "SignedIn"
)

// signInFailurePolicy fails a task Job outright when its runtime reports that
// the sign-in is required: retrying the pod cannot help until someone signs in.
func signInFailurePolicy() *batchv1.PodFailurePolicy {
	return &batchv1.PodFailurePolicy{
		Rules: []batchv1.PodFailurePolicyRule{{
			Action: batchv1.PodFailurePolicyActionFailJob,
			OnExitCodes: &batchv1.PodFailurePolicyOnExitCodesRequirement{
				ContainerName: ptr.To("agent"),
				Operator:      batchv1.PodFailurePolicyOnExitCodesOpIn,
				Values:        []int32{exitSignInRequired},
			},
		}},
	}
}

// signInFailureTime reports when a Job was failed by the sign-in rule. The
// rule is the only podFailurePolicy rule a task Job carries, so the Job's
// PodFailurePolicy reason identifies it.
func signInFailureTime(j *batchv1.Job) (time.Time, bool) {
	for _, c := range j.Status.Conditions {
		if c.Type == batchv1.JobFailed && c.Status == corev1.ConditionTrue && c.Reason == batchv1.JobReasonPodFailurePolicy {
			if c.LastTransitionTime.IsZero() {
				return time.Now(), true
			}
			return c.LastTransitionTime.Time, true
		}
	}
	return time.Time{}, false
}

// recordSignInFailure notes a sign-in failure the cluster reported for a task
// Job. A failure older than the latest sign-in belongs to the previous one and
// is ignored. It must run before the Job is retired: the Job is evidence, the
// status field is the record.
func recordSignInFailure(agent *ainselv1alpha1.Agent, j *batchv1.Job) {
	at, ok := signInFailureTime(j)
	if !ok {
		return
	}
	s := agent.Status.ChatGPTSignIn
	if s == nil {
		s = &ainselv1alpha1.AgentChatGPTSignInStatus{}
		agent.Status.ChatGPTSignIn = s
	}
	if s.SignedInAt != nil && !at.After(s.SignedInAt.Time) {
		return
	}
	if s.SignInFailedAt == nil || at.After(s.SignInFailedAt.Time) {
		s.SignInFailedAt = &metav1.Time{Time: at}
	}
}

// signInRequired derives the gate from status alone, so it survives the Jobs
// that observed it.
func signInRequired(s *ainselv1alpha1.AgentChatGPTSignInStatus) (bool, string) {
	if s == nil || s.SignedInAt == nil {
		return true, signInReasonNotSignedIn
	}
	if s.SignInFailedAt != nil && s.SignInFailedAt.After(s.SignedInAt.Time) {
		return true, signInReasonExpired
	}
	return false, signInReasonSignedIn
}

// applySignInStatus sets the SignInRequired condition for a sign-in agent, and
// removes both the condition and the sign-in record from every other agent:
// the condition is absent, not False, where sign-in does not apply.
func applySignInStatus(agent *ainselv1alpha1.Agent) {
	if !agent.UsesChatGPTSignIn() {
		apimeta.RemoveStatusCondition(&agent.Status.Conditions, ainselv1alpha1.AgentConditionSignInRequired)
		agent.Status.ChatGPTSignIn = nil
		return
	}
	required, reason := signInRequired(agent.Status.ChatGPTSignIn)
	cond := metav1.Condition{
		Type:    ainselv1alpha1.AgentConditionSignInRequired,
		Status:  metav1.ConditionFalse,
		Reason:  reason,
		Message: "ChatGPT sign-in is valid",
	}
	if required {
		cond.Status = metav1.ConditionTrue
		cond.Message = "No task runs until someone signs in with ChatGPT from the agent page"
		if reason == signInReasonExpired {
			cond.Message = "The ChatGPT sign-in expired or was revoked; " + cond.Message
		}
	}
	apimeta.SetStatusCondition(&agent.Status.Conditions, cond)
}
