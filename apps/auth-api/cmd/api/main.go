// Command api is the entry point for the nodate-flow auth API server.
// It owns all authentication, user profile, and session management endpoints.
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

	"github.com/nodate-flow/nodate-flow/apps/auth-api/internal/auth"
	"github.com/nodate-flow/nodate-flow/apps/auth-api/internal/config"
	"github.com/nodate-flow/nodate-flow/apps/auth-api/internal/db/generated"
	"github.com/nodate-flow/nodate-flow/apps/auth-api/internal/http/router"
	"github.com/nodate-flow/nodate-flow/apps/auth-api/internal/integrations"
	"github.com/nodate-flow/nodate-flow/apps/auth-api/internal/storage"
	"github.com/nodate-flow/nodate-flow/packages/go-shared/authn"
	"github.com/nodate-flow/nodate-flow/packages/go-shared/crypto"
	"github.com/nodate-flow/nodate-flow/packages/go-shared/email"
	"github.com/nodate-flow/nodate-flow/packages/go-shared/httputil"
	"github.com/nodate-flow/nodate-flow/packages/go-shared/logutil"
)

func main() {
	logger := slog.New(logutil.NewRedactHandler(
		slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}),
	)).With(slog.String("service", "auth-api"))
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

	queries := generated.New(db)

	var cipher *crypto.Cipher
	if c, cerr := crypto.NewFromEnv(); cerr == nil {
		cipher = c
	} else {
		logger.Warn("cipher disabled (TOTP unavailable)", "err", cerr)
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

	var emailSender email.Sender
	if cfg.SMTPHost != "" {
		s, smtpErr := email.NewSMTPSender(email.SMTPConfig{
			Host:     cfg.SMTPHost,
			Port:     cfg.SMTPPort,
			Username: cfg.SMTPUsername,
			Password: cfg.SMTPPassword,
			From:     cfg.SMTPFrom,
		})
		if smtpErr != nil {
			logger.Error("smtp init failed", "err", smtpErr)
			os.Exit(1)
		}
		emailSender = s
		logger.Info("email sender configured", "host", cfg.SMTPHost)
	}

	integrationsRegistry := integrations.NewRegistry(
		func() (integrations.Provider, error) {
			return integrations.NewGithub(cfg.IntGithubClientID, cfg.IntGithubClientSecret)
		},
		func() (integrations.Provider, error) {
			return integrations.NewSlack(cfg.IntSlackClientID, cfg.IntSlackClientSecret)
		},
		func() (integrations.Provider, error) {
			return integrations.NewGoogleCalendar(cfg.IntGoogleClientID, cfg.IntGoogleClientSecret)
		},
		func() (integrations.Provider, error) {
			return integrations.NewDiscord(cfg.IntDiscordClientID, cfg.IntDiscordClientSecret, cfg.IntDiscordRedirectURI)
		},
	)

	refresherCtx, refresherCancel := context.WithCancel(context.Background())
	defer refresherCancel()
	integrationsRefresher := &integrations.Refresher{
		Queries:  queries,
		Cipher:   cipher,
		Registry: integrationsRegistry,
		Logger:   logger,
	}
	go integrationsRefresher.Run(refresherCtx)
	logger.Info("integrations refresher goroutine launched")

	var oidcClient *auth.OIDCClient
	if cfg.GoogleClientID != "" && cfg.GoogleClientSecret != "" {
		oidcClient = auth.NewOIDCClient(auth.OIDCConfig{
			ClientID:     cfg.GoogleClientID,
			ClientSecret: cfg.GoogleClientSecret,
			RedirectURL:  cfg.PublicBaseURL + "/auth/oidc/google/callback",
		})
		logger.Info("google OIDC client configured")
	} else {
		logger.Warn("google OIDC disabled (NF_AUTH_GOOGLE_CLIENT_ID / NF_AUTH_GOOGLE_CLIENT_SECRET not set)")
	}

	var githubOAuth *auth.GithubOAuthClient
	if cfg.GithubOIDCClientID != "" && cfg.GithubOIDCClientSecret != "" {
		githubOAuth = auth.NewGithubOAuth(auth.GithubOAuthConfig{
			ClientID:     cfg.GithubOIDCClientID,
			ClientSecret: cfg.GithubOIDCClientSecret,
			RedirectURL:  cfg.PublicBaseURL + "/auth/oidc/github/callback",
		})
		logger.Info("github OAuth login client configured")
	} else {
		logger.Warn("github OAuth login disabled (NF_AUTH_GITHUB_OIDC_CLIENT_ID / NF_AUTH_GITHUB_OIDC_CLIENT_SECRET not set)")
	}

	var microsoftOIDC *auth.MicrosoftOIDCClient
	if cfg.MicrosoftOIDCClientID != "" && cfg.MicrosoftOIDCClientSecret != "" {
		microsoftOIDC = auth.NewMicrosoftOIDC(auth.MicrosoftOIDCConfig{
			ClientID:     cfg.MicrosoftOIDCClientID,
			ClientSecret: cfg.MicrosoftOIDCClientSecret,
			RedirectURL:  cfg.PublicBaseURL + "/auth/oidc/microsoft/callback",
		})
		logger.Info("microsoft OIDC login client configured")
	} else {
		logger.Warn("microsoft OIDC login disabled (NF_AUTH_MICROSOFT_OIDC_CLIENT_ID / NF_AUTH_MICROSOFT_OIDC_CLIENT_SECRET not set)")
	}

	// S3-compatible object storage for avatar uploads. When
	// NF_S3_ENDPOINT is empty, the avatar endpoints return
	// AUTH.AVATAR.STORAGE_UNAVAILABLE but the rest of the API still
	// boots normally — useful for local dev without a MinIO container.
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
		if berr := sc.EnsureBucket(context.Background()); berr != nil {
			logger.Error("storage bucket init failed", "err", berr)
			os.Exit(1)
		}
		storageClient = sc
		logger.Info("s3 storage enabled", "endpoint", cfg.S3Endpoint, "bucket", cfg.S3Bucket)
	} else {
		logger.Warn("s3 storage disabled: NF_S3_ENDPOINT is not set")
	}

	inner := router.Build(router.Deps{
		DB:                        db,
		Queries:                   queries,
		JWT:                       jwtIssuer,
		OIDC:                      oidcClient,
		OIDCGithub:                githubOAuth,
		OIDCMicrosoft:             microsoftOIDC,
		Cipher:                    cipher,
		CookieSecure:              cfg.CookieSecure,
		RegistrationOpen:          cfg.RegistrationOpen,
		OAuthAllowedDomains:       cfg.OAuthAllowedDomains,
		OAuthAllowedEmails:        cfg.OAuthAllowedEmails,
		MinPasswordLength:         cfg.MinPasswordLength,
		DisableRateLimit:          cfg.DisableRateLimit,
		RateLimitGlobalMax:        cfg.RateLimitGlobalMax,
		RateLimitGlobalWindowSec:  cfg.RateLimitGlobalWindowSec,
		RateLimitAuthMax:          cfg.RateLimitAuthMax,
		RateLimitAuthWindowSec:    cfg.RateLimitAuthWindowSec,
		RateLimitSessionMax:       cfg.RateLimitSessionMax,
		RateLimitSessionWindowSec: cfg.RateLimitSessionWindowSec,
		EmailSender:               emailSender,
		FlowWebURL:                cfg.FlowWebURL,
		AccountsWebURL:            cfg.AccountsWebURL,
		Integrations:              integrationsRegistry,
		PublicBaseURL:             cfg.PublicBaseURL,
		WebBaseURL:                cfg.FlowWebURL,
		Storage:                   storageClient,
	})

	if cfg.DisableRateLimit {
		logger.Warn("rate limiting disabled (NF_AUTH_DISABLE_RATE_LIMIT=true)")
	}

	outer := chi.NewRouter()
	outer.Use(httputil.BuildCORS(cfg.CorsAllowedOrigins, cfg.CorsDevLocalhost))
	outer.Mount("/", inner)

	addr := ":" + cfg.Port
	srv := &http.Server{
		Addr:              addr,
		Handler:           outer,
		ReadHeaderTimeout: 10 * time.Second,
	}

	stopCtx, stopCancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stopCancel()

	serverErr := make(chan error, 1)
	go func() {
		logger.Info("auth-api listening", "addr", addr)
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
	logger.Info("auth-api shutdown complete")
}
