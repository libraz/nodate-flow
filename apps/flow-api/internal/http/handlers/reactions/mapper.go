package reactions

import "github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"

// mapTaskReactionRow converts a ListReactionsForTaskRow to the Reaction DTO.
func mapTaskReactionRow(r generated.ListReactionsForTaskRow) Reaction {
	return Reaction{
		ID:              r.PublicID.String(),
		Emoji:           r.Emoji,
		UserID:          r.UserPublicID.String(),
		UserDisplayName: r.DisplayName,
		CreatedAt:       r.CreatedAt.Unix(),
	}
}
