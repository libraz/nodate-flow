// MCP tools over import jobs: listing a workspace's jobs with a status
// filter, and queueing a new import.

package mcp

import (
	"context"
	"database/sql"
	"encoding/json"
	stderrors "errors"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/eventbus"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/importer"
)

// importJobScanPage is how many rows a status-filtered listing reads per
// round trip, and importJobScanCap how many it will read in total.
//
// The underlying query takes no status parameter, so a filtered listing
// has to do the selection itself. Doing it after LIMIT/OFFSET — which is
// what this tool used to do — answers "are there failed imports?" with
// "no" whenever the failures are older than the first page, which for a
// failure is exactly when they are. Reading pages until the requested
// window is filled puts the filter back in front of the pagination.
//
// The cap bounds the work: past it the tool reports what it found and
// says so rather than reading a workspace's entire import history to
// answer one question. Pushing the predicate into the query is the
// durable fix and belongs in sql/queries/imports/, next to the status
// parameter ListIntakeItemsForWorkspace already has.
const (
	importJobScanPage = 200
	importJobScanCap  = 5000
)

func runListImportJobs(ctx context.Context, deps Deps, s *session, raw json.RawMessage) (any, error) {
	if _, err := requireWorkspaceMember(ctx, deps, s); err != nil {
		return nil, err
	}
	var in struct {
		Status string `json:"status"`
		Limit  int32  `json:"limit"`
		Offset int32  `json:"offset"`
	}
	if err := parseArgs(raw, &in); err != nil {
		return nil, err
	}
	if in.Limit <= 0 || in.Limit > 200 {
		in.Limit = 50
	}
	if in.Offset < 0 {
		in.Offset = 0
	}

	rows, total, truncated, err := listImportJobRows(ctx, deps, s, in.Status, in.Limit, in.Offset)
	if err != nil {
		return nil, err
	}

	type jobOut struct {
		ID             string `json:"id"`
		Source         string `json:"source"`
		Status         string `json:"status"`
		TotalItems     int    `json:"totalItems"`
		ProcessedItems int    `json:"processedItems"`
		FailedItems    int    `json:"failedItems"`
		StartedAt      *int64 `json:"startedAt,omitempty"`
		CompletedAt    *int64 `json:"completedAt,omitempty"`
		CreatedAt      int64  `json:"createdAt"`
	}
	out := make([]jobOut, 0, len(rows))
	for _, r := range rows {
		j := jobOut{
			ID:             r.PublicID.String(),
			Source:         string(r.Source),
			Status:         string(r.Status),
			TotalItems:     int(r.TotalItems),
			ProcessedItems: int(r.ProcessedItems),
			FailedItems:    int(r.FailedItems),
			CreatedAt:      r.CreatedAt.Unix(),
		}
		if r.StartedAt.Valid {
			v := r.StartedAt.Time.Unix()
			j.StartedAt = &v
		}
		if r.CompletedAt.Valid {
			v := r.CompletedAt.Time.Unix()
			j.CompletedAt = &v
		}
		out = append(out, j)
	}
	// total travels with the page so a caller can tell "there are none"
	// from "there are more than you asked for", which an agent otherwise
	// reports to its user as a fact about the workspace.
	res := map[string]any{"jobs": out, "total": total}
	if truncated {
		res["truncated"] = true
	}
	return res, nil
}

// listImportJobRows returns one page of import jobs, applying the status
// filter before the offset/limit window rather than after it. The third
// result reports that the scan cap was reached, so the caller can say the
// count is a floor instead of presenting it as complete.
func listImportJobRows(
	ctx context.Context, deps Deps, s *session, status string, limit, offset int32,
) ([]generated.ListImportJobsForWorkspaceRow, int64, bool, error) {
	if status == "" {
		rows, err := deps.Queries.ListImportJobsForWorkspace(ctx, generated.ListImportJobsForWorkspaceParams{
			WorkspaceID: s.workspaceID,
			Limit:       limit,
			Offset:      offset,
		})
		if err != nil {
			return nil, 0, false, apierrors.Wrap(apierrors.McpToolExecutionFailed, err)
		}
		var total int64
		if len(rows) > 0 {
			total = countValue(rows[0].Total)
		}
		return rows, total, false, nil
	}

	var (
		matched  []generated.ListImportJobsForWorkspaceRow
		total    int64
		scanned  int32
		scanFrom int32
	)
	for scanned < importJobScanCap {
		page, err := deps.Queries.ListImportJobsForWorkspace(ctx, generated.ListImportJobsForWorkspaceParams{
			WorkspaceID: s.workspaceID,
			Limit:       importJobScanPage,
			Offset:      scanFrom,
		})
		if err != nil {
			return nil, 0, false, apierrors.Wrap(apierrors.McpToolExecutionFailed, err)
		}
		for _, r := range page {
			if string(r.Status) != status {
				continue
			}
			total++
			if total > int64(offset) && int32(len(matched)) < limit { //#nosec G115 -- matched is capped by limit, itself bounded to 200 by the caller
				matched = append(matched, r)
			}
		}
		read := int32(len(page)) //#nosec G115 -- a page holds at most importJobScanPage rows
		scanned += read
		scanFrom += read
		if len(page) < importJobScanPage {
			return matched, total, false, nil
		}
	}
	return matched, total, true, nil
}

