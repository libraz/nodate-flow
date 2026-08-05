// Package sessadapter bridges flow-api's sqlc-generated session queries
// to the shared sessionstore.SessionQueries interface.
package sessadapter

import (
	"context"
	"database/sql"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/packages/go-shared/dbtype"
	"github.com/libraz/nodate-flow/packages/go-shared/sessionstore"
)

// NewMySQLStore returns a shared sessionstore.MySQLStore backed by the
// flow-api sqlc query bundle. The queriesAdapter bridges the
// app-specific generated.Queries to the shared SessionQueries interface.
func NewMySQLStore(db *sql.DB, q *generated.Queries) *sessionstore.MySQLStore {
	return sessionstore.NewMySQLStore(db, &queriesAdapter{q: q})
}

// queriesAdapter bridges the app-specific sqlc generated.Queries to
// the shared [sessionstore.SessionQueries] interface.
type queriesAdapter struct {
	q *generated.Queries
}

func (a *queriesAdapter) CreateSession(ctx context.Context, p sessionstore.CreateSessionParams) (int64, error) {
	return a.q.CreateSession(ctx, generated.CreateSessionParams{
		PublicID:    p.PublicID,
		UserID:      p.UserID,
		RefreshHash: p.RefreshHash,
		UserAgent:   p.UserAgent,
		IpAddress:   p.IpAddress,
		ExpiresAt:   p.ExpiresAt,
	})
}

func (a *queriesAdapter) FindSessionByRefreshHash(ctx context.Context, refreshHash string) (sessionstore.FindSessionByRefreshHashRow, error) {
	row, err := a.q.FindSessionByRefreshHash(ctx, refreshHash)
	if err != nil {
		return sessionstore.FindSessionByRefreshHashRow{}, err
	}
	return sessionstore.FindSessionByRefreshHashRow{
		ID:          row.ID,
		PublicID:    row.PublicID,
		UserID:      row.UserID,
		RefreshHash: row.RefreshHash,
		UserAgent:   row.UserAgent,
		IpAddress:   row.IpAddress,
		ExpiresAt:   row.ExpiresAt,
		RevokedAt:   row.RevokedAt,
		LastUsedAt:  row.LastUsedAt,
		CreatedAt:   row.CreatedAt,
	}, nil
}

func (a *queriesAdapter) RevokeSession(ctx context.Context, userID uint32, publicID dbtype.PublicID) error {
	return a.q.RevokeSession(ctx, generated.RevokeSessionParams{
		UserID:   userID,
		PublicID: publicID,
	})
}

func (a *queriesAdapter) ListSessionsForUser(ctx context.Context, userID uint32, limit, offset int32) ([]sessionstore.ListSessionsForUserRow, error) {
	rows, err := a.q.ListSessionsForUser(ctx, generated.ListSessionsForUserParams{
		UserID: userID,
		Limit:  limit,
		Offset: offset,
	})
	if err != nil {
		return nil, err
	}
	out := make([]sessionstore.ListSessionsForUserRow, len(rows))
	for i, r := range rows {
		out[i] = sessionstore.ListSessionsForUserRow{
			PublicID:   r.PublicID,
			UserAgent:  r.UserAgent,
			IpAddress:  r.IpAddress,
			ExpiresAt:  r.ExpiresAt,
			LastUsedAt: r.LastUsedAt,
			CreatedAt:  r.CreatedAt,
		}
	}
	return out, nil
}

func (a *queriesAdapter) FindSessionByRotatedFromHash(ctx context.Context, rotatedFromHash string) (sessionstore.FindAnySessionByRefreshHashRow, error) {
	row, err := a.q.FindSessionByRotatedFromHash(ctx, sql.NullString{String: rotatedFromHash, Valid: true})
	if err != nil {
		return sessionstore.FindAnySessionByRefreshHashRow{}, err
	}
	return sessionstore.FindAnySessionByRefreshHashRow{
		ID:          row.ID,
		PublicID:    row.PublicID,
		UserID:      row.UserID,
		RefreshHash: row.RefreshHash,
		ExpiresAt:   row.ExpiresAt,
		RevokedAt:   row.RevokedAt,
		LastUsedAt:  row.LastUsedAt,
		Enabled:     row.Enabled,
		CreatedAt:   row.CreatedAt,
	}, nil
}

func (a *queriesAdapter) RevokeAllSessionsForUserExcept(ctx context.Context, userID uint32, publicID dbtype.PublicID) error {
	return a.q.RevokeAllSessionsForUserExcept(ctx, generated.RevokeAllSessionsForUserExceptParams{
		UserID:   userID,
		PublicID: publicID,
	})
}

func (a *queriesAdapter) FindAnySessionByRefreshHash(ctx context.Context, refreshHash string) (sessionstore.FindAnySessionByRefreshHashRow, error) {
	row, err := a.q.FindAnySessionByRefreshHash(ctx, refreshHash)
	if err != nil {
		return sessionstore.FindAnySessionByRefreshHashRow{}, err
	}
	return sessionstore.FindAnySessionByRefreshHashRow{
		ID:          row.ID,
		PublicID:    row.PublicID,
		UserID:      row.UserID,
		RefreshHash: row.RefreshHash,
		ExpiresAt:   row.ExpiresAt,
		RevokedAt:   row.RevokedAt,
		LastUsedAt:  row.LastUsedAt,
		Enabled:     row.Enabled,
		CreatedAt:   row.CreatedAt,
	}, nil
}

func (a *queriesAdapter) RevokeAllSessionsForUser(ctx context.Context, userID uint32) error {
	return a.q.RevokeAllSessionsForUser(ctx, userID)
}
