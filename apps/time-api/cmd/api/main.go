// Command api is the entry point for the nodate-time HTTP API server.
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
	_ "github.com/go-sql-driver/mysql"

	"github.com/nodate-flow/nodate-flow/apps/time-api/internal/auth"
	"github.com/nodate-flow/nodate-flow/apps/time-api/internal/config"
	timedb "github.com/nodate-flow/nodate-flow/apps/time-api/internal/db"
	"github.com/nodate-flow/nodate-flow/apps/time-api/internal/http/router"
	"github.com/nodate-flow/nodate-flow/apps/time-api/internal/notifications"
	"github.com/nodate-flow/nodate-flow/packages/go-shared/authn"
	"github.com/nodate-flow/nodate-flow/packages/go-shared/email"
	"github.com/nodate-flow/nodate-flow/packages/go-shared/httputil"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
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
	db.SetMaxOpenConns(cfg.DbMaxOpenConns)
	db.SetMaxIdleConns(cfg.DbMaxIdleConns)
	db.SetConnMaxLifetime(cfg.DbConnMaxLifetime)
	if err := db.Ping(); err != nil {
		logger.Error("db ping failed", "err", err)
		os.Exit(1)
	}
	defer db.Close()

	// Schema-presence probe — warns when the connected database is
	// missing a table time-api relies on. The production deployment
	// path skips the probe to avoid an information_schema enumeration
	// on every boot; local / staging / test runs opt in so a stale
	// MySQL volume surfaces a clear message instead of a generic 500
	// at request time. Intentionally fail-open.
	if cfg.Env != "prod" {
		probeCtx, probeCancel := context.WithTimeout(context.Background(), 5*time.Second)
		timedb.VerifySchema(probeCtx, db, logger)
		probeCancel()
	}

	jwtPriv, err := authn.DeriveEd25519Key(os.Getenv("NF_SECRET_KEY"), "nodate-flow:jwt:v1")
	if err != nil {
		logger.Error("jwt key derivation failed", "err", err)
		os.Exit(1)
	}
	jwtIssuer, err := auth.NewJWTIssuer(jwtPriv, "nodate-flow", "api", 15*time.Minute)
	if err != nil {
		logger.Error("jwt issuer init failed", "err", err)
		os.Exit(1)
	}

	// Wire the outbound email transport. When NF_TIME_SMTP_HOST is
	// unset we fall through to the noop sender — invite rows are still
	// created, but the magic-link message is never delivered.
	var emailSender email.Sender = email.NoopSender{}
	if cfg.SmtpHost != "" {
		s, smtpErr := email.NewSMTPSender(email.SMTPConfig{
			Host:     cfg.SmtpHost,
			Port:     cfg.SmtpPort,
			Username: cfg.SmtpUsername,
			Password: cfg.SmtpPassword,
			From:     cfg.SmtpFrom,
		})
		if smtpErr != nil {
			logger.Error("smtp init failed", "err", smtpErr)
			os.Exit(1)
		}
		emailSender = s
		logger.Info("email sender configured", "host", cfg.SmtpHost)
	}

	inner := router.Build(router.Deps{
		DB:          db,
		JWT:         jwtIssuer,
		EmailSender: emailSender,
		EmailFrom:   cfg.SmtpFrom,
		WebBaseURL:  cfg.WebBaseURL,
	})

	outer := chi.NewRouter()
	outer.Use(httputil.BuildCORS(cfg.CorsAllowedOrigins))
	outer.Mount("/", inner)

	addr := ":" + cfg.Port
	srv := &http.Server{
		Addr:              addr,
		Handler:           outer,
		ReadHeaderTimeout: 10 * time.Second,
	}

	stopCtx, stopCancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stopCancel()

	go notifications.StartNotificationScheduler(stopCtx, db, time.Minute)

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

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer shutdownCancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		logger.Error("graceful shutdown failed", "err", err)
		os.Exit(1)
	}
	logger.Info("shutdown complete")
}
