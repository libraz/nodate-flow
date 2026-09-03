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

	"github.com/libraz/nodate-flow/apps/flow-api/internal/ai/embed"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/dbretry"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/eventbus"
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
	return func(ctx context.Context, workspaceInternalID uint32, eventType string, _ uint64) {
		if eventType != string(eventbus.TaskCreated) && eventType != string(eventbus.TaskUpdated) {
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

	// The lookup key is the embedder's own model, which is what wrote
	// every row: see [embed.Client.Model]. Thresholds come from
	// ai_settings, with the ADR 0003 defaults on miss.
	model := p.Embedder.Model()
	high := DuplicateThreshold
	low := RelatesThreshold
	if settings, err := p.Queries.GetAiSettings(ctx, workspaceID); err == nil {
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
	//
	// This runs off an event, not a request: there is no actor whose
	// visibility could scope the pool, and the titles it reads go into
	// relation_suggestions rows rather than onto any wire. The reader is
	// gated instead -- every statement that projects a suggestion's task
	// titles carries the Layer 4 rule against both ends -- so the pool
	// here is the workspace's, and a suggestion between two tasks stays
	// invisible to anyone who may not see them both.
	candidates, err := p.Queries.ListCandidateTaskEmbeddings(ctx, generated.ListCandidateTaskEmbeddingsParams{
		WorkspaceID:   workspaceID,
		Model:         model,
		TaskID:        taskID,
		IsElevated:    1,
		ActorUserID:   0,
		ActorUserID_2: 0,
		ActorUserID_3: 0,
		Limit:         MaxCandidates,
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

	if len(matches) == 0 {
		return
	}

	// The suggestion event names both tasks so a client can open them.
	// Only the public id can do that: the internal key identifies nothing
	// the client can look up, and every other event spells taskId as a
	// UUID string, so a number here would also split the field's type in
	// the generated SDK.
	var srcPub types.PublicID
	const srcPubQuery = `SELECT public_id FROM tasks WHERE id = ? AND workspace_id = ? LIMIT 1`
	if err := p.DB.QueryRowContext(ctx, srcPubQuery, taskID, workspaceID).Scan(&srcPub); err != nil {
		slog.Error("relations pipeline: resolve source task public id", "err", err)
		return
	}

	// Create suggestion rows.
	for _, m := range matches {
		pub := types.New()
		conf := fmt.Sprintf("%.4f", m.score)
		srcID := int64(taskID)
		// The suggestion row and the event announcing it go in together.
		// This pass runs detached from the request that triggered it (a
		// notify hook spawns it with WithoutCancel), so the task it
		// describes can be deleted while it works. Appending afterwards
		// on its own connection then failed the events.task_id foreign
		// key and left a suggestion no event ever announced. Inserting
		// the suggestion first inside the transaction takes a shared lock
		// on the parent task rows, so by the time the event insert runs
		// the task cannot have gone away: either both rows land or
		// neither does.
		if err := dbretry.InTx(ctx, p.DB, "relations.suggest", nil, func(ctx context.Context, tx *dbretry.Tx) error {
			if _, err := p.Queries.WithTx(tx.RawTx()).CreateRelationSuggestion(ctx, generated.CreateRelationSuggestionParams{
				PublicID:      pub,
				WorkspaceID:   workspaceID,
				SourceTaskID:  taskID,
				TargetTaskID:  m.taskID,
				SuggestedKind: m.kind,
				Confidence:    conf,
			}); err != nil {
				return err
			}
			return eventbus.Append(ctx, tx, eventbus.Event{
				Type:        eventbus.RelationSuggested,
				WorkspaceID: workspaceID,
				TaskID:      &srcID,
				Payload: map[string]any{
					"suggestionId": pub.String(),
					"sourceTaskId": srcPub.String(),
					"targetTaskId": m.publicID.String(),
					"kind":         string(m.kind),
					"confidence":   m.score,
				},
			})
		}); err != nil {
			// An already-suggested pair (the pair is uniquely keyed) and a
			// task deleted since the candidates were read are both normal
			// outcomes for a background pass over data it does not own.
			slog.DebugContext(ctx, "relations pipeline: suggestion skipped",
				"workspace_id", workspaceID,
				"source_task_id", taskID,
				"target_task_id", m.taskID,
				"err", err,
			)
			continue
		}
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
