package dashboard

import (
	"database/sql"

	"github.com/nodate-flow/nodate-flow/apps/api/internal/db/generated"
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

// nullTimeUnix converts a sql.NullTime to a unix-seconds int64.
// Returns 0 for NULL (e.g. initial updated_at before any mutation).
func nullTimeUnix(t sql.NullTime) int64 {
	if !t.Valid {
		return 0
	}
	return t.Time.Unix()
}

// totalAsInt64 normalizes the COUNT(*) OVER() return type into int64.
func totalAsInt64(v interface{}) int64 {
	switch x := v.(type) {
	case int64:
		return x
	case int:
		return int64(x)
	case uint64:
		return int64(x)
	case []byte:
		var n int64
		for _, c := range x {
			if c < '0' || c > '9' {
				return n
			}
			n = n*10 + int64(c-'0')
		}
		return n
	default:
		return 0
	}
}
