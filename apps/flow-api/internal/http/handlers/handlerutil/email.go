package handlerutil

import (
	"context"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/acl"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
)

// EmailVisibility decides which members' addresses a caller may read: their
// own, or everyone's when the caller owns the workspace. Admins and below
// know other members by name and avatar.
type EmailVisibility struct {
	all  bool
	self types.PublicID
}

// NewEmailVisibility resolves the caller's public id once for a response.
func NewEmailVisibility(ctx context.Context, q *generated.Queries, role acl.WorkspaceRole, actorID uint32) (EmailVisibility, error) {
	if role == acl.WorkspaceRoleOwner {
		return EmailVisibility{all: true}, nil
	}
	self, err := q.FindUserPublicIdById(ctx, actorID)
	if err != nil {
		return EmailVisibility{}, err
	}
	return EmailVisibility{self: self}, nil
}

// Of returns the address the caller may read for a member, or "".
func (v EmailVisibility) Of(member types.PublicID, email string) string {
	if v.all || member == v.self {
		return email
	}
	return ""
}
