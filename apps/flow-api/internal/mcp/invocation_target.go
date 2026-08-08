package mcp

import (
	"context"
	"sync/atomic"
)

// mcp_invocations has a task_id column and it was never written: every
// row landed with NULL, so the audit trail could say an agent called
// update_task and not which task it updated. That is the question an
// AI-native product exists to answer, and the workspace activity view
// renders MCP rows under the caller's human name, so an agent's work
// arrives wearing somebody's face with no way to trace what it touched.
//
// The tool bodies are not the right place to report it. There are more
// than forty of them and any one that forgets reopens the hole quietly.
// Instead the id is stamped where task access is already centralised —
// the resolvers in acl.go, which every task-touching tool must go
// through because acl_static_test.go refuses any other path — plus the
// two creation sites that mint a task rather than resolve one. The
// carrier below is what lets a resolver deep in a call stack hand the
// id back to the audit writer without threading a return value through
// every intermediate signature.

// invocationTarget is the per-call attribution slot. It is written by
// the ACL resolvers and read once by the audit writer after the tool
// returns, from another goroutine in no case — but the field is atomic
// anyway because a tool is free to fan out its own work.
type invocationTarget struct {
	taskID atomic.Uint32
}

type invocationTargetKey struct{}

// withInvocationTarget attaches a fresh attribution slot to the context
// for one tool call.
func withInvocationTarget(ctx context.Context) context.Context {
	return context.WithValue(ctx, invocationTargetKey{}, &invocationTarget{})
}

// noteInvocationTask records the task a tool call acted on. The first
// task wins: a tool that touches several (apply_steps creating children
// under a parent) is attributed to the one it was pointed at, not to
// whichever child happened to be resolved last. Calls outside a tool
// dispatch — the AI orchestrator reusing a resolver, tests driving a
// tool body directly — find no slot and do nothing.
func noteInvocationTask(ctx context.Context, taskID uint32) {
	if taskID == 0 {
		return
	}
	t, ok := ctx.Value(invocationTargetKey{}).(*invocationTarget)
	if !ok {
		return
	}
	t.taskID.CompareAndSwap(0, taskID)
}

// invocationTaskID returns the task recorded for this call, or zero when
// the tool touched no task.
func invocationTaskID(ctx context.Context) uint32 {
	t, ok := ctx.Value(invocationTargetKey{}).(*invocationTarget)
	if !ok {
		return 0
	}
	return t.taskID.Load()
}
