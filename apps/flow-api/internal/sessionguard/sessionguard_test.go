package sessionguard

import (
	"context"
	"database/sql"
	"errors"
	"testing"
)

type fakeExec struct {
	stmts     []string
	ctxErrs   []error
	failOnNil bool
	failed    error
}

func (f *fakeExec) ExecContext(ctx context.Context, query string, _ ...any) (sql.Result, error) {
	f.stmts = append(f.stmts, query)
	f.ctxErrs = append(f.ctxErrs, ctx.Err())
	if f.failOnNil && query == "SET @nf_item_projection_engine = NULL" {
		return nil, errors.New("conn lost")
	}
	return nil, nil
}

func (f *fakeExec) Fail(err error) { f.failed = err }

func TestDisarmRunsAfterRequestContextCancelled(t *testing.T) {
	f := &fakeExec{}
	ctx, cancel := context.WithCancel(context.Background())
	disarm, err := Arm(ctx, f, ItemProjectionEngine)
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	disarm()
	if len(f.stmts) != 2 || f.stmts[1] != "SET @nf_item_projection_engine = NULL" {
		t.Fatalf("statements = %v", f.stmts)
	}
	if f.ctxErrs[1] != nil {
		t.Fatalf("disarm ran on a dead context: %v", f.ctxErrs[1])
	}
}

func TestFailedDisarmForbidsTransactionCommit(t *testing.T) {
	f := &fakeExec{failOnNil: true}
	disarm, err := Arm(context.Background(), f, ItemProjectionEngine)
	if err != nil {
		t.Fatal(err)
	}
	disarm()
	if f.failed == nil {
		t.Fatal("transaction was not failed after the disarm failed")
	}
}
