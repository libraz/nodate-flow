// Package export contains Huma operation handlers for the
// /workspaces/{wsId}/export endpoints (task data export).
package export

import (
	"database/sql"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/audit"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/handlers/handlerutil"
)

// Deps is the dependency bundle passed to each handler in this package.
type Deps struct {
	DB      *sql.DB
	Queries *generated.Queries
	Audit   *audit.Recorder
}

// httpErr delegates to handlerutil.HTTPErr.
var httpErr = handlerutil.HTTPErr

// Input is the input for GET /workspaces/{wsId}/export/tasks.
//
// Limit override: bulk export is the canonical "scan everything once"
// surface; the cap (10000) and default (5000) are deliberately far above
// handlerutil.MaxListLimit so that users can pull a CSV of an entire
// workspace in a single download. Cost is bounded by the DB query
// timeout and the streaming CSV encoder.
type Input struct {
	WsID string `path:"wsId"`
	// This operation answers in JSON and only in JSON. The parameter
	// used to accept csv as well — and defaulted to it — while the
	// handler ignored it and returned JSON regardless, echoing back the
	// format it had not produced. A caller asking for csv therefore got
	// a JSON body that said "csv" and no indication that the file they
	// wanted lives at a different path. Declaring the one value the
	// route can actually serve turns that into a rejection naming the
	// CSV route instead of a wrong answer wearing the right label.
	Format string `query:"format" enum:"json" default:"json" doc:"Export format. This route serves JSON; the CSV download is GET /workspaces/{wsId}/export/tasks.csv"`
	LensID string `query:"lensId,omitempty" doc:"Optional lens public id to scope the export"`
	Limit  int32  `query:"limit" minimum:"1" maximum:"10000" default:"5000" doc:"Maximum number of rows to export"`
}

// RowCountHeader carries the number of task rows the CSV download
// contains. It is how a caller tells a complete export from one that
// stopped at the row ceiling, which the file itself cannot say.
const RowCountHeader = "X-Export-Row-Count"

// CSVInput is the request surface of the CSV download. It carries no
// format parameter: the route is the format.
type CSVInput struct {
	WsID   string `path:"wsId"`
	LensID string `query:"lensId,omitempty" doc:"Optional lens public id to scope the export"`
	Limit  int32  `query:"limit" minimum:"1" maximum:"10000" default:"5000" doc:"Maximum number of rows to export"`
}

// ExportedTask is the public DTO for a single exported task row.
type ExportedTask struct {
	ID                  string  `json:"id" doc:"Task public id (UUID v7)"`
	Title               string  `json:"title"`
	Description         *string `json:"description,omitempty"`
	Status              string  `json:"status" doc:"Derived state (open, waiting, review, etc.)"`
	Priority            int32   `json:"priority"`
	DueOn               *string `json:"dueOn,omitempty" doc:"Due date (YYYY-MM-DD)"`
	StartedOn           *string `json:"startedOn,omitempty" doc:"Started date (YYYY-MM-DD)"`
	CompletedAt         *int64  `json:"completedAt,omitempty" doc:"Completion time (unix seconds)"`
	ProjectID           string  `json:"projectId" doc:"Project public id (UUID v7)"`
	ProjectName         string  `json:"projectName"`
	AssigneeID          *string `json:"assigneeId,omitempty" doc:"Assignee public id (UUID v7)"`
	AssigneeDisplayName *string `json:"assigneeDisplayName,omitempty"`
	UpdatedAt           *int64  `json:"updatedAt,omitempty" doc:"Last update time (unix seconds)"`
	CreatedAt           int64   `json:"createdAt" doc:"Creation time (unix seconds)"`
}

// Body is the response body for the JSON export format.
type Body struct {
	Format string         `json:"format" doc:"Export format used"`
	Count  int            `json:"count" doc:"Number of exported rows"`
	Tasks  []ExportedTask `json:"tasks"`
}

// Output is the Huma response wrapper.
type Output struct {
	Body Body
}
