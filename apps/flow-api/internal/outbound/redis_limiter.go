// redis_limiter.go — Redis-backed token bucket so a fleet of api
// replicas can share a single egress budget per destination.
//
// Implementation uses a Lua script for atomic token accounting; the
// script is EVALSHA'd by the client after the first call. The state
// key format is:
//
//	nf:ratelimit:<destination>   HASH { tokens, last }
//
// TTL is set on first write to capacity/rate seconds so an idle
// destination doesn't leak keys forever.
package outbound

import (
	"context"
	"errors"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/redis/go-redis/v9"
)

// RedisLimiter is a distributed token bucket backed by Redis. It
// satisfies [RateLimiter] so it plugs into the same Registry as
// [Limiter]. All counters are tracked in-memory per replica for
// Stats(); the authoritative rate accounting lives in Redis.
type RedisLimiter struct {
	rdb      *redis.Client
	key      string
	capacity float64
	refill   float64
	pollWait time.Duration
	allowed  atomic.Uint64
	waited   atomic.Uint64
	denied   atomic.Uint64
}

// NewRedisLimiter constructs a RedisLimiter. destination is used as
// part of the Redis key so one client can run multiple limiters.
func NewRedisLimiter(rdb *redis.Client, destination string, ratePerSec float64, burst int) *RedisLimiter {
	if ratePerSec <= 0 {
		ratePerSec = 1
	}
	if burst <= 0 {
		burst = 1
	}
	return &RedisLimiter{
		rdb:      rdb,
		key:      "nf:ratelimit:" + destination,
		capacity: float64(burst),
		refill:   ratePerSec,
		pollWait: 50 * time.Millisecond,
	}
}

// tokenScript is an atomic "refill + consume 1 token" Lua. Returns
// 1 when the token was granted, 0 when the bucket was empty.
const tokenScript = `
local cap = tonumber(ARGV[1])
local refill = tonumber(ARGV[2])
local now = tonumber(ARGV[3])
local state = redis.call('HMGET', KEYS[1], 'tokens', 'last')
local tokens = tonumber(state[1]) or cap
local last = tonumber(state[2]) or now
local elapsed = math.max(0, now - last)
tokens = math.min(cap, tokens + elapsed * refill)
local granted = 0
if tokens >= 1 then
  tokens = tokens - 1
  granted = 1
end
redis.call('HMSET', KEYS[1], 'tokens', tokens, 'last', now)
redis.call('EXPIRE', KEYS[1], math.ceil(cap / refill) + 60)
return granted
`

// Allow implements [RateLimiter].
func (l *RedisLimiter) Allow() bool {
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	return l.tryConsume(ctx)
}

// Wait implements [RateLimiter]. Polls the bucket every pollWait
// until a token is granted or ctx is cancelled.
func (l *RedisLimiter) Wait(ctx context.Context) error {
	waited := false
	for {
		if l.tryConsume(ctx) {
			if waited {
				l.waited.Add(1)
			}
			return nil
		}
		waited = true
		t := time.NewTimer(l.pollWait)
		select {
		case <-ctx.Done():
			t.Stop()
			l.denied.Add(1)
			return errors.Join(ErrLimitExceeded, ctx.Err())
		case <-t.C:
		}
	}
}

// Stats implements [RateLimiter].
func (l *RedisLimiter) Stats() LimiterStats {
	return LimiterStats{
		Allowed: l.allowed.Load(),
		Waited:  l.waited.Load(),
		Denied:  l.denied.Load(),
	}
}

func (l *RedisLimiter) tryConsume(ctx context.Context) bool {
	nowSec := float64(time.Now().UnixNano()) / float64(time.Second)
	res, err := l.rdb.Eval(ctx, tokenScript, []string{l.key},
		strconv.FormatFloat(l.capacity, 'f', -1, 64),
		strconv.FormatFloat(l.refill, 'f', -1, 64),
		strconv.FormatFloat(nowSec, 'f', -1, 64),
	).Int()
	if err != nil {
		// Fail-open on Redis errors so a broken cache does not take
		// down the api surface. Log at the call site if desired.
		return true
	}
	if res == 1 {
		l.allowed.Add(1)
		return true
	}
	return false
}

// compile-time check
var _ RateLimiter = (*RedisLimiter)(nil)
