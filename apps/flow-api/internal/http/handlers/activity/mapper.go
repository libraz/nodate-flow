package activity

import (
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
)

// mapRow converts a ListWorkspaceActivityRow to the public Entry DTO. This
// is the only place that crosses the time / public-id boundary for the
// activity feed.
//
// The view's source / source_table / actor_kind / severity columns are
// CAST(... AS CHAR ...) expressions, which sqlc exposes as interface{} and
// the MySQL driver scans as []byte; ifaceString normalises both []byte and
// string. Only public_ids are surfaced — no internal id leaves this layer.
func mapRow(r generated.ListWorkspaceActivityRow) Entry {
	e := Entry{
		PublicID:     r.PublicID.String(),
		Source:       ifaceString(r.Source),
		SourceTable:  ifaceString(r.SourceTable),
		ActorKind:    ifaceString(r.ActorKind),
		Action:       r.Action,
		ResourceType: r.ResourceType,
		Severity:     ifaceString(r.Severity),
		OccurredAt:   r.OccurredAt.Unix(),
	}

	// actor_user_public_id is a LEFT JOIN column: sqlc emits *types.PublicID,
	// nil when the source row had no user actor (system / agent entries).
	if r.ActorUserPublicID != nil {
		s := r.ActorUserPublicID.String()
		e.ActorUserPublicID = &s
	}

	// resource_public_id is non-nullable in the Go type but the audit leg
	// can carry the zero PublicID when the action was not resource-scoped;
	// promote zero -> nil so the wire shape distinguishes "no resource".
	if zero := (generated.ListWorkspaceActivityRow{}).ResourcePublicID; r.ResourcePublicID != zero {
		s := r.ResourcePublicID.String()
		e.ResourcePublicID = &s
	}

	return e
}

// ifaceString normalises a CAST(... AS CHAR) column. sqlc types these as
// interface{}; the MySQL driver returns []byte for them, but a string is
// accepted too for robustness. Any other type yields the empty string.
func ifaceString(v interface{}) string {
	switch t := v.(type) {
	case []byte:
		return string(t)
	case string:
		return t
	default:
		return ""
	}
}