// countValue reads a COUNT(*) OVER() column, which the driver hands back
// as int64 or as the decimal text of one depending on the connection's
// prepared-statement mode.
func countValue(v any) int64 {
	switch x := v.(type) {
	case int64:
		return x
	case int32:
		return int64(x)
	case int:
		return int64(x)
	case uint64:
		return int64(x) //#nosec G115 -- COUNT(*) result, bounded by workspace size
	case []byte:
		var n int64
		for _, c := range x {
			if c < '0' || c > '9' {
				return n
			}
			n = n*10 + int64(c-'0')
		}
		return n
	default:
		return 0
	}
}

// parseImportConfig turns the tool's configJson argument into the blob
// the row stores, refusing the same values REST refuses.
//
// The check has to live on this path too, not only on the handler.
// config_json is plaintext and read back by the job endpoints, so the
// rule that keeps a credential out of it is worth exactly as much as
// its weakest entry point — and MCP is not a back door here, it is a
// front one: an agent is the caller most likely to be handed a token
// and told to "import from GitHub". Validating only the REST path would
// have left the column open through the surface the product points
// agents at.
//
// Decoding into an object rather than calling json.Valid also brings
// this in line with REST, whose body type is an object: a bare array or
// string was accepted here and would then fail in the worker, which
// reads config_json as an object.
func parseImportConfig(source, raw string) (json.RawMessage, error) {
	const empty = `{}`
	if raw == "" {
		return json.RawMessage(empty), nil
	}
	var cfg map[string]any
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		return nil, apierrors.New(apierrors.McpToolArgumentsInvalid)
	}
	if cfg == nil {
		return json.RawMessage(empty), nil
	}
	if err := importer.ValidateConfig(source, cfg); err != nil {
		if stderrors.Is(err, importer.ErrConfigKeySecret) {
			return nil, apierrors.New(apierrors.WsImportConfigSecretRejected)
		}
		return nil, apierrors.New(apierrors.WsImportConfigKeyUnknown)
	}
	return json.RawMessage(raw), nil
}

func runCreateImportJob(ctx context.Context, deps Deps, s *session, raw json.RawMessage) (any, error) {
	if _, err := requireWorkspaceMember(ctx, deps, s); err != nil {
		return nil, err
	}
	var in struct {
		Source     string `json:"source"`
		ProjectID  string `json:"projectId"`
		ConfigJSON string `json:"configJson"`
	}
	if err := parseArgs(raw, &in); err != nil {
		return nil, err
	}
	if in.Source == "" {
		return nil, apierrors.New(apierrors.McpToolArgumentsInvalid)
	}
	// Validate source value.
	switch in.Source {
	case "github", "jira", "linear", "csv":
		// ok
	default:
		return nil, apierrors.New(apierrors.McpToolArgumentsInvalid)
	}

	// Before any lookup: config_json is stored in plaintext, so a
	// credential that gets past here has already been written down.
	configJSON, err := parseImportConfig(in.Source, in.ConfigJSON)
	if err != nil {
		return nil, err
	}

	var projectID sql.NullInt32
	if in.ProjectID != "" {
		prjPub, err := types.Parse(in.ProjectID)
		if err != nil {
			return nil, apierrors.New(apierrors.McpToolArgumentsInvalid)
		}
		prj, err := deps.Queries.FindProjectByPublicId(ctx, generated.FindProjectByPublicIdParams{
			WorkspaceID: s.workspaceID,
			PublicID:    prjPub,
		})
		if err != nil {
			if stderrors.Is(err, sql.ErrNoRows) {
				return nil, apierrors.New(apierrors.WsProjectNotFound)
			}
			return nil, apierrors.Wrap(apierrors.McpToolExecutionFailed, err)
		}
		projectID = sql.NullInt32{Int32: int32(prj.ID), Valid: true} //#nosec G115 -- project id is projects.id (BIGINT UNSIGNED), fits int32 within realistic deployments

		// One import at a time per project, the same rule REST enforces.
		// Two concurrent imports of the same source into the same project
		// produce duplicate tasks that nobody can tell apart afterwards,
		// and an agent is far more likely than a person to start the
		// second one — it does not see the progress bar that stops a human.
		_, err = deps.Queries.FindRunningImportForProject(ctx, generated.FindRunningImportForProjectParams{
			WorkspaceID: s.workspaceID,
			ProjectID:   projectID,
		})
		if err == nil {
			return nil, apierrors.New(apierrors.WsImportAlreadyRunning)
		}
		if !stderrors.Is(err, sql.ErrNoRows) {
			return nil, apierrors.Wrap(apierrors.McpToolExecutionFailed, err)
		}
	}

	pub := newPublicID()
	_, err = deps.Queries.CreateImportJob(ctx, generated.CreateImportJobParams{
		PublicID:          pub,
		WorkspaceID:       s.workspaceID,
		ProjectID:         projectID,
		InitiatedByUserID: sql.NullInt32{Int32: int32(s.userID), Valid: true}, //#nosec G115 -- session user id is users.id (BIGINT UNSIGNED), fits int32 within realistic deployments
		Source:            generated.ImportJobsSource(in.Source),
		ConfigJson:        configJSON,
	})
	if err != nil {
		return nil, apierrors.Wrap(apierrors.McpToolExecutionFailed, err)
	}

	// The job row is committed and the worker will pick it up; a retry
	// would queue a second import of the same source.
	recordMutation(ctx, deps, s, mutation{
		EventType:    eventbus.ImportJobCreated,
		AuditAction:  "import.create",
		ResourceType: "import_job",
		ResourceID:   pub.String(),
		Payload: map[string]any{
			"importJobId": pub.String(),
			"source":      in.Source,
			"via":         "mcp",
		},
		CallSite: "mcp.create_import_job",
	})

	return map[string]any{"ok": true, "importJobId": pub.String()}, nil
}
