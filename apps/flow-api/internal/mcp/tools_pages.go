package mcp

import (
	"context"
	"database/sql"
	"encoding/json"
	stderrors "errors"
	"strings"
	"time"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/dbretry"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/eventbus"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/errormodel"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/handlers/handlerutil"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/handlers/pages"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/mutationlog"
	"github.com/libraz/nodate-flow/packages/go-shared/stringutil"
)

// ----------------------------------------------------------------------------
// Page / Wiki tools
// ----------------------------------------------------------------------------
// pageListRowToMap converts a page list row to a map for JSON output.
func pageListRowToMap(pub types.PublicID, title string, isAI bool, sortWeight int32, updatedAt sql.NullTime, createdAt time.Time) map[string]any {
	m := map[string]any{
		"id":            pub.String(),
		"title":         title,
		"isAiGenerated": isAI,
		"sortWeight":    sortWeight,
		"createdAt":     createdAt.Unix(),
	}
	if updatedAt.Valid {
		m["updatedAt"] = updatedAt.Time.Unix()
	}
	return m
}

// runListPages lists wiki pages for the workspace. When parentPageId is
// provided it lists child pages; when projectId is provided it lists
// pages scoped to that project; otherwise it returns root-level pages.
func runListPages(ctx context.Context, deps Deps, s *session, raw json.RawMessage) (any, error) {
	var in struct {
		ProjectID    string `json:"projectId"`
		ParentPageID string `json:"parentPageId"`
		Limit        int32  `json:"limit"`
		Offset       int32  `json:"offset"`
	}
	if err := parseArgs(raw, &in); err != nil {
		return nil, err
	}
	if in.Limit <= 0 || in.Limit > 200 {
		in.Limit = 50
	}
	if _, err := requireWorkspaceMember(ctx, deps, s); err != nil {
		return nil, err
	}

	items := []map[string]any{}
	var total int64

	if in.ParentPageID != "" {
		parentInternal, _, err := resolvePage(ctx, deps, s, in.ParentPageID)
		if err != nil {
			return nil, err
		}
		rows, err := deps.Queries.ListChildPages(ctx, generated.ListChildPagesParams{
			WorkspaceID:  s.workspaceID,
			ParentPageID: sql.NullInt32{Int32: int32(parentInternal), Valid: true}, //#nosec G115 -- parent page id is pages.id (BIGINT UNSIGNED), fits int32 within realistic deployments
			Limit:        in.Limit,
			Offset:       in.Offset,
		})
		if err != nil {
			return nil, apierrors.Wrap(apierrors.McpToolExecutionFailed, err)
		}
		if len(rows) > 0 {
			total = handlerutil.TotalAsInt64(rows[0].Total)
		}
		for _, r := range rows {
			m := pageListRowToMap(r.PublicID, r.Title, r.IsAiGenerated, r.SortWeight, r.UpdatedAt, r.CreatedAt)
			if r.ProjectName.Valid {
				m["projectId"] = r.ProjectPublicID.String()
				m["projectName"] = r.ProjectName.String
			}
			items = append(items, m)
		}
	} else if in.ProjectID != "" {
		prjID, err := resolveProject(ctx, deps, s, in.ProjectID)
		if err != nil {
			return nil, err
		}
		rows, err := deps.Queries.ListPagesForProject(ctx, generated.ListPagesForProjectParams{
			WorkspaceID: s.workspaceID,
			ProjectID:   sql.NullInt32{Int32: int32(prjID), Valid: true}, //#nosec G115 -- project id is projects.id (BIGINT UNSIGNED), fits int32 within realistic deployments
			Limit:       in.Limit,
			Offset:      in.Offset,
		})
		if err != nil {
			return nil, apierrors.Wrap(apierrors.McpToolExecutionFailed, err)
		}
		if len(rows) > 0 {
			total = handlerutil.TotalAsInt64(rows[0].Total)
		}
		for _, r := range rows {
			m := pageListRowToMap(r.PublicID, r.Title, r.IsAiGenerated, r.SortWeight, r.UpdatedAt, r.CreatedAt)
			if r.ParentPagePublicID != (types.PublicID{}) {
				m["parentPageId"] = r.ParentPagePublicID.String()
			}
			items = append(items, m)
		}
	} else {
		rows, err := deps.Queries.ListPagesForWorkspace(ctx, generated.ListPagesForWorkspaceParams{
			WorkspaceID: s.workspaceID,
			Limit:       in.Limit,
			Offset:      in.Offset,
		})
		if err != nil {
			return nil, apierrors.Wrap(apierrors.McpToolExecutionFailed, err)
		}
		if len(rows) > 0 {
			total = handlerutil.TotalAsInt64(rows[0].Total)
		}
		for _, r := range rows {
			m := pageListRowToMap(r.PublicID, r.Title, r.IsAiGenerated, r.SortWeight, r.UpdatedAt, r.CreatedAt)
			if r.ProjectName.Valid {
				m["projectId"] = r.ProjectPublicID.String()
				m["projectName"] = r.ProjectName.String
			}
			items = append(items, m)
		}
	}
	return map[string]any{"pages": items, "total": total}, nil
}

