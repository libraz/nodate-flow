package dbtype

import (
	"database/sql"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPtrFromNullString(t *testing.T) {
	t.Parallel()

	t.Run("invalid returns nil", func(t *testing.T) {
		t.Parallel()
		assert.Nil(t, PtrFromNullString(sql.NullString{Valid: false, String: "ignored"}))
	})

	t.Run("valid returns pointer with copied value", func(t *testing.T) {
		t.Parallel()
		got := PtrFromNullString(sql.NullString{Valid: true, String: "hello"})
		require.NotNil(t, got)
		assert.Equal(t, "hello", *got)
	})

	t.Run("valid empty string is preserved", func(t *testing.T) {
		t.Parallel()
		got := PtrFromNullString(sql.NullString{Valid: true, String: ""})
		require.NotNil(t, got)
		assert.Equal(t, "", *got)
	})
}

func TestStringFromNullString(t *testing.T) {
	t.Parallel()

	t.Run("invalid returns empty", func(t *testing.T) {
		t.Parallel()
		assert.Equal(t, "", StringFromNullString(sql.NullString{Valid: false, String: "ignored"}))
	})

	t.Run("valid returns string", func(t *testing.T) {
		t.Parallel()
		assert.Equal(t, "hello", StringFromNullString(sql.NullString{Valid: true, String: "hello"}))
	})
}

func TestPtrFromNullInt32(t *testing.T) {
	t.Parallel()

	t.Run("invalid returns nil", func(t *testing.T) {
		t.Parallel()
		assert.Nil(t, PtrFromNullInt32(sql.NullInt32{Valid: false, Int32: 42}))
	})

	t.Run("valid returns pointer", func(t *testing.T) {
		t.Parallel()
		got := PtrFromNullInt32(sql.NullInt32{Valid: true, Int32: 42})
		require.NotNil(t, got)
		assert.Equal(t, int32(42), *got)
	})

	t.Run("valid zero is preserved", func(t *testing.T) {
		t.Parallel()
		got := PtrFromNullInt32(sql.NullInt32{Valid: true, Int32: 0})
		require.NotNil(t, got)
		assert.Equal(t, int32(0), *got)
	})
}

func TestPtrFromNullInt64(t *testing.T) {
	t.Parallel()

	t.Run("invalid returns nil", func(t *testing.T) {
		t.Parallel()
		assert.Nil(t, PtrFromNullInt64(sql.NullInt64{Valid: false, Int64: 99}))
	})

	t.Run("valid returns pointer", func(t *testing.T) {
		t.Parallel()
		got := PtrFromNullInt64(sql.NullInt64{Valid: true, Int64: 1 << 40})
		require.NotNil(t, got)
		assert.Equal(t, int64(1<<40), *got)
	})
}

func TestPtrFromNullBool(t *testing.T) {
	t.Parallel()

	t.Run("invalid returns nil", func(t *testing.T) {
		t.Parallel()
		assert.Nil(t, PtrFromNullBool(sql.NullBool{Valid: false, Bool: true}))
	})

	t.Run("valid true returns pointer to true", func(t *testing.T) {
		t.Parallel()
		got := PtrFromNullBool(sql.NullBool{Valid: true, Bool: true})
		require.NotNil(t, got)
		assert.True(t, *got)
	})

	t.Run("valid false returns pointer to false", func(t *testing.T) {
		t.Parallel()
		got := PtrFromNullBool(sql.NullBool{Valid: true, Bool: false})
		require.NotNil(t, got)
		assert.False(t, *got)
	})
}

func TestPtrFromNullFloat64(t *testing.T) {
	t.Parallel()

	t.Run("invalid returns nil", func(t *testing.T) {
		t.Parallel()
		assert.Nil(t, PtrFromNullFloat64(sql.NullFloat64{Valid: false, Float64: 3.14}))
	})

	t.Run("valid returns pointer", func(t *testing.T) {
		t.Parallel()
		got := PtrFromNullFloat64(sql.NullFloat64{Valid: true, Float64: 3.14})
		require.NotNil(t, got)
		assert.InDelta(t, 3.14, *got, 1e-9)
	})
}

func TestUnixSecondsFromNullTime(t *testing.T) {
	t.Parallel()

	t.Run("invalid returns nil", func(t *testing.T) {
		t.Parallel()
		assert.Nil(t, UnixSecondsFromNullTime(sql.NullTime{Valid: false}))
	})

	t.Run("valid returns unix seconds", func(t *testing.T) {
		t.Parallel()
		// 2024-01-02 03:04:05 UTC → 1704164645.
		ts := time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC)
		got := UnixSecondsFromNullTime(sql.NullTime{Valid: true, Time: ts})
		require.NotNil(t, got)
		assert.Equal(t, ts.Unix(), *got)
		assert.Equal(t, int64(1704164645), *got)
	})

	t.Run("unix is timezone-independent", func(t *testing.T) {
		t.Parallel()
		// Same instant in two different zones should produce identical
		// Unix seconds — the helper must not depend on Location.
		ts := time.Date(2024, 6, 15, 12, 0, 0, 0, time.UTC)
		jst := time.FixedZone("JST", 9*3600)
		tsJST := ts.In(jst)

		gotUTC := UnixSecondsFromNullTime(sql.NullTime{Valid: true, Time: ts})
		gotJST := UnixSecondsFromNullTime(sql.NullTime{Valid: true, Time: tsJST})
		require.NotNil(t, gotUTC)
		require.NotNil(t, gotJST)
		assert.Equal(t, *gotUTC, *gotJST)
	})
}

