package controller

import (
	"context"
	"fmt"
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
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
// Pi writes auth.json in place (never by rename), so writes follow the link.
// Pi locks the resolved file, so its lock directory (auth.json.lock) is
// created on this volume too: UID 1000 must be able to create entries in the
// volume root. With security hardening the pod's fsGroup grants that (the
// storage class must support fsGroup); without it the init container chowns
// the root.

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
	err := r.Get(ctx, client.ObjectKeyFromObject(pvc), pvc)
	if err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("reading credential volume: %w", err)
	}
	exists := err == nil
	if !agent.UsesChatGPTSignIn() {
		// Read from the cache first: most agents never had a volume, and a
		// claim of this name the agent does not own is not ours to delete.
		if !exists || !metav1.IsControlledBy(pvc, agent) {
			return nil
		}
		if err := r.Delete(ctx, pvc); err != nil && !apierrors.IsNotFound(err) {
			return fmt.Errorf("deleting credential volume: %w", err)
		}
		return nil
	}
	if exists {
		return nil // a PVC's spec is immutable once bound; nothing to update
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

// Sign-in Job. When a user starts a ChatGPT sign-in, the hub names the
// session in signInSessionAnnotation. The operator then stops dispatching new
// task Jobs, waits for the running ones to finish (the credential volume is
// ReadWriteOnce and two pods must not race on the refresh token), and runs a
// short Job that drives the sign-in on the volume. Its outcome is recorded in
// status.chatGPTSignIn, so the session is handled exactly once.

const (
	// signInSessionAnnotation is written by the hub: the session to run a
	// sign-in pod for.
	signInSessionAnnotation = "ainsel.dev/chatgpt-sign-in-session"

	// credentialJobLabel marks a Job that uses the credential volume but is
	// not a task run. Its value says which kind.
	credentialJobLabel  = "ainsel.dev/credential-job" // #nosec G101 -- label key, not a credential
	credentialJobSignIn = "signin"

	// signInSessionLabel carries the session a sign-in Job serves.
	signInSessionLabel = "ainsel.dev/sign-in-session"

	// signInJobDeadline bounds the sign-in pod. It outlives the hub's
	// 10-minute session, so an abandoned sign-in ends at the hub first and
	// the pod exits on the closed session.
	signInJobDeadline int64 = 900

	signInScript = "/usr/local/share/pi-extensions/ainsel-chatgpt-auth/signin.mjs"
)

// signInPodSecretName is the Secret the hub writes the sign-in pod's token to
// (services/hub: signInPodSecretName).
func signInPodSecretName(agentName string) string {
	return agentName + "-chatgpt-sign-in"
}

// signInWanted returns the session the hub asked for and the operator has not
// handled yet, or "".
func signInWanted(agent *ainselv1alpha1.Agent) string {
	want := agent.Annotations[signInSessionAnnotation]
	if want == "" {
		return ""
	}
	if s := agent.Status.ChatGPTSignIn; s != nil && s.HandledSession == want {
		return ""
	}
	return want
}

// signInJobName is deterministic per session, so a repeated reconcile finds
// the Job it already created.
func signInJobName(agentName, session string) string {
	suffix := strings.ToLower(session)
	if len(suffix) > 8 {
		suffix = suffix[len(suffix)-8:]
	}
	prefix := "signin-" + agentName
	if max := maxK8sNameLen - len(suffix) - 1; len(prefix) > max {
		prefix = strings.TrimSuffix(prefix[:max], "-")
	}
	return prefix + "-" + suffix
}

// buildSignInJob renders the sign-in Job: the hub address, internal token,
// session and the session's own token, and the sign-in script.
func buildSignInJob(agent *ainselv1alpha1.Agent, deploy *appsv1.Deployment, session string) *batchv1.Job {
	env := []corev1.EnvVar{
		{Name: "AGENT_NAME", Value: agent.Name},
		{Name: "AINSEL_SIGN_IN_SESSION", Value: session},
		// The session's own token, written by the hub. Only this pod gets
		// it, so a task pod holding the shared internal token cannot act as
		// the sign-in pod.
		{Name: "AINSEL_SIGN_IN_TOKEN", ValueFrom: &corev1.EnvVarSource{
			SecretKeyRef: &corev1.SecretKeySelector{
				LocalObjectReference: corev1.LocalObjectReference{Name: signInPodSecretName(agent.Name)},
				Key:                  "token",
			},
		}},
	}
	for _, c := range deploy.Spec.Template.Spec.Containers {
		if c.Name != "agent" {
			continue
		}
		for _, e := range c.Env {
			if e.Name == "HUB_URL" || e.Name == "HUB_INTERNAL_VALIDATE_SECRET" {
				env = append(env, e)
			}
		}
	}
	job := credentialPodJob(agent, deploy, signInJobName(agent.Name, session), credentialJobSignIn, signInScript, env, signInJobDeadline)
	job.Labels[signInSessionLabel] = session
	job.Spec.Template.Labels[signInSessionLabel] = session
	job.Spec.BackoffLimit = ptr.To(int32(0))
	return job
}

// credentialPodJob renders a Job that uses the credential volume without being
// a task run: the agent's runtime image and security settings from its pod
// template, but only the agent container, only the credential volume, only
// the given env and the given script. No init container: nothing of Pi's
// agent directory is used.
func credentialPodJob(agent *ainselv1alpha1.Agent, deploy *appsv1.Deployment, name, kind, script string, env []corev1.EnvVar, deadline int64) *batchv1.Job {
	tpl := deploy.Spec.Template.DeepCopy()
	var agentC corev1.Container
	for _, c := range tpl.Spec.Containers {
		if c.Name == "agent" {
			agentC = c
		}
	}
	agentC.Env = env
	agentC.EnvFrom = nil
	agentC.Command = []string{"sh", "-c", `AINSEL_PI_ROOT="$(npm root -g)" exec node ` + script}
	agentC.Args = nil
	agentC.Ports = nil
	agentC.ReadinessProbe, agentC.LivenessProbe, agentC.StartupProbe = nil, nil, nil
	agentC.VolumeMounts = []corev1.VolumeMount{{Name: credentialVolumeName, MountPath: signInMountPath}}

	labels := map[string]string{}
	for k, v := range deploy.Labels {
		labels[k] = v
	}
	labels[AgentNameLabel] = agent.Name
	labels[credentialJobLabel] = kind

	tpl.Labels = map[string]string{}
	for k, v := range labels {
		tpl.Labels[k] = v
	}
	tpl.Spec.Containers = []corev1.Container{agentC}
	tpl.Spec.InitContainers = nil
	tpl.Spec.ShareProcessNamespace = nil
	tpl.Spec.RestartPolicy = corev1.RestartPolicyNever
	tpl.Spec.TerminationGracePeriodSeconds = ptr.To(taskJobGracePeriod)
	tpl.Spec.Volumes = []corev1.Volume{{
		Name: credentialVolumeName,
		VolumeSource: corev1.VolumeSource{
			// The agent's resources are named agent-<name> (see Reconcile).
			PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{ClaimName: credentialPVCName("agent-" + agent.Name)},
		},
	}}

	return &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: agent.Namespace,
			Labels:    labels,
		},
		Spec: batchv1.JobSpec{
			Completions:             ptr.To(int32(1)),
			Parallelism:             ptr.To(int32(1)),
			BackoffLimit:            ptr.To(int32(0)),
			ActiveDeadlineSeconds:   ptr.To(deadline),
			TTLSecondsAfterFinished: ptr.To(taskJobTTLAfterFinished),
			Template:                *tpl,
		},
	}
}

