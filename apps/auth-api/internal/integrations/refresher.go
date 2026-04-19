package integrations

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"time"

	"github.com/nodate-flow/nodate-flow/packages/go-shared/crypto"
	"github.com/nodate-flow/nodate-flow/apps/auth-api/internal/db/generated"
)

// RefresherQuerier is the narrow subset of [generated.Querier] the
// refresher needs. Declared as an interface so tests can substitute
// an in-memory fake without pulling in the full sqlc surface.
type RefresherQuerier interface {
	ListConnectionsExpiringBefore(ctx context.Context, cutoff sql.NullTime) ([]generated.ListConnectionsExpiringBeforeRow, error)
	UpdateConnectionTokens(ctx context.Context, arg generated.UpdateConnectionTokensParams) error
}

// Refresher is a background worker that proactively refreshes
// OAuth access tokens before they expire, so foreground handlers
// (MCP, AI ingest) never race a 401. It loops on Interval and, on
// each tick, picks up every enabled user_integrations row whose
// access_token_expires_at is within LeadTime of "now", decrypts
// the refresh token, calls Provider.Refresh, and writes the new
// token ciphertext + expiry back to the row.
//
// Providers that return ErrRefreshNotSupported (GitHub OAuth Apps,
// Slack user tokens) are skipped — their rows normally do not have
// a non-NULL expires_at in the first place so they never surface
// in the listing anyway, but the guard keeps the worker correct
// if a provider later starts issuing expiries.
type Refresher struct {
	Queries  RefresherQuerier
	Cipher   *crypto.Cipher
	Registry *Registry
	Logger   *slog.Logger

	// Interval is the ticker period. Defaults to 10 minutes.
	Interval time.Duration
	// LeadTime is the "refresh a token if it expires within this
	// window" horizon. Defaults to 15 minutes.
	LeadTime time.Duration
}

const (
	defaultRefresherInterval = 10 * time.Minute
	defaultRefresherLeadTime = 15 * time.Minute
)

// Run blocks until ctx is cancelled, running RefreshOnce on every
// tick. Errors from a single pass are logged and do not crash the
// loop; individual row failures inside a pass are likewise logged
// so one broken account cannot starve the rest.
func (r *Refresher) Run(ctx context.Context) {
	interval := r.Interval
	if interval <= 0 {
		interval = defaultRefresherInterval
	}
	log := r.logger()
	log.Info("integrations refresher started", "interval", interval, "lead_time", r.leadTime())

	// Kick once immediately so a freshly started binary does not
	// wait a full interval before its first refresh pass.
	if err := r.RefreshOnce(ctx); err != nil && !errors.Is(err, context.Canceled) {
		log.Warn("integrations refresher initial pass failed", "err", err)
	}

	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			log.Info("integrations refresher stopped")
			return
		case <-t.C:
			if err := r.RefreshOnce(ctx); err != nil && !errors.Is(err, context.Canceled) {
				log.Warn("integrations refresher pass failed", "err", err)
			}
		}
	}
}

// RefreshOnce runs a single refresh pass and returns. Exposed so
// tests (and a possible future admin endpoint) can drive the
// worker synchronously without spinning up the ticker loop.
func (r *Refresher) RefreshOnce(ctx context.Context) error {
	log := r.logger()
	cutoff := sql.NullTime{Time: time.Now().Add(r.leadTime()), Valid: true}
	rows, err := r.Queries.ListConnectionsExpiringBefore(ctx, cutoff)
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	for _, row := range rows {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		r.refreshRow(ctx, row, log)
	}
	return nil
}

func (r *Refresher) refreshRow(ctx context.Context, row generated.ListConnectionsExpiringBeforeRow, log *slog.Logger) {
	provider, err := r.Registry.Get(string(row.Provider))
	if err != nil {
		log.Warn("integrations refresher: provider unavailable",
			"provider", row.Provider, "user_id", row.UserID, "err", err)
		return
	}
	if !row.RefreshTokenCiphertext.Valid || len(row.RefreshTokenCiphertext.String) == 0 {
		return
	}
	refreshPlain, err := r.Cipher.Decrypt([]byte(row.RefreshTokenCiphertext.String))
	if err != nil {
		log.Warn("integrations refresher: decrypt refresh token failed",
			"provider", row.Provider, "user_id", row.UserID, "err", err)
		return
	}
	tok, err := provider.Refresh(ctx, string(refreshPlain))
	if err != nil {
		if errors.Is(err, ErrRefreshNotSupported) {
			return
		}
		log.Warn("integrations refresher: provider refresh failed",
			"provider", row.Provider, "user_id", row.UserID, "err", err)
		return
	}
	if tok == nil || tok.AccessToken == "" {
		log.Warn("integrations refresher: empty token set",
			"provider", row.Provider, "user_id", row.UserID)
		return
	}
	newAccess, err := r.Cipher.Encrypt([]byte(tok.AccessToken))
	if err != nil {
		log.Warn("integrations refresher: encrypt access token failed",
			"provider", row.Provider, "user_id", row.UserID, "err", err)
		return
	}
	// Preserve existing refresh token if the provider did not
	// rotate one (Google's common case).
	newRefresh := row.RefreshTokenCiphertext
	if tok.RefreshToken != "" && tok.RefreshToken != string(refreshPlain) {
		enc, encErr := r.Cipher.Encrypt([]byte(tok.RefreshToken))
		if encErr != nil {
			log.Warn("integrations refresher: encrypt refresh token failed",
				"provider", row.Provider, "user_id", row.UserID, "err", encErr)
			return
		}
		newRefresh = sql.NullString{String: string(enc), Valid: true}
	}
	var expires sql.NullTime
	if !tok.ExpiresAt.IsZero() {
		expires = sql.NullTime{Time: tok.ExpiresAt, Valid: true}
	}
	if err := r.Queries.UpdateConnectionTokens(ctx, generated.UpdateConnectionTokensParams{
		AccessTokenCiphertext:  newAccess,
		RefreshTokenCiphertext: newRefresh,
		AccessTokenExpiresAt:   expires,
		ID:                     row.ID,
	}); err != nil {
		log.Warn("integrations refresher: update row failed",
			"provider", row.Provider, "user_id", row.UserID, "err", err)
		return
	}
	log.Info("integrations refresher: token refreshed",
		"provider", row.Provider, "user_id", row.UserID, "expires_at", tok.ExpiresAt)
}

func (r *Refresher) logger() *slog.Logger {
	if r.Logger != nil {
		return r.Logger
	}
	return slog.Default()
}

func (r *Refresher) leadTime() time.Duration {
	if r.LeadTime > 0 {
		return r.LeadTime
	}
	return defaultRefresherLeadTime
}
