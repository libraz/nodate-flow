package asanaimport

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSyncGuardFixtures(t *testing.T) {
	var f struct {
		Version int `json:"version"`
		Cases   []struct {
			Name    string  `json:"name"`
			Present int     `json:"present"`
			Synced  int     `json:"synced"`
			Delete  int     `json:"delete"`
			Moved   int     `json:"moved"`
			Refuse  *string `json:"refuse"`
		} `json:"cases"`
	}
	require.NoError(t, json.Unmarshal(readCoreFixture(t, "sync-guard.json"), &f))
	require.Equal(t, 1, f.Version)
	require.NotEmpty(t, f.Cases)
	for _, c := range f.Cases {
		t.Run(c.Name, func(t *testing.T) {
			want := ""
			if c.Refuse != nil {
				want = *c.Refuse
			}
			assert.Equal(t, want, Guard(GuardInput{Present: c.Present, Synced: c.Synced, Delete: c.Delete, Moved: c.Moved}))
		})
	}
}