// recordSignInOutcome notes a finished sign-in Job: the session is handled
// either way, and a success is the new sign-in.
func recordSignInOutcome(agent *ainselv1alpha1.Agent, j *batchv1.Job) {
	session := j.Labels[signInSessionLabel]
	s := agent.Status.ChatGPTSignIn
	if s == nil {
		s = &ainselv1alpha1.AgentChatGPTSignInStatus{}
		agent.Status.ChatGPTSignIn = s
	}
	for _, c := range j.Status.Conditions {
		if c.Status != corev1.ConditionTrue {
			continue
		}
		switch c.Type {
		case batchv1.JobComplete:
			at := c.LastTransitionTime.Time
			if j.Status.CompletionTime != nil {
				at = j.Status.CompletionTime.Time
			}
			if at.IsZero() {
				at = time.Now()
			}
			if s.SignedInAt == nil || at.After(s.SignedInAt.Time) {
				s.SignedInAt = &metav1.Time{Time: at}
			}
			// Only the Job of the session the hub currently names may mark
			// it handled: an older Job, listed later, must not undo it.
			if session != "" && signInWanted(agent) == session {
				s.HandledSession = session
			}
		case batchv1.JobFailed:
			if session != "" && signInWanted(agent) == session {
				s.HandledSession = session
			}
		}
	}
}

