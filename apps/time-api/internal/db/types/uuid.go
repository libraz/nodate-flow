package types

import (
	"database/sql/driver"
	"fmt"

	"github.com/google/uuid"
)

// PublicID is a UUID v7 stored as MySQL BINARY(16).
type PublicID uuid.UUID

// New generates a fresh UUID v7 wrapped as a PublicID.
func New() PublicID {
	return PublicID(uuid.Must(uuid.NewV7()))
}

// FromUUID converts a uuid.UUID into a PublicID without copying.
func FromUUID(u uuid.UUID) PublicID { return PublicID(u) }

// UUID returns the underlying uuid.UUID value.
func (p PublicID) UUID() uuid.UUID { return uuid.UUID(p) }

// String returns the canonical UUID string representation.
func (p PublicID) String() string { return uuid.UUID(p).String() }

// Value implements driver.Valuer by returning the 16 raw bytes of the UUID.
func (p PublicID) Value() (driver.Value, error) {
	b := uuid.UUID(p)
	return b[:], nil
}

// Scan implements sql.Scanner.
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
