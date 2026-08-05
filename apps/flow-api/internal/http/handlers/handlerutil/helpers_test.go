package handlerutil

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/google/uuid"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
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

func TestNullTimeDateStr(t *testing.T) {
	t.Parallel()
	if got := NullTimeDateStr(sql.NullTime{Valid: false}); got != "" {
		t.Errorf("NULL: got %q want empty", got)
	}
	when := time.Date(2026, 4, 27, 12, 0, 0, 0, time.UTC)
	if got := NullTimeDateStr(sql.NullTime{Time: when, Valid: true}); got != "2026-04-27" {
		t.Errorf("valid: got %q want 2026-04-27", got)
	}
}

// TestNullTimeDate covers the NULL/zero, exact-UTC-midnight, and
// JST-near-day-boundary cases. The JST case (23:30 local on 2026-04-28
// = 14:30 UTC same day) is the regression guard for the bug where
// NullTimeDate skipped .UTC() while NullTimeDateStr called it: a
// driver returning JST-localised time would render different calendar
// days depending on which helper a mapper happened to call.
func TestNullTimeDate(t *testing.T) {
	t.Parallel()

	t.Run("NULL", func(t *testing.T) {
		t.Parallel()
		if got := NullTimeDate(sql.NullTime{Valid: false}); got != nil {
			t.Errorf("NULL: got %v want nil", got)
		}
	})

	t.Run("UTC midnight", func(t *testing.T) {
		t.Parallel()
		when := time.Date(2026, 4, 28, 0, 0, 0, 0, time.UTC)
		got := NullTimeDate(sql.NullTime{Time: when, Valid: true})
		if got == nil || *got != "2026-04-28" {
			t.Errorf("UTC midnight: got %v want pointer to 2026-04-28", got)
		}
	})

	t.Run("JST near day boundary matches NullTimeDateStr", func(t *testing.T) {
		t.Parallel()
		// 23:30 JST on 2026-04-28 = 14:30 UTC on 2026-04-28; both helpers
		// must report 2026-04-28. A driver that returns the value with
		// JST attached would otherwise tempt a non-UTC formatter into
		// returning 2026-04-29 here.
		jst := time.FixedZone("JST", 9*60*60)
		when := time.Date(2026, 4, 28, 23, 30, 0, 0, jst)
		gotPtr := NullTimeDate(sql.NullTime{Time: when, Valid: true})
		gotStr := NullTimeDateStr(sql.NullTime{Time: when, Valid: true})
		if gotPtr == nil || *gotPtr != "2026-04-28" {
			t.Errorf("NullTimeDate JST: got %v want pointer to 2026-04-28", gotPtr)
		}
		if gotStr != "2026-04-28" {
			t.Errorf("NullTimeDateStr JST: got %q want 2026-04-28", gotStr)
		}
		if gotPtr == nil || *gotPtr != gotStr {
			t.Errorf("helpers must agree: NullTimeDate=%v NullTimeDateStr=%q", gotPtr, gotStr)
		}
	})
}

func TestBytesToUUIDString(t *testing.T) {
	t.Parallel()
	if got := BytesToUUIDString(nil); got != "" {
		t.Errorf("nil: got %q want empty", got)
	}
	if got := BytesToUUIDString([]byte{0, 1, 2}); got != "" {
		t.Errorf("short slice: got %q want empty", got)
	}
	u := uuid.New()
	want := u.String()
	if got := BytesToUUIDString(u[:]); got != want {
		t.Errorf("got %q want %q", got, want)
	}
}

func TestNullBytesToUUIDString(t *testing.T) {
	t.Parallel()
	if got := NullBytesToUUIDString(sql.NullString{Valid: false}); got != "" {
		t.Errorf("NULL: got %q want empty", got)
	}
	if got := NullBytesToUUIDString(sql.NullString{String: "abc", Valid: true}); got != "" {
		t.Errorf("short string: got %q want empty", got)
	}
	u := uuid.New()
	got := NullBytesToUUIDString(sql.NullString{String: string(u[:]), Valid: true})
	if got != u.String() {
		t.Errorf("got %q want %q", got, u.String())
	}
}

