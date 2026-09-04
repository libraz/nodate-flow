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
		func(provider, model, workspaceID string, costCents int64, _ error) {
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

// TestEmbedQuery_RecordsTheInvocation pins the accounting for the query
// side of similarity search. The vector is thrown away after the search,
// so the ai_invocations row is the only trace the call left, and without
// it the workspace is billed for spend nothing can attribute.
func TestEmbedQuery_RecordsTheInvocation(t *testing.T) {
	t.Parallel()

	upstream := errors.New("embedding endpoint refused the request")

	cases := []struct {
		name        string
		providerErr error
		wantStatus  string
	}{
		{name: "success", wantStatus: "ok"},
		{name: "failure", providerErr: upstream, wantStatus: "error"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			provider := &fakeProvider{err: tc.providerErr}
			guard := &fakeGuard{}
			var logged []InvocationRecord
			var hookErrs []error
			client := New(provider, &fakeStore{}).WithMetering(
				guard,
				func(_ context.Context, rec InvocationRecord) { logged = append(logged, rec) },
				func(_, _, _ string, _ int64, err error) { hookErrs = append(hookErrs, err) },
				func(s string) string { return "redacted:" + s },
			)

			vec, err := client.EmbedQuery(context.Background(), 7, "find me something similar")
			switch {
			case tc.providerErr == nil && err != nil:
				t.Fatalf("EmbedQuery returned error: %v", err)
			case tc.providerErr != nil && !errors.Is(err, upstream):
				t.Fatalf("EmbedQuery error = %v, want %v", err, upstream)
			}
			if tc.providerErr == nil && len(vec) == 0 {
				t.Fatal("EmbedQuery returned no vector on the success path")
			}
			if guard.calls != 1 {
				t.Fatalf("guard calls = %d, want 1", guard.calls)
			}
			if provider.calls != 1 {
				t.Fatalf("provider calls = %d, want 1", provider.calls)
			}
			if len(hookErrs) != 1 {
				t.Fatalf("metrics samples = %d, want 1", len(hookErrs))
			}
			if tc.providerErr == nil && hookErrs[0] != nil {
				t.Errorf("a successful call reported err = %v; the metric would label it an error", hookErrs[0])
			}
			if tc.providerErr != nil && !errors.Is(hookErrs[0], upstream) {
				t.Errorf("hook err = %v, want the provider error", hookErrs[0])
			}
			if len(logged) != 1 {
				t.Fatalf("logged records = %d, want 1", len(logged))
			}
			rec := logged[0]
			if rec.WorkspaceID != 7 || rec.Purpose != "embed_query" || rec.Status != tc.wantStatus {
				t.Fatalf("unexpected invocation record: %+v", rec)
			}
			if rec.PromptRedacted != "redacted:find me something similar" {
				t.Errorf("prompt was not redacted: %q", rec.PromptRedacted)
			}
		})
	}
}

// TestEmbedQuery_StoresNothing pins that a query embedding leaves no
// task_embeddings row. It belongs to no task, so a row for it would key
// off an id it does not have.
func TestEmbedQuery_StoresNothing(t *testing.T) {
	t.Parallel()
	store := &fakeStore{}
	client := New(&fakeProvider{}, store)

	if _, err := client.EmbedQuery(context.Background(), 7, "query text"); err != nil {
		t.Fatalf("EmbedQuery returned error: %v", err)
	}
	if store.upserts != 0 {
		t.Fatalf("upserts = %d, want 0", store.upserts)
	}
}

// TestEmbedQuery_BudgetBlockSkipsProvider is the budget hole itself: a
// workspace past its daily cap must be refused before the tokens are
// spent, not after.
func TestEmbedQuery_BudgetBlockSkipsProvider(t *testing.T) {
	t.Parallel()
	blocked := errors.New("budget blocked")
	provider := &fakeProvider{}
	guard := &fakeGuard{err: blocked}
	var logged []InvocationRecord
	client := New(provider, &fakeStore{}).WithMetering(
		guard,
		func(_ context.Context, rec InvocationRecord) { logged = append(logged, rec) },
		nil,
		nil,
	)

	_, err := client.EmbedQuery(context.Background(), 7, "query text")
	if !errors.Is(err, blocked) {
		t.Fatalf("EmbedQuery error = %v, want %v", err, blocked)
	}
	if provider.calls != 0 {
		t.Fatalf("provider calls = %d, want 0: the guard must refuse before the call is made", provider.calls)
	}
	if len(logged) != 0 {
		t.Fatalf("logged records = %d, want 0: no call was made to record", len(logged))
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
