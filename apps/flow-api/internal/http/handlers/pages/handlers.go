package pages

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"

	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/ai"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/audit"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/types"
	apierrors "github.com/nodate-flow/nodate-flow/apps/flow-api/internal/errors"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/eventbus"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/http/handlers/handlerutil"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/http/middleware"
	"github.com/nodate-flow/nodate-flow/packages/go-shared/apierr"
	"github.com/nodate-flow/nodate-flow/packages/go-shared/logutil"
	"github.com/nodate-flow/nodate-flow/packages/go-shared/stringutil"
)

// actorPtr delegates to handlerutil.ActorPtr.
var actorPtr = handlerutil.ActorPtr

// isDuplicateEntry delegates to handlerutil.IsDuplicateEntry.
var isDuplicateEntry = handlerutil.IsDuplicateEntry

// resolvePageInternal looks up the internal page id by workspace_id + public_id.
func resolvePageInternal(ctx context.Context, db *sql.DB, wsID uint32, pub types.PublicID) (uint32, error) {
	const q = `SELECT id FROM pages WHERE workspace_id = ? AND public_id = ? AND enabled = TRUE LIMIT 1`
	var id uint32
	if err := db.QueryRowContext(ctx, q, wsID, pub).Scan(&id); err != nil {
		return 0, httpErr(apierr.SpecForErrNoRows(err, apierrors.PagePageNotFound, apierrors.InternalUnexpected))
	}
	return id, nil
}

// checkAncestorDepthAndCircular walks up the ancestor chain from the
// proposed parent, verifying that:
//  1. The chain does not exceed MaxPageDepth.
//  2. The target page (selfID) is not an ancestor (circular reference).
//
// selfID may be 0 for create operations (no circularity check needed).
func checkAncestorDepthAndCircular(ctx context.Context, db *sql.DB, wsID uint32, parentInternalID uint32, selfID uint32) error {
	const q = `SELECT id, parent_page_id FROM pages WHERE workspace_id = ? AND id = ? AND enabled = TRUE LIMIT 1`
	currentID := parentInternalID
	depth := 1 // starting at depth 1 since we will be a child of parentInternalID

	for depth <= MaxPageDepth {
		if selfID != 0 && currentID == selfID {
			return httpErr(apierrors.PagePageCircularParent)
		}
		var id uint32
		var parentID sql.NullInt32
		err := db.QueryRowContext(ctx, q, wsID, currentID).Scan(&id, &parentID)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				// Reached a page that doesn't exist or is disabled; treat as root.
				break
			}
			return httpErr(apierrors.InternalUnexpected)
		}
		if !parentID.Valid {
			// Reached root.
			break
		}
		currentID = uint32(parentID.Int32) //#nosec G115 -- parent_id is pages.id (BIGINT UNSIGNED), fits uint32 within realistic deployments
		depth++
	}
	if depth > MaxPageDepth {
		return httpErr(apierrors.PagePageMaxDepth)
	}
	return nil
}

// resolveProjectInternal resolves a project public id to its internal id
// within the given workspace.
func resolveProjectInternal(ctx context.Context, q *generated.Queries, wsID uint32, projectPublicID string) (sql.NullInt32, error) {
	pid, err := types.Parse(projectPublicID)
	if err != nil {
		return sql.NullInt32{}, httpErr(apierrors.ValidationPathParamInvalid)
	}
	row, err := q.FindProjectByPublicId(ctx, generated.FindProjectByPublicIdParams{
		WorkspaceID: wsID,
		PublicID:    pid,
	})
	if err != nil {
		return sql.NullInt32{}, httpErr(apierr.SpecForErrNoRows(err, apierrors.WsProjectNotFound, apierrors.InternalUnexpected))
	}
	return sql.NullInt32{Int32: int32(row.ID), Valid: true}, nil //#nosec G115 -- parent page id is pages.id (BIGINT UNSIGNED), fits int32 within realistic deployments
}

