package handlerutil

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/types"
)

// TestDecodeCursorEmpty verifies the documented "empty string =
// first page" contract: zero values, nil error.
func TestDecodeCursorEmpty(t *testing.T) {
	t.Parallel()
	tt, pid, err := DecodeCursor("")
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if !tt.IsZero() {
		t.Errorf("expected zero time, got %v", tt)
	}
	var zero types.PublicID
	if pid != zero {
		t.Errorf("expected zero public id, got %s", pid)
	}
}

// TestEncodeDecodeRoundTrip verifies that an arbitrary pair survives a
// round-trip without losing precision at the second granularity.
func TestEncodeDecodeRoundTrip(t *testing.T) {
	t.Parallel()
	original := time.Date(2025, 6, 15, 14, 30, 45, 0, time.UTC)
	pid := types.New()

	encoded := EncodeCursor(original, pid)
	if encoded == "" {
		t.Fatal("encoded cursor unexpectedly empty")
	}

	gotTime, gotPID, err := DecodeCursor(encoded)
	if err != nil {
		t.Fatalf("decode failed: %v", err)
	}
	if !gotTime.Equal(original) {
		t.Errorf("time mismatch: got %v want %v", gotTime, original)
	}
	if gotPID != pid {
		t.Errorf("public id mismatch: got %s want %s", gotPID, pid)
	}
}

// TestEncodeDecodePreservesMilliseconds locks in the wire-format
// invariant that the cursor carries millisecond precision: the keyset
// queries compare against DATETIME(3) columns, so dropping the
// sub-second component (a previous regression) silently skips rows
// whose true timestamp falls between the truncated cursor second and
// the actual last-row time. See TestKeysetHandlerListTasksWorkspace.
func TestEncodeDecodePreservesMilliseconds(t *testing.T) {
	t.Parallel()
	// 789 ms after the second boundary — chosen so a seconds-only
	// encoder would round to *.000 and lose the .789.
	original := time.Date(2025, 6, 15, 14, 30, 45, 789_000_000, time.UTC)
	pid := types.New()

	gotTime, _, err := DecodeCursor(EncodeCursor(original, pid))
	if err != nil {
		t.Fatalf("decode failed: %v", err)
	}
	if !gotTime.Equal(original) {
		t.Fatalf("ms precision lost: got %v want %v (delta=%v)",
			gotTime, original, original.Sub(gotTime))
	}
	if gotTime.Nanosecond() != 789_000_000 {
		t.Errorf("expected 789ms preserved, got nanos=%d", gotTime.Nanosecond())
	}
}

// TestEncodeIsURLSafe confirms the cursor uses base64url (no '+', '/',
// or '=' padding) so it can be passed through a query string without
// percent-escaping.
func TestEncodeIsURLSafe(t *testing.T) {
	t.Parallel()
	encoded := EncodeCursor(time.Now().UTC(), types.New())
	for _, ch := range []string{"+", "/", "="} {
		if strings.Contains(encoded, ch) {
			t.Errorf("encoded cursor contains URL-unsafe %q: %s", ch, encoded)
		}
	}
}

// TestDecodeCursorInvalidBase64 rejects garbage that fails base64
// decoding before we even get to JSON.
func TestDecodeCursorInvalidBase64(t *testing.T) {
	t.Parallel()
	_, _, err := DecodeCursor("!!!not-base64!!!")
	if err == nil {
		t.Fatal("expected decode error for invalid base64")
	}
}

// TestDecodeCursorInvalidJSON rejects a base64 blob that decodes to
// non-JSON bytes.
func TestDecodeCursorInvalidJSON(t *testing.T) {
	t.Parallel()
	bad := base64.RawURLEncoding.EncodeToString([]byte("not json"))
	_, _, err := DecodeCursor(bad)
	if err == nil {
		t.Fatal("expected decode error for invalid json payload")
	}
}

// TestDecodeCursorInvalidPublicID rejects a payload whose `p` field is
// not a parseable UUID — the keyset query would otherwise see a zero
// public id which silently changes pagination semantics.
func TestDecodeCursorInvalidPublicID(t *testing.T) {
	t.Parallel()
	bad := base64.RawURLEncoding.EncodeToString([]byte(`{"t":1700000000,"p":"not-a-uuid"}`))
	_, _, err := DecodeCursor(bad)
	if err == nil {
		t.Fatal("expected decode error for invalid public id")
	}
}

// TestMillisJSONShape pins the wire format of the Millis named type:
// Go marshals named integer types as plain JSON numbers, so the
// cursor blob is byte-identical to the int64-backed predecessor and
// existing decoded fixtures remain valid. A future change that gives
// Millis a custom MarshalJSON (for example wrapping it in a string)
// would silently break every cursor in flight; this test fails first.
func TestMillisJSONShape(t *testing.T) {
	t.Parallel()
	buf, err := json.Marshal(cursorPayload{T: Millis(1_700_000_000_123), P: "00000000-0000-7000-8000-000000000000"})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got := string(buf)
	want := `{"t":1700000000123,"p":"00000000-0000-7000-8000-000000000000"}`
	if got != want {
		t.Fatalf("Millis wire shape changed: got %s want %s", got, want)
	}
}
