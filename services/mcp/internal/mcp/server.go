// Package mcp assembles the model-context-protocol server exposed by the
// service. New wires all built-in tools to their hub-backed backends,
// StartPluginTools keeps the plugin-provided tool set in sync by polling
// the hub, and StreamableHTTPHandler serves the server over HTTP.
package mcp

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/mark3labs/mcp-go/server"

	"github.com/ainsel-project/ainsel/services/mcp/internal/tools"
)

// Backends holds connection details for all backend services.
// All MCP tools route through the hub; no direct backend connections needed.
type Backends struct {
	HubURL string
	// InternalValidateSecret authenticates the hub's internal endpoints, which is
	// what the plugin tool aggregator needs to list plugin tools.
	InternalValidateSecret string
}

// New creates a configured MCP server with all tools registered.
func New(log *slog.Logger, b Backends) *server.MCPServer {
	s := server.NewMCPServer(
		"ainsel-mcp",
		"0.1.0",
		server.WithToolCapabilities(true),
	)

	agents := tools.NewAgentTools(b.HubURL)
	s.AddTool(agents.ListAgentsTool(), agents.ListAgents)
	s.AddTool(agents.GetAgentTool(), agents.GetAgent)
	s.AddTool(agents.UpdateAgentTool(), agents.UpdateAgent)
	s.AddTool(agents.CreateAgentTool(), agents.CreateAgent)
	s.AddTool(agents.DeleteAgentTool(), agents.DeleteAgent)
	s.AddTool(agents.PauseAgentTool(), agents.PauseAgent)
	s.AddTool(agents.ResumeAgentTool(), agents.ResumeAgent)

	triggers := tools.NewTriggerTools(b.HubURL)
	s.AddTool(triggers.ListTriggersTool(), triggers.ListTriggers)
	s.AddTool(triggers.GetTriggerTool(), triggers.GetTrigger)
	s.AddTool(triggers.DeleteTriggerTool(), triggers.DeleteTrigger)
	s.AddTool(triggers.CreateTriggerTool(), triggers.CreateTrigger)
	s.AddTool(triggers.UpdateTriggerTool(), triggers.UpdateTrigger)

	cronTriggers := tools.NewCronTriggerTools(b.HubURL)
	s.AddTool(cronTriggers.ListCronTriggersTool(), cronTriggers.ListCronTriggers)
	s.AddTool(cronTriggers.GetCronTriggerTool(), cronTriggers.GetCronTrigger)
	s.AddTool(cronTriggers.CreateCronTriggerTool(), cronTriggers.CreateCronTrigger)
	s.AddTool(cronTriggers.UpdateCronTriggerTool(), cronTriggers.UpdateCronTrigger)
	s.AddTool(cronTriggers.DeleteCronTriggerTool(), cronTriggers.DeleteCronTrigger)

	connectors := tools.NewConnectorTools(b.HubURL)
	s.AddTool(connectors.ListConnectorsTool(), connectors.ListConnectors)
	s.AddTool(connectors.GetConnectorTool(), connectors.GetConnector)
	s.AddTool(connectors.UpdateConnectorTool(), connectors.UpdateConnector)

	// Channels are the streams events live in: one per connector, one per agent
	// inbox, plus custom grouping channels. The subscription list answers
	// "who receives what" without re-joining connectors, agents and triggers.
	channelTools := tools.NewChannelTools(b.HubURL)
	s.AddTool(channelTools.ListChannelsTool(), channelTools.ListChannels)
	s.AddTool(channelTools.GetChannelTool(), channelTools.GetChannel)
	s.AddTool(channelTools.ListChannelSubscriptionsTool(), channelTools.ListChannelSubscriptions)
	s.AddTool(channelTools.GetChannelEventsTool(), channelTools.GetChannelEvents)
	s.AddTool(channelTools.CreateChannelTool(), channelTools.CreateChannel)
	s.AddTool(channelTools.UpdateChannelTool(), channelTools.UpdateChannel)
	s.AddTool(channelTools.DeleteChannelTool(), channelTools.DeleteChannel)
	s.AddTool(channelTools.AttachChannelBridgeTool(), channelTools.AttachChannelBridge)
	s.AddTool(channelTools.DetachChannelBridgeTool(), channelTools.DetachChannelBridge)

	workflows := tools.NewWorkflowTools(b.HubURL)
	s.AddTool(workflows.SummarizeWorkflowsTool(), workflows.SummarizeWorkflows)

	invocations := tools.NewInvocationTools(b.HubURL)
	s.AddTool(invocations.ListInvocationsTool(), invocations.ListInvocations)
	s.AddTool(invocations.GetInvocationTool(), invocations.GetInvocation)

	activity := tools.NewActivityTools(b.HubURL)
	s.AddTool(activity.SummarizeAgentActivityTool(), activity.SummarizeAgentActivity)

	agentImages := tools.NewAgentImageTools(b.HubURL)
	s.AddTool(agentImages.ListAgentImagesTool(), agentImages.ListAgentImages)
	s.AddTool(agentImages.GetAgentImageTool(), agentImages.GetAgentImage)
	s.AddTool(agentImages.CreateAgentImageTool(), agentImages.CreateAgentImage)
	s.AddTool(agentImages.UpdateAgentImageTool(), agentImages.UpdateAgentImage)
	s.AddTool(agentImages.DeleteAgentImageTool(), agentImages.DeleteAgentImage)

	mcpServers := tools.NewMCPServerTools(b.HubURL)
	s.AddTool(mcpServers.ListMCPServersTool(), mcpServers.ListMCPServers)
	s.AddTool(mcpServers.GetMCPServerTool(), mcpServers.GetMCPServer)

	// Plugins are third-party code the platform routes to. Registration is
	// separate from deployment (the operator ships the workload), and use is
	// gated by two explicit admin decisions: a permission approval and a
	// group-access grant.
	plugins := tools.NewPluginTools(b.HubURL)
	s.AddTool(plugins.ListPluginsTool(), plugins.ListPlugins)
	s.AddTool(plugins.GetPluginTool(), plugins.GetPlugin)
	s.AddTool(plugins.InstallPluginTool(), plugins.InstallPlugin)
	s.AddTool(plugins.UninstallPluginTool(), plugins.UninstallPlugin)
	s.AddTool(plugins.EnablePluginTool(), plugins.EnablePlugin)
	s.AddTool(plugins.DisablePluginTool(), plugins.DisablePlugin)
	s.AddTool(plugins.ApprovePluginPermissionsTool(), plugins.ApprovePluginPermissions)
	s.AddTool(plugins.SetPluginGroupAccessTool(), plugins.SetPluginGroupAccess)

	personas := tools.NewPersonaTools(b.HubURL)
	s.AddTool(personas.ListPersonasTool(), personas.ListPersonas)
	s.AddTool(personas.GetPersonaTool(), personas.GetPersona)
	s.AddTool(personas.ListPersonaVersionsTool(), personas.ListPersonaVersions)
	s.AddTool(personas.GetPersonaVersionTool(), personas.GetPersonaVersion)
	s.AddTool(personas.CreatePersonaTool(), personas.CreatePersona)
	s.AddTool(personas.UpdatePersonaTool(), personas.UpdatePersona)
	s.AddTool(personas.DeletePersonaTool(), personas.DeletePersona)

	skills := tools.NewSkillTools(b.HubURL)
	s.AddTool(skills.ListSkillsTool(), skills.ListSkills)
	s.AddTool(skills.GetSkillTool(), skills.GetSkill)
	s.AddTool(skills.CreateSkillTool(), skills.CreateSkill)
	s.AddTool(skills.UpdateSkillTool(), skills.UpdateSkill)
	s.AddTool(skills.DeleteSkillTool(), skills.DeleteSkill)

	errs := tools.NewErrorTools(b.HubURL)
	s.AddTool(errs.GetRecentErrorsTool(), errs.GetRecentErrors)

	usage := tools.NewUsageTools(b.HubURL)
	s.AddTool(usage.GetTokenUsageTool(), usage.GetTokenUsage)
	s.AddTool(usage.GetStatsTool(), usage.GetStats)

	// The user registry and the caller's own access tokens. Reads are open to any
	// authenticated caller; the writes are admin-only and the hub enforces that on
	// the forwarded bearer — the MCP side checks nothing and passes 403 through.
	userTools := tools.NewUserTools(b.HubURL)
	s.AddTool(userTools.ListUsersTool(), userTools.ListUsers)
	s.AddTool(userTools.GetUserTool(), userTools.GetUser)
	s.AddTool(userTools.CreateUserTool(), userTools.CreateUser)
	s.AddTool(userTools.UpdateUserTool(), userTools.UpdateUser)
	s.AddTool(userTools.DeleteUserTool(), userTools.DeleteUser)

	// Personal access tokens: the value appears in exactly one response, and no
	// read tool can render it.
	userTokenTools := tools.NewUserTokenTools(b.HubURL)
	s.AddTool(userTokenTools.ListUserTokensTool(), userTokenTools.ListUserTokens)
	s.AddTool(userTokenTools.CreateUserTokenTool(), userTokenTools.CreateUserToken)
	s.AddTool(userTokenTools.RevokeUserTokenTool(), userTokenTools.RevokeUserToken)

	// The authz audit trail (admin-only, secret-free rows).
	auditTools := tools.NewAuditTools(b.HubURL)
	s.AddTool(auditTools.ListAuthEventsTool(), auditTools.ListAuthEvents)

	events := tools.NewEventTools(b.HubURL)
	s.AddTool(events.GetStreamInfoTool(), events.GetStreamInfo)
	s.AddTool(events.ListRecentEventsTool(), events.ListRecentEvents)

	logs := tools.NewLogTools(b.HubURL)
	s.AddTool(logs.GetAgentLogsTool(), logs.GetAgentLogs)
	s.AddTool(logs.QueryLogsTool(), logs.QueryLogs)

	metrics := tools.NewMetricsTools(b.HubURL)
	s.AddTool(metrics.GetAgentMetricsTool(), metrics.GetAgentMetrics)
	s.AddTool(metrics.QueryMetricsTool(), metrics.QueryMetrics)

	health := tools.NewHealthTools(b.HubURL)
	s.AddTool(health.GetPlatformHealthTool(), health.GetPlatformHealth)

	_ = log
	return s
}

// StartPluginTools keeps plugin operator tools on s: it polls the hub for the
// plugin tool list and adds, replaces and removes tools until ctx is done. The
// set of built-in names it must never shadow is taken from s itself, right after
// the static registrations above, so it cannot drift from the tools registered
// here. Without an internal token the aggregator logs once and stays off.
func StartPluginTools(ctx context.Context, s *server.MCPServer, b Backends, interval time.Duration) {
	builtin := map[string]bool{}
	for name := range s.ListTools() {
		builtin[name] = true
	}
	agg := tools.NewPluginToolAggregator(b.HubURL, b.InternalValidateSecret, s, builtin, interval)
	go agg.Run(ctx)
}

// StreamableHTTPHandler returns an http.Handler for the MCP streamable HTTP transport.
func StreamableHTTPHandler(s *server.MCPServer) http.Handler {
	return server.NewStreamableHTTPServer(s)
}
