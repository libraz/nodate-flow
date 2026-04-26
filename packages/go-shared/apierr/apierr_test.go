package apierr

import (
	"database/sql"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNotFoundOr(t *testing.T) {
	t.Parallel()

	notFound := New(&Spec{Code: "WS.X.NOT_FOUND", Status: 404, Message: "not found"})
	internal := New(&Spec{Code: "INTERNAL.UNEXPECTED", Status: 500, Message: "unexpected"})

	t.Run("sql.ErrNoRows returns notFound", func(t *testing.T) {
		t.Parallel()
		got := NotFoundOr(sql.ErrNoRows, notFound, internal)
		require.NotNil(t, got)
		assert.Same(t, notFound, got)
	})

	t.Run("wrapped sql.ErrNoRows returns notFound", func(t *testing.T) {
		t.Parallel()
		wrapped := fmt.Errorf("query foo: %w", sql.ErrNoRows)
		got := NotFoundOr(wrapped, notFound, internal)
		assert.Same(t, notFound, got)
	})

	t.Run("other error returns internal", func(t *testing.T) {
		t.Parallel()
		got := NotFoundOr(errors.New("boom"), notFound, internal)
		assert.Same(t, internal, got)
	})

	t.Run("nil err returns internal", func(t *testing.T) {
		t.Parallel()
		got := NotFoundOr(nil, notFound, internal)
		assert.Same(t, internal, got)
	})
}

func TestSpecForErrNoRows(t *testing.T) {
	t.Parallel()

	notFound := &Spec{Code: "WS.X.NOT_FOUND", Status: 404, Message: "not found"}
	internal := &Spec{Code: "INTERNAL.UNEXPECTED", Status: 500, Message: "unexpected"}

	t.Run("sql.ErrNoRows returns notFound", func(t *testing.T) {
		t.Parallel()
		got := SpecForErrNoRows(sql.ErrNoRows, notFound, internal)
		assert.Same(t, notFound, got)
	})

	t.Run("wrapped sql.ErrNoRows returns notFound", func(t *testing.T) {
		t.Parallel()
		wrapped := fmt.Errorf("scan: %w", sql.ErrNoRows)
		got := SpecForErrNoRows(wrapped, notFound, internal)
		assert.Same(t, notFound, got)
	})

	t.Run("other error returns internal", func(t *testing.T) {
		t.Parallel()
		got := SpecForErrNoRows(errors.New("boom"), notFound, internal)
		assert.Same(t, internal, got)
	})

	t.Run("nil err returns internal", func(t *testing.T) {
		t.Parallel()
		got := SpecForErrNoRows(nil, notFound, internal)
		assert.Same(t, internal, got)
	})
}
