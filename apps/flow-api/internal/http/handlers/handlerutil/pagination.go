// Package handlerutil — pagination helpers.
//
// This file centralises the limit/offset validation that was previously
// duplicated as struct tags across every list endpoint. Tag-level
// validation still applies (Huma enforces minimum/maximum/default), but
// handlers should call [Bind] before reaching the query layer so the
// effective values are always consistent — even on the rare paths that
// build a [PageParams] outside Huma input binding (background jobs,
// internal callers).
package handlerutil

// Repository-wide pagination constants. List endpoints should adopt
// these by default; only deviate when a feature genuinely needs a
// different cap (e.g. bulk export) and document the reason in a
// comment next to the override.
const (
	// DefaultListLimit is the default page size for list endpoints
	// when the client does not specify one.
	DefaultListLimit int32 = 50
	// MaxListLimit is the maximum page size accepted by list
	// endpoints. Endpoints that truly need to scan more rows in a
	// single response (e.g. unified cross-workspace dashboards,
	// CSV exports) pass an explicit override to [Bind].
	MaxListLimit int32 = 200
)

// StandardLimitTag is the canonical Huma struct-tag string for a Limit
// query parameter on a generic list endpoint. List handlers should
// copy this value verbatim onto their `Limit int32` field unless the
// endpoint has a documented exception. The value is kept here so that
// any audit (`rg "StandardLimitTag"`) reaches every list endpoint via a
// single source of truth.
const StandardLimitTag = `query:"limit" minimum:"1" maximum:"200" default:"50"`

// StandardOffsetTag is the canonical Huma struct-tag string for an
// Offset query parameter on a generic list endpoint. Handlers using a
// keyset cursor still expose Offset for the OFFSET fallback path, so
// the tag applies repository-wide.
const StandardOffsetTag = `query:"offset" minimum:"0" default:"0"`

// PageParams is the validated, clamped pagination tuple consumed by
// query layers. The fields mirror SQL LIMIT / OFFSET 1:1.
type PageParams struct {
	Limit  int32
	Offset int32
}

// Bind clamps and defaults a (limit, offset) tuple from raw user
// input. The function is deliberately tolerant — Huma's struct-tag
// validation is the primary gate — so that internal callers that
// bypass the framework still produce sane values.
//
//   - limit <= 0       → defaultLimit
//   - limit > maxLimit → maxLimit
//   - offset < 0       → 0
//
// Callers pass the per-endpoint defaultLimit / maxLimit so that
// hot lists (small page) and bulk exports (large page) share one
// helper without giving up their domain-specific caps.
func Bind(rawLimit, rawOffset, defaultLimit, maxLimit int32) PageParams {
	limit := rawLimit
	if limit <= 0 {
		limit = defaultLimit
	}
	if maxLimit > 0 && limit > maxLimit {
		limit = maxLimit
	}
	offset := rawOffset
	if offset < 0 {
		offset = 0
	}
	return PageParams{Limit: limit, Offset: offset}
}
