package eventbus

import (
	"os"
	"path/filepath"
	"testing"
)

// The check in this file is the positive control for
// [scanSwallowedAppends]. A derived guard that reports nothing looks
// identical whether the tree is clean or the derivation stopped matching,
// and the previous version of this guard shipped in exactly that state:
// it recognised one spelling of the defect while the dominant one — check
// the error, log it, carry on — walked past it in dozens of places. So
// the scan is pointed at a tree built to contain each shape it must
// report, and at the near misses it must not.

// TestScanReportsWhatItIsMeantToReport pins, in one pass over one tree:
// the three losing shapes, the same three one indirection out through a
// wrapper that only forwards the error, the same defect spelled against
// the other appender, and the forms that are not losses — propagation, a
// best-effort call, an append inside a transaction whose failure the
// closure returns, and a same-named method on some other value.
func TestScanReportsWhatItIsMeantToReport(t *testing.T) {
	t.Parallel()

	offenders, err := scanSwallowedAppends(writeControlTree(t))
	if err != nil {
		t.Fatalf("scan control tree: %v", err)
	}

	want := []struct {
		file  string
		line  int
		entry string
	}{
		{"internal/handlers/direct.go", 12, "eventbus.Append"},
		{"internal/handlers/direct.go", 14, "eventbus.Append"},
		{"internal/handlers/direct.go", 18, "eventbus.AppendJudgeEvent"},
		{"internal/handlers/viaeventlog.go", 9, "eventlog.Append"},
		{"internal/handlers/viafacade.go", 15, "handlers.announce"},
		{"internal/handlers/viafacade.go", 17, "handlers.announce"},
		{"internal/handlers/viafacade.go", 20, "handlers.announce"},
	}
	if len(offenders) != len(want) {
		t.Fatalf("the scan reported %d losses, want %d:\n  %v", len(offenders), len(want), offenders)
	}
	for i, w := range want {
		o := offenders[i]
		if o.File != w.file || o.Line != w.line || o.Entry != w.entry {
			t.Errorf("finding %d is %s, want %s:%d %s", i, o, w.file, w.line, w.entry)
		}
	}
}

// writeControlTree lays out a minimal module and returns the root
// [scanSwallowedAppends] would be given. Line numbers are asserted, so
// the fixtures are written with the offending statements at fixed
// positions.
func writeControlTree(t *testing.T) string {
	t.Helper()
	root := t.TempDir()

	write := func(rel, body string) {
		path := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			t.Fatalf("mkdir %s: %v", rel, err)
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}

	// The three losing shapes spelled against the qualified entry points,
	// followed by the forms that are not losses.
	write("internal/handlers/direct.go", `package handlers

import (
	"context"
	"log/slog"
)

type Deps struct{ DB DB }

func lose(ctx context.Context, deps Deps) error {
	// blank identifier
	_ = eventbus.Append(ctx, deps.DB, eventbus.Event{})
	// checked, logged, carries on
	if err := eventbus.Append(ctx, deps.DB, eventbus.Event{}); err != nil {
		slog.ErrorContext(ctx, "eventbus.Append failed", slog.Any("err", err))
	}
	// discarded by calling it as a statement
	eventbus.AppendJudgeEvent(ctx, deps.DB, eventbus.Event{})
	return nil
}

func keep(ctx context.Context, deps Deps, guard *Guard) error {
	if err := eventbus.Append(ctx, deps.DB, eventbus.Event{}); err != nil {
		return err
	}
	eventbus.AppendBestEffort(ctx, deps.DB, eventbus.Event{}, "handlers.keep")
	_ = guard.Append(ctx, deps.DB, eventbus.Event{})
	return dbretry.InTx(ctx, deps.DB, "handlers.keep", nil, func(ctx context.Context, tx *dbretry.Tx) error {
		return eventbus.Append(ctx, tx, eventbus.Event{})
	})
}
`)

	// The same three shapes one indirection out. announce forwards the
	// append's error and nothing else, so losing what it returns loses the
	// append; report never appends and must not be derived.
	write("internal/handlers/viafacade.go", `package handlers

import (
	"context"
	"log/slog"
)

func announce(ctx context.Context, db DB) error {
	return eventbus.Append(ctx, db, eventbus.Event{})
}

func report(ctx context.Context, db DB) error { return nil }

func loseViaFacade(ctx context.Context, deps Deps) error {
	_ = announce(ctx, deps.DB)
	_ = report(ctx, deps.DB)
	if err := announce(ctx, deps.DB); err != nil {
		slog.ErrorContext(ctx, "announce failed", slog.Any("err", err))
	}
	announce(ctx, deps.DB)
	if err := announce(ctx, deps.DB); err != nil {
		return err
	}
	return nil
}
`)

	// The other appender writing the same table. It returns the new row's
	// id alongside the error, so the losing shapes are spelled with two
	// results.
	write("internal/handlers/viaeventlog.go", `package handlers

import (
	"context"
	"log/slog"
)

func loseEventlog(ctx context.Context, deps Deps) error {
	if _, err := eventlog.Append(ctx, deps.DB, eventlog.Event{}); err != nil {
		slog.ErrorContext(ctx, "eventlog.Append failed", slog.Any("err", err))
	}
	if _, err := eventlog.Append(ctx, deps.DB, eventlog.Event{}); err != nil {
		return err
	}
	return nil
}
`)

	// A different package with a same-named local helper that does not
	// append. Nothing here may be reported.
	write("internal/unrelated/unrelated.go", `package unrelated

import "context"

func announce(ctx context.Context) error { return nil }

func use(ctx context.Context) error {
	_ = announce(ctx)
	return nil
}
`)

	return root
}
