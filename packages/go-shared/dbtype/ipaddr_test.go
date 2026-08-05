package dbtype_test

import (
	"database/sql"
	"testing"

	"github.com/libraz/nodate-flow/packages/go-shared/dbtype"
)

func TestNullStringFromIPPacksToSixteenBytes(t *testing.T) {
	t.Parallel()

	cases := []string{
		"192.0.2.1",
		"127.0.0.1",
		"::1",
		"2001:db8:85a3:8d3:1319:8a2e:370:7348",
		"fd00:1234:5678:9abc:def0:1234:5678:9abc",
		"::ffff:192.0.2.1",
	}
	for _, ip := range cases {
		t.Run(ip, func(t *testing.T) {
			t.Parallel()

			got := dbtype.NullStringFromIP(ip)
			if !got.Valid {
				t.Fatalf("NullStringFromIP(%q) = NULL, want a packed value", ip)
			}
			// The column is VARBINARY(16); a longer value is rejected by
			// MySQL in STRICT mode, which is how the text form broke
			// logins from real IPv6 clients.
			if len(got.String) != 16 {
				t.Fatalf("NullStringFromIP(%q) packed to %d bytes, want 16", ip, len(got.String))
			}
		})
	}
}

func TestIPRoundTrip(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		in   string
		want string
	}{
		{name: "ipv4", in: "192.0.2.1", want: "192.0.2.1"},
		{name: "ipv4 loopback", in: "127.0.0.1", want: "127.0.0.1"},
		{name: "ipv6 loopback", in: "::1", want: "::1"},
		{
			name: "ipv6 global",
			in:   "2001:db8:85a3:8d3:1319:8a2e:370:7348",
			want: "2001:db8:85a3:8d3:1319:8a2e:370:7348",
		},
		// An address that is IPv4 underneath always reads back as the
		// dotted quad, whichever notation it was written in.
		{name: "ipv4-mapped", in: "::ffff:192.0.2.1", want: "192.0.2.1"},
		// A zone identifier is dropped: it has no meaning outside the
		// host that observed the address.
		{name: "zoned link-local", in: "fe80::1%eth0", want: "fe80::1"},
		{name: "surrounding space", in: "  192.0.2.1  ", want: "192.0.2.1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := dbtype.IPStringFromNullString(dbtype.NullStringFromIP(tc.in))
			if got != tc.want {
				t.Fatalf("round trip of %q = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestNullStringFromIPRejectsUnusableInput(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		in   string
	}{
		{name: "empty", in: ""},
		{name: "not an address", in: "not-an-ip"},
		{name: "hostname", in: "localhost"},
		{name: "address with port", in: "192.0.2.1:8080"},
		{name: "truncated ipv6", in: "2001:db8:::1"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got := dbtype.NullStringFromIP(tc.in)
			if got.Valid {
				t.Fatalf("NullStringFromIP(%q) = %q, want NULL", tc.in, got.String)
			}
			if s := dbtype.IPStringFromNullString(got); s != "" {
				t.Fatalf("IPStringFromNullString(NULL) = %q, want \"\"", s)
			}
		})
	}
}

func TestIPStringFromNullStringReadsLegacyTextRows(t *testing.T) {
	t.Parallel()

	// Rows written before the packing helper existed hold the text form
	// (short addresses fit the column, which is why the bug went
	// unnoticed in local and CI runs).
	cases := []struct {
		in   string
		want string
	}{
		{in: "127.0.0.1", want: "127.0.0.1"},
		{in: "::1", want: "::1"},
		{in: "192.0.2.1", want: "192.0.2.1"},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			t.Parallel()

			got := dbtype.IPStringFromNullString(sql.NullString{String: tc.in, Valid: true})
			if got != tc.want {
				t.Fatalf("IPStringFromNullString(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestIPStringFromNullStringDecodesFourByteValues(t *testing.T) {
	t.Parallel()

	got := dbtype.IPStringFromNullString(sql.NullString{String: string([]byte{192, 0, 2, 1}), Valid: true})
	if got != "192.0.2.1" {
		t.Fatalf("IPStringFromNullString(4 packed bytes) = %q, want %q", got, "192.0.2.1")
	}
}

func TestIPStringFromNullStringOnGarbage(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		in   sql.NullString
	}{
		{name: "null", in: sql.NullString{}},
		{name: "empty string", in: sql.NullString{String: "", Valid: true}},
		{name: "wrong width", in: sql.NullString{String: "abcdefg", Valid: true}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := dbtype.IPStringFromNullString(tc.in); got != "" {
				t.Fatalf("IPStringFromNullString(%v) = %q, want \"\"", tc.in, got)
			}
		})
	}
}

func TestIPPtrFromNullString(t *testing.T) {
	t.Parallel()

	if got := dbtype.IPPtrFromNullString(sql.NullString{}); got != nil {
		t.Fatalf("IPPtrFromNullString(NULL) = %q, want nil", *got)
	}
	got := dbtype.IPPtrFromNullString(dbtype.NullStringFromIP("2001:db8::1"))
	if got == nil {
		t.Fatal("IPPtrFromNullString(packed) = nil, want a value")
	}
	if *got != "2001:db8::1" {
		t.Fatalf("IPPtrFromNullString(packed) = %q, want %q", *got, "2001:db8::1")
	}
}
