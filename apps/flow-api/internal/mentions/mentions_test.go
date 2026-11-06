package mentions

import (
	"errors"
	"testing"
)

func TestValidateActorRequiresExclusivePositiveIdentity(t *testing.T) {
	user := int64(4)
	agent := int64(8)
	negative := int64(-1)
	zero := int64(0)
	for _, tc := range []struct {
		name    string
		userID  *int64
		agentID *int64
		wantErr bool
	}{
		{name: "process actor"},
		{name: "human actor", userID: &user},
		{name: "agent actor", agentID: &agent},
		{name: "both identities", userID: &user, agentID: &agent, wantErr: true},
		{name: "zero user", userID: &zero, wantErr: true},
		{name: "negative agent", agentID: &negative, wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := validateActor(tc.userID, tc.agentID)
			if tc.wantErr && !errors.Is(err, ErrInvalidActor) {
				t.Fatalf("validateActor error = %v, want ErrInvalidActor", err)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("validateActor returned unexpected error: %v", err)
			}
		})
	}
}
