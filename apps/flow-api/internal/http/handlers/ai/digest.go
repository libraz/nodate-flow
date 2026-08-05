package ai

import (
	"context"
	"database/sql"
	"time"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/ai/digest"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/handlers/handlerutil"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/middleware"
)

// digestLimit caps how many tasks the weekly digest ingests.
// Workspaces are small enough that 500 is plenty without a second
// aggregation query.
const digestLimit = 500

// WeeklyDigestInput is the path input for
// GET /workspaces/{wsId}/ai/weekly-digest.
type WeeklyDigestInput struct {
	WsID string `path:"wsId"`
}

// WeeklyDigestCounts is the digest's by-state histogram mapped into a
// flat struct so the OpenAPI schema is stable.
type WeeklyDigestCounts struct {
	Open      int `json:"open"`
	Waiting   int `json:"waiting"`
	Review    int `json:"review"`
	Done      int `json:"done"`
	Cancelled int `json:"cancelled"`
}

// WeeklyDigestTask is the compact task projection returned in the
// "completedThisWeek" / "overdueOpen" lists.
type WeeklyDigestTask struct {
	TaskID string `json:"taskId"`
	Title  string `json:"title"`
	Date   string `json:"date"`
}

// WeeklyDigestOutput is the Huma envelope.
type WeeklyDigestOutput struct {
	Body struct {
		Counts            WeeklyDigestCounts `json:"counts"`
		CompletedThisWeek []WeeklyDigestTask `json:"completedThisWeek"`
		OverdueOpen       []WeeklyDigestTask `json:"overdueOpen"`
		Markdown          string             `json:"markdown"`
	}
}

// WeeklyDigest handles GET /workspaces/{wsId}/ai/weekly-digest. It
// walks the workspace task list view, folds each row into a
// digest.TaskSnapshot, and runs digest.Build. No LLM call is made.
func WeeklyDigest(deps Deps) func(context.Context, *WeeklyDigestInput) (*WeeklyDigestOutput, error) {
	return func(ctx context.Context, _ *WeeklyDigestInput) (*WeeklyDigestOutput, error) {
		ws, ok := middleware.WorkspaceFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsWorkspaceNotFound)
		}
		rows, err := deps.Queries.ListTasksForWorkspace(ctx, generated.ListTasksForWorkspaceParams{
			WorkspaceID: ws.ID,
			Limit:       digestLimit,
			Offset:      0,
		})
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		now := time.Now().UTC()
		snaps := make([]digest.TaskSnapshot, 0, len(rows))
		for _, r := range rows {
			s := digest.TaskSnapshot{
				TaskID: r.PublicID.String(),
				Title:  r.Title,
				State:  digest.State(r.DerivedState),
			}
			if r.DueOn.Valid {
				s.HasDueOn = true
				s.DueOn = r.DueOn.Time
			}
			if r.CompletedAt.Valid {
				s.HasCompleted = true
				s.CompletedAt = r.CompletedAt.Time
			}
			snaps = append(snaps, s)
		}
		d := digest.Build(snaps, now)

		out := &WeeklyDigestOutput{}
		out.Body.Counts = WeeklyDigestCounts{
			Open:      d.Counts[digest.StateOpen],
			Waiting:   d.Counts[digest.StateWaiting],
			Review:    d.Counts[digest.StateReview],
			Done:      d.Counts[digest.StateDone],
			Cancelled: d.Counts[digest.StateCancelled],
		}
		out.Body.CompletedThisWeek = make([]WeeklyDigestTask, 0, len(d.CompletedThisWeek))
		for _, t := range d.CompletedThisWeek {
			out.Body.CompletedThisWeek = append(out.Body.CompletedThisWeek, WeeklyDigestTask{
				TaskID: t.TaskID,
				Title:  t.Title,
				Date:   handlerutil.NullTimeDateStr(sql.NullTime{Time: t.CompletedAt, Valid: true}),
			})
		}
		out.Body.OverdueOpen = make([]WeeklyDigestTask, 0, len(d.OverdueOpen))
		for _, t := range d.OverdueOpen {
			out.Body.OverdueOpen = append(out.Body.OverdueOpen, WeeklyDigestTask{
				TaskID: t.TaskID,
				Title:  t.Title,
				Date:   handlerutil.NullTimeDateStr(sql.NullTime{Time: t.DueOn, Valid: true}),
			})
		}
		out.Body.Markdown = d.Markdown
		return out, nil
	}
}
