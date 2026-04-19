package sessionstore

import (
	"context"
	"database/sql"

	"github.com/nodate-flow/nodate-flow/apps/auth-api/internal/db/generated"
	"github.com/nodate-flow/nodate-flow/packages/go-shared/dbtype"
	ss "github.com/nodate-flow/nodate-flow/packages/go-shared/sessionstore"
)

// MySQLStore is the default [Store] implementation backed by the
// sqlc-generated query bundle via the shared sessionstore package.
type MySQLStore = ss.MySQLStore

// NewMySQLStore returns a [Store] backed by the sqlc query bundle.
// The db handle is used for transaction support in RotateRefreshHash.
func NewMySQLStore(db *sql.DB, q *generated.Queries) *MySQLStore {
	return ss.NewMySQLStore(db, &queriesAdapter{q: q})
}

// queriesAdapter bridges the app-specific sqlc generated.Queries to
// the shared [ss.SessionQueries] interface.
type queriesAdapter struct {
	q *generated.Queries
}

func (a *queriesAdapter) CreateSession(ctx context.Context, p ss.CreateSessionParams) (int64, error) {
	return a.q.CreateSession(ctx, generated.CreateSessionParams{
		PublicID:    p.PublicID,
		UserID:      p.UserID,
		RefreshHash: p.RefreshHash,
		UserAgent:   p.UserAgent,
		IpAddress:   p.IpAddress,
		ExpiresAt:   p.ExpiresAt,
	})
}

func (a *queriesAdapter) FindSessionByRefreshHash(ctx context.Context, refreshHash string) (ss.FindSessionByRefreshHashRow, error) {
	row, err := a.q.FindSessionByRefreshHash(ctx, refreshHash)
	if err != nil {
		return ss.FindSessionByRefreshHashRow{}, err
	}
	return ss.FindSessionByRefreshHashRow{
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

func (a *queriesAdapter) ListSessionsForUser(ctx context.Context, userID uint32, limit, offset int32) ([]ss.ListSessionsForUserRow, error) {
	rows, err := a.q.ListSessionsForUser(ctx, generated.ListSessionsForUserParams{
		UserID: userID,
		Limit:  limit,
		Offset: offset,
	})
	if err != nil {
		return nil, err
	}
	out := make([]ss.ListSessionsForUserRow, len(rows))
	for i, r := range rows {
		out[i] = ss.ListSessionsForUserRow{
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

func (a *queriesAdapter) RevokeAllSessionsForUserExcept(ctx context.Context, userID uint32, publicID dbtype.PublicID) error {
	return a.q.RevokeAllSessionsForUserExcept(ctx, generated.RevokeAllSessionsForUserExceptParams{
		UserID:   userID,
		PublicID: publicID,
	})
}
