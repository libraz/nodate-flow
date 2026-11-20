package timeline

import (
	"database/sql"
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	"github.com/libraz/nodate-flow/packages/go-shared/avatarutil"
)

func TestToDTOActorAvatarProjection(t *testing.T) {
	t.Parallel()

	actorID := uuid.MustParse("01900000-0000-7000-8000-000000000001")
	actorPublicID := actorID.String()
	actorPublicIDBytes, err := actorID.MarshalBinary()
	require.NoError(t, err)

	tests := []struct {
		name            string
		actorID         []byte
		avatarURL       sql.NullString
		storageObjectID sql.NullInt32
		wantAvatarURL   string
		wantAvatarField bool
	}{
		{
			name:    "user without avatar omits the field",
			actorID: actorPublicIDBytes,
		},
		{
			name:            "external avatar passes through",
			actorID:         actorPublicIDBytes,
			avatarURL:       sql.NullString{String: "https://images.example.test/user.png?size=96", Valid: true},
			wantAvatarURL:   "https://images.example.test/user.png?size=96",
			wantAvatarField: true,
		},
		{
			name:            "uploaded avatar takes precedence over external url",
			actorID:         actorPublicIDBytes,
			avatarURL:       sql.NullString{String: "https://images.example.test/stale.png", Valid: true},
			storageObjectID: sql.NullInt32{Int32: 48213, Valid: true},
			wantAvatarURL:   avatarutil.ProxyURL(actorPublicID, avatarutil.OpaqueTag(48213), ""),
			wantAvatarField: true,
		},
		{
			name:            "disabled user join omits avatar and actor identity",
			avatarURL:       sql.NullString{},
			storageObjectID: sql.NullInt32{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			event := toDTO(timelineRow{
				publicID:                   types.New(),
				actorPublicID:              tt.actorID,
				actorAvatarURL:             tt.avatarURL,
				actorAvatarStorageObjectID: tt.storageObjectID,
			})
			require.Equal(t, tt.wantAvatarURL, event.ActorAvatarURL)
			if len(tt.actorID) == 0 {
				require.Empty(t, event.ActorUserID)
			}

			body, err := json.Marshal(event)
			require.NoError(t, err)
			var dto map[string]any
			require.NoError(t, json.Unmarshal(body, &dto))
			_, hasAvatarField := dto["actorAvatarUrl"]
			require.Equal(t, tt.wantAvatarField, hasAvatarField)
		})
	}
}
