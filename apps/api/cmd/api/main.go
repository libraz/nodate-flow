// Command api is the entry point for the nodate-flow HTTP API server.
package main

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/cors"
	_ "github.com/go-sql-driver/mysql"

	"github.com/nodate-flow/nodate-flow/apps/api/internal/ai/agentruntime"
	"github.com/nodate-flow/nodate-flow/apps/api/internal/ai/providers"
	"github.com/nodate-flow/nodate-flow/apps/api/internal/auth"
	"github.com/nodate-flow/nodate-flow/apps/api/internal/auth/sessionstore"
	"github.com/nodate-flow/nodate-flow/apps/api/internal/config"
	"github.com/nodate-flow/nodate-flow/apps/api/internal/outbound"
	"github.com/nodate-flow/nodate-flow/apps/api/internal/crypto"
	"github.com/nodate-flow/nodate-flow/apps/api/internal/db/generated"
	"github.com/nodate-flow/nodate-flow/apps/api/internal/eventbus"
	"github.com/nodate-flow/nodate-flow/apps/api/internal/http/router"
	nflog "github.com/nodate-flow/nodate-flow/apps/api/internal/log"
	"github.com/nodate-flow/nodate-flow/apps/api/internal/stream"
)

func main() {
	logger := nflog.New(nflog.Config{})
	slog.SetDefault(logger)

	cfg, err := config.Load()
	if err != nil {
		logger.Error("config load failed", "err", err)
		os.Exit(1)
	}

	if cfg.DbDsn == "" {
		logger.Error("NF_DB_DSN is not set")
		os.Exit(1)
	}
	db, err := sql.Open("mysql", cfg.DbDsn)
	if err != nil {
		logger.Error("db open failed", "err", err)
		os.Exit(1)
	}
	db.SetMaxOpenConns(32)
	db.SetMaxIdleConns(8)
	db.SetConnMaxLifetime(30 * time.Minute)
	if err := db.Ping(); err != nil {
		logger.Error("db ping failed", "err", err)
		os.Exit(1)
	}
	defer db.Close()
	queries := generated.New(db)

	// Cipher is optional at scaffold time: if NF_SECRET_KEY is unset the
	// AI provider endpoints will return AI.PROVIDER.NOT_CONFIGURED for
	// any write call, but the rest of the api boots normally so MVP
	// flows that don't touch LLMs keep working.
	var cipher *crypto.Cipher
	if c, cerr := crypto.NewFromEnv(); cerr == nil {
		cipher = c
	} else {
		logger.Warn("ai cipher disabled", "err", cerr)
	}

	// Per-provider egress rate limits (4.SEC-2). A zero rps leaves the
	// registry empty and every provider call falls through to the
	// underlying *http.Client untouched.
	if cfg.OutboundLlmRps > 0 {
		burst := cfg.OutboundLlmBurst
		if burst <= 0 {
			if b := int(cfg.OutboundLlmRps); b > 0 {
				burst = b
			} else {
				burst = 1
			}
		}
		for _, dest := range []string{
			providers.DestAnthropic,
			providers.DestOpenAI,
			providers.DestGoogle,
			providers.DestOllama,
		} {
			providers.ConfigureLimiter(dest, outbound.NewLimiter(cfg.OutboundLlmRps, burst))
		}
		logger.Info("outbound llm rate limit enabled",
			"rps", cfg.OutboundLlmRps, "burst", burst)
	}

	jwtIssuer, err := auth.NewJWTIssuer(nil, "nodate-flow", "api", 15*time.Minute)
	if err != nil {
		logger.Error("jwt issuer init failed", "err", err)
		os.Exit(1)
	}

	// Realtime SSE fan-out (ADR 0005). The notifier is in-process
	// for v1; swap for a Redis/NATS implementation when moving to
	// multi-replica. eventbus.Append calls the tap after every
	// successful insert so SSE subscribers see task.* and
	// ai.suggestion.* changes without polling.
	var notifier stream.Notifier
	var tap *stream.EventbusTap
	var streamRemember stream.RememberWorkspaceFunc
	var aiInvocationPublisher func(context.Context, uint32)
	if cfg.StreamEnabled {
		in := stream.NewInProcessNotifier()
		notifier = in
		tap = stream.NewEventbusTap(in)
		eventbus.SetNotifyHook(tap.Publish)
		streamRemember = tap.RememberWorkspace
		aiInvocationPublisher = tap.PublishAiInvocation
	}

	// Session store driver. MySQL is the default; NF_SESSION_STORE=redis
	// selects the redis-tagged driver (requires building with -tags redis).
	// Unknown values fall back to MySQL with a warning so misconfiguration
	// never locks users out.
	var sessions sessionstore.Store = sessionstore.NewMySQLStore(queries)
	switch cfg.SessionStore {
	case "", "mysql":
		// default
	case "redis":
		logger.Warn("NF_SESSION_STORE=redis requires the -tags redis build; falling back to MySQL")
	default:
		logger.Warn("unknown NF_SESSION_STORE value; falling back to MySQL", "value", cfg.SessionStore)
	}

	// Stream / outbound backend selectors. The in-process (memory) drivers
	// are the default; "redis" is only honored when the binary is compiled
	// with `-tags redis` so mis-set env values degrade gracefully to the
	// in-process behavior instead of panicking at startup.
	if cfg.StreamBackend == "redis" {
		logger.Warn("NF_STREAM_BACKEND=redis requires the -tags redis build; using in-process notifier")
	}
	if cfg.OutboundBackend == "redis" {
		logger.Warn("NF_OUTBOUND_BACKEND=redis requires the -tags redis build; using in-process limiter")
	}

	inner := router.Build(router.Deps{
		DB:                 db,
		Queries:            queries,
		Sessions:           sessions,
		JWT:                jwtIssuer,
		Cipher:             cipher,
		GhWebhookSecret:    cfg.GhWebhookSecret,
		SlackSigningSecret: cfg.SlackSigningSecret,
		GoogleChannelToken: cfg.GoogleChannelToken,
		DefaultWorkspaceID: cfg.DefaultWorkspaceID,
		CookieSecure:       cfg.CookieSecure,
		AiMock:             cfg.AiMock,
		StreamNotifier:        notifier,
		StreamRemember:        streamRemember,
		AiInvocationPublisher: aiInvocationPublisher,
	})

	// Wrap the router with the request logger so the prod binary keeps
	// its access logs; tests build the router directly without it.
	outer := chi.NewRouter()
	outer.Use(nflog.RequestLogger(logger))
	outer.Use(buildCORS(cfg.CorsAllowedOrigins))
	outer.Mount("/", inner)

	// 4.AGENT-1: cron scheduler. Ticks once a minute, dispatches every
	// enabled non-paused agent with a cron_expr to a log-only runner.
	// Production wiring will swap LogRunner for an orchestrator-backed
	// runner; the loop and Source contract stay identical.
	scheduler := &agentruntime.Scheduler{
		Source:   &agentruntime.DBSource{DB: db},
		Runner:   &agentruntime.LogRunner{Sink: func(_ context.Context, j agentruntime.Job, _ time.Time) {
			logger.Info("agent runtime: dispatch", "agent_id", j.AgentID, "ws", j.WsID)
		}},
		Interval: time.Minute,
	}
	schedulerCtx, schedulerCancel := context.WithCancel(context.Background())
	defer schedulerCancel()
	if err := scheduler.Start(schedulerCtx); err != nil {
		logger.Warn("agent scheduler start failed", "err", err)
	}

	addr := ":" + cfg.Port
	srv := &http.Server{
		Addr:              addr,
		Handler:           outer,
		ReadHeaderTimeout: 10 * time.Second,
	}

	// Graceful shutdown: on SIGINT / SIGTERM stop the scheduler, drain
	// in-flight HTTP requests, then exit. A 20s hard deadline prevents
	// a stuck handler from blocking the pod forever.
	stopCtx, stopCancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stopCancel()

	serverErr := make(chan error, 1)
	go func() {
		logger.Info("listening", "addr", addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErr <- err
		}
		close(serverErr)
	}()

	select {
	case err := <-serverErr:
		if err != nil {
			logger.Error("server exited", "err", err)
			os.Exit(1)
		}
	case <-stopCtx.Done():
		logger.Info("shutdown signal received")
	}

	scheduler.Stop()
	schedulerCancel()

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer shutdownCancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		logger.Error("graceful shutdown failed", "err", err)
		os.Exit(1)
	}
	logger.Info("shutdown complete")
}

// buildCORS returns a chi CORS middleware configured from the runtime
// allowlist. Credentials are enabled so the refresh cookie and
// Authorization header round-trip to the browser; a single "*" entry
// disables credentials (per the CORS spec wildcard rules) and allows
// any origin. An empty allowlist disables CORS entirely.
func buildCORS(allowed []string) func(http.Handler) http.Handler {
	if len(allowed) == 0 {
		return func(next http.Handler) http.Handler { return next }
	}
	allowCreds := true
	if len(allowed) == 1 && allowed[0] == "*" {
		allowCreds = false
	}
	return cors.Handler(cors.Options{
		AllowedOrigins:   allowed,
		AllowedMethods:   []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"Accept", "Authorization", "Content-Type", "X-Requested-With"},
		ExposedHeaders:   []string{"X-Request-Id"},
		AllowCredentials: allowCreds,
		MaxAge:           600,
	})
}
