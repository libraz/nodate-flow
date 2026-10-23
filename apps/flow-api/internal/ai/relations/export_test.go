package relations

import "context"

// SetProcess overrides the per-task step for the external test package,
// which cannot reach the unexported seam directly.
func (p *Pipeline) SetProcess(fn func(ctx context.Context, workspaceID, taskID uint32)) {
	p.process = fn
}
