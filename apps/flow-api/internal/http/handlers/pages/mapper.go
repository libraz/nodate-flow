package pages

import (
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/types"
)

// publicIDOrEmpty returns the UUID string of a types.PublicID, or ""
// when it is the zero value (i.e. a LEFT JOIN returned NULL).
func publicIDOrEmpty(p types.PublicID) string {
	var zero types.PublicID
	if p == zero {
		return ""
	}
	return p.String()
}

// publicIDPtr returns a *string for a types.PublicID, or nil when zero.
func publicIDPtr(p types.PublicID) *string {
	var zero types.PublicID
	if p == zero {
		return nil
	}
	s := p.String()
	return &s
}

// mapGetRow converts a GetPageByPublicIdRow to a PageDTO.
func mapGetRow(r generated.GetPageByPublicIdRow) PageDTO {
	return PageDTO{
		ID:                 r.PublicID.String(),
		ProjectID:          publicIDPtr(r.ProjectPublicID),
		ProjectName:        nullStrPtr(r.ProjectName),
		CreatorID:          r.CreatorPublicID.String(),
		CreatorDisplayName: r.CreatorDisplayName,
		ParentPageID:       publicIDPtr(r.ParentPagePublicID),
		ParentPageTitle:    nullStrPtr(r.ParentPageTitle),
		Title:              r.Title,
		Body:               r.Body,
		IsAIGenerated:      r.IsAiGenerated,
		SortWeight:         r.SortWeight,
		Notes:              nullStrPtr(r.Notes),
		UpdatedAt:          nullTimeUnix(r.UpdatedAt),
		CreatedAt:          r.CreatedAt.Unix(),
	}
}

// mapWorkspaceListRow converts a ListPagesForWorkspaceRow to a PageSummaryDTO.
func mapWorkspaceListRow(r generated.ListPagesForWorkspaceRow) PageSummaryDTO {
	return PageSummaryDTO{
		ID:                 r.PublicID.String(),
		ProjectID:          publicIDPtr(r.ProjectPublicID),
		ProjectName:        nullStrPtr(r.ProjectName),
		CreatorID:          r.CreatorPublicID.String(),
		CreatorDisplayName: r.CreatorDisplayName,
		Title:              r.Title,
		IsAIGenerated:      r.IsAiGenerated,
		SortWeight:         r.SortWeight,
		UpdatedAt:          nullTimeUnix(r.UpdatedAt),
		CreatedAt:          r.CreatedAt.Unix(),
	}
}

// mapChildListRow converts a ListChildPagesRow to a PageSummaryDTO.
func mapChildListRow(r generated.ListChildPagesRow) PageSummaryDTO {
	return PageSummaryDTO{
		ID:                 r.PublicID.String(),
		ProjectID:          publicIDPtr(r.ProjectPublicID),
		ProjectName:        nullStrPtr(r.ProjectName),
		CreatorID:          r.CreatorPublicID.String(),
		CreatorDisplayName: r.CreatorDisplayName,
		Title:              r.Title,
		IsAIGenerated:      r.IsAiGenerated,
		SortWeight:         r.SortWeight,
		UpdatedAt:          nullTimeUnix(r.UpdatedAt),
		CreatedAt:          r.CreatedAt.Unix(),
	}
}

// mapSearchRow converts a SearchPagesRow to a PageSummaryDTO.
func mapSearchRow(r generated.SearchPagesRow) PageSummaryDTO {
	return PageSummaryDTO{
		ID:                 r.PublicID.String(),
		ProjectID:          publicIDPtr(r.ProjectPublicID),
		ProjectName:        nullStrPtr(r.ProjectName),
		CreatorID:          r.CreatorPublicID.String(),
		CreatorDisplayName: r.CreatorDisplayName,
		ParentPageID:       publicIDPtr(r.ParentPagePublicID),
		Title:              r.Title,
		IsAIGenerated:      r.IsAiGenerated,
		SortWeight:         r.SortWeight,
		UpdatedAt:          nullTimeUnix(r.UpdatedAt),
		CreatedAt:          r.CreatedAt.Unix(),
	}
}
