package ai

import (
	"database/sql"
	"strings"
	"testing"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
)

// TestInboxTriagePromptOmitsUnreadableTitles holds the triage prompt to
// the reader's visibility: the row stays, so the item can still be
// scored, but the title of a task the reader may not open never reaches
// the provider or the cached proposal served back to them.
func TestInboxTriagePromptOmitsUnreadableTitles(t *testing.T) {
	t.Parallel()

	rows := []generated.ListInboxRow{
		{
			PublicID:    types.New(),
			TaskTitle:   sql.NullString{String: "Readable task title", Valid: true},
			TaskVisible: sql.NullBool{Bool: true, Valid: true},
		},
		{
			PublicID:    types.New(),
			TaskTitle:   sql.NullString{String: "Private task title", Valid: true},
			TaskVisible: sql.NullBool{Bool: false, Valid: true},
		},
	}

	prompt := buildInboxTriagePrompt(rows)
	if strings.Contains(prompt, "Private task title") {
		t.Errorf("prompt carries the title of a task the reader may not read: %s", prompt)
	}
	if !strings.Contains(prompt, "Readable task title") {
		t.Errorf("prompt dropped a readable title, so this test proves nothing: %s", prompt)
	}
	if !strings.Contains(prompt, rows[1].PublicID.String()) {
		t.Errorf("the unreadable item must still be listed for scoring: %s", prompt)
	}
}
