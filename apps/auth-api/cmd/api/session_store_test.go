package main

import (
	"io"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/libraz/nodate-flow/packages/go-shared/sessionstore"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// TestBuildSessionStore_IsMySQL pins the driver to the table the
// access-token validator reads; a session created anywhere else would
// make every token it backs fail on both services.
func TestBuildSessionStore_IsMySQL(t *testing.T) {
	t.Parallel()
	store := buildSessionStore(nil, nil, discardLogger())
	assert.IsType(t, &sessionstore.MySQLStore{}, store)
}