func TestNullBytesToUUIDPtr(t *testing.T) {
	t.Parallel()
	if got := NullBytesToUUIDPtr(sql.NullString{Valid: false}); got != nil {
		t.Errorf("NULL: got %v want nil", got)
	}
	if got := NullBytesToUUIDPtr(sql.NullString{String: "x", Valid: true}); got != nil {
		t.Errorf("short string: got %v want nil", got)
	}
	u := uuid.New()
	got := NullBytesToUUIDPtr(sql.NullString{String: string(u[:]), Valid: true})
	if got == nil || *got != u.String() {
		t.Errorf("got %v want pointer to %q", got, u.String())
	}
}

func TestRawBytesToUUIDPtr(t *testing.T) {
	t.Parallel()
	if got := RawBytesToUUIDPtr(nil); got != nil {
		t.Errorf("nil: got %v want nil", got)
	}
	if got := RawBytesToUUIDPtr("not bytes"); got != nil {
		t.Errorf("string input: got %v want nil", got)
	}
	if got := RawBytesToUUIDPtr([]byte{1, 2, 3}); got != nil {
		t.Errorf("short slice: got %v want nil", got)
	}
	u := uuid.New()
	got := RawBytesToUUIDPtr(u[:])
	if got == nil || *got != u.String() {
		t.Errorf("got %v want pointer to %q", got, u.String())
	}
}

// A CSV column a person opens has to read as a time. These used to
// render the raw integer, so a spreadsheet showed ten digits under
// "Created At" beside a readable "Due Date" in the same row.
func TestFormatUnixISO(t *testing.T) {
	t.Parallel()
	if got := FormatUnixISO(0); got != "1970-01-01T00:00:00Z" {
		t.Errorf("zero: got %q", got)
	}
	if got := FormatUnixISO(1700000000); got != "2023-11-14T22:13:20Z" {
		t.Errorf("got %q", got)
	}
}

func TestFormatOptionalUnixISO(t *testing.T) {
	t.Parallel()
	if got := FormatOptionalUnixISO(nil); got != "" {
		t.Errorf("nil: got %q want empty", got)
	}
	v := int64(1700000000)
	if got := FormatOptionalUnixISO(&v); got != "2023-11-14T22:13:20Z" {
		t.Errorf("got %q", got)
	}
}

// The zone is stated rather than implied. A timestamp written without
// one is read in whatever zone the reader's spreadsheet assumes, which
// is a silent several-hour error in a file people reconcile against.
func TestFormatUnixISOStatesItsZone(t *testing.T) {
	t.Parallel()
	for _, ts := range []int64{0, 1, 1700000000, 2147483647} {
		if got := FormatUnixISO(ts); got[len(got)-1] != 'Z' {
			t.Errorf("FormatUnixISO(%d) = %q, want a trailing Z", ts, got)
		}
	}
}

func TestDerefStr(t *testing.T) {
	t.Parallel()
	if got := DerefStr(nil); got != "" {
		t.Errorf("nil: got %q want empty", got)
	}
	v := "hi"
	if got := DerefStr(&v); got != "hi" {
		t.Errorf("got %q want hi", got)
	}
}

// TestWriteSpecError verifies the helper emits a problem+json envelope
// matching the Huma route shape (type / title / status / detail) for raw
// chi handlers that cannot route through the Huma error pipeline.
func TestWriteSpecError(t *testing.T) {
	t.Parallel()
	rr := httptest.NewRecorder()
	spec := &apierrors.Spec{Code: "WS.WORKSPACE.NOT_FOUND", Status: 404, Message: "Workspace not found"}
	WriteSpecError(rr, spec)

	if rr.Code != http.StatusNotFound {
		t.Errorf("status: got %d want 404", rr.Code)
	}
	if ct := rr.Header().Get("Content-Type"); ct != "application/problem+json; charset=utf-8" {
		t.Errorf("content-type: got %q", ct)
	}
	var got struct {
		Type   string `json:"type"`
		Title  string `json:"title"`
		Status int    `json:"status"`
		Detail string `json:"detail"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if got.Type != spec.Code {
		t.Errorf("type: got %q want %q", got.Type, spec.Code)
	}
	if got.Status != spec.Status {
		t.Errorf("status field: got %d want %d", got.Status, spec.Status)
	}
	if got.Detail != spec.Message {
		t.Errorf("detail: got %q want %q", got.Detail, spec.Message)
	}
	if got.Title != http.StatusText(spec.Status) {
		t.Errorf("title: got %q want %q", got.Title, http.StatusText(spec.Status))
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
