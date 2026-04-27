package auth

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nodate-flow/nodate-flow/apps/auth-api/internal/audit"
)

// captureSink is an audit.Sink that stores every Record call so tests
// can assert on action, actor, and metadata.
type captureSink struct {
	mu      sync.Mutex
	entries []audit.Entry
}

func (c *captureSink) Record(_ context.Context, e audit.Entry) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries = append(c.entries, e)
}

func (c *captureSink) snapshot() []audit.Entry {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]audit.Entry, len(c.entries))
	copy(out, c.entries)
	return out
}

// TestRecordCipherDecryptFailure_EmitsHighSeverityAudit asserts the
// helper produces an audit entry with the canonical action name,
// the actor user, and severity=high metadata so operators can filter
// for these incidents in the audit feed.
func TestRecordCipherDecryptFailure_EmitsHighSeverityAudit(t *testing.T) {
	t.Parallel()
	sink := &captureSink{}
	deps := Deps{Audit: sink}
	recordCipherDecryptFailure(context.Background(), deps, 42, "login_totp", errors.New("aes: tag mismatch"))

	entries := sink.snapshot()
	require.Len(t, entries, 1, "must emit exactly one audit entry per failure")
	got := entries[0]
	assert.Equal(t, "auth.cipher_decrypt_failed", got.Action)
	assert.Equal(t, uint32(42), got.ActorID)
	assert.Equal(t, "user", got.ResourceType)
	assert.Equal(t, "login_totp", got.Metadata["context"],
		"context label distinguishes which decrypt site fired")
	assert.Equal(t, "high", got.Metadata["severity"],
		"severity flag is required for incident routing")
}

// TestRecordCipherDecryptFailure_NilSinkIsSafe asserts the helper
// degrades gracefully when no audit sink is configured: the log line
// still fires (verified in production via slog), but the call must
// not panic on a nil interface dereference.
func TestRecordCipherDecryptFailure_NilSinkIsSafe(t *testing.T) {
	t.Parallel()
	deps := Deps{Audit: nil}
	require.NotPanics(t, func() {
		recordCipherDecryptFailure(context.Background(), deps, 1, "login_totp", errors.New("x"))
	})
}
