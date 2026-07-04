// Package dbtype contains shared scalar types used by the sqlc-generated
// database layer and hand-written code. The PublicID type lives here so that
// both flow-api and auth-api can share the same UUID v7 / BINARY(16) handling
// without duplication.
package dbtype

import (
	"database/sql/driver"
	"fmt"
	"strings"

	"github.com/google/uuid"
)

// PublicID is a UUID v7 stored as MySQL BINARY(16).
//
// driver.Valuer returns the 16 raw bytes (not the canonical 36-char string
// form), which is what the BINARY(16) column expects. The default
// uuid.UUID Valuer returns the string form, which fails to bind into
// BINARY(16) with "Data too long for column".
type PublicID uuid.UUID

// New generates a fresh UUID v7 wrapped as a PublicID.
func New() PublicID {
	return PublicID(uuid.Must(uuid.NewV7()))
}

// FromUUID converts a uuid.UUID into a PublicID without copying.
func FromUUID(u uuid.UUID) PublicID { return PublicID(u) }

// Parse parses a canonical UUID string into a PublicID.
func Parse(s string) (PublicID, error) {
	u, err := uuid.Parse(s)
	if err != nil {
		return PublicID{}, err
	}
	return PublicID(u), nil
}

// UUID returns the underlying uuid.UUID value.
func (p PublicID) UUID() uuid.UUID { return uuid.UUID(p) }

// String returns the canonical UUID string representation.
func (p PublicID) String() string { return uuid.UUID(p).String() }

// Value implements driver.Valuer by returning the 16 raw bytes of the UUID.
func (p PublicID) Value() (driver.Value, error) {
	b := uuid.UUID(p)
	return b[:], nil
}

// Scan implements sql.Scanner. It accepts BINARY(16) ([]byte), the canonical
// string form, or NULL.
func (p *PublicID) Scan(src any) error {
	switch v := src.(type) {
	case []byte:
		if len(v) != 16 {
			return fmt.Errorf("PublicID: expected 16 bytes, got %d", len(v))
		}
		copy((*p)[:], v)
		return nil
	case string:
		u, err := uuid.Parse(v)
		if err != nil {
			return err
		}
		*p = PublicID(u)
		return nil
	case nil:
		*p = PublicID{}
		return nil
	default:
		return fmt.Errorf("PublicID: unsupported scan type %T", src)
	}
}

// MarshalJSON encodes the PublicID as a JSON string in canonical UUID form.
func (p PublicID) MarshalJSON() ([]byte, error) {
	return []byte(`"` + p.String() + `"`), nil
}

// UnmarshalJSON decodes a canonical UUID JSON string into a PublicID.
func (p *PublicID) UnmarshalJSON(b []byte) error {
	if strings.TrimSpace(string(b)) == "null" {
		*p = PublicID{}
		return nil
	}
	if len(b) < 2 {
		return fmt.Errorf("PublicID: invalid json")
	}
	u, err := uuid.Parse(string(b[1 : len(b)-1]))
	if err != nil {
		return err
	}
	*p = PublicID(u)
	return nil
}
