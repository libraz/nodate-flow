package lifecycle

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/bgloop"
)

func TestShutdownSignalsResidentsBeforeConcurrentDeadlineWaits(t *testing.T) {
	const budget = 80 * time.Millisecond
	var stopped atomic.Int32
	entered := make(chan time.Time, 2)
	exited := make(chan struct{}, 2)
	resident := func() Resident {
		return Resident{
			BeginStop: func() { stopped.Add(1) },
			Wait: func(ctx context.Context) error {
				if got := stopped.Load(); got != 2 {
					return errors.New("wait started before every resident received BeginStop")
				}
				deadline, ok := ctx.Deadline()
				if !ok {
					return errors.New("shutdown context has no deadline")
				}
				entered <- deadline
				<-ctx.Done()
				exited <- struct{}{}
				return ctx.Err()
			},
		}
	}

	s := NewShutdown(ShutdownConfig{Budget: budget, Residents: []Resident{resident(), resident()}})
	ctx := s.BeginStop()
	deadline, ok := ctx.Deadline()
	if !ok {
		t.Fatal("BeginStop returned a context without a deadline")
	}
	shutdownDone := make(chan error, 1)
	go func() { shutdownDone <- s.Shutdown(ctx) }()

	for range 2 {
		select {
		case got := <-entered:
			if !got.Equal(deadline) {
				t.Fatalf("resident deadline = %v, want shared deadline %v", got, deadline)
			}
		case <-ctx.Done():
			t.Fatal("shutdown deadline elapsed before both resident waits began")
		}
	}
	if got := stopped.Load(); got != 2 {
		t.Fatalf("BeginStop called for %d residents, want 2", got)
	}
	if err := <-shutdownDone; !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Shutdown error = %v, want context deadline exceeded", err)
	}
	for range 2 {
		select {
		case <-exited:
		case <-time.After(time.Second):
			t.Fatal("resident did not observe the shared shutdown deadline")
		}
	}
}

func TestShutdownUsesDeadlineCapturedBeforeCoordinatorConstruction(t *testing.T) {
	captured := time.Now().Add(-time.Second)
	s := NewShutdown(ShutdownConfig{Deadline: captured})
	ctx := s.BeginStop()
	got, ok := ctx.Deadline()
	if !ok || !got.Equal(captured) {
		t.Fatalf("shutdown deadline = %v (present=%v), want captured startup deadline %v", got, ok, captured)
	}
	if !errors.Is(ctx.Err(), context.DeadlineExceeded) {
		t.Fatalf("past captured deadline context error = %v, want deadline exceeded", ctx.Err())
	}
	if err := s.Shutdown(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Shutdown error = %v, want deadline exceeded", err)
	}
}

func TestShutdownHonorsEarlierSuppliedPhaseDeadline(t *testing.T) {
	captured := time.Now().Add(5 * time.Second)
	supplied := time.Now().Add(time.Second)
	observed := make(chan time.Time, 1)
	s := NewShutdown(ShutdownConfig{
		Deadline: captured,
		Residents: []Resident{{Wait: func(ctx context.Context) error {
			deadline, _ := ctx.Deadline()
			observed <- deadline
			<-ctx.Done()
			return ctx.Err()
		}}},
	})
	base := s.BeginStop()
	suppliedCtx, cancel := context.WithDeadline(context.Background(), supplied)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- s.Shutdown(suppliedCtx) }()

	select {
	case got := <-observed:
		if !got.Equal(supplied) {
			t.Fatalf("wait deadline = %v, want earlier supplied deadline %v (base %v)", got, supplied, base)
		}
	case <-suppliedCtx.Done():
		t.Fatal("shutdown wait did not begin before the earlier supplied deadline")
	}
	if err := <-done; !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Shutdown error = %v, want earlier supplied deadline", err)
	}
}

func TestShutdownDeadlineAbortsHooksWhileProducersAreStillDraining(t *testing.T) {
	runner := bgloop.NewHookRunner("lifecycle.deadline", 1, time.Minute)
	hookStarted := make(chan struct{})
	hookCanceled := make(chan struct{})
	if !runner.Go(context.Background(), 0, func(ctx context.Context) {
		close(hookStarted)
		<-ctx.Done()
		close(hookCanceled)
	}) {
		t.Fatal("runner refused hook before shutdown")
	}
	<-hookStarted

	const budget = 80 * time.Millisecond
	s := NewShutdown(ShutdownConfig{
		Budget: budget,
		Hooks:  runner,
		Residents: []Resident{{
			BeginStop: func() {},
			Wait: func(ctx context.Context) error {
				<-ctx.Done()
				return ctx.Err()
			},
		}},
	})
	ctx := s.BeginStop()
	err := s.Shutdown(ctx)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Shutdown error = %v, want deadline exceeded", err)
	}
	select {
	case <-hookCanceled:
	case <-ctx.Done():
		select {
		case <-hookCanceled:
		case <-time.After(time.Second):
			t.Fatal("hook did not observe the shared shutdown deadline")
		}
	}
}

func TestShutdownAllowsFinishingHTTPRequestToSubmitHookBeforeDatabaseClose(t *testing.T) {
	runner := bgloop.NewHookRunner("lifecycle.http", 1, time.Second)
	requestStarted := make(chan struct{})
	finishRequest := make(chan struct{})
	shutdownStarted := make(chan struct{})
	hookAccepted := make(chan bool, 1)
	hookStarted := make(chan struct{})
	finishHook := make(chan struct{})
	hookFinished := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		close(requestStarted)
		<-finishRequest
		hookAccepted <- runner.Go(context.Background(), 0, func(context.Context) {
			close(hookStarted)
			<-finishHook
			close(hookFinished)
		})
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	server.Config.RegisterOnShutdown(func() { close(shutdownStarted) })

	responseDone := make(chan error, 1)
	go func() {
		response, err := http.Get(server.URL)
		if err == nil {
			response.Body.Close()
		}
		responseDone <- err
	}()
	<-requestStarted

	var notReadyCalls atomic.Int32
	var dbClosedAfterHook atomic.Bool
	s := NewShutdown(ShutdownConfig{
		Budget:       5 * time.Second,
		MarkNotReady: func() { notReadyCalls.Add(1) },
		Servers:      []*http.Server{server.Config},
		Hooks:        runner,
		CloseDB: func() error {
			select {
			case <-hookFinished:
				dbClosedAfterHook.Store(true)
			default:
			}
			return nil
		},
	})
	ctx := s.BeginStop()
	shutdownDone := make(chan error, 1)
	go func() { shutdownDone <- s.Shutdown(ctx) }()
	<-shutdownStarted
	close(finishRequest)

	if err := <-responseDone; err != nil {
		t.Fatalf("finishing request: %v", err)
	}
	if accepted := <-hookAccepted; !accepted {
		t.Fatal("event hook was refused while its HTTP request was finishing")
	}
	<-hookStarted
	select {
	case err := <-shutdownDone:
		t.Fatalf("Shutdown returned before the accepted hook completed: %v", err)
	default:
	}
	close(finishHook)
	if err := <-shutdownDone; err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	if !dbClosedAfterHook.Load() {
		t.Fatal("database close ran before the accepted hook completed")
	}
	if got := notReadyCalls.Load(); got != 1 {
		t.Fatalf("readiness stop called %d times, want one across repeated shutdown", got)
	}
	if err := s.Shutdown(ctx); err != nil {
		t.Fatalf("repeated Shutdown: %v", err)
	}
}
