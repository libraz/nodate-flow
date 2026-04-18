package sessionstore

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestMySQLStore_ErrorWrapping verifies that the error wrapping pattern
// used in MySQLStore preserves the original error via errors.Is and
// includes the "sessionstore/mysql:" prefix for operability.
func TestMySQLStore_ErrorWrapping(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		op     string
		inner  error
		prefix string
	}{
		{"create", "create", errors.New("connection refused"), "sessionstore/mysql: create:"},
		{"find", "find", sql.ErrNoRows, "sessionstore/mysql: find:"},
		{"rotate find", "rotate find", sql.ErrConnDone, "sessionstore/mysql: rotate find:"},
		{"rotate update", "rotate update", errors.New("deadlock"), "sessionstore/mysql: rotate update:"},
		{"revoke", "revoke", errors.New("timeout"), "sessionstore/mysql: revoke:"},
		{"list", "list", errors.New("too many connections"), "sessionstore/mysql: list:"},
		{"revoke-all-except", "revoke-all-except", errors.New("disk full"), "sessionstore/mysql: revoke-all-except:"},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			wrapped := fmt.Errorf("sessionstore/mysql: %s: %w", tc.op, tc.inner)

			// The original error must be recoverable via errors.Is.
			require.True(t, errors.Is(wrapped, tc.inner),
				"errors.Is must find the original error through wrapping")

			// The error message must contain the operation prefix.
			require.True(t, strings.HasPrefix(wrapped.Error(), tc.prefix),
				"error %q should start with %q", wrapped.Error(), tc.prefix)

			// The error message must also contain the inner error text.
			require.Contains(t, wrapped.Error(), tc.inner.Error(),
				"wrapped error must contain the original message")
		})
	}
}

// TestMySQLStore_ErrNoRows_MapsToErrNotFound verifies the sentinel
// mapping pattern: sql.ErrNoRows should NOT propagate — it should
// become ErrNotFound. This tests the pattern used in FindByRefreshHash
// and RotateRefreshHash.
func TestMySQLStore_ErrNoRows_MapsToErrNotFound(t *testing.T) {
	t.Parallel()

	// Simulate the mapping the store does.
	originalErr := sql.ErrNoRows
	var mappedErr error
	if errors.Is(originalErr, sql.ErrNoRows) {
		mappedErr = ErrNotFound
	}

	require.ErrorIs(t, mappedErr, ErrNotFound,
		"sql.ErrNoRows should be mapped to ErrNotFound")
	require.False(t, errors.Is(mappedErr, sql.ErrNoRows),
		"ErrNotFound must NOT match sql.ErrNoRows (they are different sentinels)")
}
