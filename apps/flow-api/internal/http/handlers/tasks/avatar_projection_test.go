package tasks

import (
	"database/sql"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
)

func TestTaskMappersProjectAvatarStorageKeys(t *testing.T) {
	t.Parallel()

	userID := types.New()
	key := "avatars/" + userID.String() + "/0190abcd-1111-7000-8000-000000000000.jpg"
	want := "/avatars/" + userID.String() + "?v=0190abcd"
	stored := sql.NullString{String: key, Valid: true}
	external := sql.NullString{String: "https://cdn.example/a.png", Valid: true}

	require.Equal(t, want, rowToActor(generated.ListActorsForTaskRow{UserPublicID: userID, AvatarUrl: stored}).AvatarURL)
	require.Equal(t, "https://cdn.example/a.png", rowToActor(generated.ListActorsForTaskRow{UserPublicID: userID, AvatarUrl: external}).AvatarURL)

	require.Equal(t, want, rowToComment(generated.ListCommentsForTaskRow{AuthorPublicID: userID, AuthorAvatarUrl: stored}).AuthorAvatarURL)
	require.Equal(t, want, rowToCommentKeyset(generated.ListCommentsForTaskKeysetRow{AuthorPublicID: userID, AuthorAvatarUrl: stored}).AuthorAvatarURL)
	require.Equal(t, "https://cdn.example/a.png", rowToComment(generated.ListCommentsForTaskRow{AuthorPublicID: userID, AuthorAvatarUrl: external}).AuthorAvatarURL)
}
