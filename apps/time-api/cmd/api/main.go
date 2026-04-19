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
	"github.com/go-chi/cors"
	_ "github.com/go-sql-driver/mysql"

	"github.com/nodate-flow/nodate-flow/apps/time-api/internal/auth"
	"github.com/nodate-flow/nodate-flow/apps/time-api/internal/config"
	"github.com/nodate-flow/nodate-flow/apps/time-api/internal/http/router"
	"github.com/nodate-flow/nodate-flow/apps/time-api/internal/notifications"
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
	db.SetMaxOpenConns(32)
	db.SetMaxIdleConns(8)
	db.SetConnMaxLifetime(30 * time.Minute)
	if err := db.Ping(); err != nil {
		logger.Error("db ping failed", "err", err)
		os.Exit(1)
	}
	defer db.Close()

	jwtIssuer, err := auth.NewJWTIssuer(nil, "nodate-flow", "api", 15*time.Minute)
	if err != nil {
		logger.Error("jwt issuer init failed", "err", err)
		os.Exit(1)
	}

	inner := router.Build(router.Deps{
		DB:  db,
		JWT: jwtIssuer,
	})

	outer := chi.NewRouter()
	outer.Use(buildCORS(cfg.CorsAllowedOrigins))
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
