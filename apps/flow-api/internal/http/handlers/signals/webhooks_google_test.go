package signals

import (
	"strings"
	"testing"
)

// TestGoogleDeliveryKeyIsPerDelivery pins the property that makes Drive
// push usable: two notifications on the SAME channel must produce two
// different dedupe keys, while a redelivery of one notification must
// produce the same key. Keying on the channel id alone satisfies the
// second half and fails the first, which silently limits a channel to a
// single stored signal for its whole lifetime.
func TestGoogleDeliveryKeyIsPerDelivery(t *testing.T) {
	const channel = "0f7d2b4c-6a11-4f5e-9c2d-1a2b3c4d5e6f"

	first := googleDeliveryKey(channel, "1")
	second := googleDeliveryKey(channel, "2")
	retry := googleDeliveryKey(channel, "2")

	if !first.Valid || !second.Valid {
		t.Fatalf("delivery keys must be non-NULL: first=%+v second=%+v", first, second)
	}
	if first.String == second.String {
		t.Fatalf("distinct message numbers produced the same dedupe key %q; "+
			"every notification on the channel would collapse onto the first one", first.String)
	}
	if retry.String != second.String {
		t.Fatalf("redelivery key = %q; want %q so the retry dedupes", retry.String, second.String)
	}
	if !strings.Contains(second.String, channel) {
		t.Fatalf("dedupe key %q must stay scoped to the channel id", second.String)
	}
}

// TestGoogleDeliveryKeyWithoutMessageNumber asserts a delivery we cannot
// identify is stored with a NULL external_id rather than falling back to
// the channel id. NULL means "not deduped"; the channel id would mean
// "every later notification on this channel is dropped".
func TestGoogleDeliveryKeyWithoutMessageNumber(t *testing.T) {
	if k := googleDeliveryKey("channel-with-no-message-number", ""); k.Valid {
		t.Fatalf("missing message number produced external_id %q; want NULL", k.String)
	}
	if k := googleDeliveryKey("", "7"); k.Valid {
		t.Fatalf("missing channel id produced external_id %q; want NULL", k.String)
	}
}

// TestDedupeKeyRejectsOverlongValue guards the column width: external_id
// is VARCHAR(255), and a truncated key would map two distinct deliveries
// onto one row. Refusing to dedupe is the recoverable failure.
func TestDedupeKeyRejectsOverlongValue(t *testing.T) {
	if k := dedupeKey(strings.Repeat("x", externalIDMaxLen)); !k.Valid {
		t.Fatalf("a value of exactly the column width must be kept")
	}
	if k := dedupeKey(strings.Repeat("x", externalIDMaxLen+1)); k.Valid {
		t.Fatalf("an over-long value produced external_id of len %d; want NULL", len(k.String))
	}
}
