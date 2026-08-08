package authn

import (
	"context"
	"sync"
	"time"
)

// SingleUseStore records the identifiers of one-time tokens that have
// already been redeemed, so a token whose signature is still valid
// cannot be replayed within its lifetime.
//
// Stateless JWTs are otherwise reusable until they expire: everything
// the verifier checks travels inside the token itself. Recording the jti
// on first redemption is what turns them into one-time credentials. The
// OIDC state parameter and the TOTP step-up challenge in
// totp_challenge.go both go through this store; the store only needs an
// identifier and a remaining lifetime, so any further stateless
// one-time token fits it unchanged.
//
// The implementation shipped here is in-process. A deployment that runs
// several replicas needs a shared one; both obvious options fit this
// interface unchanged:
//
//   - a short-lived state table claimed with a guarded
//     `DELETE ... WHERE id = ? AND expires_at > CURRENT_TIMESTAMP`,
//     mirroring how oauth_states is consumed for personal integrations;
//   - Redis `SET key value NX EX ttl`, where the reply decides the winner.
type SingleUseStore interface {
	// Consume claims id for the caller. It returns true exactly once
	// per id; every later call returns false until ttl has elapsed and
	// the record is swept. ttl should be the remaining lifetime of the
	// token, so a record never outlives the credential it guards.
	//
	// Implementations must be safe for concurrent use and must elect a
	// single winner when two callers race on the same id.
	Consume(ctx context.Context, id string, ttl time.Duration) (bool, error)
}

// MemorySingleUseStore is the default [SingleUseStore]: an in-process
// map guarded by a mutex.
//
// It does not guarantee single use. It guarantees single use *within one
// process*. Running N replicas makes every token redeemable up to N
// times, because each replica keeps its own map and none of them knows
// what the others have already claimed. This is the same per-process
// state limitation the in-memory rate limiter in
// packages/go-shared/ratelimit has, where N replicas multiply the
// effective request ceiling by N, and it must be resolved before this
// service is scaled horizontally.
//
// For the OIDC state parameter the practical consequence is bounded: a
// state is still useless without the verifier cookie held by the browser
// that started the flow, and an attacker cannot write that cookie into
// someone else's browser. So horizontal scaling does not reopen the
// login-CSRF hole — it removes one layer of defence, letting a state
// that leaked into a history entry, a referer header, or a proxy log be
// redeemed more than once by whoever also holds the cookie.
type MemorySingleUseStore struct {
	mu       sync.Mutex
	expiries map[string]time.Time
	// now is swappable so tests can advance the clock without sleeping.
	now func() time.Time
}

// NewMemorySingleUseStore returns an empty in-process store.
func NewMemorySingleUseStore() *MemorySingleUseStore {
	return &MemorySingleUseStore{expiries: map[string]time.Time{}, now: time.Now}
}

// Consume implements [SingleUseStore]. Records past their ttl are swept
// on the way through, so the map stays bounded by the number of tokens
// issued within one ttl window without a background goroutine.
func (s *MemorySingleUseStore) Consume(_ context.Context, id string, ttl time.Duration) (bool, error) {
	if id == "" || ttl <= 0 {
		return false, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	now := s.now()
	for key, exp := range s.expiries {
		if !exp.After(now) {
			delete(s.expiries, key)
		}
	}
	if exp, seen := s.expiries[id]; seen && exp.After(now) {
		return false, nil
	}
	s.expiries[id] = now.Add(ttl)
	return true, nil
}
