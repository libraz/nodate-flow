package agentruntime

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

type blockingLifecycleSource struct {
	started chan struct{}
	release chan struct{}
	calls   atomic.Int32
}

func (s *blockingLifecycleSource) Due(context.Context, time.Time) ([]Job, error) {
	if s.calls.Add(1) == 1 {
		close(s.started)
		<-s.release
	}
	return nil, nil
}

func TestSchedulerShutdownWaitsForWholeSupervisorAndStaysStopped(t *testing.T) {
	source := &blockingLifecycleSource{started: make(chan struct{}), release: make(chan struct{})}
	s := &Scheduler{Source: source, Runner: &fakeRunner{}}
	if err := s.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	<-source.started

	s.BeginStop()
	if err := s.Start(context.Background()); !errors.Is(err, ErrStopped) {
		t.Fatalf("Start while the canceled tick is still unwinding = %v, want ErrStopped", err)
	}
	close(source.release)
	if err := s.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	if err := s.Start(context.Background()); !errors.Is(err, ErrStopped) {
		t.Fatalf("Start after the supervisor returned = %v, want ErrStopped", err)
	}
	if err := s.Shutdown(context.Background()); err != nil {
		t.Fatalf("second Shutdown: %v", err)
	}
}

func TestSchedulerBeginStopBeforeStartIsPermanent(t *testing.T) {
	s := &Scheduler{Source: &fakeSource{}, Runner: &fakeRunner{}}
	s.BeginStop()
	for i := 0; i < 2; i++ {
		if err := s.Start(context.Background()); !errors.Is(err, ErrStopped) {
			t.Fatalf("Start after BeginStop = %v, want ErrStopped", err)
		}
	}
	if s.handle != nil {
		t.Fatal("Start after BeginStop created a resident loop")
	}
	if err := s.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
}
