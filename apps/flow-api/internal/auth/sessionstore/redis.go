// Package sessionstore — Redis driver.
//
// Keyspace:
//
//	session:<refresh_hash>         HASH  { publicId, userId, ua, ip, expiresAt, createdAt }
//	user:<userId>:sessions         SET   of refresh_hash strings
//	session:pub:<publicId>         STRING refresh_hash  (reverse lookup for Revoke)
//
// Every key is written with EX = ttl-until-expiresAt so Redis handles
// purging. Revoke deletes the 3 keys; RotateRefreshHash rekeys the
// HASH to a new <refresh_hash> key and updates the pub reverse index.
package sessionstore

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/types"
)

// RedisStore is the Redis-backed [Store] implementation.
type RedisStore struct {
	rdb *redis.Client
}

// NewRedisStore returns a [Store] backed by an initialised
// *redis.Client. The caller owns the client lifecycle.
func NewRedisStore(rdb *redis.Client) *RedisStore { return &RedisStore{rdb: rdb} }

func sessionKey(hash string) string   { return "session:" + hash }
func userKey(userID uint32) string    { return "user:" + strconv.FormatUint(uint64(userID), 10) + ":sessions" }
func pubKey(publicID types.PublicID) string {
	return "session:pub:" + hex.EncodeToString(publicID[:])
}

