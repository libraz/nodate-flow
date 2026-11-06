// Package suggestionprivacy stores and authorizes the task references that
// informed actor-private AI suggestion batches.
package suggestionprivacy

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/dbretry"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
)

var (
	ErrInvalidBatch = errors.New("suggestion privacy: invalid batch provenance")
	ErrNotReadable  = errors.New("suggestion privacy: batch is not currently readable")
)

// ResourceTask is the currently supported provenance kind.
const ResourceTask = "task"

// Ref names a public resource captured in the provider input. Task is the
// only supported resource kind today; rejecting unknown kinds prevents a
// partial provenance record from being treated as complete.
type Ref struct {
	Kind     string
	PublicID types.PublicID
}

// Store writes immutable batch provenance and authorizes it against current
// workspace membership and task visibility.
type Store struct {
	DB *sql.DB
}

// Create persists the entire normalized task-reference set atomically. A
// valid empty set is distinguishable from a missing batch because its header
// is explicitly transitioned to provenance_valid after count verification.
func (s *Store) Create(ctx context.Context, workspaceID, actorUserID uint32, refs []Ref) (types.PublicID, error) {
	if s == nil || s.DB == nil || workspaceID == 0 || actorUserID == 0 || actorUserID > math.MaxInt32 || uint64(len(refs)) > math.MaxUint32 {
		return types.PublicID{}, ErrInvalidBatch
	}
	canonical, err := normalize(refs)
	if err != nil {
		return types.PublicID{}, err
	}
	if len(canonical) > len(refs) {
		return types.PublicID{}, ErrInvalidBatch
	}
	batchPublicID := types.New()
	err = dbretry.InTx(ctx, s.DB, "suggestionprivacy.Create", nil, func(ctx context.Context, tx *dbretry.Tx) error {
		q := generated.New(tx.RawTx())
		result, err := q.CreateAiSuggestionBatch(ctx, generated.CreateAiSuggestionBatchParams{
			PublicID:    batchPublicID,
			WorkspaceID: workspaceID,
			UserID:      sql.NullInt32{Int32: int32(actorUserID), Valid: true}, //#nosec G115 -- actorUserID is rejected above if greater than MaxInt32
			InputCount:  uint32(len(canonical)),                                //#nosec G115 -- canonical count is checked <= refs above, whose uint32 range is checked at entry
		})
		if err != nil {
			return fmt.Errorf("suggestion privacy: create batch: %w", err)
		}
		lastInsertID, err := result.LastInsertId()
		if err != nil || lastInsertID <= 0 || lastInsertID > int64(^uint32(0)) {
			return fmt.Errorf("%w: batch insert returned an invalid internal id", ErrInvalidBatch)
		}
		batchID := uint32(lastInsertID) //#nosec G115 -- checked above against uint32 max
		for _, ref := range canonical {
			if err := q.CreateAiSuggestionBatchInput(ctx, generated.CreateAiSuggestionBatchInputParams{
				BatchID:      batchID,
				TaskPublicID: ref.PublicID[:],
			}); err != nil {
				return fmt.Errorf("suggestion privacy: create batch input: %w", err)
			}
		}
		rows, err := q.MarkAiSuggestionBatchProvenanceValid(ctx, batchID)
		if err != nil {
			return fmt.Errorf("suggestion privacy: complete batch provenance: %w", err)
		}
		if rows != 1 {
			return fmt.Errorf("%w: complete batch provenance affected %d rows", ErrInvalidBatch, rows)
		}
		_, err = q.AuthorizeAiSuggestionBatch(ctx, generated.AuthorizeAiSuggestionBatchParams{
			WorkspaceID: workspaceID,
			UserID:      sql.NullInt32{Int32: int32(actorUserID), Valid: true}, //#nosec G115 -- actorUserID is rejected above if greater than MaxInt32
			PublicID:    batchPublicID,
		})
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotReadable
		}
		if err != nil {
			return fmt.Errorf("suggestion privacy: verify new batch: %w", err)
		}
		return nil
	})
	if err != nil {
		return types.PublicID{}, err
	}
	return batchPublicID, nil
}

// Authorize checks the batch through q, which must be bound to the same
// transaction as any command that consumes it. It never opens another
// connection, so concurrent access revocation cannot split authorization
// from the command's transaction.
func (s *Store) Authorize(ctx context.Context, q *generated.Queries, workspaceID, actorUserID uint32, batchPublicID types.PublicID) error {
	if s == nil || q == nil || workspaceID == 0 || actorUserID == 0 || actorUserID > math.MaxInt32 || batchPublicID == (types.PublicID{}) {
		return ErrNotReadable
	}
	_, err := q.AuthorizeAiSuggestionBatch(ctx, generated.AuthorizeAiSuggestionBatchParams{
		WorkspaceID: workspaceID,
		UserID:      sql.NullInt32{Int32: int32(actorUserID), Valid: true}, //#nosec G115 -- actorUserID is rejected above if greater than MaxInt32
		PublicID:    batchPublicID,
	})
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotReadable
	}
	if err != nil {
		return fmt.Errorf("suggestion privacy: authorize batch: %w", err)
	}
	return nil
}

func normalize(refs []Ref) ([]Ref, error) {
	seen := make(map[types.PublicID]struct{}, len(refs))
	out := make([]Ref, 0, len(refs))
	for _, ref := range refs {
		if ref.Kind != ResourceTask || ref.PublicID == (types.PublicID{}) {
			return nil, ErrInvalidBatch
		}
		if _, exists := seen[ref.PublicID]; exists {
			continue
		}
		seen[ref.PublicID] = struct{}{}
		out = append(out, ref)
	}
	return out, nil
}

// TimelinePredicate returns the actor-private proposal event predicate for
// timeline queries whose outer event alias is `ev`. Apply it to the shared
// WHERE used by both the page and total-count statements, before pagination.
func TimelinePredicate(actorUserID uint32) (string, []any) {
	return `EXISTS (
  SELECT 1
  FROM v_ai_suggestion_timeline_events suggestion_event
  INNER JOIN v_ai_suggestion_readable_batches suggestion_batch
    ON suggestion_batch.id = suggestion_event.batch_id
   AND suggestion_batch.workspace_id = suggestion_event.workspace_id
   AND suggestion_batch.user_id = suggestion_event.owner_user_id
  WHERE suggestion_event.event_id = ev.id
    AND suggestion_event.workspace_id = ev.workspace_id
    AND suggestion_event.owner_user_id = ?
    AND suggestion_event.actor_user_id = ?
)`, []any{actorUserID, actorUserID}
}