func TestDateStringFromNullTime(t *testing.T) {
	t.Parallel()

	t.Run("invalid returns nil", func(t *testing.T) {
		t.Parallel()
		assert.Nil(t, DateStringFromNullTime(sql.NullTime{Valid: false}))
	})

	t.Run("valid UTC returns YYYY-MM-DD", func(t *testing.T) {
		t.Parallel()
		ts := time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC)
		got := DateStringFromNullTime(sql.NullTime{Valid: true, Time: ts})
		require.NotNil(t, got)
		assert.Equal(t, "2024-01-02", *got)
	})

	t.Run("non-UTC zone is normalised to UTC date", func(t *testing.T) {
		t.Parallel()
		// 2024-01-02 03:00:00 +09:00 == 2024-01-01 18:00:00 UTC, so the
		// canonical date string is 2024-01-01.
		jst := time.FixedZone("JST", 9*3600)
		ts := time.Date(2024, 1, 2, 3, 0, 0, 0, jst)
		got := DateStringFromNullTime(sql.NullTime{Valid: true, Time: ts})
		require.NotNil(t, got)
		assert.Equal(t, "2024-01-01", *got)
	})

	t.Run("zero day is formatted, not omitted", func(t *testing.T) {
		t.Parallel()
		// MySQL DATE 0001-01-01 (Go zero year) round-trip safety: the
		// helper does not special-case it; it just formats. (Callers that
		// want NULL for zero must pass Valid=false.)
		ts := time.Date(1, 1, 1, 0, 0, 0, 0, time.UTC)
		got := DateStringFromNullTime(sql.NullTime{Valid: true, Time: ts})
		require.NotNil(t, got)
		assert.Equal(t, "0001-01-01", *got)
	})
}

func TestDateStringValueFromNullTime(t *testing.T) {
	t.Parallel()

	t.Run("invalid returns empty", func(t *testing.T) {
		t.Parallel()
		assert.Equal(t, "", DateStringValueFromNullTime(sql.NullTime{Valid: false}))
	})

	t.Run("valid returns UTC date", func(t *testing.T) {
		t.Parallel()
		jst := time.FixedZone("JST", 9*3600)
		ts := time.Date(2024, 1, 2, 3, 0, 0, 0, jst)
		assert.Equal(t, "2024-01-01", DateStringValueFromNullTime(sql.NullTime{Valid: true, Time: ts}))
	})
}
