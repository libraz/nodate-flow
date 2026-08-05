package dashboard

import (
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/handlers/handlerutil"
)

// mapGetRow converts a GetWidgetByPublicIDRow to a WidgetDTO.
func mapGetRow(r generated.GetWidgetByPublicIDRow) WidgetDTO {
	return WidgetDTO{
		ID:                 r.PublicID.String(),
		CreatorID:          r.CreatorPublicID.String(),
		CreatorDisplayName: r.CreatorDisplayName,
		WidgetType:         string(r.WidgetType),
		Title:              r.Title,
		Config:             r.Config,
		PositionX:          int(r.PositionX),
		PositionY:          int(r.PositionY),
		Width:              int(r.Width),
		Height:             int(r.Height),
		SortWeight:         int(r.SortWeight),
		UpdatedAt:          nullTimeUnix(r.UpdatedAt),
		CreatedAt:          r.CreatedAt.Unix(),
	}
}

// mapListRow converts a ListWidgetsForWorkspaceRow to a WidgetDTO.
func mapListRow(r generated.ListWidgetsForWorkspaceRow) WidgetDTO {
	return WidgetDTO{
		ID:                 r.PublicID.String(),
		CreatorID:          r.CreatorPublicID.String(),
		CreatorDisplayName: r.CreatorDisplayName,
		WidgetType:         string(r.WidgetType),
		Title:              r.Title,
		Config:             r.Config,
		PositionX:          int(r.PositionX),
		PositionY:          int(r.PositionY),
		Width:              int(r.Width),
		Height:             int(r.Height),
		SortWeight:         int(r.SortWeight),
		UpdatedAt:          nullTimeUnix(r.UpdatedAt),
		CreatedAt:          r.CreatedAt.Unix(),
	}
}

// nullTimeUnix delegates to handlerutil.NullTimeUnixVal (returns int64, 0 for NULL).
var nullTimeUnix = handlerutil.NullTimeUnixVal

// totalAsInt64 delegates to handlerutil.TotalAsInt64.
var totalAsInt64 = handlerutil.TotalAsInt64
