// Package dbtype IP address conversion helpers.
//
// Client IP columns (sessions.ip_address, audit_logs.ip_address, ...) are
// VARBINARY(16): an address is stored packed, never as text. A textual
// IPv6 address is up to 45 characters and would be rejected outright by
// MySQL in STRICT mode, so every write must go through
// [NullStringFromIP] and every read back to the API through
// [IPStringFromNullString] / [IPPtrFromNullString].
//
// Storage format: always the 16-byte form. An IPv4 address is stored as
// its IPv4-mapped IPv6 equivalent (::ffff:a.b.c.d) rather than 4 bytes,
// so the column width is the only thing a reader has to know about. The
// mapping is undone on read, so an IPv4 address round-trips to its
// dotted-quad text ("192.0.2.1" -> 16 bytes -> "192.0.2.1") and so does
// an IPv4-mapped input ("::ffff:192.0.2.1" -> "192.0.2.1"): the dotted
// quad is the canonical text form for anything that is IPv4 underneath.
//
// Empty and unparseable input becomes SQL NULL, and NULL reads back as
// the empty string (or nil for the pointer variant).
package dbtype

import (
	"database/sql"
	"net/netip"
	"strings"
)

// NullStringFromIP packs a client IP into the VARBINARY(16) form the
// ip_address columns expect. The value is carried in a sql.NullString
// because the driver writes its bytes verbatim; the string holds raw
// bytes, not text. Empty or unparseable input yields SQL NULL so a
// malformed X-Forwarded-For entry can never fail the write.
func NullStringFromIP(ip string) sql.NullString {
	addr, ok := parseAddr(ip)
	if !ok {
		return sql.NullString{}
	}
	packed := addr.As16()
	return sql.NullString{String: string(packed[:]), Valid: true}
}

// IPStringFromNullString unpacks a stored ip_address column back into
// its canonical text form, returning "" for SQL NULL and for values that
// cannot be decoded. Use this where the DTO field is a plain string.
func IPStringFromNullString(v sql.NullString) string {
	if !v.Valid {
		return ""
	}
	return unpackIP(v.String)
}

// IPPtrFromNullString is the pointer-returning twin of
// IPStringFromNullString, for DTO fields typed *string (JSON null).
func IPPtrFromNullString(v sql.NullString) *string {
	s := IPStringFromNullString(v)
	if s == "" {
		return nil
	}
	return &s
}

// unpackIP decodes one stored column value. Rows written before the
// packing helper existed hold the text form, so a value that parses as
// an address is returned as-is (normalised); everything else is read as
// packed bytes.
func unpackIP(raw string) string {
	if raw == "" {
		return ""
	}
	if addr, ok := parseAddr(raw); ok {
		return addr.Unmap().String()
	}
	switch b := []byte(raw); len(b) {
	case 4:
		return netip.AddrFrom4([4]byte(b)).String()
	case 16:
		return netip.AddrFrom16([16]byte(b)).Unmap().String()
	}
	return ""
}

// parseAddr parses a client IP string, dropping any IPv6 zone: a zone is
// meaningful only on the host that observed the address and has nowhere
// to live in a 16-byte column.
func parseAddr(ip string) (netip.Addr, bool) {
	addr, err := netip.ParseAddr(strings.TrimSpace(ip))
	if err != nil {
		return netip.Addr{}, false
	}
	return addr.WithZone(""), true
}
