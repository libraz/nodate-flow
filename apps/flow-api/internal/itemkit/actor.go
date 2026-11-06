package itemkit

import (
	"context"
	"fmt"

	"github.com/libraz/nodate-flow/packages/go-shared/eventbus"
)

type eventActor struct {
	userID  *uint32
	agentID *uint32
}

func humanEventActor(userID uint32) eventActor {
	if userID == 0 {
		return eventActor{}
	}
	return eventActor{userID: &userID}
}

func agentEventActor(legacyUserID, agentID uint32) (eventActor, error) {
	if legacyUserID != 0 && agentID != 0 {
		return eventActor{}, fmt.Errorf("itemkit: actor cannot be both a user and an agent")
	}
	if legacyUserID != 0 || agentID == 0 {
		return eventActor{}, fmt.Errorf("itemkit: agent writes require an agent id and no user actor")
	}
	return eventActor{agentID: &agentID}, nil
}

func (a eventActor) append(ctx context.Context, tx TX, kind eventbus.Kind, workspaceID uint32, taskID *uint32, payload map[string]any) error {
	return appendItemEventsWithActor(ctx, tx, kind, workspaceID, a.userID, a.agentID, taskID, payload)
}
