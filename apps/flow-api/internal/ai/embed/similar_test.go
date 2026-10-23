package embed

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
)

// similarStore serves one source vector, a fixed candidate pool and an
// optional ai_settings row.
type similarStore struct {
	settings   *generated.GetAiSettingsRow
	source     string
	candidates []generated.ListCandidateTaskEmbeddingsRow
	listArgs   generated.ListCandidateTaskEmbeddingsParams
}

func (s *similarStore) GetAiSettings(context.Context, uint32) (generated.GetAiSettingsRow, error) {
	if s.settings == nil {
		return generated.GetAiSettingsRow{}, sql.ErrNoRows
	}
	return *s.settings, nil
}

func (s *similarStore) GetTaskEmbedding(context.Context, generated.GetTaskEmbeddingParams) (generated.GetTaskEmbeddingRow, error) {
	if s.source == "" {
		return generated.GetTaskEmbeddingRow{}, sql.ErrNoRows
	}
	return generated.GetTaskEmbeddingRow{Vector: []byte(s.source)}, nil
}

func (s *similarStore) ListCandidateTaskEmbeddings(_ context.Context, arg generated.ListCandidateTaskEmbeddingsParams) ([]generated.ListCandidateTaskEmbeddingsRow, error) {
	s.listArgs = arg
	return s.candidates, nil
}

func candidate(id uint32, vec string) generated.ListCandidateTaskEmbeddingsRow {
	return generated.ListCandidateTaskEmbeddingsRow{ID: id, PublicID: types.New(), Title: "t", Vector: vec}
}

func newSimilarStore() *similarStore {
	return &similarStore{
		source: "[1,0]",
		candidates: []generated.ListCandidateTaskEmbeddingsRow{
			candidate(1, "[0.8,0.6]"),         // 0.80: related
			candidate(2, "[0.5,0.8660254]"),   // 0.50: below the related floor
			candidate(3, "[0.9,0.43588989]"),  // 0.90: duplicate
			candidate(4, "[1,0,0]"),           // other dimension: skipped
			candidate(5, "[0.88,0.47497368]"), // 0.88: duplicate
			candidate(6, "[0.76,0.64992307]"), // 0.76: related
		},
	}
}

// TestRankSimilarTasksAppliesDefaultsOrderAndCap pins the shared ranking
// every similarity surface returns: ADR defaults when ai_settings has no
// row, score-descending order, the duplicate cut, and the result cap.
func TestRankSimilarTasksAppliesDefaultsOrderAndCap(t *testing.T) {
	t.Parallel()

	store := newSimilarStore()
	c := &Client{Provider: &fakeProvider{}}
	got, err := c.RankSimilarTasks(context.Background(), store, SimilarQuery{
		WorkspaceID: 7, TaskID: 9, IsElevated: 0, ActorUserID: 11, ResultLimit: 3,
	})
	if err != nil {
		t.Fatalf("RankSimilarTasks: %v", err)
	}
	wantIDs := []uint32{3, 5, 1}
	wantDup := []bool{true, true, false}
	if len(got) != len(wantIDs) {
		t.Fatalf("got %d candidates, want %d: %+v", len(got), len(wantIDs), got)
	}
	for i := range got {
		if got[i].ID != wantIDs[i] || got[i].Duplicate != wantDup[i] {
			t.Errorf("candidate %d = id %d duplicate %v, want id %d duplicate %v", i, got[i].ID, got[i].Duplicate, wantIDs[i], wantDup[i])
		}
	}
	if store.listArgs.Limit != DefaultCandidateLimit || store.listArgs.ActorUserID_3 != 11 || store.listArgs.TaskID != 9 {
		t.Errorf("candidate query args = %+v, want limit %d, actor 11 on every slot, task 9", store.listArgs, DefaultCandidateLimit)
	}
}

// TestRankSimilarTasksReadsWorkspaceThresholds pins that a stored
// ai_settings row moves both cuts.
func TestRankSimilarTasksReadsWorkspaceThresholds(t *testing.T) {
	t.Parallel()

	store := newSimilarStore()
	store.settings = &generated.GetAiSettingsRow{DuplicateThresholdHigh: "0.95", DuplicateThresholdLow: "0.85"}
	c := &Client{Provider: &fakeProvider{}}
	got, err := c.RankSimilarTasks(context.Background(), store, SimilarQuery{WorkspaceID: 7, TaskID: 9, IsElevated: 1})
	if err != nil {
		t.Fatalf("RankSimilarTasks: %v", err)
	}
	if len(got) != 2 || got[0].ID != 3 || got[1].ID != 5 || got[0].Duplicate || got[1].Duplicate {
		t.Fatalf("got %+v, want ids 3 and 5, both related", got)
	}
}

// TestRankSimilarTasksWithoutSourceVector pins the soft miss interactive
// callers turn into an empty list rather than an error.
func TestRankSimilarTasksWithoutSourceVector(t *testing.T) {
	t.Parallel()

	store := newSimilarStore()
	store.source = ""
	c := &Client{Provider: &fakeProvider{}}
	if _, err := c.RankSimilarTasks(context.Background(), store, SimilarQuery{WorkspaceID: 7, TaskID: 9}); !errors.Is(err, ErrNoSourceVector) {
		t.Fatalf("err = %v, want ErrNoSourceVector", err)
	}
	var nilClient *Client
	if _, err := nilClient.RankSimilarTasks(context.Background(), store, SimilarQuery{WorkspaceID: 7, TaskID: 9}); !errors.Is(err, ErrNoSourceVector) {
		t.Fatalf("nil client err = %v, want ErrNoSourceVector", err)
	}
}
