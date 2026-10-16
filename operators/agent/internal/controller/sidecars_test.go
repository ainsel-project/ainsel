package controller

import (
	"context"
	"os"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	ainselv1alpha1 "github.com/ainsel-project/ainsel/shared/api/api/v1alpha1"
)

var _ = Describe("Agent Controller", func() {
	Context("When composing sidecar containers", func() {
		const (
			resourceName  = fixtureAgentName
			testImageName = fixtureImageName
			testImageURL  = fixtureImageURL
		)

		ctx := context.Background()

		typeNamespacedName := types.NamespacedName{
			Name:      resourceName,
			Namespace: "default",
		}

		BeforeEach(func() {
			ensureAgentFixtures(ctx)
		})

		AfterEach(func() {
			cleanupAgentFixtures(ctx)
		})

		It("should inject sidecar containers from AgentImage spec", func() {
			agentName := "agent-" + resourceName
			By("Updating the AgentImage to include a chat sidecar")
			img := &ainselv1alpha1.AgentImage{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: testImageName, Namespace: "default"}, img)).To(Succeed())
			img.Spec.Sidecars = []ainselv1alpha1.AgentImageSidecar{
				{
					Name:    "chat",
					Image:   "dpinsel/ainsel-chat-mcp:main",
					Port:    8081,
					MCPPath: "/mcp",
				},
			}
			Expect(k8sClient.Update(ctx, img)).To(Succeed())

			By("Triggering reconciliation")
			controllerReconciler := &AgentReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
			}
			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())

			By("Verifying the Deployment has two containers")
			deploy := &appsv1.Deployment{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{
				Name:      agentName,
				Namespace: "default",
			}, deploy)).To(Succeed())
			Expect(deploy.Spec.Template.Spec.Containers).To(HaveLen(2))
			Expect(deploy.Spec.Template.Spec.Containers[0].Name).To(Equal("agent"))
			Expect(deploy.Spec.Template.Spec.Containers[1].Name).To(Equal("chat"))
			Expect(deploy.Spec.Template.Spec.Containers[1].Image).To(Equal("dpinsel/ainsel-chat-mcp:main"))

			By("Verifying AGENT_NAME is propagated to the sidecar")
			sidecarEnv := deploy.Spec.Template.Spec.Containers[1].Env
			sidecarAgentName := findEnvVar(sidecarEnv, "AGENT_NAME")
			Expect(sidecarAgentName).NotTo(BeNil())
			Expect(sidecarAgentName.Value).To(Equal(resourceName))

			By("Verifying PORT is set on the sidecar")
			sidecarPort := findEnvVar(sidecarEnv, "PORT")
			Expect(sidecarPort).NotTo(BeNil())
			Expect(sidecarPort.Value).To(Equal("8081"))

			By("Verifying MCP_SERVERS includes the sidecar localhost entry")
			mainEnv := deploy.Spec.Template.Spec.Containers[0].Env
			mcpServers := findEnvVar(mainEnv, "MCP_SERVERS")
			Expect(mcpServers).NotTo(BeNil())
			Expect(mcpServers.Value).To(ContainSubstring("chat=http://localhost:8081/mcp"))

			By("Verifying platform env vars (HUB_URL, HUB_INTERNAL_VALIDATE_SECRET) are injected into MCP sidecars")
			// These are injected by the operator from its own environment so
			// the chat-mcp sidecar can reach the hub without per-AgentImage
			// manual configuration. When the operator's env is unset the
			// vars are simply absent (not injected with empty values).
			if hubURL := findEnvVar(sidecarEnv, "HUB_URL"); hubURL != nil {
				Expect(hubURL.Value).To(Equal(resolveHubURL()))
			}
			if hubToken := findEnvVar(sidecarEnv, "HUB_INTERNAL_VALIDATE_SECRET"); hubToken != nil {
				Expect(hubToken.Value).To(Equal(resolveHubInternalToken()))
			}
			// Verify no HUB_URL is injected when the operator env is unset.
			if resolveHubURL() == "" {
				Expect(findEnvVar(sidecarEnv, "HUB_URL")).To(BeNil())
			}
			if resolveHubInternalToken() == "" {
				Expect(findEnvVar(sidecarEnv, "HUB_INTERNAL_VALIDATE_SECRET")).To(BeNil())
			}

			By("Cleaning up — removing sidecars from the image")
			Expect(k8sClient.Get(ctx, types.NamespacedName{Name: testImageName, Namespace: "default"}, img)).To(Succeed())
			img.Spec.Sidecars = nil
			Expect(k8sClient.Update(ctx, img)).To(Succeed())
			_, err = controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())
		})

		It("should auto-inject the chat-mcp sidecar when CHAT_MCP_IMAGE is set", func() {
			agentName := "agent-" + resourceName
			By("Setting CHAT_MCP_IMAGE in the operator environment")
			originalChatImage := os.Getenv("CHAT_MCP_IMAGE")
			Expect(os.Setenv("CHAT_MCP_IMAGE", "dpinsel/ainsel-chat-mcp:auto")).To(Succeed())
			DeferCleanup(func() {
				if originalChatImage == "" {
					Expect(os.Unsetenv("CHAT_MCP_IMAGE")).To(Succeed())
				} else {
					Expect(os.Setenv("CHAT_MCP_IMAGE", originalChatImage)).To(Succeed())
				}
			})

			By("Triggering reconciliation")
			controllerReconciler := &AgentReconciler{
				Client: k8sClient,
				Scheme: k8sClient.Scheme(),
			}
			_, err := controllerReconciler.Reconcile(ctx, reconcile.Request{
				NamespacedName: typeNamespacedName,
			})
			Expect(err).NotTo(HaveOccurred())

			By("Verifying the Deployment has two containers")
			deploy := &appsv1.Deployment{}
			Expect(k8sClient.Get(ctx, types.NamespacedName{
				Name:      agentName,
				Namespace: "default",
			}, deploy)).To(Succeed())
			Expect(deploy.Spec.Template.Spec.Containers).To(HaveLen(2))
			Expect(deploy.Spec.Template.Spec.Containers[0].Name).To(Equal("agent"))
			Expect(deploy.Spec.Template.Spec.Containers[1].Name).To(Equal("chat"))
			Expect(deploy.Spec.Template.Spec.Containers[1].Image).To(Equal("dpinsel/ainsel-chat-mcp:auto"))

			By("Verifying MCP_SERVERS includes the chat sidecar entry")
			mainEnv := deploy.Spec.Template.Spec.Containers[0].Env
			mcpServers := findEnvVar(mainEnv, "MCP_SERVERS")
			Expect(mcpServers).NotTo(BeNil())
			Expect(mcpServers.Value).To(ContainSubstring("chat=http://localhost:8081/mcp"))
		})
	})
})
