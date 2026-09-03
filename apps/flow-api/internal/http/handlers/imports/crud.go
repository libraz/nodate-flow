package imports

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/audit"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/dbretry"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/eventbus"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/middleware"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/importer"
	"github.com/libraz/nodate-flow/packages/go-shared/apierr"
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
			projectID = sql.NullInt32{Int32: int32(prj.ID), Valid: true} //#nosec G115 -- project_id is projects.id (BIGINT UNSIGNED), fits int32 within realistic deployments

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

		// Validate before marshalling: config_json is stored as typed,
		// so a credential that reaches this point has already been
		// written down in plaintext by the time anything else notices.
		if err := checkImportConfig(in.Body); err != nil {
			return nil, err
		}
		configJSON, err := json.Marshal(in.Body.ConfigJSON)
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		if err := checkCSVPayloadSize(in.Body); err != nil {
			return nil, err
		}

		pub := types.New()
		if _, err := deps.Queries.CreateImportJob(ctx, generated.CreateImportJobParams{
			PublicID:          pub,
			WorkspaceID:       ws.ID,
			ProjectID:         projectID,
			InitiatedByUserID: sql.NullInt32{Int32: int32(actorID), Valid: true}, //#nosec G115 -- actor user id sourced from session, fits int32 within realistic deployments
			Source:            generated.ImportJobsSource(in.Body.Source),
			ConfigJson:        configJSON,
		}); err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		eventbus.AppendBestEffort(ctx, dbretry.AutoCommit(deps.DB), eventbus.Event{
			Type:        eventbus.ImportJobCreated,
			WorkspaceID: ws.ID,
			ActorUserID: actorPtr(ctx),
			Payload:     map[string]any{"importJobId": pub.String(), "source": in.Body.Source},
		}, "imports.Create")

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

		eventbus.AppendBestEffort(ctx, dbretry.AutoCommit(deps.DB), eventbus.Event{
			Type:        eventbus.ImportJobCancelled,
			WorkspaceID: ws.ID,
			ActorUserID: actorPtr(ctx),
			Payload:     map[string]any{"importJobId": pub.String()},
		}, "imports.Cancel")

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

// checkCSVPayloadSize rejects a csv job whose payload is over the
// importer's ceiling, before the row is written.
//
// The check belongs here rather than in the worker because the payload
// travels in config_json: accepting the request stores the whole body in
// the queue row, and telling the user it was too big only after that
// costs a round trip and leaves a megabyte of rejected data in the
// table. The worker enforces the row-count ceiling, which needs a parse
// and cannot be answered from the request alone.
// checkImportConfig refuses a configuration that names a credential or
// a setting the source does not have, and maps the two apart so the
// caller is told which one it hit.
func checkImportConfig(body CreateImportBody) error {
	err := importer.ValidateConfig(body.Source, body.ConfigJSON)
	switch {
	case err == nil:
		return nil
	case errors.Is(err, importer.ErrConfigKeySecret):
		return httpErr(apierrors.WsImportConfigSecretRejected)
	default:
		return httpErr(apierrors.WsImportConfigKeyUnknown)
	}
}

func checkCSVPayloadSize(body CreateImportBody) error {
	if body.Source != string(generated.ImportJobsSourceCsv) || body.ConfigJSON == nil {
		return nil
	}
	payload, ok := body.ConfigJSON["csv"].(string)
	if !ok {
		return nil
	}
	if len(payload) > importer.MaxCSVBytes {
		return httpErr(apierrors.ValidationFileTooLarge)
	}
	return nil
}
