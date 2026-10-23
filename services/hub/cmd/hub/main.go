package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/ainsel-project/ainsel/services/hub/internal/api"
	"github.com/ainsel-project/ainsel/services/hub/internal/invocations"
	_ "github.com/ainsel-project/ainsel/services/hub/internal/metrics"
	"github.com/ainsel-project/ainsel/services/hub/internal/tasklogs"
	"github.com/ainsel-project/ainsel/services/hub/pkg/version"
	ainselv1alpha1 "github.com/ainsel-project/ainsel/shared/api/api/v1alpha1"

	"golang.org/x/sync/errgroup"
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
)

var scheme = runtime.NewScheme()

func init() {
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))
	utilruntime.Must(ainselv1alpha1.AddToScheme(scheme))
}

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))
	slog.Info("starting hub", "version", version.Version, "commit", version.Commit)

	cfg := containerConfig{
		dbURL:                  envOrDefault("HUB_DB_URL", ""),
		namespace:              envOrDefault("HUB_NAMESPACE", "ainsel"),
		hubPort:                envOrDefault("HUB_PORT", "8080"),
		metricsPort:            envOrDefault("HUB_METRICS_PORT", "9090"),
		promURL:                envOrDefault("HUB_PROMETHEUS_URL", ""),
		metricsSource:          envOrDefault("HUB_METRICS_SOURCE", ""),
		claimTimeoutSecs:       envIntOrDefault("TASK_CLAIM_TIMEOUT_SECONDS", 1800),
		taskStartTimeoutSecs:   envIntOrDefault("TASK_START_TIMEOUT_SECONDS", 900),
		authEventRetentionDays: envIntOrDefault("HUB_AUTH_EVENT_RETENTION_DAYS", 90),
		authEventMaxRows:       envIntOrDefault("HUB_AUTH_EVENT_MAX_ROWS", 100000),
		connectorCfg:           api.LoadConnectorConfig(),
		hubAllowInsecureNoAuth: os.Getenv("HUB_ALLOW_INSECURE_NO_AUTH") == "true",
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	c, err := newContainer(ctx, cfg, containerDeps{})
	if err != nil {
		slog.Error("startup failed", "error", err)
		os.Exit(1)
	}
	defer c.Close()

	g, ctx := errgroup.WithContext(ctx)

	// Trigger sync loop (initial sync + 30s ticker).
	g.Go(runUntilCanceled(ctx, func(ctx context.Context) error {
		return runSyncLoop(ctx, c.triggerStore, c.idx, c.cronEmitter)
	}))

	// Channel registry sync: provision a channel per connector and per agent,
	// mark the orphans, and stamp the birth channel onto history that predates
	// the entity. Runs once the informer cache is in sync, then on a ticker —
	// non-fatal, because a missed round only delays a channel appearing.
	g.Go(runUntilCanceled(ctx, func(ctx context.Context) error {
		return runChannelSyncLoop(ctx, c.channelRecon, c.mgr)
	}))

	// Plugin MCP registry sync: a plugin that declares an MCP endpoint gets a
	// registry row pointing agents at the hub's own agent endpoint, and loses it
	// when the Plugin goes away. One pass at startup, then a ticker; non-fatal,
	// because a failed pass leaves the previous registration standing.
	g.Go(runUntilCanceled(ctx, func(ctx context.Context) error {
		return runPluginSyncLoop(ctx, c)
	}))

	// API HTTP server.
	g.Go(func() error {
		slog.Info("API server starting", "port", cfg.hubPort)
		if err := c.apiHTTPServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			return fmt.Errorf("API server: %w", err)
		}
		return nil
	})

	// Metrics HTTP server.
	g.Go(func() error {
		slog.Info("metrics server starting", "port", cfg.metricsPort)
		if err := c.metricsServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			return fmt.Errorf("metrics server: %w", err)
		}
		return nil
	})

	// Webhook-driven router.
	g.Go(runUntilCanceled(ctx, func(ctx context.Context) error {
		return c.rtr.Run(ctx)
	}))

	// Cron emitter.
	g.Go(runUntilCanceled(ctx, func(ctx context.Context) error {
		return c.cronEmitter.Run(ctx)
	}))

	// K8s manager (cache + informers).
	g.Go(runUntilCanceled(ctx, func(ctx context.Context) error {
		slog.Info("starting manager for cache and informers")
		return c.mgr.Start(ctx)
	}))

	// Ownership backfill (one-shot after cache sync; non-fatal).
	g.Go(func() error {
		if err := runBackfill(ctx, c); err != nil {
			slog.Error("backfill", "error", err)
		}
		return nil
	})

	// Skill delivery: the shared skills ConfigMap mirrors the skills
	// agents have enabled, not the whole registry. This pass runs once at
	// startup to prune catalogue entries (which is what frees the object
	// after it filled) and then keeps retrying anything that did not fit.
	g.Go(runUntilCanceled(ctx, func(ctx context.Context) error {
		return runSkillsDelivery(ctx, c)
	}))

	// Periodic task log pruning (hourly, non-fatal).
	g.Go(runUntilCanceled(ctx, func(ctx context.Context) error {
		ticker := time.NewTicker(1 * time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-ticker.C:
				n, err := c.taskLogStore.Prune(ctx, tasklogs.DefaultRetention)
				if err != nil {
					slog.Error("task log prune failed", "error", err)
				} else if n > 0 {
					slog.Info("pruned old task log entries", "count", n)
				}
				cn, err := c.taskLogStore.PruneConversations(ctx, tasklogs.ConversationRetention)
				if err != nil {
					slog.Error("conversation prune failed", "error", err)
				} else if cn > 0 {
					slog.Info("pruned old conversation messages", "count", cn)
				}
				inv, err := c.invStore.Prune(ctx, invocations.Retention)
				if err != nil {
					slog.Error("invocation prune failed", "error", err)
				} else if inv > 0 {
					slog.Info("pruned old invocations", "count", inv)
				}
			}
		}
	}))

	// Auth-event audit-trail retention (issue #116): hourly, non-fatal, same
	// cadence as task-log pruning. Age first (the privacy/compliance lever),
	// then the row cap (burst protection between sweeps); the cap keeps the
	// newest rows.
	g.Go(runUntilCanceled(ctx, func(ctx context.Context) error {
		retention := time.Duration(cfg.authEventRetentionDays) * 24 * time.Hour
		ticker := time.NewTicker(1 * time.Hour)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-ticker.C:
				n, err := c.auditEventStore.Prune(ctx, retention)
				if err != nil {
					slog.Error("auth event prune failed", "error", err)
				} else if n > 0 {
					slog.Info("pruned old auth events", "count", n)
				}
				n, err = c.auditEventStore.TrimToMax(ctx, cfg.authEventMaxRows)
				if err != nil {
					slog.Error("auth event trim failed", "error", err)
				} else if n > 0 {
					slog.Info("trimmed auth events to row cap", "count", n)
				}
			}
		}
	}))

	// Agent queue signals: publishes queue depth on Agent status for the operator
	// to scale on (see internal/queuesignal).
	g.Go(runUntilCanceled(ctx, func(ctx context.Context) error {
		return c.queueSignals.Run(ctx)
	}))

	// Stale claim reaper (every 5m, non-fatal).
	g.Go(runUntilCanceled(ctx, func(ctx context.Context) error {
		claimTimeout := time.Duration(cfg.claimTimeoutSecs) * time.Second
		slog.Info("stale claim reaper started", "timeout", claimTimeout, "interval", 5*time.Minute)
		ticker := time.NewTicker(5 * time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-ticker.C:
				reaped, err := c.eventQueue.ReapStaleClaims(ctx, claimTimeout)
				if err != nil {
					slog.Error("stale claim reap failed", "error", err)
					continue
				}
				// Deduplicate agent names so we send at most one NOTIFY per agent.
				notified := make(map[string]bool)
				for _, rt := range reaped {
					slog.Warn("reaped stale claim", "task_id", rt.ID, "agent_name", rt.AgentName, "timeout", claimTimeout)
					if !notified[rt.AgentName] {
						notified[rt.AgentName] = true
						if err := c.eventQueue.NotifyAgent(ctx, rt.AgentName); err != nil {
							slog.Error("notify agent after reap failed", "agent_name", rt.AgentName, "error", err)
						}
					}
				}
			}
		}
	}))

	// Graceful shutdown: on ctx cancellation, shut down HTTP servers.
	// Returns the first non-nil shutdown error so g.Wait() surfaces it.
	g.Go(func() error {
		<-ctx.Done()
		slog.Info("shutting down")
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer shutdownCancel()
		if err := c.apiHTTPServer.Shutdown(shutdownCtx); err != nil {
			slog.Error("API server shutdown error", "error", err)
			return fmt.Errorf("API server shutdown: %w", err)
		}
		if err := c.metricsServer.Shutdown(shutdownCtx); err != nil {
			slog.Error("metrics server shutdown error", "error", err)
			return fmt.Errorf("metrics server shutdown: %w", err)
		}
		return nil
	})

	if err := g.Wait(); err != nil {
		slog.Error("hub stopped with error", "error", err)
		os.Exit(1)
	}
	slog.Info("hub stopped")
}