// runGetPage fetches a single wiki page by public id, including content.
func runGetPage(ctx context.Context, deps Deps, s *session, raw json.RawMessage) (any, error) {
	var in struct {
		PageID string `json:"pageId"`
	}
	if err := parseArgs(raw, &in); err != nil {
		return nil, err
	}
	if _, err := requireWorkspaceMember(ctx, deps, s); err != nil {
		return nil, err
	}
	pub, err := types.Parse(in.PageID)
	if err != nil {
		return nil, apierrors.New(apierrors.PagePageNotFound)
	}
	row, err := deps.Queries.GetPageByPublicId(ctx, generated.GetPageByPublicIdParams{
		WorkspaceID: s.workspaceID,
		PublicID:    pub,
	})
	if err != nil {
		if stderrors.Is(err, sql.ErrNoRows) {
			return nil, apierrors.New(apierrors.PagePageNotFound)
		}
		return nil, apierrors.Wrap(apierrors.McpToolExecutionFailed, err)
	}
	out := map[string]any{
		"id":            row.PublicID.String(),
		"title":         row.Title,
		"body":          row.Body,
		"isAiGenerated": row.IsAiGenerated,
		"sortWeight":    row.SortWeight,
		"creatorId":     row.CreatorPublicID.String(),
		"creatorName":   row.CreatorDisplayName,
		"createdAt":     row.CreatedAt.Unix(),
	}
	if row.UpdatedAt.Valid {
		out["updatedAt"] = row.UpdatedAt.Time.Unix()
	}
	if row.ProjectName.Valid {
		out["projectId"] = row.ProjectPublicID.String()
		out["projectName"] = row.ProjectName.String
	}
	if row.ParentPageTitle.Valid {
		out["parentPageId"] = row.ParentPagePublicID.String()
		out["parentPageTitle"] = row.ParentPageTitle.String
	}
	if row.Notes.Valid {
		out["notes"] = row.Notes.String
	}
	return out, nil
}

