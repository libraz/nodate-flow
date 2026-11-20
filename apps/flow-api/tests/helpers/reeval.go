package helpers

import (
	"context"
	"database/sql"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/constraint/reeval"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/eventbus"
)

// reevalJobTimeout bounds one re-evaluation job; the request context that
// scheduled it is gone by the time the job runs.
const reevalJobTimeout = 30 * time.Second

// reevalIdleTimeout is how long WaitReevalIdle waits for a workspace.
const reevalIdleTimeout = 10 * time.Second

var (
	reevalOnce sync.Once
	reevalDB   *sql.DB

	reevalMu       sync.Mutex
	reevalInFlight = map[uint32]int{}
)

// RegisterConstraintReevaluation installs the constraint re-evaluation
// subscriber and the eventlog bridge once per process, as cmd/api does.
// Jobs run in goroutines that are counted per workspace so a test can wait
// for them with WaitReevalIdle. The process shares one database; a later
// call with a different handle fails the test.
func RegisterConstraintReevaluation(t testing.TB, db *sql.DB) {
	t.Helper()
	if err := registerConstraintReevaluation(db); err != nil {
		t.Fatalf("%v", err)
	}
}

func registerConstraintReevaluation(db *sql.DB) error {
	if db == nil {
		return fmt.Errorf("RegisterConstraintReevaluation requires a non-nil *sql.DB")
	}
	reevalOnce.Do(func() {
		reevalDB = db
		sub := &reeval.Subscriber{DB: db, Go: runCountedReevalJob}
		eventbus.AddNotifyHook(sub.Hook())
		eventbus.BridgeEventlog()
	})
	if reevalDB != db {
		return fmt.Errorf("RegisterConstraintReevaluation: constraint re-evaluation is bound to a different *sql.DB; the harness assumes one shared database")
	}
	return nil
}

// runCountedReevalJob counts the job before it returns, so a hook that has
// returned has already been accounted for. The job outlives the request, so
// it keeps the request context's values but not its cancellation.
func runCountedReevalJob(ctx context.Context, workspaceID uint32, job func(context.Context)) bool {
	reevalMu.Lock()
	reevalInFlight[workspaceID]++
	reevalMu.Unlock()
	go func() {
		defer func() {
			reevalMu.Lock()
			reevalInFlight[workspaceID]--
			if reevalInFlight[workspaceID] == 0 {
				delete(reevalInFlight, workspaceID)
			}
			reevalMu.Unlock()
		}()
		ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), reevalJobTimeout)
		defer cancel()
		job(ctx)
	}()
	return true
}

// WaitReevalIdle blocks until no re-evaluation job is running for the
// workspace. Hooks run synchronously on the writing request, so once the
// response is back every job the write caused is already counted. It fails
// the test after 10 seconds.
func WaitReevalIdle(t testing.TB, workspaceID uint32) {
	t.Helper()
	deadline := time.Now().Add(reevalIdleTimeout)
	for {
		reevalMu.Lock()
		n := reevalInFlight[workspaceID]
		reevalMu.Unlock()
		if n == 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("constraint re-evaluation for workspace %d still running after %s (%d jobs)", workspaceID, reevalIdleTimeout, n)
		}
		time.Sleep(5 * time.Millisecond)
	}
}
