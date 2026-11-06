// Package lifecycle coordinates graceful shutdown of the flow-api process.
package lifecycle

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/bgloop"
)

const defaultShutdownBudget = 20 * time.Second

// Resident separates a fast stop signal from a context-bounded wait. BeginStop
// must only signal cancellation; Wait must honor the context it receives.
type Resident struct {
	BeginStop func()
	Wait      func(context.Context) error
}

// ShutdownConfig describes the process resources that share one shutdown
// deadline. HookRunner remains open until HTTP requests and residents finish,
// because a finishing request may still append an event and submit a hook.
type ShutdownConfig struct {
	Budget time.Duration
	// Deadline, when set, is the deadline captured by the caller when
	// shutdown was first requested. It prevents a signal received during
	// startup from receiving a fresh budget after startup finishes.
	Deadline time.Time

	MarkNotReady func()
	CancelWork   context.CancelFunc
	Servers      []*http.Server
	Residents    []Resident
	Hooks        *bgloop.HookRunner

	// StopMetrics signals metric producers to stop and should return promptly.
	StopMetrics  func()
	DrainMetrics func(context.Context) error
	DrainTracing func(context.Context) error
	// CloseDB runs last. The coordinator bounds how long it waits; a DB
	// implementation that cannot honor a context may finish after timeout
	// while the process is already exiting with a deadline error.
	CloseDB func() error
}

// Shutdown coordinates a single process shutdown and its deadline.
type Shutdown struct {
	config ShutdownConfig

	mu        sync.Mutex
	started   bool
	beginDone chan struct{}
	stopCtx   context.Context
	cancel    context.CancelFunc

	shutdownDone chan struct{}
	shutdownErr  error
}

// NewShutdown creates a coordinator. A non-positive budget uses 20 seconds.
func NewShutdown(config ShutdownConfig) *Shutdown {
	if config.Budget <= 0 {
		config.Budget = defaultShutdownBudget
	}
	return &Shutdown{config: config, beginDone: make(chan struct{})}
}

// BeginStop marks readiness false, starts the one process deadline and
// signals the root work context and every resident. It is safe to call more
// than once. The returned context carries the same deadline used by every
// subsequent phase.
func (s *Shutdown) BeginStop() context.Context {
	s.mu.Lock()
	if s.started {
		ctx, done := s.stopCtx, s.beginDone
		s.mu.Unlock()
		<-done
		return ctx
	}
	s.started = true
	deadline := s.config.Deadline
	if deadline.IsZero() {
		deadline = time.Now().Add(s.config.Budget)
	}
	s.stopCtx, s.cancel = context.WithDeadline(context.Background(), deadline)
	ctx := s.stopCtx
	s.mu.Unlock()

	if s.config.MarkNotReady != nil {
		s.config.MarkNotReady()
	}
	if s.config.CancelWork != nil {
		s.config.CancelWork()
	}
	for _, resident := range s.config.Residents {
		if resident.BeginStop != nil {
			resident.BeginStop()
		}
	}
	close(s.beginDone)
	return ctx
}

// Shutdown runs the drain phases under the one deadline established by
// BeginStop. The first call performs shutdown; concurrent and later calls
// wait for it or return the same result.
func (s *Shutdown) Shutdown(ctx context.Context) error {
	base := s.BeginStop()
	<-s.beginDone

	s.mu.Lock()
	if s.shutdownDone != nil {
		done := s.shutdownDone
		s.mu.Unlock()
		select {
		case <-done:
			s.mu.Lock()
			err := s.shutdownErr
			s.mu.Unlock()
			return err
		default:
		}
		select {
		case <-done:
			s.mu.Lock()
			err := s.shutdownErr
			s.mu.Unlock()
			return err
		case <-ctxDone(ctx):
			return ctx.Err()
		}
	}
	done := make(chan struct{})
	s.shutdownDone = done
	s.mu.Unlock()

	phaseCtx, cancelPhase := s.phaseContext(base, ctx)
	err := s.run(phaseCtx)
	cancelPhase()

	s.mu.Lock()
	s.shutdownErr = err
	close(done)
	s.mu.Unlock()
	return err
}

