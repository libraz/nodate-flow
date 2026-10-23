package pages

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/acl"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/ai"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/dbretry"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/eventbus"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/handlers/handlerutil"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/middleware"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/mutationlog"
	"github.com/libraz/nodate-flow/packages/go-shared/apierr"
	"github.com/libraz/nodate-flow/packages/go-shared/logutil"
	"github.com/libraz/nodate-flow/packages/go-shared/stringutil"
)

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

// lockPageLinks locks a live page row and returns its stored project_id and
// parent_page_id, so an update that does not name them writes back the
// committed values and a concurrent reparent waits for this one.
func lockPageLinks(ctx context.Context, tx *dbretry.Tx, wsID uint32, pub types.PublicID) (id uint32, project, parent sql.NullInt32, err error) {
	const q = `SELECT id, project_id, parent_page_id FROM pages WHERE workspace_id = ? AND public_id = ? AND enabled = TRUE LIMIT 1 FOR UPDATE`
	if err := tx.QueryRowContext(ctx, q, wsID, pub).Scan(&id, &project, &parent); err != nil {
		return 0, project, parent, err
	}
	return id, project, parent, nil
}

// checkAncestorDepthAndCircular walks up the ancestor chain from the
// proposed parent, verifying that:
//  1. The chain does not exceed MaxPageDepth.
//  2. The target page (selfID) is not an ancestor (circular reference).
//
// selfID may be 0 for create operations (no circularity check needed).
// Each ancestor is read with a row lock inside the caller's transaction,
// so two opposite reparents cannot both pass the walk and form a cycle.
func checkAncestorDepthAndCircular(ctx context.Context, tx *dbretry.Tx, wsID uint32, parentInternalID uint32, selfID uint32) (*apierr.Spec, error) {
	const q = `SELECT id, parent_page_id FROM pages WHERE workspace_id = ? AND id = ? AND enabled = TRUE LIMIT 1 FOR UPDATE`
	currentID := parentInternalID
	depth := 1 // starting at depth 1 since we will be a child of parentInternalID

	for depth <= MaxPageDepth {
		if selfID != 0 && currentID == selfID {
			return apierrors.PagePageCircularParent, nil
		}
		var id uint32
		var parentID sql.NullInt32
		err := tx.QueryRowContext(ctx, q, wsID, currentID).Scan(&id, &parentID)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				// Reached a page that doesn't exist or is disabled; treat as root.
				break
			}
			return nil, err
		}
		if !parentID.Valid {
			// Reached root.
			break
		}
		currentID = uint32(parentID.Int32) //#nosec G115 -- parent_id is pages.id (BIGINT UNSIGNED), fits uint32 within realistic deployments
		depth++
	}
	if depth > MaxPageDepth {
		return apierrors.PagePageMaxDepth, nil
	}
	return nil, nil
}

// resolveProjectForWrite resolves a project public id within the workspace
// for a write that places a page in it, holding the actor to the project
// editor floor.
func resolveProjectForWrite(ctx context.Context, deps Deps, wsID, actorID uint32, projectPublicID string) (sql.NullInt32, error) {
	pid, err := types.Parse(projectPublicID)
	if err != nil {
		return sql.NullInt32{}, httpErr(apierrors.ValidationPathParamInvalid)
	}
	row, err := deps.Queries.FindProjectByPublicId(ctx, generated.FindProjectByPublicIdParams{
		WorkspaceID: wsID,
		PublicID:    pid,
	})
	if err != nil {
		return sql.NullInt32{}, httpErr(apierr.SpecForErrNoRows(err, apierrors.WsProjectNotFound, apierrors.InternalUnexpected))
	}
	if spec := handlerutil.RequireProjectRole(ctx, deps.DB, wsID, row.ID, actorID, acl.ProjectRoleEditor); spec != nil {
		return sql.NullInt32{}, httpErr(spec)
	}
	return sql.NullInt32{Int32: int32(row.ID), Valid: true}, nil //#nosec G115 -- projects.id fits int32 within realistic deployments
}

// checkStoredProjectFloor applies the editor floor to the project a page
// already belongs to; a page outside any project needs no project role.
func checkStoredProjectFloor(ctx context.Context, tx *dbretry.Tx, wsID, actorID uint32, project sql.NullInt32) *apierr.Spec {
	if !project.Valid {
		return nil
	}
	return handlerutil.RequireProjectRole(ctx, tx, wsID, uint32(project.Int32), actorID, acl.ProjectRoleEditor) //#nosec G115 -- projects.id, positive by construction
}