// Create implements [Store].
func (s *RedisStore) Create(ctx context.Context, p CreateParams) (uint32, error) {
	ttl := time.Until(p.ExpiresAt)
	if ttl <= 0 {
		return 0, errors.New("sessionstore/redis: expiresAt in the past")
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	pipe := s.rdb.TxPipeline()
	pipe.HSet(ctx, sessionKey(p.RefreshHash), map[string]interface{}{
		"publicId":  hex.EncodeToString(p.PublicID[:]),
		"userId":    p.UserID,
		"ua":        p.UserAgent,
		"ip":        p.IPAddress,
		"expiresAt": p.ExpiresAt.Format(time.RFC3339Nano),
		"createdAt": now,
	})
	pipe.Expire(ctx, sessionKey(p.RefreshHash), ttl)
	pipe.SAdd(ctx, userKey(p.UserID), p.RefreshHash)
	pipe.Expire(ctx, userKey(p.UserID), ttl)
	pipe.Set(ctx, pubKey(p.PublicID), p.RefreshHash, ttl)
	if _, err := pipe.Exec(ctx); err != nil {
		return 0, fmt.Errorf("sessionstore/redis: create: %w", err)
	}
	return 0, nil
}

// FindByRefreshHash implements [Store].
func (s *RedisStore) FindByRefreshHash(ctx context.Context, hash string) (*Session, error) {
	m, err := s.rdb.HGetAll(ctx, sessionKey(hash)).Result()
	if err != nil {
		return nil, fmt.Errorf("sessionstore/redis: hgetall: %w", err)
	}
	if len(m) == 0 {
		return nil, ErrNotFound
	}
	uid64, err := strconv.ParseUint(m["userId"], 10, 32)
	if err != nil {
		return nil, fmt.Errorf("sessionstore/redis: invalid userId %q: %w", m["userId"], err)
	}
	exp, err := time.Parse(time.RFC3339Nano, m["expiresAt"])
	if err != nil {
		return nil, fmt.Errorf("sessionstore/redis: invalid expiresAt %q: %w", m["expiresAt"], err)
	}
	created, err := time.Parse(time.RFC3339Nano, m["createdAt"])
	if err != nil {
		return nil, fmt.Errorf("sessionstore/redis: invalid createdAt %q: %w", m["createdAt"], err)
	}
	pubBytes, err := hex.DecodeString(m["publicId"])
	if err != nil {
		return nil, fmt.Errorf("sessionstore/redis: invalid publicId %q: %w", m["publicId"], err)
	}
	var pub types.PublicID
	copy(pub[:], pubBytes)
	var lastUsed *time.Time
	if raw, ok := m["lastUsedAt"]; ok && raw != "" {
		if t, err := time.Parse(time.RFC3339Nano, raw); err == nil {
			lastUsed = &t
		}
	}
	return &Session{
		PublicID:    pub,
		UserID:      uint32(uid64),
		RefreshHash: hash,
		UserAgent:   m["ua"],
		IPAddress:   m["ip"],
		ExpiresAt:   exp,
		LastUsedAt:  lastUsed,
		CreatedAt:   created,
	}, nil
}

// RotateRefreshHash implements [Store]. Reads the old entry, writes a
// new one under the new hash key with the extended TTL, then deletes
// the old key. The user-set and pub reverse index are updated in the
// same TxPipeline so failures leave no dangling references.
func (s *RedisStore) RotateRefreshHash(ctx context.Context, oldHash, newHash string, expiresAt time.Time) error {
	old, err := s.FindByRefreshHash(ctx, oldHash)
	if err != nil {
		return err
	}
	ttl := time.Until(expiresAt)
	if ttl <= 0 {
		return errors.New("sessionstore/redis: rotated expiresAt in the past")
	}
	pipe := s.rdb.TxPipeline()
	pipe.HSet(ctx, sessionKey(newHash), map[string]interface{}{
		"publicId":    hex.EncodeToString(old.PublicID[:]),
		"userId":      old.UserID,
		"ua":          old.UserAgent,
		"ip":          old.IPAddress,
		"expiresAt":   expiresAt.Format(time.RFC3339Nano),
		"createdAt":   old.CreatedAt.Format(time.RFC3339Nano),
		"lastUsedAt":  time.Now().UTC().Format(time.RFC3339Nano),
	})
	pipe.Expire(ctx, sessionKey(newHash), ttl)
	pipe.Del(ctx, sessionKey(oldHash))
	pipe.SRem(ctx, userKey(old.UserID), oldHash)
	pipe.SAdd(ctx, userKey(old.UserID), newHash)
	pipe.Expire(ctx, userKey(old.UserID), ttl)
	pipe.Set(ctx, pubKey(old.PublicID), newHash, ttl)
	_, err = pipe.Exec(ctx)
	return err
}

// ListActive implements [Store]. It iterates the user's set of
// refresh hashes and hydrates each HASH entry. Expired keys drop out
// automatically because Redis TTL purges them.
func (s *RedisStore) ListActive(ctx context.Context, userID uint32) ([]Session, error) {
	hashes, err := s.rdb.SMembers(ctx, userKey(userID)).Result()
	if err != nil {
		return nil, fmt.Errorf("sessionstore/redis: smembers: %w", err)
	}
	out := make([]Session, 0, len(hashes))
	for _, h := range hashes {
		sess, err := s.FindByRefreshHash(ctx, h)
		if errors.Is(err, ErrNotFound) {
			// TTL purged; drop the stale reference.
			_ = s.rdb.SRem(ctx, userKey(userID), h).Err()
			continue
		}
		if err != nil {
			return nil, err
		}
		out = append(out, *sess)
	}
	return out, nil
}

// RevokeAllExcept implements [Store].
func (s *RedisStore) RevokeAllExcept(ctx context.Context, userID uint32, keep types.PublicID) error {
	sessions, err := s.ListActive(ctx, userID)
	if err != nil {
		return err
	}
	for _, sess := range sessions {
		if sess.PublicID == keep {
			continue
		}
		if err := s.Revoke(ctx, userID, sess.PublicID); err != nil {
			return err
		}
	}
	return nil
}

// Revoke implements [Store]. The publicID reverse index gives us the
// current refresh hash so we can delete the HASH and pop it from the
// user's set in one pipeline. The session's userID is validated before
// deletion to prevent cross-user revocation.
func (s *RedisStore) Revoke(ctx context.Context, userID uint32, publicID types.PublicID) error {
	hash, err := s.rdb.Get(ctx, pubKey(publicID)).Result()
	if errors.Is(err, redis.Nil) {
		return nil
	}
	if err != nil {
		return err
	}
	// Validate that the session belongs to the requesting user.
	sess, err := s.FindByRefreshHash(ctx, hash)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil // Already expired / purged.
		}
		return err
	}
	if sess.UserID != userID {
		return nil // Not this user's session; treat as no-op (idempotent).
	}
	pipe := s.rdb.TxPipeline()
	pipe.Del(ctx, sessionKey(hash))
	pipe.Del(ctx, pubKey(publicID))
	pipe.SRem(ctx, userKey(userID), hash)
	_, err = pipe.Exec(ctx)
	return err
}
