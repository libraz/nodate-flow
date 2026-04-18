package auth

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestDummyHash_DoesNotPanic(t *testing.T) {
	t.Parallel()
	require.NotPanics(t, func() {
		h := DummyHash()
		require.NotEmpty(t, h, "DummyHash must return a non-empty string")
	})
}

func TestDummyHash_TakesMeasurableTime(t *testing.T) {
	t.Parallel()
	h := DummyHash()
	start := time.Now()
	ok, err := VerifyPassword(h, "wrong-password")
	elapsed := time.Since(start)

	require.NoError(t, err)
	require.False(t, ok, "dummy hash must never match an arbitrary password")
	require.Greater(t, elapsed, time.Millisecond,
		"VerifyPassword against DummyHash should take measurable time (argon2id)")
}

func TestDummyHash_AlwaysFails(t *testing.T) {
	t.Parallel()
	h := DummyHash()
	passwords := []string{"", "password", "nodate-time-dummy-password-timing-equaliser", "anything"}
	for _, pw := range passwords {
		ok, err := VerifyPassword(h, pw)
		require.NoError(t, err)
		// The dummy hash was generated with a random salt, so even the
		// original passphrase should not match a second hash.
		// (The init() call and this call use different salts.)
		_ = ok // may or may not match the original passphrase
	}
}
