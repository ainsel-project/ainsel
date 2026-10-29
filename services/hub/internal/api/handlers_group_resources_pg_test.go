package api

import (
	"context"
	"net/http"
	"testing"

	"github.com/ainsel-project/ainsel/services/hub/internal/channels"
	"github.com/ainsel-project/ainsel/services/hub/internal/db"
	"github.com/ainsel-project/ainsel/services/hub/internal/eventqueue"
	"github.com/ainsel-project/ainsel/services/hub/internal/invocations"
	"github.com/ainsel-project/ainsel/services/hub/internal/mcpservers"
	"github.com/ainsel-project/ainsel/services/hub/internal/triggers"
	pgcontainer "github.com/testcontainers/testcontainers-go/modules/postgres"
)

// Postgres-backed coverage for the types whose stores have no in-memory fake:
// trigger, cron-trigger, channel (standalone only) and mcp-server. Follows the
// existing integration-suite pattern: one container, skipped when Docker is
// unavailable.

func groupResPGServer(t *testing.T, fake *groupResAuthzFake) *Server {
	t.Helper()
	ctx := context.Background()
	c, err := pgcontainer.Run(ctx, "postgres:17-alpine",
		pgcontainer.WithDatabase("ainsel_test"),
		pgcontainer.WithUsername("test"),
		pgcontainer.WithPassword("test"),
		pgcontainer.BasicWaitStrategies(),
	)
	if err != nil {
		t.Skipf("docker unavailable: %v", err)
	}
	t.Cleanup(func() { _ = c.Terminate(context.Background()) })
	dsn, err := c.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("connection string: %v", err)
	}
	if err := db.Migrate(ctx, dsn); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	pool, err := db.Open(ctx, dsn)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(pool.Close)

	eq := eventqueue.NewStore(pool)
	ts := triggers.NewStore(pool)
	cs := channels.NewStore(pool)
	svc := channels.NewService(cs, eq, ts)

	s := groupResTestServer(t, fake)
	s.SetEventQueue(eq)
	s.SetTriggerStore(ts)
	s.SetChannelService(svc, channels.NewTransfer(svc, eq, invocations.NewMemoryStore(100)))
	s.mcp = mcpservers.NewService(mcpservers.NewStore(pool))
	return s
}

func TestGroupResources_TriggersCarryAgentRef(t *testing.T) {
	fake := newGroupResAuthzFake()
	fake.add("g1", "trigger", "t-1", "alice")
	fake.add("g1", "cron-trigger", "ct-1", "alice")
	fake.add("g1", "trigger", "t-stale", "alice") // no trigger row -> dropped
	s := groupResPGServer(t, fake)

	ctx := context.Background()
	if err := s.triggerStore.CreateTrigger(ctx, &triggers.Trigger{
		ID: "t-1", DisplayName: "On issues", AgentRef: "review-bot", ConnectorRef: "forgejo",
	}); err != nil {
		t.Fatalf("seed trigger: %v", err)
	}
	if err := s.triggerStore.CreateCronTrigger(ctx, &triggers.CronTrigger{
		ID: "ct-1", DisplayName: "Nightly", AgentRef: "review-bot", Schedule: "0 3 * * *", Prompt: "report",
	}); err != nil {
		t.Fatalf("seed cron trigger: %v", err)
	}

	code, items := getGroupResources(t, s, "/api/v1/groups/g1/resources", "alice")
	if code != http.StatusOK {
		t.Fatalf("status: %d", code)
	}
	set := itemSet(items)
	if len(items) != 2 {
		t.Fatalf("want 2 items (stale trigger dropped), got %+v", items)
	}
	if got := set["trigger/t-1"]; got.Name != "On issues" || got.AgentRef != "review-bot" {
		t.Errorf("trigger = %+v, want name On issues and agentRef review-bot", got)
	}
	if got := set["cron-trigger/ct-1"]; got.Name != "Nightly" || got.AgentRef != "review-bot" {
		t.Errorf("cron-trigger = %+v, want name Nightly and agentRef review-bot", got)
	}
}

func TestGroupResources_StandaloneChannelsAndMCPServers(t *testing.T) {
	fake := newGroupResAuthzFake()
	fake.add("g1", "mcp-server", "mcp-1", "alice")
	s := groupResPGServer(t, fake)

	ctx := context.Background()
	ch, err := s.channelSvc.CreateCustom(ctx, "team-inbox", "grouped")
	if err != nil {
		t.Fatalf("create channel: %v", err)
	}
	// The channel store generates the id; map it into the group after creation.
	fake.add("g1", "channel", ch.ID, "alice")

	if err := s.mcp.Create(ctx, &mcpservers.MCPServer{Name: "mcp-1", DisplayName: "Docs MCP", URL: "https://mcp.test"}); err != nil {
		t.Fatalf("create mcp server: %v", err)
	}

	code, items := getGroupResources(t, s, "/api/v1/groups/g1/resources", "alice")
	if code != http.StatusOK {
		t.Fatalf("status: %d", code)
	}
	set := itemSet(items)
	if len(items) != 2 {
		t.Fatalf("want 2 items, got %+v", items)
	}
	if got := set["channel/"+ch.ID]; got.Name != "team-inbox" {
		t.Errorf("channel name = %q, want team-inbox", got.Name)
	}
	if got := set["mcp-server/mcp-1"]; got.Name != "Docs MCP" {
		t.Errorf("mcp-server name = %q, want Docs MCP", got.Name)
	}
}
