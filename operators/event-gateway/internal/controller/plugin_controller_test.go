package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	discoveryv1 "k8s.io/api/discovery/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/ainsel-project/ainsel/operators/event-gateway/internal/signing"
	commonv1alpha1 "github.com/ainsel-project/ainsel/shared/api/api/v1alpha1"
)

// manifestJSON builds a minimal valid plugin manifest body for tests, with the
// id, version, pluginApi and permissions overridden per case.
func manifestJSON(id, version, pluginAPI string, permissions ...string) string {
	m := map[string]any{
		"id":          id,
		"displayName": "Mem0",
		"version":     version,
		"pluginApi":   pluginAPI,
	}
	if permissions != nil {
		m["permissions"] = permissions
	}
	data, err := json.Marshal(m)
	Expect(err).NotTo(HaveOccurred())
	return string(data)
}

// fakeVerifier is a test verifier that can be configured to return specific results.
type fakeVerifier struct {
	verified bool
	signer   string
	err      error
	callCnt  int
}

func (v *fakeVerifier) Verify(ctx context.Context, imageRef string) (signing.Result, error) {
	v.callCnt++
	if v.err != nil {
		return signing.Result{}, v.err
	}
	return signing.Result{Verified: v.verified, Signer: v.signer}, nil
}

// Test the map functions that convert EndpointSlices and Pods to Plugin requests
var _ = Describe("Plugin Controller Map Functions", func() {
	It("maps EndpointSlice to matching Plugin", func() {
		ctx := context.Background()
		reconciler := &PluginReconciler{Client: k8sClient, Scheme: k8sClient.Scheme()}

		// Create two Plugins
		digest := "sha256:" + sha256Hex("test")
		plugin1 := &commonv1alpha1.Plugin{
			ObjectMeta: metav1.ObjectMeta{Name: "plugin1", Namespace: "default"},
			Spec: commonv1alpha1.PluginSpec{
				Service: commonv1alpha1.PluginServiceRef{Name: "svc1", Port: 8080},
				Image: commonv1alpha1.PluginImage{
					Repository: "example.com/img",
					Digest:     digest,
				},
			},
		}
		plugin2 := &commonv1alpha1.Plugin{
			ObjectMeta: metav1.ObjectMeta{Name: "plugin2", Namespace: "default"},
			Spec: commonv1alpha1.PluginSpec{
				Service: commonv1alpha1.PluginServiceRef{Name: "svc2", Port: 8080},
				Image: commonv1alpha1.PluginImage{
					Repository: "example.com/img",
					Digest:     digest,
				},
			},
		}
		Expect(k8sClient.Create(ctx, plugin1)).To(Succeed())
		Expect(k8sClient.Create(ctx, plugin2)).To(Succeed())

		// Create EndpointSlice for svc1
		slice := &discoveryv1.EndpointSlice{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "svc1-slice",
				Namespace: "default",
				Labels: map[string]string{
					"kubernetes.io/service-name": "svc1",
				},
			},
			AddressType: discoveryv1.AddressTypeIPv4,
		}
		Expect(k8sClient.Create(ctx, slice)).To(Succeed())

		// Map should return only plugin1
		requests := reconciler.mapEndpointSliceToPlugin(ctx, slice)
		Expect(requests).To(HaveLen(1))
		Expect(requests[0].Name).To(Equal("plugin1"))
	})

	It("maps Pod to matching Plugin", func() {
		ctx := context.Background()
		reconciler := &PluginReconciler{Client: k8sClient, Scheme: k8sClient.Scheme()}

		// Create two Services with different selectors
		svc1 := &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{Name: "pod-map-svc1", Namespace: "default"},
			Spec: corev1.ServiceSpec{
				Selector: map[string]string{"app": "plugin-mem0"},
				Ports: []corev1.ServicePort{
					{
						Name:       "http",
						Port:       8080,
						Protocol:   corev1.ProtocolTCP,
						TargetPort: intstr.FromInt(8080),
					},
				},
			},
		}
		svc2 := &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{Name: "pod-map-svc2", Namespace: "default"},
			Spec: corev1.ServiceSpec{
				Selector: map[string]string{"app": "other"},
				Ports: []corev1.ServicePort{
					{
						Name:       "http",
						Port:       8080,
						Protocol:   corev1.ProtocolTCP,
						TargetPort: intstr.FromInt(8080),
					},
				},
			},
		}
		Expect(k8sClient.Create(ctx, svc1)).To(Succeed())
		Expect(k8sClient.Create(ctx, svc2)).To(Succeed())
		defer func() {
			Expect(k8sClient.Delete(ctx, svc1)).To(Succeed())
			Expect(k8sClient.Delete(ctx, svc2)).To(Succeed())
		}()

		// Create two Plugins referencing the two services
		digest := "sha256:" + sha256Hex("test")
		plugin1 := &commonv1alpha1.Plugin{
			ObjectMeta: metav1.ObjectMeta{Name: "plugin-map-1", Namespace: "default"},
			Spec: commonv1alpha1.PluginSpec{
				Service: commonv1alpha1.PluginServiceRef{Name: "pod-map-svc1", Port: 8080},
				Image: commonv1alpha1.PluginImage{
					Repository: "example.com/img",
					Digest:     digest,
				},
			},
		}
		plugin2 := &commonv1alpha1.Plugin{
			ObjectMeta: metav1.ObjectMeta{Name: "plugin-map-2", Namespace: "default"},
			Spec: commonv1alpha1.PluginSpec{
				Service: commonv1alpha1.PluginServiceRef{Name: "pod-map-svc2", Port: 8080},
				Image: commonv1alpha1.PluginImage{
					Repository: "example.com/img",
					Digest:     digest,
				},
			},
		}
		Expect(k8sClient.Create(ctx, plugin1)).To(Succeed())
		Expect(k8sClient.Create(ctx, plugin2)).To(Succeed())
		defer func() {
			Expect(k8sClient.Delete(ctx, plugin1)).To(Succeed())
			Expect(k8sClient.Delete(ctx, plugin2)).To(Succeed())
		}()

		// Create a Pod labeled with the first service's selector
		pod1 := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "pod-matching",
				Namespace: "default",
				Labels:    map[string]string{"app": "plugin-mem0"},
			},
			Spec: corev1.PodSpec{
				Containers: []corev1.Container{
					{
						Name:  "app",
						Image: "example.com/app:latest",
					},
				},
			},
		}
		Expect(k8sClient.Create(ctx, pod1)).To(Succeed())
		defer func() {
			Expect(k8sClient.Delete(ctx, pod1)).To(Succeed())
		}()

		// Map should return only plugin-map-1
		requests := reconciler.mapPodToPlugin(ctx, pod1)
		Expect(requests).To(HaveLen(1))
		Expect(requests[0].Name).To(Equal("plugin-map-1"))

		// Create a Pod with unrelated labels
		pod2 := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "pod-unrelated",
				Namespace: "default",
				Labels:    map[string]string{"app": "unrelated"},
			},
			Spec: corev1.PodSpec{
				Containers: []corev1.Container{
					{
						Name:  "app",
						Image: "example.com/app:latest",
					},
				},
			},
		}
		Expect(k8sClient.Create(ctx, pod2)).To(Succeed())
		defer func() {
			Expect(k8sClient.Delete(ctx, pod2)).To(Succeed())
		}()

		// Map should return no requests for unrelated pod
		requests = reconciler.mapPodToPlugin(ctx, pod2)
		Expect(requests).To(HaveLen(0))
	})
})