// Create handles POST /workspaces/{wsId}/pages.
func Create(deps Deps) func(context.Context, *CreatePageInput) (*CreatePageOutput, error) {
	return func(ctx context.Context, in *CreatePageInput) (*CreatePageOutput, error) {
		ws, ok := middleware.WorkspaceFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsWorkspaceNotFound)
		}
		actorID, ok := middleware.ActorFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsMemberRoleDenied)
		}

		// Resolve optional project.
		var projectID sql.NullInt32
		if in.Body.ProjectID != nil && *in.Body.ProjectID != "" {
			var err error
			projectID, err = resolveProjectInternal(ctx, deps.Queries, ws.ID, *in.Body.ProjectID)
			if err != nil {
				return nil, err
			}
		}

		// Resolve optional parent page and validate depth.
		var parentPageID sql.NullInt32
		if in.Body.ParentPageID != nil && *in.Body.ParentPageID != "" {
			parentPub, err := types.Parse(*in.Body.ParentPageID)
			if err != nil {
				return nil, httpErr(apierrors.ValidationPathParamInvalid)
			}
			parentInternal, err := resolvePageInternal(ctx, deps.DB, ws.ID, parentPub)
			if err != nil {
				return nil, err
			}
			// Validate depth (selfID=0 for create, no circularity check needed).
			if err := checkAncestorDepthAndCircular(ctx, deps.DB, ws.ID, parentInternal, 0); err != nil {
				return nil, err
			}
			parentPageID = sql.NullInt32{Int32: int32(parentInternal), Valid: true} //#nosec G115 -- parent page id is pages.id (BIGINT UNSIGNED), fits int32 within realistic deployments
		}

		pub := types.New()
		_, err := deps.Queries.CreatePage(ctx, generated.CreatePageParams{
			PublicID:      pub,
			WorkspaceID:   ws.ID,
			ProjectID:     projectID,
			CreatorID:     actorID,
			ParentPageID:  parentPageID,
			Title:         in.Body.Title,
			Body:          in.Body.Body,
			IsAiGenerated: false,
		})
		if err != nil {
			if isDuplicateEntry(err) {
				return nil, httpErr(apierrors.PagePageTitleTaken)
			}
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		if err := eventbus.Append(ctx, deps.DB, eventbus.Event{
			Type:        eventbus.PageCreated,
			WorkspaceID: ws.ID,
			ActorUserID: actorPtr(ctx),
			Payload: map[string]any{
				"pageId": pub.String(),
				"title":  in.Body.Title,
			},
		}); err != nil {
			slog.ErrorContext(ctx, "eventbus.Append failed",
				slog.Any("err", err),
				slog.String("handler", "pages.Create"),
				slog.String("event_type", string(eventbus.PageCreated)),
				logutil.LogEntity("workspace", ws.PublicID),
				slog.String("page_public_id", pub.String()),
			)
		}

		deps.Audit.Record(ctx, audit.Entry{
			Action:       "page.create",
			ActorID:      actorID,
			WorkspaceID:  ws.ID,
			ResourceType: "page",
			ResourceID:   pub.String(),
			Metadata:     map[string]any{"title": in.Body.Title},
		})

		// Re-fetch to return the full DTO with joined fields.
		row, err := deps.Queries.GetPageByPublicId(ctx, generated.GetPageByPublicIdParams{
			WorkspaceID: ws.ID,
			PublicID:    pub,
		})
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		return &CreatePageOutput{Body: mapGetRow(row)}, nil
	}
}

// List handles GET /workspaces/{wsId}/pages (root pages only).
func List(deps Deps) func(context.Context, *ListPagesInput) (*ListPagesOutput, error) {
	return func(ctx context.Context, in *ListPagesInput) (*ListPagesOutput, error) {
		ws, ok := middleware.WorkspaceFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsWorkspaceNotFound)
		}

		limit := in.Limit
		if limit <= 0 {
			limit = 50
		}

		rows, err := deps.Queries.ListPagesForWorkspace(ctx, generated.ListPagesForWorkspaceParams{
			WorkspaceID: ws.ID,
			Limit:       limit,
			Offset:      in.Offset,
		})
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		out := &ListPagesOutput{}
		out.Body.Pages = make([]PageSummaryDTO, 0, len(rows))
		for _, r := range rows {
			out.Body.Pages = append(out.Body.Pages, mapWorkspaceListRow(r))
		}
		if len(rows) > 0 {
			out.Body.Total = totalAsInt64(rows[0].Total)
		}
		return out, nil
	}
}

