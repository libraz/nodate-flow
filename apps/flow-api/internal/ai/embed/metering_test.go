package embed

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
)

type fakeStore struct {
	existing generated.GetTaskEmbeddingRow
	getErr   error
	upsert   generated.UpsertTaskEmbeddingParams
	upserts  int
}

func (s *fakeStore) GetTaskEmbedding(context.Context, generated.GetTaskEmbeddingParams) (generated.GetTaskEmbeddingRow, error) {
	return s.existing, s.getErr
}

func (s *fakeStore) UpsertTaskEmbedding(_ context.Context, arg generated.UpsertTaskEmbeddingParams) error {
	s.upsert = arg
	s.upserts++
	return nil
}

type fakeProvider struct {
	model string
	calls int
	err   error
}

func (p *fakeProvider) Model() string {
	if p.model == "" {
		return "text-embedding-3-small"
	}
	return p.model
}

func (p *fakeProvider) ProviderName() string { return "fake-embed" }

func (p *fakeProvider) Embed(context.Context, string) ([]float32, error) {
	p.calls++
	if p.err != nil {
		return nil, p.err
	}
	return []float32{1, 0, 0}, nil
}

type fakeGuard struct {
	calls int
	err   error
}

func (g *fakeGuard) Check(context.Context, uint32) error {
	g.calls++
	return g.err
}

func TestEmbedTask_MeteringSuccess(t *testing.T) {
	t.Parallel()
	store := &fakeStore{getErr: sql.ErrNoRows}
	provider := &fakeProvider{}
	guard := &fakeGuard{}
	var logged []InvocationRecord
	var metrics []struct {
		provider string
		model    string
		ws       string
		cost     int64
	}

	client := New(provider, store).WithMetering(
		guard,
		func(_ context.Context, rec InvocationRecord) { logged = append(logged, rec) },
		func(provider, model, workspaceID string, costCents int64) {
			metrics = append(metrics, struct {
				provider string
				model    string
				ws       string
				cost     int64
			}{provider: provider, model: model, ws: workspaceID, cost: costCents})
		},
		func(s string) string { return "redacted:" + s },
	)

	err := client.EmbedTask(context.Background(), 7, 42, "Title", "Description")
	if err != nil {
		t.Fatalf("EmbedTask returned error: %v", err)
	}
	if guard.calls != 1 {
		t.Fatalf("guard calls = %d, want 1", guard.calls)
	}
	if provider.calls != 1 {
		t.Fatalf("provider calls = %d, want 1", provider.calls)
	}
	if store.upserts != 1 {
		t.Fatalf("upserts = %d, want 1", store.upserts)
	}
	if len(logged) != 1 {
		t.Fatalf("logged records = %d, want 1", len(logged))
	}
	rec := logged[0]
	if rec.WorkspaceID != 7 || rec.Purpose != "embed_task" || rec.Model != "text-embedding-3-small" || rec.Status != "ok" {
		t.Fatalf("unexpected invocation record: %+v", rec)
	}
	if rec.PromptRedacted != "redacted:Title\n\nDescription" {
		t.Fatalf("prompt was not redacted/composed: %q", rec.PromptRedacted)
	}
	if rec.ResponseRedacted != "embedding vector omitted" {
		t.Fatalf("unexpected response marker: %q", rec.ResponseRedacted)
	}
	if rec.TokensInput == 0 {
		t.Fatal("tokens_input must be estimated for embedding cost accounting")
	}
	if len(metrics) != 1 || metrics[0].provider != "fake-embed" || metrics[0].model != "text-embedding-3-small" || metrics[0].ws != "7" {
		t.Fatalf("unexpected metrics: %+v", metrics)
	}
}

func TestEmbedTask_BudgetBlockSkipsProvider(t *testing.T) {
	t.Parallel()
	blocked := errors.New("budget blocked")
	store := &fakeStore{getErr: sql.ErrNoRows}
	provider := &fakeProvider{}
	guard := &fakeGuard{err: blocked}
	client := New(provider, store).WithMetering(guard, nil, nil, nil)

	err := client.EmbedTask(context.Background(), 7, 42, "Title", "Description")
	if !errors.Is(err, blocked) {
		t.Fatalf("EmbedTask error = %v, want %v", err, blocked)
	}
	if provider.calls != 0 {
		t.Fatalf("provider calls = %d, want 0", provider.calls)
	}
	if store.upserts != 0 {
		t.Fatalf("upserts = %d, want 0", store.upserts)
	}
}

func TestEmbedTask_SameHashSkipsMeteringAndProvider(t *testing.T) {
	t.Parallel()
	text := "Title\n\nDescription"
	store := &fakeStore{existing: generated.GetTaskEmbeddingRow{ContentHash: hashText(text)}}
	provider := &fakeProvider{}
	guard := &fakeGuard{}
	var logged []InvocationRecord
	client := New(provider, store).WithMetering(
		guard,
		func(_ context.Context, rec InvocationRecord) { logged = append(logged, rec) },
		nil,
		nil,
	)

	err := client.EmbedTask(context.Background(), 7, 42, "Title", "Description")
	if err != nil {
		t.Fatalf("EmbedTask returned error: %v", err)
	}
	if guard.calls != 0 {
		t.Fatalf("guard calls = %d, want 0", guard.calls)
	}
	if provider.calls != 0 {
		t.Fatalf("provider calls = %d, want 0", provider.calls)
	}
	if len(logged) != 0 {
		t.Fatalf("logged records = %d, want 0", len(logged))
	}
}
