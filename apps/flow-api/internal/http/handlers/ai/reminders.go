package ai

import (
	"context"
	"sort"
	"time"

	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/ai/reminders"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/generated"
	apierrors "github.com/nodate-flow/nodate-flow/apps/flow-api/internal/errors"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/http/middleware"
)

// remindersLimit caps how many tasks we evaluate per call. The rule
// engine is pure Go and cheap, but we still bound the list-view query.
const remindersLimit = 200

// ListRemindersInput is the path input for
// GET /workspaces/{wsId}/ai/reminders.
type ListRemindersInput struct {
	WsID string `path:"wsId"`
}

// TaskReminder is a single (task, reminder) pair.
type TaskReminder struct {
	TaskID       string `json:"taskId"`
	Title        string `json:"title"`
	State        string `json:"state"`
	DueOn        string `json:"dueOn"`
	Kind         string `json:"kind"`
	DaysUntilDue int    `json:"daysUntilDue"`
	Message      string `json:"message"`
}

// ListRemindersOutput wraps the reminder list.
type ListRemindersOutput struct {
	Body struct {
		Total     int            `json:"total"`
		Reminders []TaskReminder `json:"reminders"`
	}
}

// kindWeight orders reminders from most to least urgent for the
// response list. Overdue first, then due today, then due soon.
var kindWeight = map[reminders.Kind]int{
	reminders.KindOverdue:  0,
	reminders.KindDueToday: 1,
	reminders.KindDueSoon:  2,
}

// ListReminders handles GET /workspaces/{wsId}/ai/reminders. It walks
// the workspace's task list view, runs the deterministic reminder
// rules on each row, and returns only rows that produced a reminder,
// sorted by urgency then by daysUntilDue. No LLM call is made.
func ListReminders(deps Deps) func(context.Context, *ListRemindersInput) (*ListRemindersOutput, error) {
	return func(ctx context.Context, _ *ListRemindersInput) (*ListRemindersOutput, error) {
		ws, ok := middleware.WorkspaceFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsWorkspaceNotFound)
		}
		rows, err := deps.Queries.ListTasksForWorkspace(ctx, generated.ListTasksForWorkspaceParams{
			WorkspaceID: ws.ID,
			Limit:       remindersLimit,
			Offset:      0,
		})
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		now := time.Now().UTC()
		out := &ListRemindersOutput{}
		out.Body.Total = len(rows)
		out.Body.Reminders = []TaskReminder{}
		for _, r := range rows {
			sig := reminders.Signals{
				State: reminders.State(r.DerivedState),
				Now:   now,
			}
			if r.DueOn.Valid {
				sig.HasDueOn = true
				sig.DueOn = r.DueOn.Time
			}
			rm := reminders.Evaluate(sig)
			if rm == nil {
				continue
			}
			out.Body.Reminders = append(out.Body.Reminders, TaskReminder{
				TaskID:       r.PublicID.String(),
				Title:        r.Title,
				State:        string(r.DerivedState),
				DueOn:        r.DueOn.Time.Format("2006-01-02"),
				Kind:         string(rm.Kind),
				DaysUntilDue: rm.DaysUntilDue,
				Message:      rm.Message,
			})
		}
		sort.SliceStable(out.Body.Reminders, func(i, j int) bool {
			wi := kindWeight[reminders.Kind(out.Body.Reminders[i].Kind)]
			wj := kindWeight[reminders.Kind(out.Body.Reminders[j].Kind)]
			if wi != wj {
				return wi < wj
			}
			return out.Body.Reminders[i].DaysUntilDue < out.Body.Reminders[j].DaysUntilDue
		})
		return out, nil
	}
}
