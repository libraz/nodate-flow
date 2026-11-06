package importer

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
)

func TestWorkerShutdownBeforeStartIsImmediate(t *testing.T) {
	w := NewWorker(nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := w.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown before Start: %v", err)
	}
	w.Start(context.Background())
	if err := w.Wait(context.Background()); err != nil {
		t.Fatalf("Wait after a prevented late Start: %v", err)
	}
	if w.handle != nil {
		t.Fatal("Start after shutdown created a resident loop")
	}
}

func TestWorkerStartAndStopRaceWaitsForWholeSupervisor(t *testing.T) {
	w := NewWorker(nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	start := make(chan struct{})
	var callers sync.WaitGroup
	callers.Add(2)
	go func() {
		defer callers.Done()
		<-start
		w.Start(ctx)
	}()
	go func() {
		defer callers.Done()
		<-start
		w.BeginStop()
	}()
	close(start)
	callers.Wait()

	w.BeginStop()
	if err := w.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown after racing Start: %v", err)
	}
}

func TestWorkerStartAfterStopDoesNotReportStarted(t *testing.T) {
	var logs bytes.Buffer
	r := NewWorker(nil, nil, slog.New(slog.NewTextHandler(&logs, nil)))
	r.BeginStop()
	r.Start(context.Background())
	if strings.Contains(logs.String(), "import worker started") {
		t.Fatalf("a Start after BeginStop logged that it started:\n%s", logs.String())
	}
}
