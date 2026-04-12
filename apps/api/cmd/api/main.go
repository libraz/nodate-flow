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
	"runtime/debug"
	"strings"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/cors"
	_ "github.com/go-sql-driver/mysql"

	"github.com/nodate-flow/nodate-flow/apps/api/internal/ai"
	"github.com/nodate-flow/nodate-flow/apps/api/internal/ai/agentruntime"
	"github.com/nodate-flow/nodate-flow/apps/api/internal/ai/providers"
	"github.com/nodate-flow/nodate-flow/apps/api/internal/auth"
	"github.com/nodate-flow/nodate-flow/apps/api/internal/config"
	"github.com/nodate-flow/nodate-flow/apps/api/internal/crypto"
	"github.com/nodate-flow/nodate-flow/apps/api/internal/db/generated"
	"github.com/nodate-flow/nodate-flow/apps/api/internal/eventbus"
	"github.com/nodate-flow/nodate-flow/apps/api/internal/http/router"
	"github.com/nodate-flow/nodate-flow/apps/api/internal/integrations"
	"github.com/nodate-flow/nodate-flow/apps/api/internal/integrations/email"
	nflog "github.com/nodate-flow/nodate-flow/apps/api/internal/log"
	"github.com/nodate-flow/nodate-flow/apps/api/internal/notification"
	"github.com/nodate-flow/nodate-flow/apps/api/internal/obs"
	"github.com/nodate-flow/nodate-flow/apps/api/internal/outbound"
	"github.com/nodate-flow/nodate-flow/apps/api/internal/storage"
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

	// OpenTelemetry tracing. When NF_OTEL_ENDPOINT is empty the provider
	// is a no-op so the rest of the codebase can call otel.Tracer() freely.
	traceShutdown, err := obs.InitTracer(context.Background(), obs.TracerConfig{
		Endpoint:       cfg.OtelEndpoint,
		ServiceName:    "nodate-flow-api",
		ServiceVersion: resolveVersion(),
		Insecure:       cfg.OtelInsecure,
	})
	if err != nil {
		logger.Error("otel tracer init failed", "err", err)
		os.Exit(1)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := traceShutdown(ctx); err != nil {
			logger.Error("otel tracer shutdown failed", "err", err)
		}
	}()
	if cfg.OtelEndpoint != "" {
		logger.Info("otel tracing enabled", "endpoint", cfg.OtelEndpoint)
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

	// Start background DB connection pool metrics collector. The channel
	// is closed during graceful shutdown to stop the polling goroutine.
	dbStatsDone := make(chan struct{})
	obs.StartDBStatsCollector(db, dbStatsDone)

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

	// S3-compatible object storage for file uploads. When NF_S3_ENDPOINT
	// is empty the presign endpoints return INTERNAL.UNEXPECTED but the
	// rest of the API still boots normally.
	var storageClient *storage.Client
	if cfg.S3Endpoint != "" {
		sc, serr := storage.NewClient(storage.Config{
			Endpoint:  cfg.S3Endpoint,
			AccessKey: cfg.S3AccessKey,
			SecretKey: cfg.S3SecretKey,
			Bucket:    cfg.S3Bucket,
			UseSSL:    cfg.S3UseSSL,
		})
		if serr != nil {
			logger.Error("storage client init failed", "err", serr)
			os.Exit(1)
		}
		if err := sc.EnsureBucket(context.Background()); err != nil {
			logger.Error("storage bucket init failed", "err", err)
			os.Exit(1)
		}
		storageClient = sc
		logger.Info("s3 storage enabled", "endpoint", cfg.S3Endpoint, "bucket", cfg.S3Bucket)
	} else {
		logger.Warn("s3 storage disabled: NF_S3_ENDPOINT is not set")
	}

	// Per-provider egress rate limits (4.SEC-2). When NF_OUTBOUND_BACKEND=redis
	// is set and the binary is built with -tags redis, the limiters are
	// swapped for RedisLimiter so the budget is shared across replicas.
	// Otherwise the default in-process token bucket applies.
	llmDests := []string{
		providers.DestAnthropic,
		providers.DestOpenAI,
		providers.DestGoogle,
		providers.DestOllama,
	}
	if cfg.OutboundLlmRps > 0 {
		if !configureOutboundLimiters(cfg, logger, llmDests) {
			burst := cfg.OutboundLlmBurst
			if burst <= 0 {
				if b := int(cfg.OutboundLlmRps); b > 0 {
					burst = b
				} else {
					burst = 1
				}
			}
			for _, dest := range llmDests {
				providers.ConfigureLimiter(dest, outbound.NewLimiter(cfg.OutboundLlmRps, burst))
			}
			logger.Info("outbound llm rate limit enabled",
				"rps", cfg.OutboundLlmRps, "burst", burst)
		}
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
		// Prefer a Redis-backed notifier when the env asks for it and
		// the binary was compiled with -tags redis; otherwise fall
		// through to the in-process notifier. The EventbusTap is wired
		// on top of whichever notifier is selected so eventbus.Append
		// publishes uniformly.
		if rn := buildStreamNotifier(cfg, logger); rn != nil {
			notifier = rn
			tap = stream.NewEventbusTap(rn)
		} else {
			in := stream.NewInProcessNotifier()
			notifier = in
			tap = stream.NewEventbusTap(in)
		}
		eventbus.SetNotifyHook(tap.Publish)
		streamRemember = tap.RememberWorkspace
		aiInvocationPublisher = tap.PublishAiInvocation
	}

	// Session store driver. MySQL is the default; NF_SESSION_STORE=redis
	// selects the redis-tagged driver when the binary is built with
	// -tags redis. Mis-configured values log and fall back to MySQL
	// so a bad env never locks users out.
	sessions := buildSessionStore(cfg, queries, logger)

	// Agent runtime wiring. The runner (and optionally the mysql
	// queue) is constructed before router.Build so the manual
	// trigger endpoint can enqueue / dispatch through the same
	// instances that the scheduler uses.
	var runner agentruntime.Runner
	if cfg.AgentRunner == "orchestrator" {
		var executor agentruntime.AgentExecutor
		if cipher != nil && !cfg.AiMock {
			resolver := providers.NewWorkspaceResolver(queries, cipher)
			budget := ai.BudgetReaderFunc(func(ctx context.Context, wsID uint32) (int64, error) {
				return queries.SumAiCostTodayForWorkspace(ctx, generated.SumAiCostTodayForWorkspaceParams{
					WorkspaceID: wsID,
					InvokedAt:   time.Now().UTC().Truncate(24 * time.Hour),
				})
			})
			executor = &ai.AgentExecutor{
				Queries:      queries,
				Resolver:     resolver,
				Guard:        ai.NewCostGuard(budget, 0),
				OnInvocation: obs.RecordAIInvocation,
			}
			logger.Info("agent executor: workspace resolver")
		} else {
			logger.Info("agent executor: nil (bookkeeping only)")
		}
		runner = &agentruntime.OrchestratorRunner{DB: db, Executor: executor}
		logger.Info("agent runner: orchestrator")
	} else {
		runner = &agentruntime.LogRunner{Sink: func(_ context.Context, j agentruntime.Job, _ time.Time) {
			logger.Info("agent runtime: dispatch", "agent_id", j.AgentID, "ws", j.WsID)
		}}
	}
	var agentQueue agentruntime.Queue
	if cfg.AgentQueueBackend == "mysql" {
		agentQueue = agentruntime.NewMySQLQueue(db)
	} else {
		// The in-process queue lets single-binary deployments still
		// use the on_event trigger without opting into the mysql
		// queue. Workers below consume from whichever queue is set.
		agentQueue = agentruntime.NewInProcessQueue(256)
	}

	// Register the on_event trigger against the eventbus notify
	// fan-out. This sits alongside the stream tap so a single
	// eventbus.Append both wakes SSE subscribers and enqueues any
	// agents that opted in to the same event kind.
	eventTrigger := &agentruntime.EventTrigger{
		Queries: agentruntime.NewSqlcOnEventQuerier(db),
		Queue:   agentQueue,
		Logger:  logger,
	}
	eventbus.AddNotifyHook(eventTrigger.NotifyHook())

	// Outbound email transport. When NF_SMTP_HOST is set the sender
	// relays through the configured SMTP server; otherwise a NoopSender
	// is used so handlers can always call Send and check for
	// email.ErrNotConfigured on the return path.
	var emailSender email.Sender
	if cfg.SmtpHost != "" {
		s, serr := email.NewSMTPSender(email.SMTPConfig{
			Host:     cfg.SmtpHost,
			Port:     cfg.SmtpPort,
			Username: cfg.SmtpUsername,
			Password: cfg.SmtpPassword,
			From:     cfg.SmtpFrom,
		})
		if serr != nil {
			logger.Error("smtp sender init failed", "err", serr)
			os.Exit(1)
		}
		emailSender = s
		logger.Info("smtp email enabled", "host", cfg.SmtpHost, "port", cfg.SmtpPort)
	} else {
		emailSender = email.NoopSender{}
		logger.Warn("smtp email disabled: NF_SMTP_HOST is not set")
	}

	// Notification fan-out: creates per-user notification rows whenever
	// the eventbus fires a relevant event (task.created, task.comment.added,
	// etc.). Email delivery is attempted when the SMTP sender is configured.
	notifFanout := notification.NewFanout(db, queries, emailSender)
	eventbus.AddNotifyHook(notifFanout.Hook())

	integrationsRegistry := integrations.NewRegistry(
		func() (integrations.Provider, error) {
			return integrations.NewGithub(cfg.GithubClientID, cfg.GithubClientSecret)
		},
		func() (integrations.Provider, error) {
			return integrations.NewSlack(cfg.SlackClientID, cfg.SlackClientSecret)
		},
		func() (integrations.Provider, error) {
			return integrations.NewGoogleCalendar(cfg.GoogleClientID, cfg.GoogleClientSecret)
		},
	)

	// Background OAuth token refresher: keeps Google Calendar
	// access tokens fresh so foreground MCP / ingest handlers
	// never race a 401. Other providers (GitHub, Slack) return
	// ErrRefreshNotSupported and are silently skipped.
	integrationsRefresher := &integrations.Refresher{
		Queries:  queries,
		Cipher:   cipher,
		Registry: integrationsRegistry,
		Logger:   logger,
	}
	refresherCtx, refresherCancel := context.WithCancel(context.Background())
	defer refresherCancel()
	go integrationsRefresher.Run(refresherCtx)
	logger.Info("integrations refresher goroutine launched")

	inner := router.Build(router.Deps{
		DB:                    db,
		Queries:               queries,
		Sessions:              sessions,
		JWT:                   jwtIssuer,
		Cipher:                cipher,
		GhWebhookSecret:       cfg.GhWebhookSecret,
		SlackSigningSecret:    cfg.SlackSigningSecret,
		GoogleChannelToken:    cfg.GoogleChannelToken,
		DefaultWorkspaceID:    cfg.DefaultWorkspaceID,
		CookieSecure:          cfg.CookieSecure,
		RegistrationOpen:      cfg.RegistrationOpen,
		AiMock:                cfg.AiMock,
		StreamNotifier:        notifier,
		StreamRemember:        streamRemember,
		AiInvocationPublisher: aiInvocationPublisher,
		AgentQueue:            agentQueue,
		AgentRunner:           runner,
		Storage:               storageClient,
		EmailSender:           emailSender,
		EmbedOpenAIKey:        cfg.EmbedOpenAIKey,
		EmbedModel:            cfg.EmbedModel,
		EmbedBaseURL:          cfg.EmbedBaseURL,
		Integrations:          integrationsRegistry,
		PublicBaseURL:         cfg.PublicBaseURL,
		WebBaseURL:            cfg.WebBaseURL,
	})

	// Wrap the router with the request logger so the prod binary keeps
	// its access logs; tests build the router directly without it.
	outer := chi.NewRouter()
	outer.Use(nflog.RequestLogger(logger))
	outer.Use(obs.TraceMiddleware())
	outer.Use(obs.MetricsMiddleware())
	outer.Use(buildCORS(cfg.CorsAllowedOrigins))

	// Prometheus metrics endpoint. Mounted before the application routes
	// so it is not gated by auth middleware.
	outer.Handle("/metrics", obs.MetricsHandler())

	outer.Mount("/", inner)

	// 4.AGENT-1: interval scheduler. Ticks every NF_AGENT_TICK_INTERVAL
	// and dispatches due agents to the runner built above. When
	// AgentQueueBackend=mysql the scheduler enqueues into agent_runs
	// instead, and separate Worker goroutines pull and execute.
	scheduler := &agentruntime.Scheduler{
		Source:   &agentruntime.DBSource{DB: db},
		Runner:   runner,
		Interval: cfg.AgentTickInterval,
	}
	scheduler.Queue = agentQueue
	var agentWorkers []*agentruntime.Worker
	var workerCancel context.CancelFunc
	var agentPurger *agentruntime.Purger
	workerCount := cfg.AgentWorkerCount
	if workerCount <= 0 {
		workerCount = 1
	}
	{
		var wctx context.Context
		wctx, workerCancel = context.WithCancel(context.Background())
		for i := 0; i < workerCount; i++ {
			w := &agentruntime.Worker{Queue: agentQueue, Runner: runner}
			agentWorkers = append(agentWorkers, w)
			go w.Loop(wctx)
		}
		logger.Info("agent workers started", "count", workerCount, "backend", cfg.AgentQueueBackend)
	}
	if cfg.AgentQueueBackend == "mysql" && cfg.AgentRunsPurgeInterval > 0 {
		agentPurger = &agentruntime.Purger{
			Queries:   queries,
			Interval:  cfg.AgentRunsPurgeInterval,
			Retention: cfg.AgentRunsRetention,
			Logger:    logger,
		}
		if err := agentPurger.Start(context.Background()); err != nil {
			logger.Warn("agent_runs purger start failed", "err", err)
		} else {
			logger.Info("agent_runs purger started",
				"interval", cfg.AgentRunsPurgeInterval,
				"retention", cfg.AgentRunsRetention)
		}
	}
	_ = agentWorkers
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
	refresherCancel()
	if workerCancel != nil {
		workerCancel()
	}
	if agentPurger != nil {
		agentPurger.Stop()
	}

	close(dbStatsDone)

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

// resolveVersion returns NF_VERSION, the Go build main module version, or
// "dev". Used for OTel service.version.
func resolveVersion() string {
	if v := strings.TrimSpace(os.Getenv("NF_VERSION")); v != "" {
		return v
	}
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}
	return "dev"
}
