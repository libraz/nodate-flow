// Package types re-exports the shared PublicID type so that sqlc-generated
// code continues to compile without changes to sqlc.yaml overrides.
package types

import "github.com/libraz/nodate-flow/packages/go-shared/dbtype"

// PublicID is a UUID v7 stored as MySQL BINARY(16).
type PublicID = dbtype.PublicID

// New generates a fresh UUID v7 wrapped as a PublicID.
var New = dbtype.New

// FromUUID converts a uuid.UUID into a PublicID without copying.
var FromUUID = dbtype.FromUUID

// Parse parses a canonical UUID string into a PublicID.
var Parse = dbtype.Parse
