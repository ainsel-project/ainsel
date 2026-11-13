package controller

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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