// Keep-alive. A sign-in agent that runs no task for a long time would never
// refresh its token, and OpenAI does not document how long an unused refresh
// token lives. So once the sign-in has gone unverified for
// SignInKeepAliveInterval, and nothing else uses the volume or is queued to,
// the operator runs check.mjs once: it refreshes the token if needed and
// exits 78 when the sign-in is dead, exactly like a task's pre-check.

const (
	credentialJobKeepAlive = "keepalive"

	// keepAliveDeadline bounds the check pod.
	keepAliveDeadline int64 = 300

	checkScript = "/usr/local/share/pi-extensions/ainsel-chatgpt-auth/check.mjs"
)

// keepAliveDue reports whether a working sign-in has gone unverified for
// interval. Zero turns the keep-alive off.
func keepAliveDue(agent *ainselv1alpha1.Agent, now time.Time, interval time.Duration) bool {
	if interval <= 0 || !agent.UsesChatGPTSignIn() {
		return false
	}
	s := agent.Status.ChatGPTSignIn
	if required, _ := signInRequired(s); required {
		return false
	}
	last := s.SignedInAt.Time
	if s.LastVerifiedAt != nil && s.LastVerifiedAt.After(last) {
		last = s.LastVerifiedAt.Time
	}
	return now.Sub(last) >= interval
}

// KeepAliveIntervalProblem rejects an interval the keep-alive cannot honour:
// it runs at most once per hour (see keepAliveJobName), so anything shorter
// would only keep it busy. Zero disables it.
func KeepAliveIntervalProblem(d time.Duration) string {
	if d < 0 || (d > 0 && d < time.Hour) {
		return fmt.Sprintf("sign-in keep-alive interval %s: use 0 (off) or at least 1h", d)
	}
	return ""
}

// keepAliveJobName is fixed per agent and hour, so a keep-alive that failed
// for an ordinary reason (network, OpenAI down) is tried again at most hourly
// rather than in a loop.
func keepAliveJobName(agentName string, now time.Time) string {
	suffix := fmt.Sprintf("%d", now.Unix()/3600)
	prefix := "keepalive-" + agentName
	if max := maxK8sNameLen - len(suffix) - 1; len(prefix) > max {
		prefix = strings.TrimSuffix(prefix[:max], "-")
	}
	return prefix + "-" + suffix
}

// buildKeepAliveJob renders the keep-alive Job: the check script, no hub
// access, and the sign-in failure rule so a dead sign-in fails it outright.
func buildKeepAliveJob(agent *ainselv1alpha1.Agent, deploy *appsv1.Deployment, now time.Time) *batchv1.Job {
	// Force the refresh: an access token that is still valid would otherwise
	// be read without ever using (and so keeping alive) the refresh token.
	env := []corev1.EnvVar{{Name: "AINSEL_FORCE_REFRESH", Value: "1"}}
	job := credentialPodJob(agent, deploy, keepAliveJobName(agent.Name, now), credentialJobKeepAlive, checkScript, env, keepAliveDeadline)
	job.Spec.PodFailurePolicy = signInFailurePolicy()
	return job
}

// recordVerified notes that a pod confirmed the sign-in at the Job's
// completion: a finished keep-alive, or a task whose pre-check passed.
func recordVerified(agent *ainselv1alpha1.Agent, j *batchv1.Job) {
	at, ok := completionTime(j)
	if !ok {
		return
	}
	s := agent.Status.ChatGPTSignIn
	if s == nil {
		s = &ainselv1alpha1.AgentChatGPTSignInStatus{}
		agent.Status.ChatGPTSignIn = s
	}
	if s.LastVerifiedAt == nil || at.After(s.LastVerifiedAt.Time) {
		s.LastVerifiedAt = &metav1.Time{Time: at}
	}
}

// completionTime reports when a Job completed successfully.
func completionTime(j *batchv1.Job) (time.Time, bool) {
	for _, c := range j.Status.Conditions {
		if c.Type != batchv1.JobComplete || c.Status != corev1.ConditionTrue {
			continue
		}
		if j.Status.CompletionTime != nil {
			return j.Status.CompletionTime.Time, true
		}
		if !c.LastTransitionTime.IsZero() {
			return c.LastTransitionTime.Time, true
		}
	}
	return time.Time{}, false
}
