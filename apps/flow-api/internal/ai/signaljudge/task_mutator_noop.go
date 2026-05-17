// Package signaljudge — TaskMutator stub for Phase 3 wiring.
//
// The Applier accepts a [TaskMutator] interface so production wiring
// can plug in the existing task / comment handlers (the canonical
// path that emits task.transition.complete, task.comment.added, and
// task.created events). Phase 3 lands the Applier and its event
// emission contract; the production TaskMutator that actually closes
// tasks and writes comments is wired piecewise as the existing
// handlers are exposed as reusable functions.
//
// [LogOnlyTaskMutator] is the safe default for Phase 3: it logs every
// call at warn level and returns success without touching the
// database. The Applier still emits TaskAutoCompleted / SignalApplied
// / TaskRetroDrafted events; only the task-row side effects are
// skipped. This lets the judge-loop event stream go live without
// blocking on the handler refactor that Phase 6 will sort out.
package signaljudge

import (
	"context"
	"log/slog"
)

// LogOnlyTaskMutator is a no-op TaskMutator that logs every call.
// It satisfies the interface so the Applier compiles end-to-end;
// production wiring replaces it once the task-handler extraction
// lands.
type LogOnlyTaskMutator struct {
	// Logger is used for the warn-level diagnostics; nil falls back
	// to slog.Default().
	Logger *slog.Logger
}

// CompleteTask logs the request and returns nil.
func (m *LogOnlyTaskMutator) CompleteTask(ctx context.Context, workspaceID uint32, taskInternalID int64, agentID uint32) error {
	logger := m.Logger
	if logger == nil {
		logger = slog.Default()
	}
	logger.WarnContext(ctx, "signaljudge: TaskMutator.CompleteTask is a no-op stub",
		slog.Uint64("workspace_internal", uint64(workspaceID)),
		slog.Int64("task_internal", taskInternalID),
		slog.Uint64("agent_internal", uint64(agentID)),
	)
	return nil
}

// AddComment logs the request and returns nil.
func (m *LogOnlyTaskMutator) AddComment(ctx context.Context, workspaceID uint32, taskInternalID int64, agentID uint32, body string) error {
	logger := m.Logger
	if logger == nil {
		logger = slog.Default()
	}
	logger.WarnContext(ctx, "signaljudge: TaskMutator.AddComment is a no-op stub",
		slog.Uint64("workspace_internal", uint64(workspaceID)),
		slog.Int64("task_internal", taskInternalID),
		slog.Uint64("agent_internal", uint64(agentID)),
		slog.Int("body_len", len(body)),
	)
	return nil
}

// DraftRetroTask logs the request and returns (0, "", nil).
func (m *LogOnlyTaskMutator) DraftRetroTask(ctx context.Context, workspaceID uint32, sourceTaskInternalID int64, agentID uint32, title string, draft bool) (int64, string, error) {
	logger := m.Logger
	if logger == nil {
		logger = slog.Default()
	}
	logger.WarnContext(ctx, "signaljudge: TaskMutator.DraftRetroTask is a no-op stub",
		slog.Uint64("workspace_internal", uint64(workspaceID)),
		slog.Int64("source_task_internal", sourceTaskInternalID),
		slog.Uint64("agent_internal", uint64(agentID)),
		slog.String("title", title),
		slog.Bool("draft", draft),
	)
	return 0, "", nil
}