// runCreatePage creates a new wiki page.
//
// Live sibling pages under one parent carry distinct titles —
// uniq_pages_workspace_id_scope_parent_page_id_title_active, with root
// pages counted as siblings of each other — so a title already in use
// there is refused. The refusal is the caller's to resolve, which is why
// it is named the way the REST route names it instead of being reported
// as a tool fault an agent retries.
//
// The classification reads MySQL's error number and nothing else, and
// pages carries two further unique keys, both over public_id. A repeated
// UUID v7 would therefore be reported as a taken title as well. It is not
// a case this branch can tell apart, and the REST route carries the same
// imprecision for the same reason.
func runCreatePage(ctx context.Context, deps Deps, s *session, raw json.RawMessage) (any, error) {
	var in struct {
		Title        string `json:"title"`
		Body         string `json:"body"`
		ParentPageID string `json:"parentPageId"`
		ProjectID    string `json:"projectId"`
	}
	if err := parseArgs(raw, &in); err != nil {
		return nil, err
	}
	if in.Title == "" {
		return nil, apierrors.New(apierrors.McpToolArgumentsInvalid)
	}
	if _, err := requireWorkspaceMember(ctx, deps, s); err != nil {
		return nil, err
	}

	var projectID sql.NullInt32
	if in.ProjectID != "" {
		prjID, err := resolveProjectForWrite(ctx, deps, s, in.ProjectID)
		if err != nil {
			return nil, err
		}
		projectID = sql.NullInt32{Int32: int32(prjID), Valid: true} //#nosec G115 -- project id is projects.id (BIGINT UNSIGNED), fits int32 within realistic deployments
	}

	var parentPageID sql.NullInt32
	if in.ParentPageID != "" {
		parentInternal, _, err := resolvePage(ctx, deps, s, in.ParentPageID)
		if err != nil {
			return nil, err
		}
		parentPageID = sql.NullInt32{Int32: int32(parentInternal), Valid: true} //#nosec G115 -- parent page id is pages.id (BIGINT UNSIGNED), fits int32 within realistic deployments
	}

	pub := newPublicID()
	var answered error
	if err := dbretry.InTx(ctx, deps.DB, "mcp.create_page", nil, func(ctx context.Context, tx *dbretry.Tx) error {
		answered = nil
		// The nesting limit is checked by the walk REST page writes run,
		// under row locks on the ancestors, in the transaction that inserts.
		if parentPageID.Valid {
			spec, err := pages.CheckAncestorDepthAndCircular(ctx, tx, s.workspaceID, uint32(parentPageID.Int32), 0) //#nosec G115 -- pages.id, positive by construction
			if err != nil {
				return err
			}
			if spec != nil {
				answered = apierrors.New(spec)
				return answered
			}
		}
		if _, err := deps.Queries.WithTx(tx.RawTx()).CreatePage(ctx, generated.CreatePageParams{
			PublicID:      pub,
			WorkspaceID:   s.workspaceID,
			ProjectID:     projectID,
			CreatorID:     s.userID,
			ParentPageID:  parentPageID,
			Title:         in.Title,
			Body:          in.Body,
			IsAiGenerated: false,
		}); err != nil {
			return err
		}
		return recorder(deps).RecordInTx(ctx, tx, mutationActor(s), mutationlog.Mutation{
			EventType:    eventbus.PageCreated,
			AuditAction:  "page.create",
			ResourceType: "page",
			ResourceID:   pub.String(),
			Payload: map[string]any{
				"pageId": pub.String(),
				"title":  in.Title,
				"via":    "mcp",
			},
			CallSite: "mcp.create_page",
		})
	}); err != nil {
		if answered != nil {
			return nil, answered
		}
		if handlerutil.IsDuplicateEntry(err) {
			return nil, apierrors.New(apierrors.PagePageTitleTaken)
		}
		return nil, apierrors.Wrap(apierrors.McpToolExecutionFailed, err)
	}
	return map[string]any{"id": pub.String()}, nil
}

