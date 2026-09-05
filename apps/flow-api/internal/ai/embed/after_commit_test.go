package embed

import (
	"context"
	"database/sql"
	"errors"
	"testing"
)

// TestAfterCommit_NilClientIsSilent pins the contract every write path
// depends on. A deployment with no embedding provider leaves the client
// nil, and a task write there reaches this call the same way any other
// does; if it panicked, the absence of an optional feature would take
// down task creation.
func TestRefreshTaskAfterCommit_NilClientIsSilent(t *testing.T) {
	t.Parallel()
	RefreshTaskAfterCommit(context.Background(), nil, 7, 42, "Title", "Description")
	RefreshTaskAfterCommit(context.Background(), &Client{}, 7, 42, "Title", "Description")
}

// TestAfterCommit_UpsertsThroughTheClient checks that the entry point is
// wired to the same write EmbedTask performs, rather than being a shape
// that swallows the call along with the error.
func TestRefreshTaskAfterCommit_UpsertsThroughTheClient(t *testing.T) {
	t.Parallel()
	store := &fakeStore{getErr: sql.ErrNoRows}
	client := New(&fakeProvider{}, store)

	RefreshTaskAfterCommit(context.Background(), client, 7, 42, "Title", "Description")

	if store.upserts != 1 {
		t.Fatalf("upserts = %d, want 1", store.upserts)
	}
	if store.upsert.TaskID != 42 || store.upsert.WorkspaceID != 7 {
		t.Fatalf("upsert keyed on task %d in workspace %d, want 42 in 7",
			store.upsert.TaskID, store.upsert.WorkspaceID)
	}
}

// TestAfterCommit_FailureDoesNotPropagate holds the reason this entry
// point returns nothing. The write it follows has already committed, so
// a failed refresh has to leave the caller with no outcome to act on.
func TestRefreshTaskAfterCommit_FailureDoesNotPropagate(t *testing.T) {
	t.Parallel()
	store := &fakeStore{getErr: sql.ErrNoRows}
	provider := &fakeProvider{err: errors.New("provider unreachable")}
	client := New(provider, store)

	RefreshTaskAfterCommit(context.Background(), client, 7, 42, "Title", "Description")

	if provider.calls != 1 {
		t.Fatalf("provider calls = %d, want 1", provider.calls)
	}
	if store.upserts != 0 {
		t.Fatalf("upserts = %d, want 0: a failed provider call has nothing to store", store.upserts)
	}
}
