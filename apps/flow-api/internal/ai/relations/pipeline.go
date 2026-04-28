// Package relations implements the background AI pipeline that
// auto-detects candidate task relationships by embedding similarity.
// When a task is created or updated, the pipeline embeds it (if
// needed), finds similar tasks via cosine similarity, and creates
// relation_suggestions rows for candidates above threshold.
package relations

import (
	"context"
	"database/sql"
	stderrors "errors"
	"fmt"
	"log/slog"
	"sort"
	"strconv"

	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/ai/embed"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/types"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/eventbus"
)

// Thresholds for classification (ADR 0003 defaults).
const (
	// DuplicateThreshold is the minimum cosine similarity to suggest
	// "duplicates".
	DuplicateThreshold = 0.87
	// RelatesThreshold is the minimum cosine similarity to suggest
	// "relates". Scores below this are skipped.
	RelatesThreshold = 0.75
	// MaxCandidates is the maximum number of candidate embeddings to
	// compare against.
	MaxCandidates = 200
	// MaxSuggestions is the maximum number of suggestions created per
	// pipeline run for a single task.
	MaxSuggestions = 10
)

// Pipeline is the relation auto-detect AI pipeline. It listens for
// task.created and task.updated events and creates relation_suggestions
// rows for candidates above the similarity threshold.
type Pipeline struct {
	DB       *sql.DB
	Queries  *generated.Queries
	Embedder *embed.Client
}

// Hook returns an eventbus.NotifyHook that triggers processTask in a
// background goroutine when a task is created or updated.
func (p *Pipeline) Hook() eventbus.NotifyHook {
	return func(ctx context.Context, workspaceInternalID uint32, eventType string, _ uint32) {
		if eventType != eventbus.TaskCreated && eventType != eventbus.TaskUpdated {
			return
		}
		// The NotifyHook fires after each Append; the event row was just
		// written. We find the most recent task event for this workspace
		// to extract the task_id. Because the hook must be non-blocking,
		// we spawn a goroutine. Use WithoutCancel so the request's tracing
		// metadata is preserved while the goroutine outlives the response.
		go p.processLatestTaskEvent(context.WithoutCancel(ctx), workspaceInternalID, eventType)
	}
}

// processLatestTaskEvent reads the most recent task event for the
// workspace and dispatches processTask if it carries a task_id.
func (p *Pipeline) processLatestTaskEvent(ctx context.Context, workspaceID uint32, eventType string) {
	defer func() {
		if r := recover(); r != nil {
			slog.Error("relations pipeline panic", "workspace", workspaceID, "err", r)
		}
	}()

	// Find the last event of this type for the workspace.
	const q = `SELECT task_id FROM events
WHERE workspace_id = ? AND type = ?
ORDER BY id DESC LIMIT 1`
	var taskID sql.NullInt32
	if err := p.DB.QueryRowContext(ctx, q, workspaceID, eventType).Scan(&taskID); err != nil {
		if !stderrors.Is(err, sql.ErrNoRows) {
			slog.Error("relations pipeline: fetch event", "err", err)
		}
		return
	}
	if !taskID.Valid {
		return
	}
	p.processTask(ctx, workspaceID, uint32(taskID.Int32)) //#nosec G115 -- task_id is tasks.id (BIGINT UNSIGNED), fits uint32 within realistic deployments
}