// runUpdatePage updates a wiki page's title or content.
//
// Renaming a page onto a title a live sibling already holds is refused
// under the same key, and with the same code, as creating one there —
// see [runCreatePage], including why a duplicate key on this table cannot
// be read as a title conflict with certainty.
func runUpdatePage(ctx context.Context, deps Deps, s *session, raw json.RawMessage) (any, error) {
	var in struct {
		PageID string  `json:"pageId"`
		Title  *string `json:"title"`
		Body   *string `json:"body"`
	}
	if err := parseArgs(raw, &in); err != nil {
		return nil, err
	}
	if _, err := requireWorkspaceMember(ctx, deps, s); err != nil {
		return nil, err
	}
	_, pub, err := resolvePage(ctx, deps, s, in.PageID)
	if err != nil {
		return nil, err
	}

	// Build update params; COALESCE in SQL preserves existing values when NULL.
	var titleParam sql.NullString
	if in.Title != nil && *in.Title != "" {
		titleParam = sql.NullString{String: *in.Title, Valid: true}
	}
	var bodyParam sql.NullString
	if in.Body != nil {
		bodyParam = sql.NullString{String: *in.Body, Valid: true}
	}

	var answered error
	txErr := dbretry.InTx(ctx, deps.DB, "mcp.update_page", nil, func(ctx context.Context, tx *dbretry.Tx) error {
		answered = nil
		// UpdatePage writes project_id and parent_page_id verbatim, so they
		// are read under a row lock in the same transaction: a reparent
		// committed by another writer is written back as it stands rather
		// than reverted to a value read before it landed.
		//
		// no-generated-query: GetPageByPublicId — the only generated read of
		// a single page — projects the two FK columns as public ids and takes
		// no lock. Replacing this takes a generated locking read that returns
		// them as stored.
		var projectID, parentPageID sql.NullInt32
		const qPage = `SELECT project_id, parent_page_id FROM pages WHERE workspace_id = ? AND public_id = ? AND enabled = TRUE LIMIT 1 FOR UPDATE`
		if err := tx.QueryRowContext(ctx, qPage, s.workspaceID, pub).Scan(&projectID, &parentPageID); err != nil {
			if stderrors.Is(err, sql.ErrNoRows) {
				answered = apierrors.New(apierrors.PagePageNotFound)
			}
			return err
		}
		if err := checkPageProjectFloor(ctx, tx, s, projectID); err != nil {
			answered = err
			return err
		}
		// Not an existence check: an update carrying the page's current
		// title and body reports zero. The row lock above is what answers
		// for a page that is not there.
		if _, err := deps.Queries.WithTx(tx.RawTx()).UpdatePage(ctx, generated.UpdatePageParams{
			Title:        titleParam,
			Body:         bodyParam,
			ProjectID:    projectID,
			ParentPageID: parentPageID,
			WorkspaceID:  s.workspaceID,
			PublicID:     pub,
		}); err != nil {
			return err
		}
		return recorder(deps).RecordInTx(ctx, tx, mutationActor(s), mutationlog.Mutation{
			EventType:    eventbus.PageUpdated,
			AuditAction:  "page.update",
			ResourceType: "page",
			ResourceID:   pub.String(),
			Payload: map[string]any{
				"pageId": pub.String(),
				"via":    "mcp",
			},
			CallSite: "mcp.update_page",
		})
	})
	if answered != nil {
		return nil, answered
	}
	if txErr != nil {
		if handlerutil.IsDuplicateEntry(txErr) {
			return nil, apierrors.New(apierrors.PagePageTitleTaken)
		}
		return nil, apierrors.Wrap(apierrors.McpToolExecutionFailed, txErr)
	}
	return map[string]any{"id": pub.String()}, nil
}

// generatedPageTitleLimit caps, in bytes, the page title generate_page
// builds from the caller's description. It is the tool's own bound,
// narrower than pageTitleLimit.
const generatedPageTitleLimit = 200

// generatedPageTitle builds the generate_page title from the caller's
// context description.
//
// The cut lands on a rune boundary: the description arrives over MCP and
// is free-form, so a byte-indexed cut can sever a multi-byte character
// and hand the utf8mb4 column a fragment it rejects under
// STRICT_TRANS_TABLES — the page then fails to create with nothing in
// the request to explain why.
func generatedPageTitle(contextDescription string) string {
	return stringutil.TruncateBytes("Generated: "+contextDescription, generatedPageTitleLimit)
}

