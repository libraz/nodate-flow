// MCP tool over a user's recent visits: the entities they last opened in
// the workspace.

package mcp

import (
	"context"
	"encoding/json"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
)

func runListRecent(ctx context.Context, deps Deps, s *session, raw json.RawMessage) (any, error) {
	if _, err := requireWorkspaceMember(ctx, deps, s); err != nil {
		return nil, err
	}
	var in struct {
		Limit int32 `json:"limit"`
	}
	if err := parseArgs(raw, &in); err != nil {
		return nil, err
	}
	if in.Limit <= 0 || in.Limit > 20 {
		in.Limit = 20
	}
	rows, err := deps.Queries.ListRecentVisitsForUser(ctx, generated.ListRecentVisitsForUserParams{
		WorkspaceID: s.workspaceID,
		UserID:      s.userID,
		Limit:       in.Limit,
	})
	if err != nil {
		return nil, apierrors.Wrap(apierrors.McpToolExecutionFailed, err)
	}
	type visitOut struct {
		ID         string `json:"id"`
		EntityType string `json:"entityType"`
		EntityID   string `json:"entityId"`
		Title      string `json:"title,omitempty"`
		VisitedAt  int64  `json:"visitedAt"`
	}
	out := make([]visitOut, 0, len(rows))
	for _, r := range rows {
		v := visitOut{
			ID:         r.PublicID.String(),
			EntityType: string(r.EntityType),
			EntityID:   r.EntityPublicID.String(),
		}
		if r.EntityTitle.Valid {
			v.Title = r.EntityTitle.String
		}
		if r.UpdatedAt.Valid {
			v.VisitedAt = r.UpdatedAt.Time.Unix()
		} else {
			v.VisitedAt = r.CreatedAt.Unix()
		}
		out = append(out, v)
	}
	return map[string]any{"recentVisits": out}, nil
}