// errAnswered rolls a transaction back once the handler has decided the
// response itself. It is not transient, so dbretry does not retry it.
var errAnswered = errors.New("pages: answered")

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
			projectID, err = resolveProjectForWrite(ctx, deps, ws.ID, actorID, *in.Body.ProjectID)
			if err != nil {
				return nil, err
			}
		}

		// Resolve optional parent page.
		var parentInternal uint32
		if in.Body.ParentPageID != nil && *in.Body.ParentPageID != "" {
			parentPub, err := types.Parse(*in.Body.ParentPageID)
			if err != nil {
				return nil, httpErr(apierrors.ValidationPathParamInvalid)
			}
			parentInternal, err = resolvePageInternal(ctx, deps.DB, ws.ID, parentPub)
			if err != nil {
				return nil, err
			}
		}

		pub := types.New()
		var answered error
		txErr := dbretry.InTx(ctx, deps.DB, "pages.Create", nil, func(ctx context.Context, tx *dbretry.Tx) error {
			answered = nil
			var parentPageID sql.NullInt32
			if parentInternal != 0 {
				// Validate depth (selfID=0 for create, no circularity check needed).
				spec, err := checkAncestorDepthAndCircular(ctx, tx, ws.ID, parentInternal, 0)
				if err != nil {
					return err
				}
				if spec != nil {
					answered = httpErr(spec)
					return errAnswered
				}
				parentPageID = sql.NullInt32{Int32: int32(parentInternal), Valid: true} //#nosec G115 -- parent page id is pages.id (BIGINT UNSIGNED), fits int32 within realistic deployments
			}
			if _, err := generated.New(tx).CreatePage(ctx, generated.CreatePageParams{
				PublicID:      pub,
				WorkspaceID:   ws.ID,
				ProjectID:     projectID,
				CreatorID:     actorID,
				ParentPageID:  parentPageID,
				Title:         in.Body.Title,
				Body:          in.Body.Body,
				IsAiGenerated: false,
			}); err != nil {
				return err
			}
			return deps.Mutations.RecordInTx(ctx, tx, mutationlog.Actor{UserID: actorID, WorkspaceID: ws.ID}, mutationlog.Mutation{
				EventType:    eventbus.PageCreated,
				AuditAction:  "page.create",
				ResourceType: "page",
				ResourceID:   pub.String(),
				Payload:      map[string]any{"pageId": pub.String(), "title": in.Body.Title},
				CallSite:     "pages.Create",
			})
		})
		if answered != nil {
			return nil, answered
		}
		if txErr != nil {
			if isDuplicateEntry(txErr) {
				return nil, httpErr(apierrors.PagePageTitleTaken)
			}
			return nil, httpErr(apierrors.InternalUnexpected)
		}

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

		actorID, _ := middleware.ActorFromContext(ctx)

		// A named project is resolved, and held to the editor floor, before
		// the transaction; a named parent is resolved to its id.
		var namedProject sql.NullInt32
		if in.Body.ProjectID != nil && *in.Body.ProjectID != "" {
			namedProject, err = resolveProjectForWrite(ctx, deps, ws.ID, actorID, *in.Body.ProjectID)
			if err != nil {
				return nil, err
			}
		}
		var namedParent uint32
		if in.Body.ParentPageID != nil && *in.Body.ParentPageID != "" {
			parentPub, err := types.Parse(*in.Body.ParentPageID)
			if err != nil {
				return nil, httpErr(apierrors.ValidationPathParamInvalid)
			}
			namedParent, err = resolvePageInternal(ctx, deps.DB, ws.ID, parentPub)
			if err != nil {
				return nil, err
			}
		}

		var answered error
		txErr := dbretry.InTx(ctx, deps.DB, "pages.Update", nil, func(ctx context.Context, tx *dbretry.Tx) error {
			answered = nil
			// UpdatePage writes project_id and parent_page_id verbatim, so
			// start from the values committed under the row lock and
			// overwrite only what the body names.
			pageID, curProject, curParent, err := lockPageLinks(ctx, tx, ws.ID, pub)
			if err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					answered = httpErr(apierrors.PagePageNotFound)
					return errAnswered
				}
				return err
			}
			if spec := checkStoredProjectFloor(ctx, tx, ws.ID, actorID, curProject); spec != nil {
				answered = httpErr(spec)
				return errAnswered
			}
			updateParams := generated.UpdatePageParams{
				WorkspaceID:  ws.ID,
				PublicID:     pub,
				ProjectID:    curProject,
				ParentPageID: curParent,
			}
			// Title and body: non-null updates, null keeps existing (COALESCE).
			if in.Body.Title != nil {
				updateParams.Title = sql.NullString{String: *in.Body.Title, Valid: true}
			}
			if in.Body.Body != nil {
				updateParams.Body = sql.NullString{String: *in.Body.Body, Valid: true}
			}
			// Project: an empty string unsets it.
			if in.Body.ProjectID != nil {
				updateParams.ProjectID = namedProject
			}
			// Parent page: an empty string makes it a root page; any other
			// value is checked for depth and circularity under row locks.
			if in.Body.ParentPageID != nil {
				updateParams.ParentPageID = sql.NullInt32{}
				if namedParent != 0 {
					if namedParent == pageID {
						answered = httpErr(apierrors.PagePageCircularParent)
						return errAnswered
					}
					spec, err := checkAncestorDepthAndCircular(ctx, tx, ws.ID, namedParent, pageID)
					if err != nil {
						return err
					}
					if spec != nil {
						answered = httpErr(spec)
						return errAnswered
					}
					updateParams.ParentPageID = sql.NullInt32{Int32: int32(namedParent), Valid: true} //#nosec G115 -- parent page id is pages.id (BIGINT UNSIGNED), fits int32 within realistic deployments
				}
			}
			// Not an existence check: re-submitting a page's current title
			// and body changes nothing and MySQL counts zero. The row lock
			// above is what established the page exists.
			if _, err := generated.New(tx).UpdatePage(ctx, updateParams); err != nil {
				return err
			}
			return deps.Mutations.RecordInTx(ctx, tx, mutationlog.Actor{UserID: actorID, WorkspaceID: ws.ID}, mutationlog.Mutation{
				EventType:    eventbus.PageUpdated,
				AuditAction:  "page.update",
				ResourceType: "page",
				ResourceID:   pub.String(),
				Payload:      map[string]any{"pageId": pub.String()},
				CallSite:     "pages.Update",
			})
		})
		if answered != nil {
			return nil, answered
		}
		if txErr != nil {
			if isDuplicateEntry(txErr) {
				return nil, httpErr(apierrors.PagePageTitleTaken)
			}
			return nil, httpErr(apierrors.InternalUnexpected)
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
// The page is soft-disabled, which hides its whole subtree with it; a repeat
// delete finds no live page and returns PagePageNotFound.
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
		actorID, _ := middleware.ActorFromContext(ctx)
		var answered error
		txErr := dbretry.InTx(ctx, deps.DB, "pages.Delete", nil, func(ctx context.Context, tx *dbretry.Tx) error {
			answered = nil
			_, curProject, _, err := lockPageLinks(ctx, tx, ws.ID, pub)
			if err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					answered = httpErr(apierrors.PagePageNotFound)
					return errAnswered
				}
				return err
			}
			if spec := checkStoredProjectFloor(ctx, tx, ws.ID, actorID, curProject); spec != nil {
				answered = httpErr(spec)
				return errAnswered
			}
			// Scoped to the workspace and to live pages; the row lock makes
			// a zero count unreachable, but the claim is still checked.
			rows, err := generated.New(tx).DisablePage(ctx, generated.DisablePageParams{
				WorkspaceID: ws.ID,
				PublicID:    pub,
			})
			if err != nil {
				return err
			}
			if rows == 0 {
				answered = httpErr(apierrors.PagePageNotFound)
				return errAnswered
			}
			return deps.Mutations.RecordInTx(ctx, tx, mutationlog.Actor{UserID: actorID, WorkspaceID: ws.ID}, mutationlog.Mutation{
				EventType:    eventbus.PageDisabled,
				AuditAction:  "page.delete",
				ResourceType: "page",
				ResourceID:   pub.String(),
				Payload:      map[string]any{"pageId": pub.String()},
				CallSite:     "pages.Delete",
			})
		})
		if answered != nil {
			return nil, answered
		}
		if txErr != nil {
			return nil, httpErr(apierrors.InternalUnexpected)
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
			projectID, resolveErr = resolveProjectForWrite(ctx, deps, ws.ID, actorID, *in.Body.ProjectID)
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
		txErr := dbretry.InTx(ctx, deps.DB, "pages.GenerateWithAI", nil, func(ctx context.Context, tx *dbretry.Tx) error {
			if _, err := generated.New(tx).CreatePage(ctx, generated.CreatePageParams{
				PublicID:      pub,
				WorkspaceID:   ws.ID,
				ProjectID:     projectID,
				CreatorID:     actorID,
				ParentPageID:  sql.NullInt32{},
				Title:         in.Body.Title,
				Body:          body,
				IsAiGenerated: true,
			}); err != nil {
				return err
			}
			return deps.Mutations.RecordInTx(ctx, tx, mutationlog.Actor{UserID: actorID, WorkspaceID: ws.ID}, mutationlog.Mutation{
				EventType:    eventbus.PageCreated,
				AuditAction:  "page.generate",
				ResourceType: "page",
				ResourceID:   pub.String(),
				Payload: map[string]any{
					"pageId":        pub.String(),
					"title":         in.Body.Title,
					"isAiGenerated": true,
					"promptLen":     len(in.Body.Prompt),
				},
				CallSite: "pages.GenerateWithAI",
			})
		})
		if txErr != nil {
			if isDuplicateEntry(txErr) {
				return nil, httpErr(apierrors.PagePageTitleTaken)
			}
			return nil, httpErr(apierrors.InternalUnexpected)
		}

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