// runGeneratePage writes a wiki page whose body an LLM drafted from the
// caller's brief, through the same page generator and failure mapping REST
// page generation uses.
//
// A generation that did not happen is refused, and nothing is written:
// every refusal is decided before the insert. The page title is built from
// the brief, since this tool takes no title of its own.
//
// A title a live sibling already holds is refused under the same key and
// the same code as a title a caller typed — see [runCreatePage], including
// why a duplicate key on this table cannot be read as a title conflict with
// certainty. Repeated briefs in one workspace build the same title, so this
// is a collision the tool meets in ordinary use; reported as a tool fault it
// would read as a server problem an agent retries, and every retry spends
// another model call on a page that cannot be stored.
func runGeneratePage(ctx context.Context, deps Deps, s *session, raw json.RawMessage) (any, error) {
	var in struct {
		ContextDescription string   `json:"contextDescription"`
		ProjectID          string   `json:"projectId"`
		TaskIDs            []string `json:"taskIds"`
	}
	if err := parseArgs(raw, &in); err != nil {
		return nil, err
	}
	if in.ContextDescription == "" {
		return nil, apierrors.New(apierrors.McpToolArgumentsInvalid)
	}
	if _, err := requireWorkspaceMember(ctx, deps, s); err != nil {
		return nil, err
	}

	// Resolved, and held to the editor floor, before the model call so a
	// refused project spends no budget.
	var projectID sql.NullInt32
	if in.ProjectID != "" {
		prjID, err := resolveProjectForWrite(ctx, deps, s, in.ProjectID)
		if err != nil {
			return nil, err
		}
		projectID = sql.NullInt32{Int32: int32(prjID), Valid: true} //#nosec G115 -- project id is projects.id (BIGINT UNSIGNED), fits int32 within realistic deployments
	}

	// Build context from task data if task ids are provided. Each task id
	// must resolve through the shared task-visibility ACL: a task the
	// caller cannot access is rejected outright rather than silently
	// skipped, so generated page content can never incorporate task data
	// the caller is not permitted to read.
	var prompt strings.Builder
	prompt.WriteString(in.ContextDescription)
	for _, tid := range in.TaskIDs {
		_, tRow, err := resolveTaskRow(ctx, deps, s, tid)
		if err != nil {
			return nil, err
		}
		prompt.WriteString("\n\nTask: " + tRow.Title)
		if tRow.Description.Valid && tRow.Description.String != "" {
			prompt.WriteString("\nDescription: " + tRow.Description.String)
		}
	}

	// A deployment with no provider wired cannot draft anything, which is
	// the same answer the REST page-generation route gives before it
	// spends a request on the attempt.
	if deps.AI == nil {
		return nil, apierrors.New(apierrors.AiProviderNotConfigured)
	}

	title := generatedPageTitle(in.ContextDescription)
	body, err := deps.AI.GeneratePageBody(ctx, s.workspaceID, title, prompt.String())
	if err != nil {
		// The page-generation mapping REST uses: the shared AI codes, with
		// an unreachable provider named as page generation unavailable.
		spec, _ := errormodel.PageGenerationFailure(err)
		return nil, apierrors.Wrap(spec, err)
	}

	pub := newPublicID()
	if err := dbretry.InTx(ctx, deps.DB, "mcp.generate_page", nil, func(ctx context.Context, tx *dbretry.Tx) error {
		if _, err := deps.Queries.WithTx(tx.RawTx()).CreatePage(ctx, generated.CreatePageParams{
			PublicID:      pub,
			WorkspaceID:   s.workspaceID,
			ProjectID:     projectID,
			CreatorID:     s.userID,
			ParentPageID:  sql.NullInt32{},
			Title:         title,
			Body:          body,
			IsAiGenerated: true,
		}); err != nil {
			return err
		}
		return recorder(deps).RecordInTx(ctx, tx, mutationActor(s), mutationlog.Mutation{
			EventType:    eventbus.PageCreated,
			AuditAction:  "page.generate",
			ResourceType: "page",
			ResourceID:   pub.String(),
			Payload: map[string]any{
				"pageId":        pub.String(),
				"title":         title,
				"isAiGenerated": true,
				"via":           "mcp:generate_page",
			},
			CallSite: "mcp.generate_page",
		})
	}); err != nil {
		if handlerutil.IsDuplicateEntry(err) {
			return nil, apierrors.New(apierrors.PagePageTitleTaken)
		}
		return nil, apierrors.Wrap(apierrors.McpToolExecutionFailed, err)
	}
	// isAiGenerated stays in the answer even though this tool can only
	// return it true: it is the same field pages carry everywhere else.
	return map[string]any{
		"id":            pub.String(),
		"isAiGenerated": true,
	}, nil
}
