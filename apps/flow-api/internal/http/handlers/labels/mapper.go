package labels

import (
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/handlers/handlerutil"
)

// nullTimeUnix delegates to handlerutil.NullTimeUnix (returns *int64, nil for NULL).
var nullTimeUnix = handlerutil.NullTimeUnix

// nullStr delegates to handlerutil.NullStr (returns empty string for NULL).
var nullStr = handlerutil.NullStr

// totalAsInt64 delegates to handlerutil.TotalAsInt64.
var totalAsInt64 = handlerutil.TotalAsInt64

// mapLabel converts a FindLabelByPublicIdRow to the Label DTO.
func mapLabel(r generated.FindLabelByPublicIdRow) Label {
	return Label{
		ID:          r.PublicID.String(),
		Name:        r.Name,
		Color:       r.Color,
		Description: nullStr(r.Description),
		SortWeight:  r.SortWeight,
		UpdatedAt:   nullTimeUnix(r.UpdatedAt),
		CreatedAt:   r.CreatedAt.Unix(),
	}
}

// mapWorkspaceLabel converts a ListLabelsForWorkspaceRow to the Label DTO.
func mapWorkspaceLabel(r generated.ListLabelsForWorkspaceRow) Label {
	return Label{
		ID:          r.PublicID.String(),
		Name:        r.Name,
		Color:       r.Color,
		Description: nullStr(r.Description),
		SortWeight:  r.SortWeight,
		UpdatedAt:   nullTimeUnix(r.UpdatedAt),
		CreatedAt:   r.CreatedAt.Unix(),
	}
}

// mapProjectLabel converts a ListLabelsForProjectRow to the Label DTO.
func mapProjectLabel(r generated.ListLabelsForProjectRow) Label {
	return Label{
		ID:          r.PublicID.String(),
		Name:        r.Name,
		Color:       r.Color,
		Description: nullStr(r.Description),
		SortWeight:  r.SortWeight,
		UpdatedAt:   nullTimeUnix(r.UpdatedAt),
		CreatedAt:   r.CreatedAt.Unix(),
	}
}

// mapTaskLabel converts a ListTaskLabelsRow to the TaskLabel DTO.
func mapTaskLabel(r generated.ListTaskLabelsRow) TaskLabel {
	return TaskLabel{
		ID:          r.PublicID.String(),
		Name:        r.Name,
		Color:       r.Color,
		Description: nullStr(r.Description),
		SortWeight:  r.SortWeight,
		CreatedAt:   r.CreatedAt.Unix(),
	}
}
