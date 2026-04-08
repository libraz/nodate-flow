package dto

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestUnixSecondsRoundTrip(t *testing.T) {
	original := time.Date(2026, 4, 8, 12, 34, 56, 0, time.UTC)
	var u UnixSeconds
	u.FromTime(original)

	data, err := json.Marshal(u)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if string(data) != "1775651696" {
		t.Fatalf("unexpected json: %s", data)
	}

	var decoded UnixSeconds
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if decoded != u {
		t.Fatalf("round-trip mismatch: got %d want %d", decoded, u)
	}
	if got := decoded.ToTime(); !got.Equal(original) {
		t.Fatalf("ToTime mismatch: got %s want %s", got, original)
	}
}

func TestDateOnlyParseFormat(t *testing.T) {
	d, err := ParseDateOnly("2026-04-08")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if d.String() != "2026-04-08" {
		t.Fatalf("unexpected string: %s", d)
	}

	tt, err := d.ToTime()
	if err != nil {
		t.Fatalf("to time: %v", err)
	}
	if tt.Year() != 2026 || tt.Month() != 4 || tt.Day() != 8 {
		t.Fatalf("unexpected time: %s", tt)
	}

	data, err := json.Marshal(d)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if string(data) != `"2026-04-08"` {
		t.Fatalf("unexpected json: %s", data)
	}

	var decoded DateOnly
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if decoded != d {
		t.Fatalf("round-trip mismatch")
	}

	from := DateOnlyFromTime(time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC))
	if from != "2026-01-02" {
		t.Fatalf("DateOnlyFromTime: %s", from)
	}
}

func TestDateOnlyRejectsInvalid(t *testing.T) {
	if _, err := ParseDateOnly("2026-13-40"); err == nil {
		t.Fatal("expected error for invalid date")
	}
	var d DateOnly
	if err := json.Unmarshal([]byte(`"2026-13-40"`), &d); err == nil {
		t.Fatal("expected unmarshal error for invalid date")
	}
}

func TestPublicIDMarshal(t *testing.T) {
	u := uuid.MustParse("018f1a2b-3c4d-7e8f-9a0b-1c2d3e4f5061")
	p := NewPublicID(u)

	data, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	want := `"018f1a2b-3c4d-7e8f-9a0b-1c2d3e4f5061"`
	if string(data) != want {
		t.Fatalf("got %s want %s", data, want)
	}

	var decoded PublicID
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if decoded.UUID() != u {
		t.Fatalf("round-trip mismatch")
	}

	if err := json.Unmarshal([]byte(`"not-a-uuid"`), &decoded); err == nil {
		t.Fatal("expected error for invalid uuid")
	}
}
