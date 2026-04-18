package handlerutil

import (
	"database/sql"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"

	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/types"
)

func TestIsDuplicateEntryTrue(t *testing.T) {
	t.Parallel()
	err := &mysql.MySQLError{Number: 1062, Message: "Duplicate entry '1' for key 'PRIMARY'"}
	if !IsDuplicateEntry(err) {
		t.Error("expected true for error 1062")
	}
}

func TestIsDuplicateEntryFalse(t *testing.T) {
	t.Parallel()
	err := &mysql.MySQLError{Number: 1045, Message: "Access denied"}
	if IsDuplicateEntry(err) {
		t.Error("expected false for non-1062 error")
	}
}

func TestIsDuplicateEntryNil(t *testing.T) {
	t.Parallel()
	if IsDuplicateEntry(nil) {
		t.Error("nil error should return false")
	}
}

func TestPublicIDOrEmpty(t *testing.T) {
	t.Parallel()
	var zero types.PublicID
	if got := PublicIDOrEmpty(zero); got != "" {
		t.Errorf("zero id: got %q want empty", got)
	}
	id := types.New()
	if got := PublicIDOrEmpty(id); got == "" {
		t.Error("non-zero id should return non-empty string")
	}
}

func TestNullStr(t *testing.T) {
	t.Parallel()
	if got := NullStr(sql.NullString{Valid: false}); got != "" {
		t.Errorf("NULL: got %q want empty", got)
	}
	if got := NullStr(sql.NullString{String: "hello", Valid: true}); got != "hello" {
		t.Errorf("valid: got %q want hello", got)
	}
}

func TestNullStrPtr(t *testing.T) {
	t.Parallel()
	if got := NullStrPtr(sql.NullString{Valid: false}); got != nil {
		t.Errorf("NULL: got %v want nil", got)
	}
	got := NullStrPtr(sql.NullString{String: "x", Valid: true})
	if got == nil || *got != "x" {
		t.Errorf("valid: got %v want pointer to 'x'", got)
	}
}

func TestNullTime(t *testing.T) {
	t.Parallel()
	if got := NullTime(sql.NullTime{Valid: false}); got != nil {
		t.Error("NULL: expected nil")
	}
	now := time.Now()
	got := NullTime(sql.NullTime{Time: now, Valid: true})
	if got == nil || !got.Equal(now) {
		t.Errorf("valid: got %v want %v", got, now)
	}
}

func TestNullTimeUnix(t *testing.T) {
	t.Parallel()
	if got := NullTimeUnix(sql.NullTime{Valid: false}); got != nil {
		t.Error("NULL: expected nil")
	}
	now := time.Now()
	got := NullTimeUnix(sql.NullTime{Time: now, Valid: true})
	if got == nil || *got != now.Unix() {
		t.Errorf("valid: got %v want %d", got, now.Unix())
	}
}

func TestNullTimeUnixVal(t *testing.T) {
	t.Parallel()
	if got := NullTimeUnixVal(sql.NullTime{Valid: false}); got != 0 {
		t.Errorf("NULL: got %d want 0", got)
	}
	now := time.Now()
	if got := NullTimeUnixVal(sql.NullTime{Time: now, Valid: true}); got != now.Unix() {
		t.Errorf("valid: got %d want %d", got, now.Unix())
	}
}

func TestTotalAsInt64(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		in   interface{}
		want int64
	}{
		{"int64", int64(42), 42},
		{"int", int(99), 99},
		{"uint64", uint64(123), 123},
		{"bytes", []byte("456"), 456},
		{"bytes_non_digit", []byte("12abc"), 12},
		{"nil", nil, 0},
		{"string", "nope", 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := TotalAsInt64(tc.in); got != tc.want {
				t.Errorf("got %d want %d", got, tc.want)
			}
		})
	}
}
