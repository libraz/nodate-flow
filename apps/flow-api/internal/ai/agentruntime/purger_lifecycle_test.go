package agentruntime

import (
	"context"
	"database/sql"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
)

type blockingLifecyclePurger struct {
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func (q *blockingLifecyclePurger) PurgeFinishedAgentRuns(context.Context, sql.NullTime) error {
	q.once.Do(func() { close(q.started) })
	<-q.release
	return nil
}

func (*blockingLifecyclePurger) RequeueStrandedAgentRuns(context.Context, generated.RequeueStrandedAgentRunsParams) (int64, error) {
	return 0, nil
}

func (*blockingLifecyclePurger) FailExhaustedAgentRuns(context.Context, generated.FailExhaustedAgentRunsParams) (int64, error) {
	return 0, nil
}

func TestPurgerShutdownWaitsForWholeSupervisorAndStaysStopped(t *testing.T) {
	queries := &blockingLifecyclePurger{started: make(chan struct{}), release: make(chan struct{})}
	p := &Purger{Queries: queries, Interval: time.Hour}
	if err := p.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	<-queries.started

	p.BeginStop()
	if err := p.Start(context.Background()); !errors.Is(err, ErrStopped) {
		t.Fatalf("Start while the canceled tick is still unwinding = %v, want ErrStopped", err)
	}
	close(queries.release)
	if err := p.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	if err := p.Start(context.Background()); !errors.Is(err, ErrStopped) {
		t.Fatalf("Start after the supervisor returned = %v, want ErrStopped", err)
	}
	if err := p.Shutdown(context.Background()); err != nil {
		t.Fatalf("second Shutdown: %v", err)
	}
}

func TestPurgerBeginStopBeforeStartIsPermanent(t *testing.T) {
	p := &Purger{Queries: &fakePurger{}}
	p.BeginStop()
	for i := 0; i < 2; i++ {
		if err := p.Start(context.Background()); !errors.Is(err, ErrStopped) {
			t.Fatalf("Start after BeginStop = %v, want ErrStopped", err)
		}
	}
	if p.handle != nil {
		t.Fatal("Start after BeginStop created a resident loop")
	}
	if err := p.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
}
