package handlerutil

import (
	"database/sql"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
)

func TestAvatarURL(t *testing.T) {
	t.Parallel()

	id := types.New()
	key := "avatars/" + id.String() + "/0190abcd-1111-7000-8000-000000000000.jpg"

	require.Equal(t, "/avatars/"+id.String()+"?v=0190abcd", AvatarURL(sql.NullString{String: key, Valid: true}, id))
	require.Equal(t, "https://cdn.example/a.png", AvatarURL(sql.NullString{String: "https://cdn.example/a.png", Valid: true}, id))
	require.Empty(t, AvatarURL(sql.NullString{}, id))
	require.Empty(t, AvatarURL(sql.NullString{String: key, Valid: true}, types.PublicID{}))
	require.Nil(t, AvatarURLPtr(sql.NullString{}, id))
	require.Equal(t, "https://cdn.example/a.png", *AvatarURLPtr(sql.NullString{String: "https://cdn.example/a.png", Valid: true}, id))
}
