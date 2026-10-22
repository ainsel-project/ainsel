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

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

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

var _ = Describe("Plugin Controller", func() {
	const (
		namespace  = "default"
		pluginName = "mem0"
	)

	ctx := context.Background()
	key := types.NamespacedName{Name: pluginName, Namespace: namespace}

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

	reconcilePlugin := func() ctrl.Result {
		r := &PluginReconciler{
			Client: k8sClient,
			Scheme: k8sClient.Scheme(),
			ManifestURL: func(p *commonv1alpha1.Plugin) string {
				return manifestSv.URL + "/ainsel-plugin.json"
			},
		}
		res, err := r.Reconcile(ctx, reconcile.Request{NamespacedName: key})
		Expect(err).NotTo(HaveOccurred())
		return res
	}

	reconcilePluginErr := func() (ctrl.Result, error) {
		r := &PluginReconciler{
			Client: k8sClient,
			Scheme: k8sClient.Scheme(),
			ManifestURL: func(p *commonv1alpha1.Plugin) string {
				return manifestSv.URL + "/ainsel-plugin.json"
			},
		}
		return r.Reconcile(ctx, reconcile.Request{NamespacedName: key})
	}

	getReady := func() *metav1.Condition {
		plugin := &commonv1alpha1.Plugin{}
		Expect(k8sClient.Get(ctx, key, plugin)).To(Succeed())
		return findCondition(plugin.Status.Conditions, commonv1alpha1.PluginConditionReady)
	}

	It("sets Ready=True and stores the manifest", func() {
		serveManifest(manifestJSON(pluginName, "0.1.0", "1.0", "agents:read"))
		Expect(k8sClient.Create(ctx, newPlugin(false, "agents:read"))).To(Succeed())
		reconcilePlugin()

		Eventually(func() bool {
			c := getReady()
			return c != nil && c.Status == metav1.ConditionTrue && c.Reason == commonv1alpha1.PluginReasonReady
		}, 10*time.Second, 100*time.Millisecond).Should(BeTrue())

		plugin := &commonv1alpha1.Plugin{}
		Expect(k8sClient.Get(ctx, key, plugin)).To(Succeed())
		Expect(plugin.Status.Version).To(Equal("0.1.0"))
		Expect(plugin.Status.DeclaredPermissions).To(Equal([]string{"agents:read"}))
		Expect(plugin.Status.Manifest).NotTo(BeNil())
		Expect(string(plugin.Status.Manifest.Raw)).To(ContainSubstring(`"version":"0.1.0"`))
		Expect(plugin.Status.ObservedGeneration).To(Equal(plugin.Generation))
	})

	It("reports PermissionsPending when declared permissions widen", func() {
		serveManifest(manifestJSON(pluginName, "0.1.0", "1.0", "agents:read", "personas:write"))
		Expect(k8sClient.Create(ctx, newPlugin(false, "agents:read"))).To(Succeed())
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
		plugin := &commonv1alpha1.Plugin{}
		Expect(k8sClient.Get(ctx, key, plugin)).To(Succeed())
		Expect(plugin.Status.DeclaredPermissions).To(Equal([]string{"agents:read", "personas:write"}))
	})

	It("reports Unreachable and keeps the last good manifest", func() {
		serveManifest(manifestJSON(pluginName, "0.1.0", "1.0", "agents:read"))
		Expect(k8sClient.Create(ctx, newPlugin(false, "agents:read"))).To(Succeed())
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

		plugin := &commonv1alpha1.Plugin{}
		Expect(k8sClient.Get(ctx, key, plugin)).To(Succeed())
		Expect(plugin.Status.Version).To(Equal("0.1.0"))
		Expect(plugin.Status.Manifest).NotTo(BeNil())
		Expect(res.RequeueAfter).To(Equal(60 * time.Second))
	})

	It("reports InvalidManifest on bad JSON and on id mismatch", func() {
		Expect(k8sClient.Create(ctx, newPlugin(false, "agents:read"))).To(Succeed())

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
		Expect(k8sClient.Create(ctx, newPlugin(false, "agents:read"))).To(Succeed())
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
		Expect(k8sClient.Create(ctx, newPlugin(false, "agents:read"))).To(Succeed())
		reconcilePlugin()

		Eventually(func() bool {
			c := getReady()
			return c != nil && c.Reason == commonv1alpha1.PluginReasonReady
		}, 10*time.Second, 100*time.Millisecond).Should(BeTrue())

		plugin := &commonv1alpha1.Plugin{}
		Expect(k8sClient.Get(ctx, key, plugin)).To(Succeed())
		rv := plugin.ResourceVersion
		transitions := findCondition(plugin.Status.Conditions,
			commonv1alpha1.PluginConditionReady).LastTransitionTime

		By("reconciling again with an unchanged manifest")
		reconcilePlugin()

		after := &commonv1alpha1.Plugin{}
		Expect(k8sClient.Get(ctx, key, after)).To(Succeed())
		Expect(after.ResourceVersion).To(Equal(rv), "status should not be re-patched")
		Expect(findCondition(after.Status.Conditions,
			commonv1alpha1.PluginConditionReady).LastTransitionTime).To(Equal(transitions))
	})

	It("builds the default manifest URL from the plugin service", func() {
		r := &PluginReconciler{}
		p := newPlugin(false)
		url := r.defaultManifestURL(p)
		Expect(url).To(Equal(fmt.Sprintf("http://plugin-mem0.%s.svc:8080/ainsel-plugin.json", namespace)))
	})
})
