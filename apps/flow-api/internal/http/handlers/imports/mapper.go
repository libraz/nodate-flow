package imports

import (
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/generated"
)

// mapFindRow converts a FindImportJobByPublicIdRow to the ImportJobBody DTO.
func mapFindRow(r generated.FindImportJobByPublicIdRow) ImportJobBody {
	return ImportJobBody{
		ID:             r.PublicID.String(),
		Source:         string(r.Source),
		Status:         string(r.Status),
		TotalItems:     int(r.TotalItems),
		ProcessedItems: int(r.ProcessedItems),
		FailedItems:    int(r.FailedItems),
		ErrorLog:       nullStrPtr(r.ErrorLog),
		StartedAt:      nullTimeUnix(r.StartedAt),
		CompletedAt:    nullTimeUnix(r.CompletedAt),
		CreatedAt:      r.CreatedAt.Unix(),
	}
}

// mapListRow converts a ListImportJobsForWorkspaceRow to the ImportJobBody DTO.
func mapListRow(r generated.ListImportJobsForWorkspaceRow) ImportJobBody {
	return ImportJobBody{
		ID:             r.PublicID.String(),
		Source:         string(r.Source),
		Status:         string(r.Status),
		TotalItems:     int(r.TotalItems),
		ProcessedItems: int(r.ProcessedItems),
		FailedItems:    int(r.FailedItems),
		StartedAt:      nullTimeUnix(r.StartedAt),
		CompletedAt:    nullTimeUnix(r.CompletedAt),
		CreatedAt:      r.CreatedAt.Unix(),
	}
}
