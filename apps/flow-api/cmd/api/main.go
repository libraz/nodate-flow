// Command api is the entry point for the nodate-flow HTTP API server.
package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"runtime/debug"
	"strings"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"
	_ "github.com/go-sql-driver/mysql"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/ai"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/ai/agentruntime"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/ai/autoactions"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/ai/providers"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/ai/signaljudge"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/auth"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/bgloop"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/config"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/dbretry"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated/calendar"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/eventbus"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/handlers/signals"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/router"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/importer"
	nflog "github.com/libraz/nodate-flow/apps/flow-api/internal/log"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/notification"
	calendarnotifs "github.com/libraz/nodate-flow/apps/flow-api/internal/notifications"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/obs"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/outbound"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/reconciler"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/storage"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/storagegc"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/stream"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/webhook"
	"github.com/libraz/nodate-flow/packages/go-shared/authn"
	"github.com/libraz/nodate-flow/packages/go-shared/crypto"
	"github.com/libraz/nodate-flow/packages/go-shared/dbtz"
	"github.com/libraz/nodate-flow/packages/go-shared/email"
	"github.com/libraz/nodate-flow/packages/go-shared/httputil"
)

func main() {
	logger := nflog.New(nflog.Config{})
	slog.SetDefault(logger)

	cfg, err := config.Load()
	if err != nil {
		logger.Error("config load failed", "err", err)
		os.Exit(1)
	}
	if err := cfg.Validate(); err != nil {
		logger.Error("config validation failed", "err", err)
		os.Exit(1)
	}

	// OpenTelemetry tracing. When NF_FLOW_OTEL_ENDPOINT is empty the provider
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
	// Pin the session timezone before opening: every stored DATETIME is a
	// UTC wall clock, and a session that answers NOW() in the host's zone
	// makes every comparison against them wrong by the offset, silently.
	db, err := sql.Open("mysql", dbtz.NormalizeDSN(cfg.DbDsn))
	if err != nil {
		logger.Error("db open failed", "err", err)
		os.Exit(1)
	}
	db.SetMaxOpenConns(cfg.DbMaxOpenConns)
	db.SetMaxIdleConns(cfg.DbMaxIdleConns)
	db.SetConnMaxLifetime(cfg.DbConnMaxLifetime)
	if err := db.Ping(); err != nil {
		logger.Error("db ping failed", "err", err)
		os.Exit(1)
	}
	// Refuse to start against a session that is not UTC. The DSN pins
	// it; this proves the pin took, because a proxy or an externally
	// assembled DSN can undo it and every symptom of that is silent.
	if err := dbtz.AssertUTCSession(context.Background(), db); err != nil {
		logger.Error("db session timezone check failed", "err", err)
		os.Exit(1)
	}
	defer db.Close()

	// Start background DB connection pool metrics collector. The channel
	// is closed during graceful shutdown to stop the polling goroutine.
	dbStatsDone := make(chan struct{})
	obs.StartDBStatsCollector(db, dbStatsDone)

	queries := generated.New(db)
	calendarQueries := calendar.New(db)

	// NF_FLOW_DEFAULT_WORKSPACE_ID routes inbound webhook deliveries whose
	// sender has no integration_source_mappings row. That is only sound
	// while the instance hosts a single tenant; past that it would file
	// one workspace's GitHub / Slack / Google events under another. The
	// receivers enforce the rule per delivery, so this check does not stop
	// the boot — it is here so the operator learns why deliveries started
	// being rejected instead of discovering it from the webhook provider's
	// retry log.
	if err := signals.CheckDefaultWorkspaceFallback(context.Background(), queries, cfg.DefaultWorkspaceID); err != nil {
		logger.Error("default workspace webhook fallback is not usable", "err", err)
	}

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

	// Per-provider egress rate limits. When NF_FLOW_OUTBOUNF_BACKEND=redis
	// is set and the binary is built with -tags redis, the limiters are
	// swapped for RedisLimiter so the budget is shared across replicas.
	// Otherwise the default in-process token bucket applies.
	llmDests := []string{
		providers.DestAnthropic,
		providers.DestOpenAI,
		providers.DestGoogle,
		providers.DestOllama,
		providers.DestOpenAIEmbed,
	}
	if cfg.OutboundLlmRps > 0 {
		burst := cfg.OutboundLlmBurst
		if burst <= 0 {
			if b := int(cfg.OutboundLlmRps); b > 0 {
				burst = b
			} else {
				burst = 1
			}
		}
		if !configureOutboundLimiters(cfg, logger, llmDests) {
			for _, dest := range llmDests {
				providers.ConfigureLimiter(dest, outbound.NewLimiter(cfg.OutboundLlmRps, burst))
			}
			logger.Info("outbound llm rate limit enabled",
				"rps", cfg.OutboundLlmRps, "burst", burst)
		}
		// Per-workspace egress cap: half the global rate so a single
		// tenant cannot exhaust the shared provider quota.
		wsRps := cfg.OutboundLlmRps / 2
		wsBurst := burst / 2
		if wsBurst <= 0 {
			wsBurst = 1
		}
		providers.ConfigureWorkspaceLimiter(wsRps, wsBurst)
		logger.Info("per-workspace llm rate limit enabled",
			"rps", wsRps, "burst", wsBurst)
	}

	jwtPriv, err := authn.DeriveEd25519Key(cfg.SecretKey, "nodate-flow:jwt:v1")
	if err != nil {
		logger.Error("jwt key derivation failed", "err", err)
		os.Exit(1)
	}
	jwtIssuer, err := auth.NewJWTIssuer(jwtPriv, "nodate-flow", "api", 15*time.Minute)
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
	// Every resident loop below hands back one stopper, called in the
	// shutdown sequence at the bottom of main. A no-op stands in while
	// the feature that owns the loop is disabled, so the shutdown path
	// does not have to know which loops were started.
	stopStreamTail := func() {}
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
		// A replica that handles a write but holds none of the
		// subscriptions has never been told the workspace's public id.
		// Without a resolver the event is dropped there, which is
		// invisible on one replica and total on several.
		tap.SetWorkspaceResolver(&stream.DBWorkspaceResolver{DB: db})
		eventbus.AddNotifyHook(tap.Publish)
		streamRemember = tap.RememberWorkspace
		aiInvocationPublisher = tap.PublishAiInvocation

		// The tap only sees appends made by this binary. When another
		// product writes to the same database its changes reach this
		// process's subscribers through the log instead.
		if cfg.StreamTail {
			tailer := stream.NewTailer(db, notifier, tap)
			tailer.SetInterval(cfg.StreamTailInterval)
			stopStreamTail = bgloop.Start(context.Background(), "stream.tailer", logger, func(ctx context.Context) {
				if err := tailer.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
					logger.Warn("event tail stopped", "err", err)
				}
			})
			logger.Info("event tail started", "interval", cfg.StreamTailInterval)
		}
	}

	// Agent runtime wiring. The runner (and optionally the mysql
	// queue) is constructed before router.Build so the manual
	// trigger endpoint can enqueue / dispatch through the same
	// instances that the scheduler uses.
	var runner agentruntime.Runner
	var judgeRunner *signaljudge.Runner
	if cfg.AgentRunner == "orchestrator" {
		var executor agentruntime.AgentExecutor
		if cipher != nil && !cfg.AiMock {
			resolver := providers.NewWorkspaceResolver(queries, cipher)
			budget := ai.BudgetReaderFunc(func(ctx context.Context, wsID uint32) (int64, error) {
				return queries.SumAiCostTodayForWorkspace(ctx, ai.DailyCostParams(ctx, queries, wsID))
			})
			invocationLogger := router.NewDBInvocationLogger(queries, aiInvocationPublisher)
			executor = &ai.AgentExecutor{
				Queries:      queries,
				Resolver:     resolver,
				Guard:        ai.NewCostGuard(budget, cfg.AiDailyBudgetCents),
				Log:          invocationLogger,
				OnInvocation: obs.RecordAIInvocation,
				PreFlight:    &ai.PreFlight{Queries: queries},
			}
			// The signal_judge runner shares the same provider
			// resolver, cost guard, and metrics hook as the task-agent
			// path so cost accounting and ai_invocations stay uniform
			// across both kinds (ADR 0008 D3).
			//
			// Wire the Applier so the runner's verdict
			// turns into events through the deterministic, non-LLM
			// stage that owns the judge-only event kinds. The Applier
			// is the sole legitimate caller of eventbus.AppendJudgeEvent
			// (the runtime guard in eventbus.Append rejects everything
			// else with INTERNAL.EVENTBUS.JUDGE_KIND_OUTSIDE_APPLIER).
			//
			// The autonomy resolver is the production
			// [signaljudge.RuleBackedResolver] which
			// walks auto_action_rules → ai_settings.auto_action_threshold
			// → signalkinds YAML default. The stub
			// [signaljudge.SuggestOnlyResolver] stays available for
			// tests but is no longer wired here.
			// The production TaskMutator wires the
			// `generate_retro` branch through to a real tasks INSERT +
			// task_dependencies(kind='retro_of') INSERT in one
			// transaction. The other two action branches
			// (complete_task / add_comment) remain no-op-with-warn on
			// [SQLTaskMutator] until their phases land; the Applier
			// still emits TaskAutoCompleted / SignalApplied so the
			// timeline contract is preserved end-to-end.
			judgeApplier := &signaljudge.Applier{
				Bus: signaljudge.AppendJudgeEventFunc(func(ctx context.Context, evt eventbus.Event) error {
					return eventbus.AppendJudgeEvent(ctx, dbretry.AutoCommit(db), evt)
				}),
				Tasks: &signaljudge.SQLTaskMutator{
					DB:      db,
					Queries: queries,
					Logger:  logger,
				},
				Resolver: &signaljudge.SQLTaskResolver{DB: db},
				Signals:  &signaljudge.SQLSignalUpdater{DB: db},
				Autonomy: signaljudge.NewRuleBackedResolver(queries, &signaljudge.SQLSettingsLookup{Queries: queries}),
				Logger:   logger,
			}
			judgeRunner = &signaljudge.Runner{
				Agents:       &signaljudge.SQLAgentLookup{DB: db},
				Signals:      &signaljudge.SQLSignalLookup{DB: db},
				Resolver:     resolver,
				Guard:        ai.NewCostGuard(budget, cfg.AiDailyBudgetCents),
				OnInvocation: obs.RecordAIInvocation,
				Log: func(ctx context.Context, rec signaljudge.InvocationRecord) {
					invocationLogger(ctx, ai.InvocationRecord{
						WorkspaceID:      rec.WorkspaceID,
						AgentID:          rec.AgentID,
						Purpose:          rec.Purpose,
						Model:            rec.Model,
						PromptRedacted:   rec.PromptRedacted,
						ResponseRedacted: rec.ResponseRedacted,
						TokensInput:      rec.TokensInput,
						TokensOutput:     rec.TokensOutput,
						CostMicros:       rec.CostMicros,
						CostCents:        rec.CostCents,
						Status:           rec.Status,
						ErrorCode:        rec.ErrorCode,
					})
				},
				Applier: judgeApplier,
				// Wire the three PromptDeps lookups so the runner
				// renders the full context window (recent tasks +
				// linked tasks + judge_instructions) instead of falling
				// back to the composeJudgePrompt JSON snapshot.
				// WorkspaceNow is left nil, so the "Now" timestamp the
				// judge reasons against is UTC and not the workspace's
				// own timezone.
				//
				// The lookups are narrow raw-SQL adapters that
				// apps/flow-api/tests/signaljudge/prompt_render_test.go
				// constructs and runs directly, so the statements that
				// test exercises are the statements a judge run
				// executes. Caps (MaxRecentTasks=20 /
				// MaxLinkedTasks=10) are enforced inside
				// BuildPromptContext regardless of how many rows the
				// lookups return, and a lookup that fails there fails
				// the run rather than shortening the prompt.
				Prompt: signaljudge.PromptDeps{
					RecentTasks:       &signaljudge.SQLRecentTasksLookup{DB: db},
					LinkedTasks:       &signaljudge.SQLLinkedTasksLookup{DB: db},
					JudgeInstructions: &signaljudge.SQLJudgeInstructionsLookup{DB: db},
				},
			}
			logger.Info("agent executor: workspace resolver")
		} else {
			logger.Info("agent executor: nil (bookkeeping only)")
		}
		// Wrap nil judgeRunner explicitly so the interface stored on
		// OrchestratorRunner.Judge is also nil (not a typed nil
		// pointer), which keeps the runner's `r.Judge != nil` guard
		// honest.
		var judgeIface agentruntime.JudgeExecutor
		if judgeRunner != nil {
			judgeIface = judgeRunner
		}
		runner = &agentruntime.OrchestratorRunner{
			DB:               db,
			Executor:         executor,
			Judge:            judgeIface,
			Queries:          queries,
			HandoffLoopLimit: cfg.AgentHandoffLoopLimit,
		}
		logger.Info("agent runner: orchestrator")
	} else {
		runner = &agentruntime.LogRunner{Sink: func(_ context.Context, j agentruntime.Job, _ time.Time) {
			logger.Info("agent runtime: dispatch", "agent_id", j.AgentID, "workspace_id", j.WsID)
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

	// Outbound email transport. When NF_FLOW_SMTP_HOST is set the sender
	// relays through the configured SMTP server; otherwise a NoopSender
	// is used so handlers can always call Send and check for
	// email.ErrNotConfigured on the return path.
	var emailSender email.Sender
	if cfg.SMTPHost != "" {
		s, serr := email.NewSMTPSender(email.SMTPConfig{
			Host:     cfg.SMTPHost,
			Port:     cfg.SMTPPort,
			Username: cfg.SMTPUsername,
			Password: cfg.SMTPPassword,
			From:     cfg.SMTPFrom,
		})
		if serr != nil {
			logger.Error("smtp sender init failed", "err", serr)
			os.Exit(1)
		}
		emailSender = s
		logger.Info("smtp email enabled", "host", cfg.SMTPHost, "port", cfg.SMTPPort)
	} else {
		emailSender = email.NoopSender{}
		logger.Warn("smtp email disabled: NF_FLOW_SMTP_HOST is not set")
	}

	// Notification fan-out: creates per-user notification rows whenever
	// the eventbus fires a relevant event (task.created, task.comment.added,
	// etc.). Email delivery is attempted when the SMTP sender is configured.
	// Each fan-out goroutine detaches from the request context and is
	// bounded by NF_NOTIFICATION_FANOUT_TIMEOUT (default 30s).
	notifFanout := notification.NewFanout(db, queries, emailSender)
	notifFanout.SetTimeout(cfg.NotificationFanoutTimeout)
	eventbus.AddNotifyHook(notifFanout.Hook())

	// Webhook delivery worker: creates delivery rows for matching
	// subscriptions and periodically POSTs payloads with HMAC signatures.
	webhookWorker := webhook.NewWorker(db, queries)
	eventbus.AddNotifyHook(webhookWorker.Hook())

	// Appends made through the cross-service eventlog (itemkit,
	// memberkit) reach every subscriber registered above. Without this
	// the item and member half of the event log fans out to nobody.
	// Registered after the subscribers so a reader sees what it covers,
	// though the bridge forwards to whatever is registered at fire time.
	eventbus.BridgeEventlog()

	// JudgeEnqueuer wakes any matching signal_judge agents the moment
	// a fresh signal lands (ADR 0008 D3). The orchestrator dispatch
	// branch in OrchestratorRunner.Run picks the queued row up via
	// the judge-shaped dedupe_key.
	//
	// Attach the Matcher so deterministic, cheap
	// filters (workspace AI kill switch, per-kind dedupe window,
	// subject existence) run before we burn an agent_runs slot. The
	// Matcher does NOT emit SignalRejected; that kind is reserved
	// for verdicts the LLM judge actively dropped.
	judgeMatcher := &signaljudge.Matcher{
		DB:     db,
		Logger: logger,
	}
	judgeEnqueuer := &signaljudge.Enqueuer{
		DB:      db,
		Queue:   agentQueue,
		Logger:  logger,
		Matcher: judgeMatcher,
	}

	inner := router.Build(router.Deps{
		DB:                    db,
		Queries:               queries,
		CalendarQueries:       calendarQueries,
		JWT:                   jwtIssuer,
		Cipher:                cipher,
		GhWebhookSecret:       cfg.GhWebhookSecret,
		SlackSigningSecret:    cfg.SlackSigningSecret,
		GoogleChannelToken:    cfg.GoogleChannelToken,
		DefaultWorkspaceID:    cfg.DefaultWorkspaceID,
		AiMock:                cfg.AiMock,
		AiDailyBudgetCents:    cfg.AiDailyBudgetCents,
		StreamNotifier:        notifier,
		StreamRemember:        streamRemember,
		AiInvocationPublisher: aiInvocationPublisher,
		AgentQueue:            agentQueue,
		AgentRunner:           runner,
		JudgeEnqueuer:         judgeEnqueuer,
		FlowAPISignalToken:    cfg.FlowAPISignalToken,
		Storage:               storageClient,
		EmailSender:           emailSender,
		EmailFrom:             cfg.SMTPFrom,
		FlowWebURL:            cfg.FlowWebURL,
		EmbedOpenAIKey:        cfg.EmbedOpenAIKey,
		EmbedModel:            cfg.EmbedModel,
		EmbedBaseURL:          cfg.EmbedBaseURL,
		DisableRateLimit:      cfg.DisableRateLimit,
		TrustedProxyHops:      cfg.TrustedProxyHops,
	})

	// Wrap the router with the request logger so the prod binary keeps
	// its access logs; tests build the router directly without it.
	outer := chi.NewRouter()
	outer.Use(nflog.RequestLogger(logger))
	outer.Use(obs.TraceMiddleware())
	outer.Use(obs.MetricsMiddleware())
	outer.Use(httputil.BuildCORS(cfg.CorsAllowedOrigins, cfg.CorsDevLocalhost))

	outer.Mount("/", inner)

	// Prometheus metrics are served on a separate internal-only listener
	// so they are never reachable through the public API port.
	metricsMux := http.NewServeMux()
	metricsMux.Handle("/metrics", obs.MetricsHandler())
	metricsAddr := ":" + cfg.MetricsPort
	metricsSrv := &http.Server{
		Addr:              metricsAddr,
		Handler:           metricsMux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() {
		logger.Info("metrics server listening", "addr", metricsAddr)
		if err := metricsSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("metrics server exited", "err", err)
		}
	}()

	// Interval scheduler. Ticks every NF_FLOW_AGENT_TICK_INTERVAL
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
	var stopAgentWorkers []func()
	var agentPurger *agentruntime.Purger
	workerCount := cfg.AgentWorkerCount
	if workerCount <= 0 {
		workerCount = 1
	}
	for i := 0; i < workerCount; i++ {
		name := fmt.Sprintf("agent.worker.%d", i)
		w := &agentruntime.Worker{Queue: agentQueue, Runner: runner, Name: name, Logger: logger}
		agentWorkers = append(agentWorkers, w)
		// Supervised: the loop already survives claim failures, and
		// bgloop covers what it cannot — a panic inside an agent run
		// would otherwise take the whole process down with it.
		stopAgentWorkers = append(stopAgentWorkers,
			bgloop.Start(context.Background(), name, logger, w.Loop))
	}
	logger.Info("agent workers started", "count", workerCount, "backend", cfg.AgentQueueBackend)
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

	webhookWorker.Start(context.Background())

	// Import worker: drains the import_jobs queue that
	// POST /workspaces/{wsId}/imports fills. Without it a created job
	// stays pending forever, and the UI reads a pending job as one that
	// is still working.
	importWorker := importer.NewWorker(db, queries, logger)
	importWorker.Start(context.Background())

	// Storage sweeper: reclaims upload reservations whose bytes never
	// arrived, and rows an interrupted delete left unreferenced. Six
	// places in the codebase were written assuming this existed.
	var storageSweeper *storagegc.Sweeper
	if storageClient != nil {
		storageSweeper = storagegc.New(db, queries, storageClient, logger)
		storageSweeper.Start(context.Background())
	} else {
		logger.Warn("storage sweeper disabled: no object store configured")
	}

	// Autonomous auto-action executor: periodically evaluates tasks and
	// applies deterministic actions (escalate overdue, close stale
	// reviews) without human intervention. Controlled by
	// NF_FLOW_AUTO_ACTION_INTERVAL (0 disables).
	autoActionExec := &autoactions.Executor{
		DB: db,
		Config: autoactions.ExecutorConfig{
			Interval:            cfg.AutoActionInterval,
			ConfidenceThreshold: float32(cfg.AutoActionThreshold),
			DryRun:              cfg.AutoActionDryRun,
		},
		HandoffLoopLimit: cfg.AgentHandoffLoopLimit,
		Logger:           logger,
	}
	stopAutoAction := func() {}
	if cfg.AutoActionInterval > 0 {
		// Only supervised when it is actually enabled: Start returns at
		// once when the interval is zero, and the supervisor would read
		// that as a loop dying and restart it forever. A disabled
		// feature must not look like a broken one.
		//
		// The stopper is the whole stop: it cancels the supervisor's
		// context, which is the one signal the executor's loop observes
		// and the one the supervisor reads as deliberate rather than as
		// a loop that died and needs bringing back mid-shutdown.
		stopAutoAction = bgloop.Start(context.Background(), "autoactions.executor", logger, autoActionExec.Start)
	} else {
		autoActionExec.Start(context.Background())
	}

	// Item-consistency reconciler: scans tasks and calendar_events for
	// drift (date mismatch, orphan role, enabled-flag mismatch) and
	// self-heals via UPDATE. 0 disables.
	stopReconciler := func() {}
	if cfg.ItemReconcilerInterval > 0 {
		rec := &reconciler.Reconciler{
			DB:       db,
			Logger:   logger,
			Metrics:  obs.ReconcilerMetrics{},
			Interval: cfg.ItemReconcilerInterval,
		}
		stopReconciler = bgloop.Start(context.Background(), "item.reconciler", logger, rec.Start)
		logger.Info("item reconciler started", "interval", cfg.ItemReconcilerInterval)
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

	// Calendar event reminder ticker (relocated from time-api per ADR
	// 0007). Scans calendar_events on a 1-minute interval, dispatches
	// reminders through the shared notification fan-out so each
	// attendee receives an in-app notification row, and marks
	// notified_at to prevent duplicates. Its context descends from
	// stopCtx, so a signal ends it; the stopper covers the other way out
	// of the select below, where the listener returned on its own and no
	// signal ever arrived.
	stopCalendarReminders := bgloop.Start(stopCtx, "calendar.reminder_scheduler", logger, func(ctx context.Context) {
		calendarnotifs.StartNotificationScheduler(ctx, db, notifFanout, time.Minute)
	})

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

	// Every resident loop stops here rather than on return from main: a
	// supervised loop whose context outlives its stop is restarted by
	// its supervisor and spends the drain window ticking against a
	// database pool that the rest of this sequence is closing.
	scheduler.Stop()
	stopAutoAction()
	webhookWorker.Stop()
	importWorker.Stop()
	if storageSweeper != nil {
		storageSweeper.Stop()
	}
	schedulerCancel()
	stopReconciler()
	stopCalendarReminders()
	stopStreamTail()
	for _, stop := range stopAgentWorkers {
		stop()
	}
	if agentPurger != nil {
		agentPurger.Stop()
	}

	close(dbStatsDone)

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer shutdownCancel()
	if err := metricsSrv.Shutdown(shutdownCtx); err != nil {
		logger.Error("metrics server shutdown failed", "err", err)
	}
	if err := srv.Shutdown(shutdownCtx); err != nil {
		logger.Error("graceful shutdown failed", "err", err)
		os.Exit(1)
	}
	// Drain in-flight notification fan-out goroutines. The shutdown
	// context already has a 20s budget shared with the HTTP server
	// drain above; if it expires we log and move on.
	if err := notifFanout.Shutdown(shutdownCtx); err != nil {
		logger.Warn("notification fanout shutdown timed out", "err", err)
	}
	// Same for the on_event agent dispatches, which detach from the
	// request that triggered them for the same reason.
	if err := eventTrigger.Shutdown(shutdownCtx); err != nil {
		logger.Warn("on_event dispatch shutdown timed out", "err", err)
	}
	logger.Info("shutdown complete")
}

// resolveVersion returns NF_FLOW_VERSION, the Go build main module version, or
// "dev". Used for OTel service.version.
func resolveVersion() string {
	if v := strings.TrimSpace(os.Getenv("NF_FLOW_VERSION")); v != "" {
		return v
	}
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}
	return "dev"
}
