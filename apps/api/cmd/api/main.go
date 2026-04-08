// Command api is the entry point for the nodate-flow HTTP API server.
package main

import (
	"database/sql"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/nodate-flow/nodate-flow/apps/api/internal/auth"
	"github.com/nodate-flow/nodate-flow/apps/api/internal/config"
	"github.com/nodate-flow/nodate-flow/apps/api/internal/crypto"
	"github.com/nodate-flow/nodate-flow/apps/api/internal/db/generated"
	"github.com/nodate-flow/nodate-flow/apps/api/internal/http/router"
	nflog "github.com/nodate-flow/nodate-flow/apps/api/internal/log"
)

func main() {
	logger := nflog.New(nflog.Config{})
	slog.SetDefault(logger)

	cfg, err := config.Load()
	if err != nil {
		logger.Error("config load failed", "err", err)
		os.Exit(1)
	}

	// DB handle is left nil at scaffold time; production wiring will set it.
	var db *sql.DB
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

	jwtIssuer, err := auth.NewJWTIssuer(nil, "nodate-flow", "api", 15*time.Minute)
	if err != nil {
		logger.Error("jwt issuer init failed", "err", err)
		os.Exit(1)
	}

	inner := router.Build(router.Deps{
		DB:                 db,
		Queries:            queries,
		JWT:                jwtIssuer,
		Cipher:             cipher,
		GhWebhookSecret:    cfg.GhWebhookSecret,
		SlackSigningSecret: cfg.SlackSigningSecret,
		DefaultWorkspaceID: cfg.DefaultWorkspaceID,
	})

	// Wrap the router with the request logger so the prod binary keeps
	// its access logs; tests build the router directly without it.
	outer := chi.NewRouter()
	outer.Use(nflog.RequestLogger(logger))
	outer.Mount("/", inner)

	addr := ":" + cfg.Port
	logger.Info("listening", "addr", addr)
	if err := http.ListenAndServe(addr, outer); err != nil { //nolint:gosec // dev scaffold
		logger.Error("server exited", "err", err)
		os.Exit(1)
	}
}