// ListChildren handles GET /workspaces/{wsId}/pages/{pageId}/children.
func ListChildren(deps Deps) func(context.Context, *ListChildPagesInput) (*ListChildPagesOutput, error) {
	return func(ctx context.Context, in *ListChildPagesInput) (*ListChildPagesOutput, error) {
		ws, ok := middleware.WorkspaceFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsWorkspaceNotFound)
		}

		pub, err := types.Parse(in.PageID)
		if err != nil {
			return nil, httpErr(apierrors.ValidationPathParamInvalid)
		}
		parentInternal, err := resolvePageInternal(ctx, deps.DB, ws.ID, pub)
		if err != nil {
			return nil, err
		}

		limit := in.Limit
		if limit <= 0 {
			limit = 50
		}

		rows, err := deps.Queries.ListChildPages(ctx, generated.ListChildPagesParams{
			WorkspaceID:  ws.ID,
			ParentPageID: sql.NullInt32{Int32: int32(parentInternal), Valid: true}, //#nosec G115 -- parent page id is pages.id (BIGINT UNSIGNED), fits int32 within realistic deployments
			Limit:        limit,
			Offset:       in.Offset,
		})
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		out := &ListChildPagesOutput{}
		out.Body.Pages = make([]PageSummaryDTO, 0, len(rows))
		for _, r := range rows {
			out.Body.Pages = append(out.Body.Pages, mapChildListRow(r))
		}
		if len(rows) > 0 {
			out.Body.Total = totalAsInt64(rows[0].Total)
		}
		return out, nil
	}
}

// Get handles GET /workspaces/{wsId}/pages/{pageId}.
func Get(deps Deps) func(context.Context, *GetPageInput) (*GetPageOutput, error) {
	return func(ctx context.Context, in *GetPageInput) (*GetPageOutput, error) {
		ws, ok := middleware.WorkspaceFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsWorkspaceNotFound)
		}
		pub, err := types.Parse(in.PageID)
		if err != nil {
			return nil, httpErr(apierrors.ValidationPathParamInvalid)
		}
		row, err := deps.Queries.GetPageByPublicId(ctx, generated.GetPageByPublicIdParams{
			WorkspaceID: ws.ID,
			PublicID:    pub,
		})
		if err != nil {
			return nil, httpErr(apierr.SpecForErrNoRows(err, apierrors.PagePageNotFound, apierrors.InternalUnexpected))
		}
		return &GetPageOutput{Body: mapGetRow(row)}, nil
	}
}

// Update handles PATCH /workspaces/{wsId}/pages/{pageId}.
func Update(deps Deps) func(context.Context, *UpdatePageInput) (*UpdatePageOutput, error) {
	return func(ctx context.Context, in *UpdatePageInput) (*UpdatePageOutput, error) {
		ws, ok := middleware.WorkspaceFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsWorkspaceNotFound)
		}
		pub, err := types.Parse(in.PageID)
		if err != nil {
			return nil, httpErr(apierrors.ValidationPathParamInvalid)
		}

		// Fetch existing page to get internal ID and current state.
		existing, err := deps.Queries.GetPageByPublicId(ctx, generated.GetPageByPublicIdParams{
			WorkspaceID: ws.ID,
			PublicID:    pub,
		})
		if err != nil {
			return nil, httpErr(apierr.SpecForErrNoRows(err, apierrors.PagePageNotFound, apierrors.InternalUnexpected))
		}

		// Build update params with COALESCE-compatible nullables.
		updateParams := generated.UpdatePageParams{
			WorkspaceID: ws.ID,
			PublicID:    pub,
		}

		// Title: send non-null to update, null to keep existing (COALESCE).
		if in.Body.Title != nil {
			updateParams.Title = sql.NullString{String: *in.Body.Title, Valid: true}
		}

		// Body: send non-null to update, null to keep existing (COALESCE).
		if in.Body.Body != nil {
			updateParams.Body = sql.NullString{String: *in.Body.Body, Valid: true}
		}

		// Project: resolve if provided.
		if in.Body.ProjectID != nil {
			if *in.Body.ProjectID == "" {
				// Explicitly unset project.
				updateParams.ProjectID = sql.NullInt32{}
			} else {
				projectID, err := resolveProjectInternal(ctx, deps.Queries, ws.ID, *in.Body.ProjectID)
				if err != nil {
					return nil, err
				}
				updateParams.ProjectID = projectID
			}
		}

		// Parent page: resolve and validate depth + circularity if provided.
		if in.Body.ParentPageID != nil {
			if *in.Body.ParentPageID == "" {
				// Explicitly unset parent (make it a root page).
				updateParams.ParentPageID = sql.NullInt32{}
			} else {
				parentPub, err := types.Parse(*in.Body.ParentPageID)
				if err != nil {
					return nil, httpErr(apierrors.ValidationPathParamInvalid)
				}
				parentInternal, err := resolvePageInternal(ctx, deps.DB, ws.ID, parentPub)
				if err != nil {
					return nil, err
				}
				// Cannot set parent to self.
				if parentInternal == existing.ID {
					return nil, httpErr(apierrors.PagePageCircularParent)
				}
				// Check that the proposed parent is not a descendant and depth is valid.
				if err := checkAncestorDepthAndCircular(ctx, deps.DB, ws.ID, parentInternal, existing.ID); err != nil {
					return nil, err
				}
				updateParams.ParentPageID = sql.NullInt32{Int32: int32(parentInternal), Valid: true} //#nosec G115 -- parent page id is pages.id (BIGINT UNSIGNED), fits int32 within realistic deployments
			}
		}

		if err := deps.Queries.UpdatePage(ctx, updateParams); err != nil {
			if isDuplicateEntry(err) {
				return nil, httpErr(apierrors.PagePageTitleTaken)
			}
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		if err := eventbus.Append(ctx, deps.DB, eventbus.Event{
			Type:        eventbus.PageUpdated,
			WorkspaceID: ws.ID,
			ActorUserID: actorPtr(ctx),
			Payload: map[string]any{
				"pageId": pub.String(),
			},
		}); err != nil {
			slog.ErrorContext(ctx, "eventbus.Append failed",
				slog.Any("err", err),
				slog.String("handler", "pages.Update"),
				slog.String("event_type", string(eventbus.PageUpdated)),
				logutil.LogEntity("workspace", ws.PublicID),
				slog.String("page_public_id", pub.String()),
			)
		}

		if actorID, ok := middleware.ActorFromContext(ctx); ok {
			deps.Audit.Record(ctx, audit.Entry{
				Action:       "page.update",
				ActorID:      actorID,
				WorkspaceID:  ws.ID,
				ResourceType: "page",
				ResourceID:   pub.String(),
			})
		}

		// Re-fetch to return the updated row.
		updated, err := deps.Queries.GetPageByPublicId(ctx, generated.GetPageByPublicIdParams{
			WorkspaceID: ws.ID,
			PublicID:    pub,
		})
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		return &UpdatePageOutput{Body: mapGetRow(updated)}, nil
	}
}