// processTask embeds the given task (if needed), finds similar tasks
// via cosine similarity, and creates relation_suggestions for
// candidates above the threshold.
func (p *Pipeline) processTask(ctx context.Context, workspaceID uint32, taskID uint32) {
	if p.Embedder == nil {
		return
	}

	// Resolve embedding model from ai_settings (with ADR 0003 defaults).
	model := p.Embedder.Provider.Model()
	high := DuplicateThreshold
	low := RelatesThreshold
	if settings, err := p.Queries.GetAiSettings(ctx, workspaceID); err == nil {
		if settings.EmbedModel != "" {
			model = settings.EmbedModel
		}
		if v, perr := strconv.ParseFloat(settings.DuplicateThresholdHigh, 64); perr == nil {
			high = v
		}
		if v, perr := strconv.ParseFloat(settings.DuplicateThresholdLow, 64); perr == nil {
			low = v
		}
	}

	// Ensure the task has an embedding.
	src, err := p.Queries.GetTaskEmbedding(ctx, generated.GetTaskEmbeddingParams{
		TaskID: taskID,
		Model:  model,
	})
	if stderrors.Is(err, sql.ErrNoRows) {
		// Embed the task.
		const taskQuery = `SELECT title, description FROM tasks WHERE id = ? AND workspace_id = ? LIMIT 1`
		var title string
		var desc sql.NullString
		if err := p.DB.QueryRowContext(ctx, taskQuery, taskID, workspaceID).Scan(&title, &desc); err != nil {
			slog.Error("relations pipeline: fetch task for embedding", "taskId", taskID, "err", err)
			return
		}
		descStr := ""
		if desc.Valid {
			descStr = desc.String
		}
		if eerr := p.Embedder.EmbedTask(ctx, workspaceID, taskID, title, descStr); eerr != nil {
			slog.Error("relations pipeline: embed task", "taskId", taskID, "err", eerr)
			return
		}
		src, err = p.Queries.GetTaskEmbedding(ctx, generated.GetTaskEmbeddingParams{
			TaskID: taskID,
			Model:  model,
		})
	}
	if err != nil {
		slog.Error("relations pipeline: get embedding after upsert", "taskId", taskID, "err", err)
		return
	}

	srcVec, err := embed.Decode(toBytes(src.Vector))
	if err != nil || len(srcVec) == 0 {
		return
	}

	// Fetch candidate embeddings.
	candidates, err := p.Queries.ListCandidateTaskEmbeddings(ctx, generated.ListCandidateTaskEmbeddingsParams{
		WorkspaceID: workspaceID,
		Model:       model,
		TaskID:      taskID,
		Limit:       MaxCandidates,
	})
	if err != nil {
		slog.Error("relations pipeline: list candidates", "err", err)
		return
	}

	// Score and filter.
	type scored struct {
		taskID   uint32
		publicID types.PublicID
		score    float64
		kind     generated.RelationSuggestionsSuggestedKind
	}
	var matches []scored
	for _, c := range candidates {
		v, derr := embed.Decode(toBytes(c.Vector))
		if derr != nil || len(v) != len(srcVec) {
			continue
		}
		score := float64(embed.Cosine(srcVec, v))
		if score < low {
			continue
		}
		kind := generated.RelationSuggestionsSuggestedKindRelates
		if score >= high {
			kind = generated.RelationSuggestionsSuggestedKindDuplicates
		}
		matches = append(matches, scored{
			taskID:   c.ID,
			publicID: c.PublicID,
			score:    score,
			kind:     kind,
		})
	}

	// Sort by score descending and cap.
	sort.SliceStable(matches, func(i, j int) bool {
		return matches[i].score > matches[j].score
	})
	if len(matches) > MaxSuggestions {
		matches = matches[:MaxSuggestions]
	}

	// Create suggestion rows.
	for _, m := range matches {
		pub := types.New()
		conf := fmt.Sprintf("%.4f", m.score)
		if _, err := p.Queries.CreateRelationSuggestion(ctx, generated.CreateRelationSuggestionParams{
			PublicID:      pub,
			WorkspaceID:   workspaceID,
			SourceTaskID:  taskID,
			TargetTaskID:  m.taskID,
			SuggestedKind: m.kind,
			Confidence:    conf,
		}); err != nil {
			// Skip duplicates silently.
			slog.Debug("relations pipeline: create suggestion skipped", "err", err)
			continue
		}

		// Emit event.
		srcID := int64(taskID)
		_ = eventbus.Append(ctx, p.DB, eventbus.Event{
			Type:        eventbus.RelationSuggested,
			WorkspaceID: workspaceID,
			TaskID:      &srcID,
			Payload: map[string]any{
				"suggestionId": pub.String(),
				"sourceTaskId": taskID,
				"targetTaskId": m.taskID,
				"kind":         string(m.kind),
				"confidence":   m.score,
			},
		})
	}
}

// toBytes coerces an interface{} column (returned by sqlc for VECTOR
// columns read back through STRING_TO_VECTOR) into a []byte, or nil
// when the underlying value is neither []byte nor string.
func toBytes(v any) []byte {
	switch x := v.(type) {
	case []byte:
		return x
	case string:
		return []byte(x)
	}
	return nil
}
