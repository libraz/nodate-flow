package projects

import (
	"database/sql"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/handlers/handlerutil"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
)

func TestProjectMemberProjectsAvatarStorageKey(t *testing.T) {
	t.Parallel()

	userID := types.New()
	key := "avatars/" + userID.String() + "/0190abcd-1111-7000-8000-000000000000.jpg"
	got := rowToProjectMember(generated.ListProjectMembersRow{UserPublicID: userID, AvatarUrl: sql.NullString{String: key, Valid: true}}, handlerutil.EmailVisibility{})
	require.Equal(t, "/avatars/"+userID.String()+"?v=0190abcd", got.AvatarURL)

	ext := rowToProjectMember(generated.ListProjectMembersRow{UserPublicID: userID, AvatarUrl: sql.NullString{String: "https://cdn.example/a.png", Valid: true}}, handlerutil.EmailVisibility{})
	require.Equal(t, "https://cdn.example/a.png", ext.AvatarURL)
}