// Delete handles DELETE /workspaces/{wsId}/pages/{pageId}.
// Children become root pages (DB handles via ON DELETE SET NULL on parent_page_id).
func Delete(deps Deps) func(context.Context, *DeletePageInput) (*DeletePageOutput, error) {
	return func(ctx context.Context, in *DeletePageInput) (*DeletePageOutput, error) {
		ws, ok := middleware.WorkspaceFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsWorkspaceNotFound)
		}
		pub, err := types.Parse(in.PageID)
		if err != nil {
			return nil, httpErr(apierrors.ValidationPathParamInvalid)
		}
		if err := deps.Queries.DisablePage(ctx, generated.DisablePageParams{
			WorkspaceID: ws.ID,
			PublicID:    pub,
		}); err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		if err := eventbus.Append(ctx, deps.DB, eventbus.Event{
			Type:        eventbus.PageDisabled,
			WorkspaceID: ws.ID,
			ActorUserID: actorPtr(ctx),
			Payload: map[string]any{
				"pageId": pub.String(),
			},
		}); err != nil {
			slog.ErrorContext(ctx, "eventbus.Append failed",
				slog.Any("err", err),
				slog.String("handler", "pages.Delete"),
				slog.String("event_type", string(eventbus.PageDisabled)),
				logutil.LogEntity("workspace", ws.PublicID),
				slog.String("page_public_id", pub.String()),
			)
		}

		if actorID, ok := middleware.ActorFromContext(ctx); ok {
			deps.Audit.Record(ctx, audit.Entry{
				Action:       "page.delete",
				ActorID:      actorID,
				WorkspaceID:  ws.ID,
				ResourceType: "page",
				ResourceID:   pub.String(),
			})
		}

		out := &DeletePageOutput{}
		out.Body.Ok = true
		return out, nil
	}
}

// Search handles GET /workspaces/{wsId}/pages/search?q=...
func Search(deps Deps) func(context.Context, *SearchPagesInput) (*SearchPagesOutput, error) {
	return func(ctx context.Context, in *SearchPagesInput) (*SearchPagesOutput, error) {
		ws, ok := middleware.WorkspaceFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsWorkspaceNotFound)
		}

		limit := in.Limit
		if limit <= 0 {
			limit = 50
		}

		// Wrap the search term for SQL LIKE with metacharacter escaping.
		pattern := "%" + stringutil.EscapeLike(in.Q) + "%"

		rows, err := deps.Queries.SearchPages(ctx, generated.SearchPagesParams{
			WorkspaceID: ws.ID,
			Title:       pattern,
			Limit:       limit,
			Offset:      in.Offset,
		})
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		out := &SearchPagesOutput{}
		out.Body.Pages = make([]PageSummaryDTO, 0, len(rows))
		for _, r := range rows {
			out.Body.Pages = append(out.Body.Pages, mapSearchRow(r))
		}
		if len(rows) > 0 {
			out.Body.Total = totalAsInt64(rows[0].Total)
		}
		return out, nil
	}
}

