package storagegc

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
)

func TestSweeperShutdownBeforeStartIsImmediate(t *testing.T) {
	s := New(nil, nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := s.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown before Start: %v", err)
	}
	s.Start(context.Background())
	if err := s.Wait(context.Background()); err != nil {
		t.Fatalf("Wait after a prevented late Start: %v", err)
	}
	if s.handle != nil {
		t.Fatal("Start after shutdown created a resident loop")
	}
}

func TestSweeperStartAndStopRaceWaitsForWholeSupervisor(t *testing.T) {
	s := New(nil, nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	start := make(chan struct{})
	var callers sync.WaitGroup
	callers.Add(2)
	go func() {
		defer callers.Done()
		<-start
		s.Start(ctx)
	}()
	go func() {
		defer callers.Done()
		<-start
		s.BeginStop()
	}()
	close(start)
	callers.Wait()

	s.BeginStop()
	if err := s.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown after racing Start: %v", err)
	}
}

func TestSweeperStartAfterStopDoesNotReportStarted(t *testing.T) {
	var logs bytes.Buffer
	r := New(nil, nil, nil, slog.New(slog.NewTextHandler(&logs, nil)))
	r.BeginStop()
	r.Start(context.Background())
	if strings.Contains(logs.String(), "storage sweeper started") {
		t.Fatalf("a Start after BeginStop logged that it started:\n%s", logs.String())
	}
}
