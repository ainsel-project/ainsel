package controller

import (
	"fmt"
	"os"

	corev1 "k8s.io/api/core/v1"

	ainselv1alpha1 "github.com/ainsel-project/ainsel/shared/api/api/v1alpha1"
)

// resolveChatMCPImage returns the container image for the chat-mcp sidecar.
// The operator reads CHAT_MCP_IMAGE from its own environment; the chart
// injects it on the operator Deployment. When unset the sidecar is not
// injected, keeping the operator safe in environments where chat is not
// configured.
func resolveChatMCPImage() string {
	return os.Getenv("CHAT_MCP_IMAGE")
}

// chatMCPEntry returns the MCP_SERVERS entry for the injected chat-mcp
// sidecar, or nothing when the AgentImage already defines a chat MCP server
// or CHAT_MCP_IMAGE is unset.
func chatMCPEntry(img *ainselv1alpha1.AgentImage) []string {
	chatImage := resolveChatMCPImage()
	if chatImage == "" {
		return nil
	}
	for _, sc := range img.Spec.Sidecars {
		if sc.Name == "chat" && sc.MCPPath != "" {
			return nil
		}
	}
	return []string{"chat=http://localhost:8081/mcp"}
}

// buildSidecars composes the sidecar containers of the agent pod: the
// sidecars declared on the AgentImage, plus the injected chat-mcp sidecar
// when CHAT_MCP_IMAGE is set on the operator Deployment.
func buildSidecars(agent *ainselv1alpha1.Agent, agentName string, img *ainselv1alpha1.AgentImage) []corev1.Container {
	var sidecars []corev1.Container
	// Inject sidecar containers declared on the AgentImage. Each sidecar
	// runs alongside the agent runtime in the same pod, sharing the
	// network namespace (accessible via localhost). Sidecar env vars
	// are sourced from the same <agent>-image-env Secret as the main
	// container.
	for _, sc := range img.Spec.Sidecars {
		scPort := sc.Port
		if scPort == 0 {
			scPort = 8080
		}
		sidecarEnv := []corev1.EnvVar{
			{Name: "AGENT_NAME", Value: agent.Name},
			{Name: "PORT", Value: fmt.Sprintf("%d", scPort)},
		}
		// Automatically inject platform-level env vars (HUB_URL,
		// HUB_INTERNAL_VALIDATE_SECRET) into MCP sidecars so they can
		// communicate with the hub without per-AgentImage manual config.
		// Only inject vars that are not already provided by sc.Env to
		// avoid duplicates — user-provided values take precedence.
		if sc.MCPPath != "" {
			userProvided := make(map[string]bool, len(sc.Env))
			for _, e := range sc.Env {
				userProvided[e.Name] = true
			}
			for _, pe := range platformSidecarEnv() {
				if !userProvided[pe.Name] {
					sidecarEnv = append(sidecarEnv, pe)
				}
			}
		}
		for _, e := range sc.Env {
			sidecarEnv = append(sidecarEnv, corev1.EnvVar{
				Name: e.Name,
				ValueFrom: &corev1.EnvVarSource{
					SecretKeyRef: &corev1.SecretKeySelector{
						LocalObjectReference: corev1.LocalObjectReference{Name: agentName + "-image-env"},
						Key:                  e.Name,
					},
				},
			})
		}
		sidecars = append(sidecars, corev1.Container{
			Name:  sc.Name,
			Image: sc.Image,
			Env:   sidecarEnv,
		})
	}
	// Inject the chat-mcp sidecar so every agent can reply to chat
	// messages without per-AgentImage configuration, but only when
	// CHAT_MCP_IMAGE is configured on the operator Deployment. Skip
	// if the AgentImage already defines a "chat" sidecar (backward compat).
	chatImage := resolveChatMCPImage()
	chatDefined := false
	for _, sc := range img.Spec.Sidecars {
		if sc.Name == "chat" {
			chatDefined = true
			break
		}
	}
	if chatImage != "" && !chatDefined {
		chatSidecarEnv := []corev1.EnvVar{
			{Name: "AGENT_NAME", Value: agent.Name},
			{Name: "PORT", Value: "8081"},
		}
		chatSidecarEnv = append(chatSidecarEnv, platformSidecarEnv()...)
		sidecars = append(sidecars, corev1.Container{
			Name:  "chat",
			Image: chatImage,
			Env:   chatSidecarEnv,
		})
	}
	return sidecars
}
