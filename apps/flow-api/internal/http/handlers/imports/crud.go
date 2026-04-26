package imports

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"

	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/audit"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/types"
	apierrors "github.com/nodate-flow/nodate-flow/apps/flow-api/internal/errors"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/eventbus"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/http/middleware"
	"github.com/nodate-flow/nodate-flow/packages/go-shared/apierr"
	"github.com/nodate-flow/nodate-flow/packages/go-shared/logutil"
)

// Create handles POST /workspaces/{wsId}/imports.
func Create(deps Deps) func(context.Context, *CreateImportInput) (*CreateImportOutput, error) {
	return func(ctx context.Context, in *CreateImportInput) (*CreateImportOutput, error) {
		ws, ok := middleware.WorkspaceFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsWorkspaceNotFound)
		}

		// Resolve optional project public_id to internal id.
		var projectID sql.NullInt32
		if in.Body.ProjectID != nil && *in.Body.ProjectID != "" {
			prjPub, err := types.Parse(*in.Body.ProjectID)
			if err != nil {
				return nil, httpErr(apierrors.WsProjectNotFound)
			}
			prj, err := deps.Queries.FindProjectByPublicId(ctx, generated.FindProjectByPublicIdParams{
				WorkspaceID: ws.ID,
				PublicID:    prjPub,
			})
			if err != nil {
				return nil, httpErr(apierr.SpecForErrNoRows(err, apierrors.WsProjectNotFound, apierrors.InternalUnexpected))
			}
			projectID = sql.NullInt32{Int32: int32(prj.ID), Valid: true}

			// Check for existing running import for this project.
			_, err = deps.Queries.FindRunningImportForProject(ctx, generated.FindRunningImportForProjectParams{
				WorkspaceID: ws.ID,
				ProjectID:   projectID,
			})
			if err == nil {
				// A running import exists.
				return nil, httpErr(apierrors.WsImportAlreadyRunning)
			}
			if !errors.Is(err, sql.ErrNoRows) {
				return nil, httpErr(apierrors.InternalUnexpected)
			}
		}

		actorID, _ := middleware.ActorFromContext(ctx)

		configJSON, err := json.Marshal(in.Body.ConfigJSON)
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		pub := types.New()
		if _, err := deps.Queries.CreateImportJob(ctx, generated.CreateImportJobParams{
			PublicID:          pub,
			WorkspaceID:       ws.ID,
			ProjectID:         projectID,
			InitiatedByUserID: sql.NullInt32{Int32: int32(actorID), Valid: true},
			Source:            generated.ImportJobsSource(in.Body.Source),
			ConfigJson:        configJSON,
		}); err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		if err := eventbus.Append(ctx, deps.DB, eventbus.Event{
			Type:        eventbus.ImportJobCreated,
			WorkspaceID: ws.ID,
			ActorUserID: actorPtr(ctx),
			Payload:     map[string]any{"importJobId": pub.String(), "source": in.Body.Source},
		}); err != nil {
			slog.ErrorContext(ctx, "eventbus.Append failed",
				slog.Any("err", err),
				slog.String("handler", "imports.Create"),
				slog.String("event_type", string(eventbus.ImportJobCreated)),
				logutil.LogEntity("workspace", ws.PublicID),
				slog.String("import_job_public_id", pub.String()),
			)
		}

		if deps.Audit != nil {
			deps.Audit.Record(ctx, audit.Entry{
				Action:       "import.create",
				ActorID:      actorID,
				WorkspaceID:  ws.ID,
				ResourceType: "import_job",
				ResourceID:   pub.String(),
				Metadata:     map[string]any{"source": in.Body.Source},
			})
		}

		row, err := deps.Queries.FindImportJobByPublicId(ctx, generated.FindImportJobByPublicIdParams{
			WorkspaceID: ws.ID,
			PublicID:    pub,
		})
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		return &CreateImportOutput{Body: mapFindRow(row)}, nil
	}
}

