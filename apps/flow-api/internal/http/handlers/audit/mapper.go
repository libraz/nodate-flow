package audit

import (
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/packages/go-shared/dbtype"
)

// mapListRow converts a ListWorkspaceAuditLogsRow to the LogEntryDTO.
// Null-capable columns are surfaced as nil pointers so the JSON payload
// matches the frontend TypeScript interface, which uses "string | null"
// for actorUserPublicId, actorDisplayName, resourcePublicId, ipAddress,
// userAgent, and "Record<string, unknown> | null" for metadataJson.
//
// Time conversion: occurred_at is DATETIME in the database, which sqlc
// exposes as a non-nullable time.Time. Every audit row has a populated
// occurred_at (NOT NULL), so the Unix-seconds conversion is unconditional.
func mapListRow(r generated.ListWorkspaceAuditLogsRow) LogEntryDTO {
	dto := LogEntryDTO{
		PublicID:     r.PublicID.String(),
		Action:       r.Action,
		ResourceType: r.ResourceType,
		IPAddress:    dbtype.IPPtrFromNullString(r.IpAddress),
		UserAgent:    nullStrPtr(r.UserAgent),
		MetadataJSON: r.MetadataJson,
		OccurredAt:   r.OccurredAt.Unix(),
	}

	// actor.public_id is produced by a LEFT JOIN; MySQL drivers scan SQL
	// NULL into the PublicID zero value, so promote zero → nil here to
	// preserve the "system / deleted actor" distinction on the wire.
	if s := publicIDOrNilString(r.ActorUserPublicID); s != "" {
		dto.ActorUserPublicID = &s
	}
	dto.ActorDisplayName = dbtype.PtrFromNullString(r.ActorDisplayName)

	// resource_public_id is BINARY(16) NULL at the column level; sqlc
	// emits a non-nullable PublicID because the overrides force that
	// Go type. Treat the zero PublicID the same as SQL NULL: absent.
	if s := publicIDOrNilString(r.ResourcePublicID); s != "" {
		dto.ResourcePublicID = &s
	}

	return dto
}
