package tasks

import (
	"context"
	"errors"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/acl"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/ai/embed"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/middleware"
)

// duplicateResultLimit caps the candidates one listing returns.
const duplicateResultLimit = 20

// ListDuplicatesInput is the Huma input for GET /tasks/{id}/duplicates.
type ListDuplicatesInput struct {
	ID string `path:"id"`
}

// DuplicateCandidate is one row of the propose_duplicates response.
type DuplicateCandidate struct {
	TaskID         string  `json:"taskId"`
	Title          string  `json:"title"`
	Score          float64 `json:"score"`
	Classification string  `json:"classification"`
}

// ListDuplicatesOutput wraps the candidate list.
type ListDuplicatesOutput struct {
	Body struct {
		Source     string               `json:"source"`
		Model      string               `json:"model"`
		Candidates []DuplicateCandidate `json:"candidates"`
	}
}

// ListDuplicates handles GET /tasks/{id}/duplicates. It finds
// similar-text tasks in the same workspace by cosine similarity on the
// stored L2-normalized embeddings (ADR 0003). Scoring is done in Go.
func ListDuplicates(deps Deps) func(context.Context, *ListDuplicatesInput) (*ListDuplicatesOutput, error) {
	return func(ctx context.Context, _ *ListDuplicatesInput) (*ListDuplicatesOutput, error) {
		ws, ok := middleware.WorkspaceFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsTaskNotFound)
		}
		task, ok := middleware.TaskFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsTaskNotFound)
		}

		// Each candidate's title is returned to the caller, so the pool
		// is the actor's readable set rather than the workspace's.
		actorID, _ := middleware.ActorFromContext(ctx)
		vis := acl.ListVisibilityArgs(actorID, acl.WorkspaceRole(ws.Role))
		model := deps.Embedder.Model()
		ranked, err := deps.Embedder.RankSimilarTasks(ctx, deps.Queries, embed.SimilarQuery{
			WorkspaceID: ws.ID,
			TaskID:      task.ID,
			SourceText: func(ctx context.Context) (string, string, error) {
				row, err := deps.Queries.FindTaskByPublicId(ctx, generated.FindTaskByPublicIdParams{
					WorkspaceID: ws.ID,
					PublicID:    types.FromUUID(task.PublicID),
				})
				if err != nil {
					return "", "", err
				}
				return row.Title, nullStr(row.Description), nil
			},
			IsElevated:  vis.IsElevated,
			ActorUserID: vis.ActorUserID,
			ResultLimit: duplicateResultLimit,
		})
		if errors.Is(err, embed.ErrNoSourceVector) {
			return emptyDuplicates(model), nil
		}
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		out := emptyDuplicates(model)
		out.Body.Source = task.PublicID.String()
		for _, c := range ranked {
			classification := "related"
			if c.Duplicate {
				classification = "duplicate"
			}
			out.Body.Candidates = append(out.Body.Candidates, DuplicateCandidate{
				TaskID:         c.PublicID.String(),
				Title:          c.Title,
				Score:          c.Score,
				Classification: classification,
			})
		}
		return out, nil
	}
}

func emptyDuplicates(model string) *ListDuplicatesOutput {
	out := &ListDuplicatesOutput{}
	out.Body.Model = model
	out.Body.Candidates = []DuplicateCandidate{}
	return out
}