func (s *Shutdown) phaseContext(base, supplied context.Context) (context.Context, context.CancelFunc) {
	if supplied == nil || supplied == base {
		return base, func() {}
	}
	var ctx context.Context
	var cancel context.CancelFunc
	if suppliedDeadline, ok := supplied.Deadline(); ok {
		if baseDeadline, hasBaseDeadline := base.Deadline(); !hasBaseDeadline || suppliedDeadline.Before(baseDeadline) {
			ctx, cancel = context.WithDeadline(base, suppliedDeadline)
		} else {
			ctx, cancel = context.WithCancel(base)
		}
	} else {
		ctx, cancel = context.WithCancel(base)
	}
	// An expired supplied deadline is already ctx's own deadline; only an explicit cancel propagates.
	stop := context.AfterFunc(supplied, func() {
		if errors.Is(supplied.Err(), context.Canceled) {
			cancel()
		}
	})
	return ctx, func() {
		stop()
		cancel()
	}
}

func ctxDone(ctx context.Context) <-chan struct{} {
	if ctx == nil {
		return nil
	}
	return ctx.Done()
}

func (s *Shutdown) run(ctx context.Context) error {
	monitorDone := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			s.forceClose()
		case <-monitorDone:
		}
	}()
	defer func() {
		close(monitorDone)
		if s.cancel != nil {
			s.cancel()
		}
	}()

	var waits []func(context.Context) error
	for _, server := range s.config.Servers {
		if server == nil {
			continue
		}
		server := server
		waits = append(waits, func(ctx context.Context) error {
			err := server.Shutdown(ctx)
			if err != nil && ctx.Err() != nil {
				_ = server.Close()
			}
			return err
		})
	}
	for _, resident := range s.config.Residents {
		if resident.Wait != nil {
			waits = append(waits, resident.Wait)
		}
	}
	err := runConcurrent(ctx, waits)
	if ctx.Err() != nil {
		s.forceClose()
		err = errors.Join(err, ctx.Err())
	}

	if s.config.Hooks != nil {
		if hookErr := s.config.Hooks.Drain(ctx); hookErr != nil {
			err = errors.Join(err, hookErr)
		}
	}
	if ctx.Err() != nil {
		s.forceClose()
		err = errors.Join(err, ctx.Err())
	}

	if s.config.StopMetrics != nil {
		s.config.StopMetrics()
	}
	postHooks := make([]func(context.Context) error, 0, 2)
	if s.config.DrainMetrics != nil {
		postHooks = append(postHooks, s.config.DrainMetrics)
	}
	if s.config.DrainTracing != nil {
		postHooks = append(postHooks, s.config.DrainTracing)
	}
	err = errors.Join(err, runConcurrent(ctx, postHooks))
	if ctx.Err() != nil {
		s.forceClose()
		err = errors.Join(err, ctx.Err())
	}

	if s.config.CloseDB != nil {
		dbClosed := make(chan error, 1)
		go func() { dbClosed <- s.config.CloseDB() }()
		select {
		case closeErr := <-dbClosed:
			err = errors.Join(err, closeErr)
		case <-ctx.Done():
			s.forceClose()
			err = errors.Join(err, ctx.Err())
		}
	}
	if ctx.Err() != nil {
		err = errors.Join(err, ctx.Err())
	}
	return err
}

func (s *Shutdown) forceClose() {
	if s.config.Hooks != nil {
		s.config.Hooks.Abort()
	}
	for _, server := range s.config.Servers {
		if server != nil {
			_ = server.Close()
		}
	}
}

func runConcurrent(ctx context.Context, tasks []func(context.Context) error) error {
	if len(tasks) == 0 {
		return nil
	}
	type result struct{ err error }
	results := make(chan result, len(tasks))
	for _, task := range tasks {
		task := task
		go func() { results <- result{err: task(ctx)} }()
	}

	var err error
	for range tasks {
		select {
		case result := <-results:
			err = errors.Join(err, result.err)
		case <-ctx.Done():
			return errors.Join(err, ctx.Err())
		}
	}
	return err
}
