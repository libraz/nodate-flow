package taskdesc

import (
	"database/sql"
	"testing"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
)

func TestActorRejectsConflictingHumanAndAgentBeforeWriting(t *testing.T) {
	actor := Actor{
		UserID:        sql.NullInt32{Int32: 11, Valid: true},
		AgentID:       sql.NullInt32{Int32: 22, Valid: true},
		AgentPublicID: types.New(),
		AgentName:     sql.NullString{String: "Agent", Valid: true},
	}
	if err := actor.validate(); err == nil {
		t.Fatal("actor with both human and agent authors passed validation")
	}
}
