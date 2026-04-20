package favorites

import "github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/generated"

// mapListRow converts a ListFavoritesForUserRow to the Favorite DTO.
func mapListRow(r generated.ListFavoritesForUserRow) Favorite {
	return Favorite{
		ID:         r.PublicID.String(),
		TargetType: string(r.TargetType),
		TargetID:   r.TargetPublicID.String(),
		FolderName: nullStr(r.FolderName),
		SortWeight: r.SortWeight,
		CreatedAt:  r.CreatedAt.Unix(),
	}
}

// mapFindRow converts a FindFavoriteByPublicIdRow to the Favorite DTO.
func mapFindRow(r generated.FindFavoriteByPublicIdRow) Favorite {
	return Favorite{
		ID:         r.PublicID.String(),
		TargetType: string(r.TargetType),
		TargetID:   r.TargetPublicID.String(),
		FolderName: nullStr(r.FolderName),
		SortWeight: r.SortWeight,
		CreatedAt:  r.CreatedAt.Unix(),
	}
}
