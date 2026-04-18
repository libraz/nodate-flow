// Package dto defines API boundary types shared by Huma operations.
//
// This package is the single boundary between database row types
// (sqlc-generated) and the JSON wire format. Rules:
//
//   - All time values are int64 unix seconds (see UnixSeconds).
//   - All date values are YYYY-MM-DD strings (see DateOnly).
//   - All public identifiers are UUID v7 strings (see PublicID).
//   - Never embed sqlc-generated structs here; map explicitly in
//     handler-local mapper.go files.
//   - Internal numeric ids (workspace_id, etc.) must never appear
//     in DTOs.
package dto
