package intake

import (
	"strconv"

	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/http/handlers/handlerutil"
)

// mapListRow converts a ListIntakeItemsForWorkspaceRow to the Item DTO.
func mapListRow(r generated.ListIntakeItemsForWorkspaceRow) Item {
	item := Item{
		ID:                   r.PublicID.String(),
		Title:                r.Title,
		Body:                 nullStr(r.Body),
		TriageStatus:         string(r.TriageStatus),
		SnoozeUntil:          handlerutil.NullTimeUnix(r.SnoozeUntil),
		AIReasoning:          nullStr(r.AiReasoning),
		TriagedByUserID:      handlerutil.PublicIDOrEmpty(r.TriagedByPublicID),
		TriagedByDisplayName: nullStr(r.TriagedByDisplayName),
		CreatedAt:            r.CreatedAt.Unix(),
	}
	if r.AiScore.Valid {
		if f, err := strconv.ParseFloat(r.AiScore.String, 64); err == nil {
			item.AIScore = &f
		}
	}
	return item
}

// mapFindRow converts a FindIntakeItemByPublicIdRow to the Item DTO.
func mapFindRow(r generated.FindIntakeItemByPublicIdRow) Item {
	item := Item{
		ID:           r.PublicID.String(),
		Title:        r.Title,
		Body:         nullStr(r.Body),
		TriageStatus: string(r.TriageStatus),
		SnoozeUntil:  handlerutil.NullTimeUnix(r.SnoozeUntil),
		AIReasoning:  nullStr(r.AiReasoning),
		CreatedAt:    r.CreatedAt.Unix(),
	}
	if r.AiScore.Valid {
		if f, err := strconv.ParseFloat(r.AiScore.String, 64); err == nil {
			item.AIScore = &f
		}
	}
	return item
}
