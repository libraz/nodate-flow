package gateway

import (
	"testing"

	"github.com/bwmarrin/discordgo"
	"github.com/stretchr/testify/require"
)

func TestDefaultSessionFactoryUsesSynchronousPresenceHandlers(t *testing.T) {
	session, err := defaultSessionFactory("token")
	require.NoError(t, err)

	adapter, ok := session.(discordgoAdapter)
	require.True(t, ok)
	require.True(t, adapter.SyncEvents, "presence event handlers must preserve gateway order")
	require.Equal(t,
		discordgo.IntentsGuildPresences|discordgo.IntentsGuildMembers,
		adapter.Identify.Intents,
	)
}
