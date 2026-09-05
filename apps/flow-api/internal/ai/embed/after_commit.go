package embed

import (
	"context"
	"log/slog"
)

// RefreshTaskAfterCommit refreshes a task's embedding once its write has committed.
// It is the entry point every write path uses, so that "a task write
// refreshes its embedding" is one symbol rather than a shape each caller
// reproduces.
//
// The nil check and the discarded error live here. c may be nil: a
// deployment with no embedding provider writes no embeddings, and a task
// write there is still a complete task write.
//
// RefreshTaskAfterCommit returns nothing, and that is a constraint rather than a
// convenience. Every caller sits at or just past a transaction boundary,
// where the write it refreshes has already succeeded and is no longer
// revocable. A returned value there is something a caller can branch on,
// and the only branches available at that point — failing the response,
// rolling back what was committed — turn a best-effort refresh into a
// reason to lose a write the user was already told about. With no value
// to inspect, that branch cannot be written. A refresh that fails is
// picked up by the reindex cron.
//
// Callers that need to know whether the embedding exists — a read path
// filling a gap before it queries the row it just wrote — must call
// [Client.EmbedTask] directly and handle its error. RefreshTaskAfterCommit cannot
// serve them and does not try to.
func RefreshTaskAfterCommit(ctx context.Context, c *Client, workspaceID, taskID uint32, title, description string) {
	if c == nil || c.Provider == nil {
		return
	}
	if err := c.EmbedTask(ctx, workspaceID, taskID, title, description); err != nil {
		// Debug, not warn. A provider failure already writes an
		// ai_invocations row with status "error" and reaches the metrics
		// hook, so an operator has a queryable record without this line.
		// The one failure mode that reaches neither is a workspace over
		// its embedding budget, which is steady-state rather than
		// exceptional and would emit a line per task write for the rest
		// of the day at any level that ships by default.
		slog.DebugContext(ctx, "embed: task embedding refresh failed",
			slog.Uint64("workspace_internal", uint64(workspaceID)),
			slog.Uint64("task_internal", uint64(taskID)),
			slog.String("err", c.redact(err.Error())),
		)
	}
}