// GenerateWithAI handles POST /workspaces/{wsId}/pages/generate. It
// resolves the workspace's default LLM provider via deps.Generator,
// asks it to draft a page body from the supplied title + prompt, and
// persists the result as a new AI-generated page (is_ai_generated=TRUE)
// optionally scoped to a project. The new page's full DTO is returned
// so the client can route directly into the editor without a follow-up
// fetch.
//
// Error mapping:
//   - no provider configured for the workspace → AI.PROVIDER.NOT_CONFIGURED
//   - upstream call failed (timeout / network / non-2xx) → PAGE.GENERATION.UPSTREAM_UNAVAILABLE
//   - DB insert failed (title clash) → PAGE.PAGE.TITLE_TAKEN
//   - any other internal failure → INTERNAL.UNEXPECTED
func GenerateWithAI(deps Deps) func(context.Context, *GeneratePageInput) (*GeneratePageOutput, error) {
	return func(ctx context.Context, in *GeneratePageInput) (*GeneratePageOutput, error) {
		ws, ok := middleware.WorkspaceFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsWorkspaceNotFound)
		}
		actorID, ok := middleware.ActorFromContext(ctx)
		if !ok {
			return nil, httpErr(apierrors.WsMemberRoleDenied)
		}

		if deps.Generator == nil {
			return nil, httpErr(apierrors.AiProviderNotConfigured)
		}

		// Resolve optional project scope before the LLM call so a bad
		// project id fails fast instead of consuming a token budget.
		var projectID sql.NullInt32
		if in.Body.ProjectID != nil && *in.Body.ProjectID != "" {
			var resolveErr error
			projectID, resolveErr = resolveProjectInternal(ctx, deps.Queries, ws.ID, *in.Body.ProjectID)
			if resolveErr != nil {
				return nil, resolveErr
			}
		}

		body, err := deps.Generator.GeneratePageBody(ctx, ws.ID, in.Body.Title, in.Body.Prompt)
		if err != nil {
			switch {
			case errors.Is(err, ai.ErrNoProvider):
				return nil, httpErr(apierrors.AiProviderNotConfigured)
			default:
				slog.WarnContext(ctx, "pages.GenerateWithAI: provider call failed",
					slog.Any("err", err),
					logutil.LogEntity("workspace", ws.PublicID),
				)
				return nil, httpErr(apierrors.PageGenerationUpstreamUnavailable)
			}
		}

		pub := types.New()
		if _, err := deps.Queries.CreatePage(ctx, generated.CreatePageParams{
			PublicID:      pub,
			WorkspaceID:   ws.ID,
			ProjectID:     projectID,
			CreatorID:     actorID,
			ParentPageID:  sql.NullInt32{},
			Title:         in.Body.Title,
			Body:          body,
			IsAiGenerated: true,
		}); err != nil {
			if isDuplicateEntry(err) {
				return nil, httpErr(apierrors.PagePageTitleTaken)
			}
			return nil, httpErr(apierrors.InternalUnexpected)
		}

		if err := eventbus.Append(ctx, deps.DB, eventbus.Event{
			Type:        eventbus.PageCreated,
			WorkspaceID: ws.ID,
			ActorUserID: actorPtr(ctx),
			Payload: map[string]any{
				"pageId":        pub.String(),
				"title":         in.Body.Title,
				"isAiGenerated": true,
			},
		}); err != nil {
			slog.ErrorContext(ctx, "eventbus.Append failed",
				slog.Any("err", err),
				slog.String("handler", "pages.GenerateWithAI"),
				slog.String("event_type", string(eventbus.PageCreated)),
				logutil.LogEntity("workspace", ws.PublicID),
				slog.String("page_public_id", pub.String()),
			)
		}

		deps.Audit.Record(ctx, audit.Entry{
			Action:       "page.generate",
			ActorID:      actorID,
			WorkspaceID:  ws.ID,
			ResourceType: "page",
			ResourceID:   pub.String(),
			Metadata:     map[string]any{"title": in.Body.Title, "promptLen": len(in.Body.Prompt)},
		})

		row, err := deps.Queries.GetPageByPublicId(ctx, generated.GetPageByPublicIdParams{
			WorkspaceID: ws.ID,
			PublicID:    pub,
		})
		if err != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
		}
		return &GeneratePageOutput{Body: mapGetRow(row)}, nil
	}
}
