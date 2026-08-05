package sessionstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/libraz/nodate-flow/packages/go-shared/dbtype"
)

// SessionQueries abstracts the sqlc-generated query methods that
// MySQLStore depends on. Each app implements this interface by
// wrapping its own generated.Queries bundle.
type SessionQueries interface {
	// CreateSession inserts a new session row and returns the last
	// insert id (int64).
	CreateSession(ctx context.Context, p CreateSessionParams) (int64, error)

	// FindSessionByRefreshHash looks up an active session by its
	// SHA-256 refresh hash. Returns sql.ErrNoRows when not found.
	FindSessionByRefreshHash(ctx context.Context, refreshHash string) (FindSessionByRefreshHashRow, error)

	// FindAnySessionByRefreshHash looks up a session by its SHA-256
	// refresh hash regardless of revoked / enabled state. Returns
	// sql.ErrNoRows when no row matches.
	FindAnySessionByRefreshHash(ctx context.Context, refreshHash string) (FindAnySessionByRefreshHashRow, error)

	// FindSessionByRotatedFromHash looks up the session whose last
	// rotation replaced the given hash. Returns sql.ErrNoRows when no
	// session superseded it.
	FindSessionByRotatedFromHash(ctx context.Context, rotatedFromHash string) (FindAnySessionByRefreshHashRow, error)

	// RevokeSession marks a session as revoked by (userID, publicID).
	RevokeSession(ctx context.Context, userID uint32, publicID dbtype.PublicID) error

	// ListSessionsForUser returns active sessions for a user with
	// pagination.
	ListSessionsForUser(ctx context.Context, userID uint32, limit, offset int32) ([]ListSessionsForUserRow, error)

	// RevokeAllSessionsForUserExcept revokes all active sessions for
	// a user except one identified by publicID.
	RevokeAllSessionsForUserExcept(ctx context.Context, userID uint32, publicID dbtype.PublicID) error

	// RevokeAllSessionsForUser revokes every active session for a user.
	RevokeAllSessionsForUser(ctx context.Context, userID uint32) error
}

// CreateSessionParams mirrors the sqlc-generated CreateSessionParams
// so the interface does not depend on any generated package.
type CreateSessionParams struct {
	PublicID    dbtype.PublicID
	UserID      uint32
	RefreshHash string
	UserAgent   sql.NullString
	IpAddress   sql.NullString
	ExpiresAt   time.Time
}

// FindSessionByRefreshHashRow mirrors the sqlc-generated row type
// returned by FindSessionByRefreshHash.
type FindSessionByRefreshHashRow struct {
	ID          uint32
	PublicID    dbtype.PublicID
	UserID      uint32
	RefreshHash string
	UserAgent   sql.NullString
	IpAddress   sql.NullString
	ExpiresAt   time.Time
	RevokedAt   sql.NullTime
	LastUsedAt  sql.NullTime
	CreatedAt   time.Time
}

// FindAnySessionByRefreshHashRow mirrors the sqlc-generated row type
// returned by FindAnySessionByRefreshHash. It omits user_agent /
// ip_address because the reuse detector only needs identity, ownership,
// and revocation state.
type FindAnySessionByRefreshHashRow struct {
	ID          uint32
	PublicID    dbtype.PublicID
	UserID      uint32
	RefreshHash string
	ExpiresAt   time.Time
	RevokedAt   sql.NullTime
	LastUsedAt  sql.NullTime
	Enabled     bool
	CreatedAt   time.Time
}

// ListSessionsForUserRow mirrors the sqlc-generated row type returned
// by ListSessionsForUser.
type ListSessionsForUserRow struct {
	PublicID   dbtype.PublicID
	UserAgent  sql.NullString
	IpAddress  sql.NullString
	ExpiresAt  time.Time
	LastUsedAt sql.NullTime
	CreatedAt  time.Time
}

// MySQLStore is the default [Store] implementation. It wraps the
// existing sqlc-generated session queries so the refactor to the
// driver interface is behavior-preserving.
type MySQLStore struct {
	db *sql.DB
	q  SessionQueries
}

// NewMySQLStore returns a [Store] backed by the sqlc query bundle.
// The db handle is used for transaction support in RotateRefreshHash.
func NewMySQLStore(db *sql.DB, q SessionQueries) *MySQLStore {
	return &MySQLStore{db: db, q: q}
}

