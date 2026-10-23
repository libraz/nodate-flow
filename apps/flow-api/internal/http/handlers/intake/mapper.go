package intake

import (
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/handlers/handlerutil"
)

// mapListRow converts a ListIntakeItemsForWorkspaceRow to the Record DTO.
func mapListRow(r generated.ListIntakeItemsForWorkspaceRow) Record {
	item := Record{
		ID:                   r.PublicID.String(),
		Title:                r.Title,
		Body:                 nullStr(r.Body),
		TriageStatus:         string(r.TriageStatus),
		SnoozeUntil:          handlerutil.NullTimeUnix(r.SnoozeUntil),
		TriagedByUserID:      handlerutil.PublicIDOrEmpty(r.TriagedByPublicID),
		TriagedByDisplayName: nullStr(r.TriagedByDisplayName),
		CreatedAt:            r.CreatedAt.Unix(),
	}
	return item
}

// mapKeysetListRow converts a ListIntakeItemsForWorkspaceKeysetRow to the Record DTO.
func mapKeysetListRow(r generated.ListIntakeItemsForWorkspaceKeysetRow) Record {
	item := Record{
		ID:                   r.PublicID.String(),
		Title:                r.Title,
		Body:                 nullStr(r.Body),
		TriageStatus:         string(r.TriageStatus),
		SnoozeUntil:          handlerutil.NullTimeUnix(r.SnoozeUntil),
		TriagedByUserID:      handlerutil.PublicIDOrEmpty(r.TriagedByPublicID),
		TriagedByDisplayName: nullStr(r.TriagedByDisplayName),
		CreatedAt:            r.CreatedAt.Unix(),
	}
	return item
}

// mapFindRow converts a FindIntakeItemByPublicIdRow to the Record DTO.
func mapFindRow(r generated.FindIntakeItemByPublicIdRow) Record {
	item := Record{
		ID:           r.PublicID.String(),
		Title:        r.Title,
		Body:         nullStr(r.Body),
		TriageStatus: string(r.TriageStatus),
		SnoozeUntil:  handlerutil.NullTimeUnix(r.SnoozeUntil),
		CreatedAt:    r.CreatedAt.Unix(),
	}
	return item
}
