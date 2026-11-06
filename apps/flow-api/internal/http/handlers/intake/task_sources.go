package intake

import (
	"context"
	"database/sql"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/middleware"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/sourceintake"
)

// TaskSourcesInput identifies the task whose captured intake sources are
// being followed. The router mounts this route inside RequireTaskAccess.
type TaskSourcesInput struct {
	ID string `path:"id"`
}

// TaskSource is a bounded, plain-text backlink to an intake source captured
// for a task. It intentionally excludes the original raw signal payload.
type TaskSource struct {
	IntakeItemID string `json:"intakeItemId"`
	SignalID     string `json:"signalId,omitempty"`
	Source       string `json:"source,omitempty"`
	Kind         string `json:"kind,omitempty"`
	Title        string `json:"title"`
	Body         string `json:"body,omitempty"`
	SourceURL    string `json:"sourceUrl,omitempty"`
	CreatedAt    int64  `json:"createdAt"`
}

// TaskSourcesBody is the response body for GET /tasks/{id}/sources.
type TaskSourcesBody struct {
	Sources []TaskSource `json:"sources"`
}

// TaskSourcesOutput is the response for GET /tasks/{id}/sources.
type TaskSourcesOutput struct {
	Body TaskSourcesBody
}

// RegisterTaskSources wires the read-only reverse source lookup under
// /tasks/{id}/sources. Its caller must place it in the existing task read
// group protected by RequireTaskAccess.
func RegisterTaskSources(api huma.API, deps Deps) {
	huma.Register(api, huma.Operation{
		OperationID: "tasks-sources-list",
		Method:      http.MethodGet,
		Path:        "/tasks/{id}/sources",
		Summary:     "List captured sources for a task",
		Description: "Returns bounded source details from enabled intake items linked to this task. Raw signal payloads are not exposed.",
		Tags:        []string{"Tasks"},
	}, ListTaskSources(deps))
}

// ListTaskSources returns the bounded source records that link back to a
// task. RequireTaskAccess resolves both task and workspace context and checks
// current visibility before this handler can read any source details.
func ListTaskSources(deps Deps) func(context.Context, *TaskSourcesInput) (*TaskSourcesOutput, error) {
	return func(ctx context.Context, in *TaskSourcesInput) (*TaskSourcesOutput, error) {
		task, taskOK := middleware.TaskFromContext(ctx)
		workspace, workspaceOK := middleware.WorkspaceFromContext(ctx)
		if !taskOK || !workspaceOK {
			return nil, httpErr(apierrors.WsTaskNotFound)
		}
		requestedID, err := types.Parse(in.ID)
		if err != nil || requestedID.UUID() != task.PublicID {
			return nil, httpErr(apierrors.WsTaskNotFound)
		}

		rows, err := deps.Queries.ListTaskIntakeSources(ctx, generated.ListTaskIntakeSourcesParams{
			WorkspaceID: workspace.ID,
			TaskID:      sql.NullInt32{Int32: int32(task.ID), Valid: true}, //#nosec G115 -- task context resolves the schema's INT UNSIGNED key
		})
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		out := &TaskSourcesOutput{Body: TaskSourcesBody{Sources: make([]TaskSource, 0, len(rows))}}
		for _, row := range rows {
			detail := sourceintake.ProjectFields(string(row.Source), row.Kind, row.Title, row.Body, row.SourceUrl)
			source := TaskSource{
				IntakeItemID: row.PublicID.String(),
				Source:       string(row.Source),
				Kind:         row.Kind,
				Title:        detail.Title,
				Body:         detail.Body,
				SourceURL:    detail.SourceURL,
				CreatedAt:    row.CreatedAt.Unix(),
			}
			switch value := row.SignalPublicID.(type) {
			case string:
				source.SignalID = value
			case []byte:
				source.SignalID = string(value)
			}
			out.Body.Sources = append(out.Body.Sources, source)
		}
		return out, nil
	}
}