// List handles GET /workspaces/{wsId}/imports.
func List(deps Deps) func(context.Context, *ListImportsInput) (*ListImportsOutput, error) {
	return func(ctx context.Context, in *ListImportsInput) (*ListImportsOutput, error) {
		ws, ok := middleware.WorkspaceFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsWorkspaceNotFound)
		}

		limit := in.Limit
		if limit <= 0 {
			limit = 50
		}

		rows, err := deps.Queries.ListImportJobsForWorkspace(ctx, generated.ListImportJobsForWorkspaceParams{
			WorkspaceID: ws.ID,
			Limit:       limit,
			Offset:      in.Offset,
		})
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		out := &ListImportsOutput{}
		out.Body.Items = make([]ImportJobBody, 0, len(rows))
		for _, r := range rows {
			out.Body.Items = append(out.Body.Items, mapListRow(r))
		}
		if len(rows) > 0 {
			out.Body.Total = totalAsInt64(rows[0].Total)
		}
		return out, nil
	}
}

// Get handles GET /workspaces/{wsId}/imports/{importId}.
func Get(deps Deps) func(context.Context, *GetImportInput) (*GetImportOutput, error) {
	return func(ctx context.Context, in *GetImportInput) (*GetImportOutput, error) {
		ws, ok := middleware.WorkspaceFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsWorkspaceNotFound)
		}

		pub, err := types.Parse(in.ImportID)
		if err != nil {
			return nil, httpErr(apierrors.WsImportNotFound)
		}

		row, err := deps.Queries.FindImportJobByPublicId(ctx, generated.FindImportJobByPublicIdParams{
			WorkspaceID: ws.ID,
			PublicID:    pub,
		})
		if err != nil {
			return nil, httpErr(apierr.SpecForErrNoRows(err, apierrors.WsImportNotFound, apierrors.InternalUnexpected))
		}

		return &GetImportOutput{Body: mapFindRow(row)}, nil
	}
}

// Cancel handles POST /workspaces/{wsId}/imports/{importId}/cancel.
func Cancel(deps Deps) func(context.Context, *CancelImportInput) (*CancelImportOutput, error) {
	return func(ctx context.Context, in *CancelImportInput) (*CancelImportOutput, error) {
		ws, ok := middleware.WorkspaceFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsWorkspaceNotFound)
		}

		pub, err := types.Parse(in.ImportID)
		if err != nil {
			return nil, httpErr(apierrors.WsImportNotFound)
		}

		row, err := deps.Queries.FindImportJobByPublicId(ctx, generated.FindImportJobByPublicIdParams{
			WorkspaceID: ws.ID,
			PublicID:    pub,
		})
		if err != nil {
			return nil, httpErr(apierr.SpecForErrNoRows(err, apierrors.WsImportNotFound, apierrors.InternalUnexpected))
		}

		// Only pending or running jobs can be cancelled.
		if row.Status != generated.ImportJobsStatusPending && row.Status != generated.ImportJobsStatusRunning {
			return nil, httpErr(apierrors.WsImportCannotCancel)
		}

		if err := deps.Queries.CancelImportJob(ctx, generated.CancelImportJobParams{
			WorkspaceID: ws.ID,
			ID:          row.ID,
		}); err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		if err := eventbus.Append(ctx, deps.DB, eventbus.Event{
			Type:        eventbus.ImportJobCancelled,
			WorkspaceID: ws.ID,
			ActorUserID: actorPtr(ctx),
			Payload:     map[string]any{"importJobId": pub.String()},
		}); err != nil {
			slog.ErrorContext(ctx, "eventbus.Append failed",
				slog.Any("err", err),
				slog.String("handler", "imports.Cancel"),
				slog.String("event_type", string(eventbus.ImportJobCancelled)),
				logutil.LogEntity("workspace", ws.PublicID),
				slog.String("import_job_public_id", pub.String()),
			)
		}

		if deps.Audit != nil {
			actorID, _ := middleware.ActorFromContext(ctx)
			deps.Audit.Record(ctx, audit.Entry{
				Action:       "import.cancel",
				ActorID:      actorID,
				WorkspaceID:  ws.ID,
				ResourceType: "import_job",
				ResourceID:   pub.String(),
			})
		}

		out := &CancelImportOutput{}
		out.Body.Ok = true
		return out, nil
	}
}