var _ = Describe("Plugin Controller", func() {
	const (
		namespace  = "default"
		pluginName = "mem0"
	)

	ctx := context.Background()
	key := types.NamespacedName{Name: pluginName, Namespace: namespace}

	// ptrBool returns a pointer to a boolean value.
	ptrBool := func(b bool) *bool { return &b }

	// The manifest server is stubbed with a swappable handler so each spec can
	// decide what the plugin "serves" at reconcile time.
	var (
		handler    atomic.Value // stores func(w http.ResponseWriter, r *http.Request)
		hitCount   atomic.Int64
		manifestSv *httptest.Server
	)

	serveManifest := func(body string) {
		handler.Store(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(body))
		})
	}
	serveStatus := func(status int) {
		handler.Store(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(status)
		})
	}

	BeforeEach(func() {
		hitCount.Store(0)
		handler.Store(func(w http.ResponseWriter, r *http.Request) {
			http.NotFound(w, r)
		})
		manifestSv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			hitCount.Add(1)
			handler.Load().(func(http.ResponseWriter, *http.Request))(w, r)
		}))
	})

	AfterEach(func() {
		manifestSv.Close()

		plugin := &commonv1alpha1.Plugin{}
		if err := k8sClient.Get(ctx, key, plugin); err == nil {
			Expect(k8sClient.Delete(ctx, plugin)).To(Succeed())
		}

		// Clean up service and endpoint slices
		svc := &corev1.Service{}
		if err := k8sClient.Get(ctx, types.NamespacedName{Name: "plugin-mem0", Namespace: namespace}, svc); err == nil {
			Expect(k8sClient.Delete(ctx, svc)).To(Succeed())
		}

		epsList := &discoveryv1.EndpointSliceList{}
		if err := k8sClient.List(ctx, epsList, client.InNamespace(namespace)); err == nil {
			for i := range epsList.Items {
				Expect(k8sClient.Delete(ctx, &epsList.Items[i])).To(Succeed())
			}
		}

		// Clean up pods
		podList := &corev1.PodList{}
		if err := k8sClient.List(ctx, podList, client.InNamespace(namespace)); err == nil {
			for i := range podList.Items {
				Expect(k8sClient.Delete(ctx, &podList.Items[i])).To(Succeed())
			}
		}
	})

	newPlugin := func(disabled bool, approved ...string) *commonv1alpha1.Plugin {
		return &commonv1alpha1.Plugin{
			ObjectMeta: metav1.ObjectMeta{
				Name:      pluginName,
				Namespace: namespace,
			},
			Spec: commonv1alpha1.PluginSpec{
				DisplayName: "Mem0",
				Service: commonv1alpha1.PluginServiceRef{
					Name: "plugin-mem0",
					Port: 8080,
				},
				Image: commonv1alpha1.PluginImage{
					Repository: "example.com/plugin-mem0",
					Digest:     "sha256:" + sha256Hex("mem0"),
				},
				Disabled:            disabled,
				ApprovedPermissions: approved,
			},
		}
	}

	// Helper to create a Service for the plugin
	createService := func() {
		svc := &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "plugin-mem0",
				Namespace: namespace,
			},
			Spec: corev1.ServiceSpec{
				Selector: map[string]string{"app": "plugin-mem0"},
				Ports: []corev1.ServicePort{
					{
						Name:       "http",
						Port:       8080,
						Protocol:   corev1.ProtocolTCP,
						TargetPort: intstr.FromInt(8080),
					},
				},
			},
		}
		Expect(k8sClient.Create(ctx, svc)).To(Succeed())
	}

	// Helper to create a Pod running the plugin
	createPodWithImageID := func(name, imageID string) {
		pod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: namespace,
				Labels:    map[string]string{"app": "plugin-mem0"},
			},
			Spec: corev1.PodSpec{
				Containers: []corev1.Container{
					{
						Name:  "plugin",
						Image: "example.com/plugin-mem0@" + imageID,
					},
				},
			},
		}
		Expect(k8sClient.Create(ctx, pod)).To(Succeed())

		// Set the pod status with containerStatuses[].imageID
		// The imageID should include the repository and @ separator as kubelet reports it
		pod.Status.ContainerStatuses = []corev1.ContainerStatus{
			{
				Name:    "plugin",
				ImageID: "example.com/plugin-mem0@" + imageID,
			},
		}
		Expect(k8sClient.Status().Update(ctx, pod)).To(Succeed())
	}

	// Helper to create an EndpointSlice for the service
	createEndpointSlice := func(endpoints []discoveryv1.Endpoint) {
		portNum := int32(8080)
		portName := "http"
		eps := &discoveryv1.EndpointSlice{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "plugin-mem0-1",
				Namespace: namespace,
				Labels: map[string]string{
					"kubernetes.io/service-name": "plugin-mem0",
				},
			},
			AddressType: discoveryv1.AddressTypeIPv4,
			Endpoints:   endpoints,
			Ports: []discoveryv1.EndpointPort{
				{
					Name:     &portName,
					Port:     &portNum,
					Protocol: (*corev1.Protocol)(ptrString("TCP")),
				},
			},
		}
		Expect(k8sClient.Create(ctx, eps)).To(Succeed())
	}

	// Helper to create a complete verified backend (Service, Pod, EndpointSlice)
	createVerifiedBackend := func(digest string) string {
		createService()
		createPodWithImageID("plugin-mem0-pod", digest)

		pod := &corev1.Pod{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "plugin-mem0-pod", Namespace: namespace}, pod)).To(Succeed())

		podIP := "10.0.0.1"
		pod.Status.PodIP = podIP
		Expect(k8sClient.Status().Update(ctx, pod)).To(Succeed())

		endpoints := []discoveryv1.Endpoint{
			{
				Addresses: []string{podIP},
				TargetRef: &corev1.ObjectReference{
					Kind:      "Pod",
					Name:      "plugin-mem0-pod",
					Namespace: namespace,
				},
				Conditions: discoveryv1.EndpointConditions{Ready: ptrBool(true)},
			},
		}
		createEndpointSlice(endpoints)

		return podIP + ":8080"
	}

	reconcilePluginWithOptionsErr := func(verifier signing.Verifier, policy *signing.Policy, now func() time.Time) (ctrl.Result, error) {
		if now == nil {
			now = time.Now
		}
		r := &PluginReconciler{
			Client: k8sClient,
			Scheme: k8sClient.Scheme(),
			ManifestURL: func(p *commonv1alpha1.Plugin, endpoint string) string {
				// Map the fake endpoint to the test server URL
				if endpoint == "10.0.0.1:8080" {
					return manifestSv.URL + "/ainsel-plugin.json"
				}
				return "http://" + endpoint + "/ainsel-plugin.json"
			},
			Verifier:      verifier,
			SigningPolicy: policy,
			Now:           now,
		}
		return r.Reconcile(ctx, reconcile.Request{NamespacedName: key})
	}

	reconcilePluginWithOptions := func(verifier signing.Verifier, policy *signing.Policy, now func() time.Time) ctrl.Result {
		res, err := reconcilePluginWithOptionsErr(verifier, policy, now)
		Expect(err).NotTo(HaveOccurred())
		return res
	}

	reconcilePlugin := func() ctrl.Result {
		return reconcilePluginWithOptions(nil, nil, time.Now)
	}

	reconcilePluginErr := func() (ctrl.Result, error) {
		return reconcilePluginWithOptionsErr(nil, nil, time.Now)
	}

	getReady := func() *metav1.Condition {
		plugin := &commonv1alpha1.Plugin{}
		Expect(k8sClient.Get(ctx, key, plugin)).To(Succeed())
		return findCondition(plugin.Status.Conditions, commonv1alpha1.PluginConditionReady)
	}

	It("sets Ready=True and stores the manifest", func() {
		serveManifest(manifestJSON(pluginName, "0.1.0", "1.0", "agents:read"))
		plugin := newPlugin(false, "agents:read")
		Expect(k8sClient.Create(ctx, plugin)).To(Succeed())
		createVerifiedBackend(plugin.Spec.Image.Digest)
		reconcilePlugin()

		Eventually(func() bool {
			c := getReady()
			return c != nil && c.Status == metav1.ConditionTrue && c.Reason == commonv1alpha1.PluginReasonReady
		}, 10*time.Second, 100*time.Millisecond).Should(BeTrue())

		p := &commonv1alpha1.Plugin{}
		Expect(k8sClient.Get(ctx, key, p)).To(Succeed())
		Expect(p.Status.Version).To(Equal("0.1.0"))
		Expect(p.Status.DeclaredPermissions).To(Equal([]string{"agents:read"}))
		Expect(p.Status.Manifest).NotTo(BeNil())
		Expect(string(p.Status.Manifest.Raw)).To(ContainSubstring(`"version":"0.1.0"`))
		Expect(p.Status.ObservedGeneration).To(Equal(p.Generation))
	})

	It("reports PermissionsPending when declared permissions widen", func() {
		serveManifest(manifestJSON(pluginName, "0.1.0", "1.0", "agents:read", "personas:write"))
		plugin := newPlugin(false, "agents:read")
		Expect(k8sClient.Create(ctx, plugin)).To(Succeed())
		createVerifiedBackend(plugin.Spec.Image.Digest)
		reconcilePlugin()

		Eventually(func() bool {
			c := getReady()
			return c != nil && c.Status == metav1.ConditionFalse &&
				c.Reason == commonv1alpha1.PluginReasonPermissionsPending
		}, 10*time.Second, 100*time.Millisecond).Should(BeTrue())

		c := getReady()
		Expect(c.Message).To(ContainSubstring("personas:write"))

		// Declared permissions are still recorded so the console can show
		// what is waiting for approval.
		p := &commonv1alpha1.Plugin{}
		Expect(k8sClient.Get(ctx, key, p)).To(Succeed())
		Expect(p.Status.DeclaredPermissions).To(Equal([]string{"agents:read", "personas:write"}))
	})

	It("reports Unreachable and keeps the last good manifest", func() {
		serveManifest(manifestJSON(pluginName, "0.1.0", "1.0", "agents:read"))
		plugin := newPlugin(false, "agents:read")
		Expect(k8sClient.Create(ctx, plugin)).To(Succeed())
		createVerifiedBackend(plugin.Spec.Image.Digest)
		reconcilePlugin()

		Eventually(func() bool {
			c := getReady()
			return c != nil && c.Reason == commonv1alpha1.PluginReasonReady
		}, 10*time.Second, 100*time.Millisecond).Should(BeTrue())

		By("making the manifest endpoint fail")
		serveStatus(http.StatusServiceUnavailable)
		res := reconcilePlugin()

		c := getReady()
		Expect(c.Status).To(Equal(metav1.ConditionFalse))
		Expect(c.Reason).To(Equal(commonv1alpha1.PluginReasonUnreachable))

		p := &commonv1alpha1.Plugin{}
		Expect(k8sClient.Get(ctx, key, p)).To(Succeed())
		Expect(p.Status.Version).To(Equal("0.1.0"))
		Expect(p.Status.Manifest).NotTo(BeNil())
		Expect(res.RequeueAfter).To(Equal(60 * time.Second))
	})

	It("reports InvalidManifest on bad JSON and on id mismatch", func() {
		plugin := newPlugin(false, "agents:read")
		Expect(k8sClient.Create(ctx, plugin)).To(Succeed())
		createVerifiedBackend(plugin.Spec.Image.Digest)

		By("serving a body that is not JSON")
		serveManifest("not json")
		reconcilePlugin()

		Eventually(func() bool {
			c := getReady()
			return c != nil && c.Status == metav1.ConditionFalse &&
				c.Reason == commonv1alpha1.PluginReasonInvalidManifest
		}, 10*time.Second, 100*time.Millisecond).Should(BeTrue())

		By("serving a manifest whose id does not match the CR name")
		serveManifest(manifestJSON("someone-else", "0.1.0", "1.0"))
		reconcilePlugin()

		Eventually(func() bool {
			c := getReady()
			return c != nil && c.Reason == commonv1alpha1.PluginReasonInvalidManifest &&
				c.Message != ""
		}, 10*time.Second, 100*time.Millisecond).Should(BeTrue())

		c := getReady()
		Expect(c.Message).To(ContainSubstring("manifest id"))
		Expect(c.Message).To(ContainSubstring("does not match"))
	})

	It("reports Incompatible for pluginApi 2.0", func() {
		serveManifest(manifestJSON(pluginName, "0.1.0", "2.0", "agents:read"))
		plugin := newPlugin(false, "agents:read")
		Expect(k8sClient.Create(ctx, plugin)).To(Succeed())
		createVerifiedBackend(plugin.Spec.Image.Digest)
		reconcilePlugin()

		Eventually(func() bool {
			c := getReady()
			return c != nil && c.Status == metav1.ConditionFalse &&
				c.Reason == commonv1alpha1.PluginReasonIncompatible
		}, 10*time.Second, 100*time.Millisecond).Should(BeTrue())
	})

	It("skips fetching when disabled", func() {
		serveManifest(manifestJSON(pluginName, "0.1.0", "1.0", "agents:read"))
		Expect(k8sClient.Create(ctx, newPlugin(true, "agents:read"))).To(Succeed())
		reconcilePlugin()

		Eventually(func() bool {
			c := getReady()
			return c != nil && c.Status == metav1.ConditionFalse &&
				c.Reason == commonv1alpha1.PluginReasonDisabled
		}, 10*time.Second, 100*time.Millisecond).Should(BeTrue())

		Expect(hitCount.Load()).To(BeZero())
	})

	It("returns without error when the plugin does not exist", func() {
		_, err := reconcilePluginErr()
		Expect(err).NotTo(HaveOccurred())
	})

	It("does not rewrite status when nothing changed", func() {
		serveManifest(manifestJSON(pluginName, "0.1.0", "1.0", "agents:read"))
		plugin := newPlugin(false, "agents:read")
		Expect(k8sClient.Create(ctx, plugin)).To(Succeed())
		createVerifiedBackend(plugin.Spec.Image.Digest)
		reconcilePlugin()

		Eventually(func() bool {
			c := getReady()
			return c != nil && c.Reason == commonv1alpha1.PluginReasonReady
		}, 10*time.Second, 100*time.Millisecond).Should(BeTrue())

		p := &commonv1alpha1.Plugin{}
		Expect(k8sClient.Get(ctx, key, p)).To(Succeed())
		rv := p.ResourceVersion
		transitions := findCondition(p.Status.Conditions,
			commonv1alpha1.PluginConditionReady).LastTransitionTime

		By("reconciling again with an unchanged manifest")
		reconcilePlugin()

		after := &commonv1alpha1.Plugin{}
		Expect(k8sClient.Get(ctx, key, after)).To(Succeed())
		Expect(after.ResourceVersion).To(Equal(rv), "status should not be re-patched")
		Expect(findCondition(after.Status.Conditions,
			commonv1alpha1.PluginConditionReady).LastTransitionTime).To(Equal(transitions))
	})

	It("builds the manifest URL from endpoint", func() {
		r := &PluginReconciler{}
		p := newPlugin(false)
		url := r.defaultManifestURL(p, "10.0.0.1:8080")
		Expect(url).To(Equal("http://10.0.0.1:8080/ainsel-plugin.json"))
	})

	It("rejects an unsigned plugin when signatures are required", func() {
		serveManifest(manifestJSON(pluginName, "0.1.0", "1.0", "agents:read"))
		plugin := newPlugin(false, "agents:read")
		Expect(k8sClient.Create(ctx, plugin)).To(Succeed())
		// Don't create backend - we expect to fail on signature before reaching endpoints

		policy := &signing.Policy{RequireSignature: true}
		verifier := &fakeVerifier{verified: false}

		reconcilePluginWithOptions(verifier, policy, time.Now)

		Eventually(func() bool {
			c := getReady()
			return c != nil && c.Status == metav1.ConditionFalse &&
				c.Reason == commonv1alpha1.PluginReasonSignatureRejected
		}, 10*time.Second, 100*time.Millisecond).Should(BeTrue())

		Expect(hitCount.Load()).To(BeZero(), "manifest should not be fetched for rejected signature")
	})

	It("allows an unsigned plugin when not required and records verified=false", func() {
		serveManifest(manifestJSON(pluginName, "0.1.0", "1.0", "agents:read"))
		plugin := newPlugin(false, "agents:read")
		Expect(k8sClient.Create(ctx, plugin)).To(Succeed())
		createVerifiedBackend(plugin.Spec.Image.Digest)

		// Policy with no trust material and requireSignature=false
		policy := &signing.Policy{RequireSignature: false}
		verifier := &fakeVerifier{}

		reconcilePluginWithOptions(verifier, policy, time.Now)

		Eventually(func() bool {
			c := getReady()
			return c != nil && c.Status == metav1.ConditionTrue &&
				c.Reason == commonv1alpha1.PluginReasonReady
		}, 10*time.Second, 100*time.Millisecond).Should(BeTrue())

		p := &commonv1alpha1.Plugin{}
		Expect(k8sClient.Get(ctx, key, p)).To(Succeed())
		Expect(p.Status.Signature).NotTo(BeNil())
		Expect(p.Status.Signature.Verified).To(BeFalse())
		Expect(p.Status.Signature.Message).To(ContainSubstring("no signing trust"))
	})

	It("fails closed on an invalid policy", func() {
		serveManifest(manifestJSON(pluginName, "0.1.0", "1.0", "agents:read"))
		plugin := newPlugin(false, "agents:read")
		Expect(k8sClient.Create(ctx, plugin)).To(Succeed())
		// Don't create backend - we expect to fail on policy error before reaching endpoints

		policy := &signing.Policy{RequireSignature: true}
		policyErr := fmt.Errorf("invalid signing policy: bad config")

		r := &PluginReconciler{
			Client: k8sClient,
			Scheme: k8sClient.Scheme(),
			ManifestURL: func(p *commonv1alpha1.Plugin, endpoint string) string {
				return manifestSv.URL + "/ainsel-plugin.json"
			},
			SigningPolicy:    policy,
			SigningPolicyErr: policyErr,
			Now:              time.Now,
		}
		_, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: key})
		Expect(err).NotTo(HaveOccurred())

		Eventually(func() bool {
			c := getReady()
			return c != nil && c.Status == metav1.ConditionFalse &&
				c.Reason == commonv1alpha1.PluginReasonSignatureRejected
		}, 10*time.Second, 100*time.Millisecond).Should(BeTrue())

		c := getReady()
		Expect(c.Message).To(ContainSubstring("invalid signing policy"))
	})

	It("reuses the signature result until image, policy or 24h changes", func() {
		serveManifest(manifestJSON(pluginName, "0.1.0", "1.0", "agents:read"))
		plugin := newPlugin(false, "agents:read")
		Expect(k8sClient.Create(ctx, plugin)).To(Succeed())
		createVerifiedBackend(plugin.Spec.Image.Digest)

		// Policy with trust material so verifier gets called
		policy := &signing.Policy{
			RequireSignature: false,
			Keys:             []string{"dummy-key"},
		}
		verifier := &fakeVerifier{verified: true, signer: "key:abc123"}
		now := time.Now()
		nowFunc := func() time.Time { return now }

		// First reconcile
		reconcilePluginWithOptions(verifier, policy, nowFunc)
		Eventually(func() bool {
			c := getReady()
			return c != nil && c.Reason == commonv1alpha1.PluginReasonReady
		}, 10*time.Second, 100*time.Millisecond).Should(BeTrue())
		Expect(verifier.callCnt).To(Equal(1))

		// Second reconcile with same state - verifier should not be called again
		reconcilePluginWithOptions(verifier, policy, nowFunc)
		Expect(verifier.callCnt).To(Equal(1))

		// Change the policy hash - verifier should be called again
		policy.RequireSignature = true // Change policy
		reconcilePluginWithOptions(verifier, policy, nowFunc)
		Expect(verifier.callCnt).To(Equal(2))

		// Advance time by 25 hours - verifier should be called again
		verifier.callCnt = 0
		nowFunc = func() time.Time { return now.Add(25 * time.Hour) }
		reconcilePluginWithOptions(verifier, policy, nowFunc)
		Expect(verifier.callCnt).To(Equal(1))
	})

	It("re-verifies after 24h", func() {
		serveManifest(manifestJSON(pluginName, "0.1.0", "1.0", "agents:read"))
		plugin := newPlugin(false, "agents:read")
		Expect(k8sClient.Create(ctx, plugin)).To(Succeed())
		createVerifiedBackend(plugin.Spec.Image.Digest)

		// Policy with trust material so verifier gets called
		policy := &signing.Policy{
			RequireSignature: false,
			Keys:             []string{"dummy-key"},
		}
		verifier := &fakeVerifier{verified: true, signer: "key:xyz"}
		now := time.Now()
		nowFunc := func() time.Time { return now }

		// First reconcile
		reconcilePluginWithOptions(verifier, policy, nowFunc)
		Eventually(func() bool {
			c := getReady()
			return c != nil && c.Reason == commonv1alpha1.PluginReasonReady
		}, 10*time.Second, 100*time.Millisecond).Should(BeTrue())
		Expect(verifier.callCnt).To(Equal(1))

		// Second reconcile within 24h - reuses cache
		reconcilePluginWithOptions(verifier, policy, nowFunc)
		Expect(verifier.callCnt).To(Equal(1), "should not re-verify within 24h")

		// Advance time past 24h - should re-verify
		verifier.callCnt = 0
		nowFunc = func() time.Time { return now.Add(25 * time.Hour) }
		reconcilePluginWithOptions(verifier, policy, nowFunc)
		Expect(verifier.callCnt).To(Equal(1), "should re-verify after 24h")
	})

	It("lists only endpoints running the verified digest", func() {
		digest := "sha256:" + sha256Hex("mem0")
		serveManifest(manifestJSON(pluginName, "0.1.0", "1.0", "agents:read"))

		plugin := newPlugin(false, "agents:read")
		Expect(k8sClient.Create(ctx, plugin)).To(Succeed())

		createService()
		createPodWithImageID("plugin-pod-1", digest)
		createPodWithImageID("plugin-pod-2", "sha256:wrongdigest")

		// Create endpoint slice with two endpoints
		pod1 := &corev1.Pod{}
		pod2 := &corev1.Pod{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "plugin-pod-1", Namespace: namespace}, pod1)).To(Succeed())
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "plugin-pod-2", Namespace: namespace}, pod2)).To(Succeed())

		pod1IP := "10.0.0.1"
		pod2IP := "10.0.0.2"
		pod1.Status.PodIP = pod1IP
		pod2.Status.PodIP = pod2IP
		Expect(k8sClient.Status().Update(ctx, pod1)).To(Succeed())
		Expect(k8sClient.Status().Update(ctx, pod2)).To(Succeed())

		endpoints := []discoveryv1.Endpoint{
			{
				Addresses: []string{pod1IP},
				TargetRef: &corev1.ObjectReference{
					Kind:      "Pod",
					Name:      "plugin-pod-1",
					Namespace: namespace,
				},
				Conditions: discoveryv1.EndpointConditions{Ready: ptrBool(true)},
			},
			{
				Addresses: []string{pod2IP},
				TargetRef: &corev1.ObjectReference{
					Kind:      "Pod",
					Name:      "plugin-pod-2",
					Namespace: namespace,
				},
				Conditions: discoveryv1.EndpointConditions{Ready: ptrBool(true)},
			},
		}
		createEndpointSlice(endpoints)

		policy := &signing.Policy{RequireSignature: false}
		verifier := &fakeVerifier{verified: true, signer: "key:abc123"}

		reconcilePluginWithOptions(verifier, policy, time.Now)

		Eventually(func() bool {
			c := getReady()
			return c != nil && c.Reason == commonv1alpha1.PluginReasonReady
		}, 10*time.Second, 100*time.Millisecond).Should(BeTrue())

		p := &commonv1alpha1.Plugin{}
		Expect(k8sClient.Get(ctx, key, p)).To(Succeed())
		Expect(p.Status.VerifiedEndpoints).To(Equal([]string{pod1IP + ":8080"}))
	})

	It("reports NoVerifiedEndpoints when no pod runs the digest", func() {
		serveManifest(manifestJSON(pluginName, "0.1.0", "1.0", "agents:read"))

		plugin := newPlugin(false, "agents:read")
		Expect(k8sClient.Create(ctx, plugin)).To(Succeed())

		createService()
		createPodWithImageID("plugin-pod-1", "sha256:wrongdigest")

		pod1 := &corev1.Pod{}
		Expect(k8sClient.Get(ctx, types.NamespacedName{Name: "plugin-pod-1", Namespace: namespace}, pod1)).To(Succeed())
		pod1IP := "10.0.0.1"
		pod1.Status.PodIP = pod1IP
		Expect(k8sClient.Status().Update(ctx, pod1)).To(Succeed())

		endpoints := []discoveryv1.Endpoint{
			{
				Addresses: []string{pod1IP},
				TargetRef: &corev1.ObjectReference{
					Kind:      "Pod",
					Name:      "plugin-pod-1",
					Namespace: namespace,
				},
				Conditions: discoveryv1.EndpointConditions{Ready: ptrBool(true)},
			},
		}
		createEndpointSlice(endpoints)

		policy := &signing.Policy{RequireSignature: false}
		verifier := &fakeVerifier{verified: true, signer: "key:abc123"}

		reconcilePluginWithOptions(verifier, policy, time.Now)

		Eventually(func() bool {
			c := getReady()
			return c != nil && c.Reason == commonv1alpha1.PluginReasonNoVerifiedEndpoints
		}, 10*time.Second, 100*time.Millisecond).Should(BeTrue())
	})

	It("surfaces verifier network errors as SignatureRejected when required and retries at resync", func() {
		serveManifest(manifestJSON(pluginName, "0.1.0", "1.0", "agents:read"))
		Expect(k8sClient.Create(ctx, newPlugin(false, "agents:read"))).To(Succeed())

		policy := &signing.Policy{RequireSignature: true}
		verifier := &fakeVerifier{err: fmt.Errorf("network error: connection refused")}

		res := reconcilePluginWithOptions(verifier, policy, time.Now)

		Eventually(func() bool {
			c := getReady()
			return c != nil && c.Status == metav1.ConditionFalse &&
				c.Reason == commonv1alpha1.PluginReasonSignatureRejected
		}, 10*time.Second, 100*time.Millisecond).Should(BeTrue())

		c := getReady()
		Expect(c.Message).To(ContainSubstring("network error"))
		Expect(res.RequeueAfter).To(Equal(60 * time.Second))
	})
})
