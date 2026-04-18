package tasks

import (
	"context"
	"database/sql"
	"errors"
	"sort"
	"strconv"

	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/ai/embed"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/types"
	apierrors "github.com/nodate-flow/nodate-flow/apps/flow-api/internal/errors"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/http/middleware"
)

// Default thresholds mirror ADR 0003 §4. Used when ai_settings has no
// row for the workspace yet.
const (
	defaultEmbedModel        = "mock-768"
	defaultDuplicateHigh     = 0.870
	defaultDuplicateLow      = 0.750
	duplicateCandidateLimit  = 200
	duplicateResultLimit     = 20
)

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

		model, high, low := resolveThresholds(ctx, deps.Queries, ws.ID)

		// Fetch the source embedding; if missing, try a write-time
		// upsert using the current task text so the first call after a
		// cold start is still useful.
		src, err := deps.Queries.GetTaskEmbedding(ctx, generated.GetTaskEmbeddingParams{
			TaskID: task.ID,
			Model:  model,
		})
		if errors.Is(err, sql.ErrNoRows) && deps.Embedder != nil {
			row, ferr := deps.Queries.FindTaskByPublicId(ctx, generated.FindTaskByPublicIdParams{
				WorkspaceID: ws.ID,
				PublicID:    types.FromUUID(task.PublicID),
			})
			if ferr != nil {
				return emptyDuplicates(model), nil
			}
			if eerr := deps.Embedder.EmbedTask(ctx, task.ID, row.Title, nullStr(row.Description)); eerr != nil {
				return emptyDuplicates(model), nil
			}
			src, err = deps.Queries.GetTaskEmbedding(ctx, generated.GetTaskEmbeddingParams{
				TaskID: task.ID,
				Model:  model,
			})
		}
		if err != nil {
			return emptyDuplicates(model), nil
		}

		srcVec, err := embed.Decode(vectorBytes(src.Vector))
		if err != nil || len(srcVec) == 0 {
			return emptyDuplicates(model), nil
		}

		rows, err := deps.Queries.ListCandidateTaskEmbeddings(ctx, generated.ListCandidateTaskEmbeddingsParams{
			WorkspaceID: ws.ID,
			Model:       model,
			TaskID:      task.ID,
			Limit:       duplicateCandidateLimit,
		})
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		out := emptyDuplicates(model)
		out.Body.Source = task.PublicID.String()
		for _, r := range rows {
			v, derr := embed.Decode(vectorBytes(r.Vector))
			if derr != nil || len(v) != len(srcVec) {
				continue
			}
			score := float64(embed.Cosine(srcVec, v))
			if score < low {
				continue
			}
			classification := "related"
			if score >= high {
				classification = "duplicate"
			}
			out.Body.Candidates = append(out.Body.Candidates, DuplicateCandidate{
				TaskID:         r.PublicID.String(),
				Title:          r.Title,
				Score:          score,
				Classification: classification,
			})
		}
		sort.SliceStable(out.Body.Candidates, func(i, j int) bool {
			return out.Body.Candidates[i].Score > out.Body.Candidates[j].Score
		})
		if len(out.Body.Candidates) > duplicateResultLimit {
			out.Body.Candidates = out.Body.Candidates[:duplicateResultLimit]
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

func resolveThresholds(ctx context.Context, q *generated.Queries, wsID uint32) (string, float64, float64) {
	s, err := q.GetAiSettings(ctx, wsID)
	if err != nil {
		return defaultEmbedModel, defaultDuplicateHigh, defaultDuplicateLow
	}
	high, lerr := strconv.ParseFloat(s.DuplicateThresholdHigh, 64)
	if lerr != nil {
		high = defaultDuplicateHigh
	}
	low, lerr := strconv.ParseFloat(s.DuplicateThresholdLow, 64)
	if lerr != nil {
		low = defaultDuplicateLow
	}
	model := s.EmbedModel
	if model == "" {
		model = defaultEmbedModel
	}
	return model, high, low
}

// vectorBytes coerces an interface{} VECTOR column to []byte. sqlc
// exposes STRING_TO_VECTOR-wrapped VECTOR reads as interface{}; the
// underlying value is []byte (MySQL binary protocol) or string.
func vectorBytes(v any) []byte {
	switch x := v.(type) {
	case []byte:
		return x
	case string:
		return []byte(x)
	}
	return nil
}