// Create implements [Store].
func (s *MySQLStore) Create(ctx context.Context, p CreateParams) (uint32, error) {
	id, err := s.q.CreateSession(ctx, CreateSessionParams{
		PublicID:    p.PublicID,
		UserID:      p.UserID,
		RefreshHash: p.RefreshHash,
		UserAgent:   sql.NullString{String: p.UserAgent, Valid: p.UserAgent != ""},
		// sessions.ip_address is VARBINARY(16): the text form of a real
		// IPv6 address does not fit and MySQL rejects the whole insert
		// in STRICT mode, which would fail the login that issued it.
		IpAddress: dbtype.NullStringFromIP(p.IPAddress),
		ExpiresAt: p.ExpiresAt,
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
		IPAddress:   dbtype.IPStringFromNullString(row.IpAddress),
		ExpiresAt:   row.ExpiresAt,
		RevokedAt:   nullTimePtr(row.RevokedAt),
		LastUsedAt:  nullTimePtr(row.LastUsedAt),
		CreatedAt:   row.CreatedAt,
	}, nil
}

// FindAnyByRefreshHash implements [Store].
func (s *MySQLStore) FindAnyByRefreshHash(ctx context.Context, hash string) (*Session, error) {
	row, err := s.q.FindAnySessionByRefreshHash(ctx, hash)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("sessionstore/mysql: find-any: %w", err)
	}
	return &Session{
		InternalID:  row.ID,
		PublicID:    row.PublicID,
		UserID:      row.UserID,
		RefreshHash: row.RefreshHash,
		ExpiresAt:   row.ExpiresAt,
		RevokedAt:   nullTimePtr(row.RevokedAt),
		LastUsedAt:  nullTimePtr(row.LastUsedAt),
		CreatedAt:   row.CreatedAt,
	}, nil
}

// FindSupersededBy implements [Store] by resolving the session whose
// last rotation replaced this hash.
func (s *MySQLStore) FindSupersededBy(ctx context.Context, hash string) (*Session, error) {
	row, err := s.q.FindSessionByRotatedFromHash(ctx, hash)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("sessionstore/mysql: find-superseded: %w", err)
	}
	return &Session{
		InternalID:  row.ID,
		PublicID:    row.PublicID,
		UserID:      row.UserID,
		RefreshHash: row.RefreshHash,
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

	// rotated_from_hash is what makes the replaced token traceable. The
	// row keeps its identity across rotations, so overwriting
	// refresh_hash on its own leaves the superseded token matching
	// nothing at all — a replay of it then looks exactly like a token
	// that was never issued, and the reuse detector has nothing to find.
	const updateQ = `UPDATE sessions
	                    SET refresh_hash = ?,
	                        rotated_from_hash = ?,
	                        expires_at = ?,
	                        last_used_at = CURRENT_TIMESTAMP
	                  WHERE id = ?`
	if _, err := tx.ExecContext(ctx, updateQ, newHash, oldHash, expiresAt, id); err != nil {
		return fmt.Errorf("sessionstore/mysql: rotate update: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("sessionstore/mysql: rotate commit: %w", err)
	}
	return nil
}

// Revoke implements [Store].
func (s *MySQLStore) Revoke(ctx context.Context, userID uint32, publicID dbtype.PublicID) error {
	if err := s.q.RevokeSession(ctx, userID, publicID); err != nil {
		return fmt.Errorf("sessionstore/mysql: revoke: %w", err)
	}
	return nil
}

// ListActive implements [Store].
func (s *MySQLStore) ListActive(ctx context.Context, userID uint32) ([]Session, error) {
	rows, err := s.q.ListSessionsForUser(ctx, userID, 100, 0)
	if err != nil {
		return nil, fmt.Errorf("sessionstore/mysql: list: %w", err)
	}
	out := make([]Session, 0, len(rows))
	for _, r := range rows {
		out = append(out, Session{
			PublicID:   r.PublicID,
			UserID:     userID,
			UserAgent:  r.UserAgent.String,
			IPAddress:  dbtype.IPStringFromNullString(r.IpAddress),
			ExpiresAt:  r.ExpiresAt,
			LastUsedAt: nullTimePtr(r.LastUsedAt),
			CreatedAt:  r.CreatedAt,
		})
	}
	return out, nil
}

// RevokeAllExcept implements [Store].
func (s *MySQLStore) RevokeAllExcept(ctx context.Context, userID uint32, keep dbtype.PublicID) error {
	if err := s.q.RevokeAllSessionsForUserExcept(ctx, userID, keep); err != nil {
		return fmt.Errorf("sessionstore/mysql: revoke-all-except: %w", err)
	}
	return nil
}

// RevokeAllForUser implements [Store].
func (s *MySQLStore) RevokeAllForUser(ctx context.Context, userID uint32) error {
	if err := s.q.RevokeAllSessionsForUser(ctx, userID); err != nil {
		return fmt.Errorf("sessionstore/mysql: revoke-all-for-user: %w", err)
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
