package integrationmappings

import (
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
)

// mapListRow converts a ListIntegrationSourceMappingsRow to the DTO.
func mapListRow(r generated.ListIntegrationSourceMappingsRow) IntegrationMapping {
	return IntegrationMapping{
		ID:          r.PublicID.String(),
		Provider:    string(r.Provider),
		ExternalKey: r.ExternalKey,
		Label:       r.Label,
		Enabled:     r.Enabled,
		UpdatedAt:   nullTimeUnix(r.UpdatedAt),
		CreatedAt:   r.CreatedAt.Unix(),
	}
}

// mapFindRow converts a FindIntegrationSourceMappingByPublicIdRow to the DTO.
func mapFindRow(r generated.FindIntegrationSourceMappingByPublicIdRow) IntegrationMapping {
	return IntegrationMapping{
		ID:          r.PublicID.String(),
		Provider:    string(r.Provider),
		ExternalKey: r.ExternalKey,
		Label:       r.Label,
		Enabled:     r.Enabled,
		UpdatedAt:   nullTimeUnix(r.UpdatedAt),
		CreatedAt:   r.CreatedAt.Unix(),
	}
}
