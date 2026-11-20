package engine

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"testing"
	"time"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
)

// errStoreUnreachable is the failure the fake connector injects. Using a
// sentinel lets the assertions match on identity rather than on a driver
// message that database/sql is free to wrap.
var errStoreUnreachable = errors.New("engine test: store unreachable")

// failingConnector hands database/sql a connector that never yields a
// connection, so every query on the resulting *sql.DB fails with
// [errStoreUnreachable]. It stands in for the transient database outage
// the store used to absorb: no server, no fixtures, no timing.
type failingConnector struct{}

func (failingConnector) Connect(context.Context) (driver.Conn, error) {
	return nil, errStoreUnreachable
}

func (failingConnector) Driver() driver.Driver { return failingDriver{} }

type failingDriver struct{}

func (failingDriver) Open(string) (driver.Conn, error) { return nil, errStoreUnreachable }

// TestSqlcStoreLoadTaskReportsUnreadableDueDate pins the distinction
// between "this task has no due date" and "the due date could not be
// read". Facts encodes the former as a nil DueOn, which every time.due_*
// builtin answers false for, so a swallowed read error would be
// indistinguishable from a definitive verdict about a deadline nobody
// managed to look at.
func TestSqlcStoreLoadTaskReportsUnreadableDueDate(t *testing.T) {
	db := sql.OpenDB(failingConnector{})
	t.Cleanup(func() { _ = db.Close() })

	store := &SqlcStore{WorkspaceID: 1, Queries: generated.New(db)}
	task, err := store.LoadTask(context.Background(), 42)
	if err == nil {
		t.Fatal("LoadTask must fail when the due_on read fails; got nil error")
	}
	if !errors.Is(err, errStoreUnreachable) {
		t.Fatalf("LoadTask must surface the underlying failure; got %v", err)
	}
	if task.Found {
		t.Fatal("a failed load must not report the task as found")
	}
	if task.Facts.DueOn != nil {
		t.Fatalf("a failed read must not fabricate a due date; got %v", *task.Facts.DueOn)
	}
	if len(task.Rows) != 0 {
		t.Fatalf("a failed load must not return constraint rows; got %d", len(task.Rows))
	}
}

// unloadableStore is a Store whose facts are never available.
type unloadableStore struct {
	marks int
}

func (s *unloadableStore) LoadTask(context.Context, uint32) (Task, error) {
	return Task{}, errStoreUnreachable
}

func (s *unloadableStore) MarkSatisfied(_ context.Context, _ string, _ time.Time) (bool, error) {
	s.marks++
	return true, nil
}

func (s *unloadableStore) MarkFailed(_ context.Context, _ string, _ time.Time) (bool, error) {
	s.marks++
	return true, nil
}

// TestEngineWritesNoMarkersWhenFactsAreUnavailable is the other half of
// the same contract: a load failure has to reach the caller as an error
// and must not leave persisted satisfied/failed markers behind, because
// those markers are what the UI and the reminder engine read as a
// verdict.
func TestEngineWritesNoMarkersWhenFactsAreUnavailable(t *testing.T) {
	store := &unloadableStore{}
	emits := 0
	eng := &Engine{Store: store, Emit: func(context.Context, uint32, string, Row, bool) error {
		emits++
		return nil
	}}

	outcomes, err := eng.evaluate(context.Background(), 1)
	if !errors.Is(err, errStoreUnreachable) {
		t.Fatalf("evaluate must surface the load failure; got %v", err)
	}
	if len(outcomes) != 0 {
		t.Fatalf("no outcome may be reported for an unevaluated task; got %d", len(outcomes))
	}
	if store.marks != 0 {
		t.Fatalf("no marker may be written for an unevaluated task; got %d", store.marks)
	}
	if emits != 0 {
		t.Fatalf("no verdict may be emitted for an unevaluated task; got %d", emits)
	}
}
