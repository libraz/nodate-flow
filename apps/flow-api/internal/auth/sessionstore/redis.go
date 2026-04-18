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

// rotateScript is a Lua script that atomically reads an old session
// key, writes a new one, and deletes the old. This eliminates the
// TOCTOU race where concurrent refresh requests could both read the
// old session before either deletes it.
var rotateScript = redis.NewScript(`
local oldKey  = KEYS[1]
local newKey  = KEYS[2]
local userSet = KEYS[3]
local pubIdx  = KEYS[4]

local data = redis.call('HGETALL', oldKey)
if #data == 0 then return redis.error_reply('NOT_FOUND') end

-- Delete old key first to prevent double rotation.
redis.call('DEL', oldKey)

-- Build field map and write new key.
for i = 1, #data, 2 do
    redis.call('HSET', newKey, data[i], data[i+1])
end
redis.call('HSET', newKey, 'expiresAt', ARGV[1])
redis.call('HSET', newKey, 'lastUsedAt', ARGV[2])
redis.call('EXPIRE', newKey, tonumber(ARGV[3]))

-- Update user set and pub reverse index.
redis.call('SREM', userSet, ARGV[4])
redis.call('SADD', userSet, ARGV[5])
redis.call('EXPIRE', userSet, tonumber(ARGV[3]))
redis.call('SET', pubIdx, ARGV[5], 'EX', tonumber(ARGV[3]))
return 'OK'
`)

// RotateRefreshHash implements [Store]. Uses a Lua script for atomic
// read-then-rekey to prevent TOCTOU races under concurrent refresh
// requests.
func (s *RedisStore) RotateRefreshHash(ctx context.Context, oldHash, newHash string, expiresAt time.Time) error {
	ttl := time.Until(expiresAt)
	if ttl <= 0 {
		return errors.New("sessionstore/redis: rotated expiresAt in the past")
	}

	// We need the publicId and userId from the old session to build
	// the correct key names. Read them first — the Lua script will
	// atomically verify the key still exists.
	old, err := s.FindByRefreshHash(ctx, oldHash)
	if err != nil {
		return err
	}

	ttlSec := int(ttl.Seconds()) + 1 // +1 to avoid off-by-one
	now := time.Now().UTC().Format(time.RFC3339Nano)

	result, err := rotateScript.Run(ctx, s.rdb,
		[]string{
			sessionKey(oldHash),
			sessionKey(newHash),
			userKey(old.UserID),
			pubKey(old.PublicID),
		},
		expiresAt.Format(time.RFC3339Nano), // ARGV[1]
		now,                                 // ARGV[2]
		ttlSec,                              // ARGV[3]
		oldHash,                             // ARGV[4]
		newHash,                             // ARGV[5]
	).Text()
	if err != nil {
		if err.Error() == "NOT_FOUND" {
			return ErrNotFound
		}
		return fmt.Errorf("sessionstore/redis: rotate: %w", err)
	}
	_ = result
	return nil
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
