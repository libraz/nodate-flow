package sessionstore

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/nodate-flow/nodate-flow/apps/api/internal/db/generated"
	"github.com/nodate-flow/nodate-flow/apps/api/internal/db/types"
)

// MySQLStore is the default [Store] implementation. It wraps the
// existing sqlc-generated session queries so the refactor to the
// driver interface is behavior-preserving.
type MySQLStore struct {
	q *generated.Queries
}

// NewMySQLStore returns a [Store] backed by the sqlc query bundle.
func NewMySQLStore(q *generated.Queries) *MySQLStore {
	return &MySQLStore{q: q}
}

// Create implements [Store].
func (s *MySQLStore) Create(ctx context.Context, p CreateParams) (uint32, error) {
	id, err := s.q.CreateSession(ctx, generated.CreateSessionParams{
		PublicID:    p.PublicID,
		UserID:      p.UserID,
		RefreshHash: p.RefreshHash,
		UserAgent:   sql.NullString{String: p.UserAgent, Valid: p.UserAgent != ""},
		IpAddress:   sql.NullString{String: p.IPAddress, Valid: p.IPAddress != ""},
		ExpiresAt:   p.ExpiresAt,
	})
	if err != nil {
		return 0, err
	}
	return uint32(id), nil //nolint:gosec // sqlc returns int64 but sessions.id is INT UNSIGNED
}

// FindByRefreshHash implements [Store].
func (s *MySQLStore) FindByRefreshHash(ctx context.Context, hash string) (*Session, error) {
	row, err := s.q.FindSessionByRefreshHash(ctx, hash)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &Session{
		InternalID:  row.ID,
		PublicID:    row.PublicID,
		UserID:      row.UserID,
		RefreshHash: row.RefreshHash,
		UserAgent:   row.UserAgent.String,
		IPAddress:   row.IpAddress.String,
		ExpiresAt:   row.ExpiresAt,
		RevokedAt:   nullTimePtr(row.RevokedAt),
		LastUsedAt:  nullTimePtr(row.LastUsedAt),
		CreatedAt:   row.CreatedAt,
	}, nil
}

// RotateRefreshHash implements [Store]. The MySQL driver resolves the
// row by the old hash first so it can target the internal PK, which
// matches the existing RotateSessionRefreshHash :exec contract.
func (s *MySQLStore) RotateRefreshHash(ctx context.Context, oldHash, newHash string, expiresAt time.Time) error {
	row, err := s.q.FindSessionByRefreshHash(ctx, oldHash)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	return s.q.RotateSessionRefreshHash(ctx, generated.RotateSessionRefreshHashParams{
		RefreshHash: newHash,
		ExpiresAt:   expiresAt,
		ID:          row.ID,
	})
}

// Revoke implements [Store].
func (s *MySQLStore) Revoke(ctx context.Context, userID uint32, publicID types.PublicID) error {
	return s.q.RevokeSession(ctx, generated.RevokeSessionParams{
		UserID:   userID,
		PublicID: publicID,
	})
}

// ListActive implements [Store].
func (s *MySQLStore) ListActive(ctx context.Context, userID uint32) ([]Session, error) {
	rows, err := s.q.ListSessionsForUser(ctx, generated.ListSessionsForUserParams{
		UserID: userID,
		Limit:  100,
		Offset: 0,
	})
	if err != nil {
		return nil, err
	}
	out := make([]Session, 0, len(rows))
	for _, r := range rows {
		out = append(out, Session{
			PublicID:   r.PublicID,
			UserID:     userID,
			UserAgent:  r.UserAgent.String,
			IPAddress:  r.IpAddress.String,
			ExpiresAt:  r.ExpiresAt,
			LastUsedAt: nullTimePtr(r.LastUsedAt),
			CreatedAt:  r.CreatedAt,
		})
	}
	return out, nil
}

// RevokeAllExcept implements [Store].
func (s *MySQLStore) RevokeAllExcept(ctx context.Context, userID uint32, keep types.PublicID) error {
	return s.q.RevokeAllSessionsForUserExcept(ctx, generated.RevokeAllSessionsForUserExceptParams{
		UserID:   userID,
		PublicID: keep,
	})
}

func nullTimePtr(n sql.NullTime) *time.Time {
	if !n.Valid {
		return nil
	}
	t := n.Time
	return &t
}
