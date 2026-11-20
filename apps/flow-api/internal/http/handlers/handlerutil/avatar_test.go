package handlerutil

import (
	"database/sql"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	"github.com/libraz/nodate-flow/packages/go-shared/avatarutil"
)

func TestAvatarURL(t *testing.T) {
	t.Parallel()

	id := types.New()
	key := "avatars/" + id.String() + "/0190abcd-1111-7000-8000-000000000000.jpg"

	require.Equal(t, "/avatars/"+id.String()+"?v=0190abcd", AvatarURL(sql.NullString{String: key, Valid: true}, sql.NullInt32{}, id))
	require.Equal(t, "https://cdn.example/a.png", AvatarURL(sql.NullString{String: "https://cdn.example/a.png", Valid: true}, sql.NullInt32{}, id))
	require.Empty(t, AvatarURL(sql.NullString{}, sql.NullInt32{}, id))
	require.Empty(t, AvatarURL(sql.NullString{String: key, Valid: true}, sql.NullInt32{}, types.PublicID{}))
	require.Equal(t, "/avatars/"+id.String()+"?v="+avatarutil.OpaqueTag(7), AvatarURL(sql.NullString{String: "https://cdn.example/a.png", Valid: true}, sql.NullInt32{Int32: 7, Valid: true}, id))
	require.Nil(t, AvatarURLPtr(sql.NullString{}, sql.NullInt32{}, id))
	require.Equal(t, "https://cdn.example/a.png", *AvatarURLPtr(sql.NullString{String: "https://cdn.example/a.png", Valid: true}, sql.NullInt32{}, id))
}
