package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/ainsel-project/ainsel/services/chat-mcp/internal/config"
	"github.com/ainsel-project/ainsel/services/chat-mcp/internal/lifecycle"
	mcpserver "github.com/ainsel-project/ainsel/services/chat-mcp/internal/mcp"
)

// main starts the chat MCP sidecar. The server exposes MCP tools that let
// the agent runtime interact with chat sessions by proxying to the hub
// backend's chat REST API.
//
// The sidecar is designed to run as a container in the agent pod, accessible
// via localhost. No authentication is required — the MCP endpoint is only
// reachable from within the pod.
func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	cfg := config.Load()

	if err := cfg.Validate(); err != nil {
		log.Error("config invalid", "error", err)
		os.Exit(1)
	}

	s := mcpserver.New(log, cfg.HubURL, cfg.HubInternalToken, cfg.AgentName)
	handler := mcpserver.StreamableHTTPHandler(s)

	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprintln(w, `{"status":"ok"}`)
	})
	mux.Handle("/mcp", handler)

	addr := fmt.Sprintf(":%d", cfg.Port)
	log.Info("starting ainsel-chat-mcp",
		"addr", addr,
		"agent", cfg.AgentName,
		"hub_url", cfg.HubURL,
		"has_internal_token", cfg.HubInternalToken != "",
	)

	srv := &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 5 * time.Second}

	// One signal drives both ways out of this process: a SIGTERM (the pod is
	// being torn down) and the runtime process exiting (the task is over).
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	go func() {
		<-ctx.Done()
		log.Info("shutting down")
		_ = srv.Close()
	}()

	// On a per-task Job the operator mounts a shared directory and shares the
	// pod's PID namespace, so the runtime's pid file tells us when the task is
	// done. Ending here is what lets the Job complete: a sidecar that keeps
	// serving holds the pod open, and the open Job holds the agent's concurrency
	// slot and blocks the task's own retry. Without TaskDir (a long-lived pod,
	// or an operator that predates this contract) the server just serves.
	if cfg.TaskDir != "" {
		w := &lifecycle.Watcher{
			PIDFile: filepath.Join(cfg.TaskDir, lifecycle.PIDFileName),
			Log:     log,
		}
		go func() {
			reason, err := w.Wait(ctx)
			if err != nil {
				if errors.Is(err, ctx.Err()) {
					return // signalled down; the goroutine above is closing the server
				}
				// A task directory we cannot interpret is not evidence that the
				// runtime died. Serving on is the safe failure: ending here would
				// strand a task that is still running.
				log.Error("runtime watcher disabled", "err", err)
				return
			}
			log.Info("ending chat sidecar with the task", "reason", string(reason), "pid_file", w.PIDFile)
			_ = srv.Close()
			if reason == lifecycle.AgentNeverStarted {
				// Nothing ever ran the task; fail so the Job records an attempt
				// instead of the pod sitting out its life as a running shell with no
				// runtime in it.
				os.Exit(1)
			}
			os.Exit(0)
		}()
	}

	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Error("server error", "err", err)
		os.Exit(1)
	}
}