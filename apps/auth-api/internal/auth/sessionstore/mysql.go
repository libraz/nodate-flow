package sessionstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/nodate-flow/nodate-flow/apps/auth-api/internal/db/generated"
	"github.com/nodate-flow/nodate-flow/apps/auth-api/internal/db/types"
)

// MySQLStore is the default [Store] implementation. It wraps the
// existing sqlc-generated session queries so the refactor to the
// driver interface is behavior-preserving.
type MySQLStore struct {
	db *sql.DB
	q  *generated.Queries
}

// NewMySQLStore returns a [Store] backed by the sqlc query bundle.
// The db handle is used for transaction support in RotateRefreshHash.
func NewMySQLStore(db *sql.DB, q *generated.Queries) *MySQLStore {
	return &MySQLStore{db: db, q: q}
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
		return 0, fmt.Errorf("sessionstore/mysql: create: %w", err)
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
		return nil, fmt.Errorf("sessionstore/mysql: find: %w", err)
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

// RotateRefreshHash implements [Store]. The rotation is wrapped in a
// transaction with SELECT ... FOR UPDATE to prevent TOCTOU races when
// concurrent refresh requests arrive for the same session.
func (s *MySQLStore) RotateRefreshHash(ctx context.Context, oldHash, newHash string, expiresAt time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("sessionstore/mysql: begin tx: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // rollback after commit is no-op

	// Lock the row to prevent concurrent rotation of the same refresh hash.
	const lockQ = `SELECT id FROM sessions WHERE refresh_hash = ? AND enabled = TRUE AND revoked_at IS NULL FOR UPDATE`
	var id uint32
	if err := tx.QueryRowContext(ctx, lockQ, oldHash).Scan(&id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return fmt.Errorf("sessionstore/mysql: rotate lock: %w", err)
	}

	const updateQ = `UPDATE sessions SET refresh_hash = ?, expires_at = ?, last_used_at = CURRENT_TIMESTAMP WHERE id = ?`
	if _, err := tx.ExecContext(ctx, updateQ, newHash, expiresAt, id); err != nil {
		return fmt.Errorf("sessionstore/mysql: rotate update: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("sessionstore/mysql: rotate commit: %w", err)
	}
	return nil
}

// Revoke implements [Store].
func (s *MySQLStore) Revoke(ctx context.Context, userID uint32, publicID types.PublicID) error {
	if err := s.q.RevokeSession(ctx, generated.RevokeSessionParams{
		UserID:   userID,
		PublicID: publicID,
	}); err != nil {
		return fmt.Errorf("sessionstore/mysql: revoke: %w", err)
	}
	return nil
}

// ListActive implements [Store].
func (s *MySQLStore) ListActive(ctx context.Context, userID uint32) ([]Session, error) {
	rows, err := s.q.ListSessionsForUser(ctx, generated.ListSessionsForUserParams{
		UserID: userID,
		Limit:  100,
		Offset: 0,
	})
	if err != nil {
		return nil, fmt.Errorf("sessionstore/mysql: list: %w", err)
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
	if err := s.q.RevokeAllSessionsForUserExcept(ctx, generated.RevokeAllSessionsForUserExceptParams{
		UserID:   userID,
		PublicID: keep,
	}); err != nil {
		return fmt.Errorf("sessionstore/mysql: revoke-all-except: %w", err)
	}
	return nil
}

func nullTimePtr(n sql.NullTime) *time.Time {
	if !n.Valid {
		return nil
	}
	t := n.Time
	return &t
}
