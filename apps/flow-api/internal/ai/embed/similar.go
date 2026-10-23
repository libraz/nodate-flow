package embed

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strconv"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
)

// Similarity thresholds (ADR 0003 §4), used when ai_settings has no row
// for the workspace or a stored value does not parse.
const (
	DefaultDuplicateThreshold = 0.870
	DefaultRelatedThreshold   = 0.750
)

// DefaultCandidateLimit bounds the stored embeddings one search scores.
const DefaultCandidateLimit = 200

// ErrNoSourceVector reports that the source task has no usable embedding
// and none could be written. Interactive callers treat it as an empty
// result; the background pipeline logs it.
var ErrNoSourceVector = errors.New("embed: source task has no usable embedding")

// SimilarStore is the sqlc query subset [Client.RankSimilarTasks] reads.
type SimilarStore interface {
	GetAiSettings(ctx context.Context, workspaceID uint32) (generated.GetAiSettingsRow, error)
	GetTaskEmbedding(ctx context.Context, arg generated.GetTaskEmbeddingParams) (generated.GetTaskEmbeddingRow, error)
	ListCandidateTaskEmbeddings(ctx context.Context, arg generated.ListCandidateTaskEmbeddingsParams) ([]generated.ListCandidateTaskEmbeddingsRow, error)
}

// SimilarQuery names the source task and the pool it is ranked against.
type SimilarQuery struct {
	WorkspaceID uint32
	TaskID      uint32
	// SourceText returns the task's title and description. It is called
	// only when the task has no stored embedding yet, to write one.
	SourceText func(ctx context.Context) (title, description string, err error)
	// IsElevated and ActorUserID scope the candidate pool to the actor's
	// readable set (acl.ListVisibilityArgs); IsElevated 1 with actor 0
	// ranks the whole workspace.
	IsElevated  int64
	ActorUserID int64
	// ResultLimit caps the ranked result.
	ResultLimit int
}

// SimilarTask is one ranked candidate.
type SimilarTask struct {
	ID       uint32
	PublicID types.PublicID
	Title    string
	Score    float64
	// Duplicate is true at or above the duplicate threshold; below it the
	// candidate is merely related.
	Duplicate bool
}

// RankSimilarTasks is the one similarity search over stored task
// embeddings: thresholds from ai_settings, a write-time embedding when
// the source has none, Go-side cosine over the candidate pool (MySQL 9.6
// Community lacks VEC_DISTANCE_COSINE), score-descending and capped.
//
// A nil client has no embeddings and yields [ErrNoSourceVector].
func (c *Client) RankSimilarTasks(ctx context.Context, q SimilarStore, in SimilarQuery) ([]SimilarTask, error) {
	if c == nil {
		return nil, ErrNoSourceVector
	}
	model := c.Model()
	high, low := DefaultDuplicateThreshold, DefaultRelatedThreshold
	if s, err := q.GetAiSettings(ctx, in.WorkspaceID); err == nil {
		if v, perr := strconv.ParseFloat(s.DuplicateThresholdHigh, 64); perr == nil {
			high = v
		}
		if v, perr := strconv.ParseFloat(s.DuplicateThresholdLow, 64); perr == nil {
			low = v
		}
	}

	key := generated.GetTaskEmbeddingParams{TaskID: in.TaskID, Model: model}
	src, err := q.GetTaskEmbedding(ctx, key)
	if errors.Is(err, sql.ErrNoRows) && in.SourceText != nil {
		title, desc, terr := in.SourceText(ctx)
		if terr != nil {
			return nil, fmt.Errorf("%w: load source text: %w", ErrNoSourceVector, terr)
		}
		if eerr := c.EmbedTask(ctx, in.WorkspaceID, in.TaskID, title, desc); eerr != nil {
			return nil, fmt.Errorf("%w: embed source: %w", ErrNoSourceVector, eerr)
		}
		src, err = q.GetTaskEmbedding(ctx, key)
	}
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrNoSourceVector, err)
	}
	srcVec, err := Decode(VectorBytes(src.Vector))
	if err != nil || len(srcVec) == 0 {
		return nil, fmt.Errorf("%w: undecodable vector", ErrNoSourceVector)
	}

	rows, err := q.ListCandidateTaskEmbeddings(ctx, generated.ListCandidateTaskEmbeddingsParams{
		WorkspaceID:   in.WorkspaceID,
		Model:         model,
		TaskID:        in.TaskID,
		IsElevated:    in.IsElevated,
		ActorUserID:   in.ActorUserID,
		ActorUserID_2: in.ActorUserID,
		ActorUserID_3: in.ActorUserID,
		Limit:         DefaultCandidateLimit,
	})
	if err != nil {
		return nil, fmt.Errorf("embed: list candidate embeddings: %w", err)
	}
	ranked := make([]SimilarTask, 0, len(rows))
	for _, r := range rows {
		v, derr := Decode(VectorBytes(r.Vector))
		if derr != nil || len(v) != len(srcVec) {
			continue
		}
		score := float64(Cosine(srcVec, v))
		if score < low {
			continue
		}
		ranked = append(ranked, SimilarTask{
			ID:        r.ID,
			PublicID:  r.PublicID,
			Title:     r.Title,
			Score:     score,
			Duplicate: score >= high,
		})
	}
	sort.SliceStable(ranked, func(i, j int) bool { return ranked[i].Score > ranked[j].Score })
	if in.ResultLimit > 0 && len(ranked) > in.ResultLimit {
		ranked = ranked[:in.ResultLimit]
	}
	return ranked, nil
}

// VectorBytes coerces an interface{} VECTOR column to []byte. sqlc
// exposes VECTOR_TO_STRING reads as interface{}; the driver hands back
// []byte or string.
func VectorBytes(v any) []byte {
	switch x := v.(type) {
	case []byte:
		return x
	case string:
		return []byte(x)
	}
	return nil
}
