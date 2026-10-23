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
	"sync"
	"time"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/ai/embed"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/bgloop"
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
	// compare against: the most recent tasks by id, so a duplicate of an
	// older task outside that window is not detected.
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
	// Runner is where Hook submits its jobs, normally the runner the
	// other event hooks share. Nil gives the pipeline a private one.
	Runner *bgloop.HookRunner

	runnerOnce sync.Once
	// process is the per-task step. Tests override it to observe which
	// task an event resolved to; production leaves it nil and the event
	// routes to [Pipeline.processTask].
	process func(ctx context.Context, workspaceID, taskID uint32)
}

// defaultTimeout is each job's deadline; a job may call the external
// embedding API and decode a few hundred vectors. defaultConcurrency
// bounds the private runner used when no shared one is installed.
const (
	defaultConcurrency = 4
	defaultTimeout     = 60 * time.Second
)

// Hook returns an eventbus.NotifyHook that submits processing of the
// task the event names to the hook runner when a task is created or
// updated.
func (p *Pipeline) Hook() eventbus.NotifyHook {
	return func(ctx context.Context, workspaceInternalID uint32, eventType string, eventInternalID uint64) {
		if eventType != string(eventbus.TaskCreated) && eventType != string(eventbus.TaskUpdated) {
			return
		}
		p.hooks().Go(ctx, defaultTimeout, func(runCtx context.Context) {
			p.processEvent(runCtx, workspaceInternalID, eventInternalID)
		})
	}
}

// Shutdown stops accepting jobs and waits for the accepted ones, or until
// ctx is done.
func (p *Pipeline) Shutdown(ctx context.Context) error {
	return p.hooks().Shutdown(ctx)
}

// hooks returns the installed runner, or a private one when none was.
func (p *Pipeline) hooks() *bgloop.HookRunner {
	p.runnerOnce.Do(func() {
		if p.Runner == nil {
			p.Runner = bgloop.NewHookRunner("relations.pipeline", defaultConcurrency, defaultTimeout)
		}
	})
	return p.Runner
}

// processEvent processes the task named by the event the hook was given.
// It reads that event row by id rather than the latest event of its type:
// in a burst, every hook reading "the latest" sees the same row, so one
// task is processed many times and the others never are.
func (p *Pipeline) processEvent(ctx context.Context, workspaceID uint32, eventInternalID uint64) {
	const q = `SELECT task_id FROM events WHERE id = ? AND workspace_id = ? LIMIT 1`
	var taskID sql.NullInt32
	if err := p.DB.QueryRowContext(ctx, q, eventInternalID, workspaceID).Scan(&taskID); err != nil {
		if !stderrors.Is(err, sql.ErrNoRows) {
			slog.ErrorContext(ctx, "relations pipeline: fetch event", "event_id", eventInternalID, "err", err)
		}
		return
	}
	if !taskID.Valid {
		return
	}
	process := p.process
	if process == nil {
		process = p.processTask
	}
	process(ctx, workspaceID, uint32(taskID.Int32)) //#nosec G115 -- task_id is tasks.id (BIGINT UNSIGNED), fits uint32 within realistic deployments
}

// processTask embeds the given task (if needed), finds similar tasks
// via cosine similarity, and creates relation_suggestions for
// candidates above the threshold.
//
// task-visibility: not-applicable — this runs off an event rather than
// for a reader, so there is no actor to scope the read by, and a
// predicate written against a zero actor would leave every private and
// project-scoped task permanently unembedded while reporting nothing.
// The read is bounded by workspace_id instead, and the title and
// description it takes reach only the embedding provider and the vector
// derived from them: [embed.Client.EmbedTask] records the invocation
// under [embed.TaskTextOmitted] rather than under the task's own words,
// because ai_invocations is readable by every member of the workspace.
// What the reader eventually sees is a relation_suggestions row, and
// every statement that projects a suggestion's task titles carries the
// Layer 4 rule against both ends
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

	srcPub, err := p.sourceTaskPublicID(ctx, workspaceID, taskID)
	if err != nil {
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
		// notify hook submits it to the hook runner), so the task it
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

// sourceTaskPublicID resolves the task's external identifier for the
// suggestion event.
//
// The suggestion event names both tasks so a client can open them. Only
// the public id can do that: the internal key identifies nothing the
// client can look up, and every other event spells taskId as a UUID
// string, so a number here would also split the field's type in the
// generated SDK.
//
// It is its own function rather than a few lines inside [processTask]
// because a public id says nothing about what a task is: keeping it out
// of the function that reads title and description keeps the reason that
// function states about task content from being read as a claim about
// this lookup too.
func (p *Pipeline) sourceTaskPublicID(ctx context.Context, workspaceID, taskID uint32) (types.PublicID, error) {
	var pub types.PublicID
	const q = `SELECT public_id FROM tasks WHERE id = ? AND workspace_id = ? LIMIT 1`
	if err := p.DB.QueryRowContext(ctx, q, taskID, workspaceID).Scan(&pub); err != nil {
		return types.PublicID{}, err
	}
	return pub, nil
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
