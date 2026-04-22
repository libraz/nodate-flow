// Package router assembles the nodate-time HTTP API router.
package router

import (
	"context"
	"database/sql"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"

	generated "github.com/nodate-flow/nodate-flow/apps/time-api/internal/db/generated"
	"github.com/nodate-flow/nodate-flow/apps/time-api/internal/auth"
	"github.com/nodate-flow/nodate-flow/apps/time-api/internal/http/handlers/calendars"
	"github.com/nodate-flow/nodate-flow/apps/time-api/internal/http/handlers/workspaces"
	"github.com/nodate-flow/nodate-flow/apps/time-api/internal/http/middleware"
)

// Deps is the dependency bundle Build needs to wire every route.
type Deps struct {
	DB  *sql.DB
	JWT *auth.JWTIssuer
}

type healthOutput struct {
	Body struct {
		Status string `json:"status"`
	}
}

// Result holds the assembled router and every huma API instance so
// that callers like dump-openapi can extract the OpenAPI spec.
type Result struct {
	Handler http.Handler
	APIs    []huma.API
}

// Build mounts every nodate-time API route onto a fresh chi router and
// returns it as an http.Handler.
func Build(deps Deps) http.Handler {
	return BuildResult(deps).Handler
}

// BuildResult is like Build but also returns the huma API instances for
// OpenAPI extraction.
func BuildResult(deps Deps) Result {
	r := chi.NewRouter()
	r.Use(middleware.ClientIP())
	r.Use(middleware.SecurityHeaders())

	newConfig := func() huma.Config {
		return huma.DefaultConfig("nodate-time", "0.0.0")
	}
	api := humachi.New(r, newConfig())
	newSubAPI := func(sub chi.Router) huma.API {
		return humachi.New(sub, newConfig())
	}

	queries := generated.New(deps.DB)

	// Build calendar handler dependencies.
	calDeps := calendars.Deps{
		Queries: queries,
		DB:      deps.DB,
	}

	// Build workspace handler dependencies.
	wsDeps := workspaces.Deps{
		Queries: queries,
	}

	// Health endpoint (public).
	huma.Register(api, huma.Operation{
		OperationID: "health",
		Method:      http.MethodGet,
		Path:        "/health",
		Summary:     "Health check",
	}, func(_ context.Context, _ *struct{}) (*healthOutput, error) {
		out := &healthOutput{}
		out.Body.Status = "ok"
		return out, nil
	})

	// Public share endpoints are rebuilt in R5.14 against calendar_public_shares.

	// Protected routes (RequireAuth).
	aclDB := passthroughDB{deps.DB}
	authMW := middleware.RequireAuth(middleware.AuthDeps{
		JWT: deps.JWT,
		DB:  aclDB,
	})
	calMW := middleware.RequireCalendarMember(aclDB)

	var subAPI huma.API
	r.Group(func(sub chi.Router) {
		sub.Use(authMW)
		subAPI = newSubAPI(sub)

		// Workspaces.
		huma.Register(subAPI, huma.Operation{
			OperationID: "workspaces-list",
			Method:      http.MethodGet,
			Path:        "/workspaces",
			Summary:     "List workspaces for the authenticated user",
		}, workspaces.List(wsDeps))
		// Workspace-scoped calendar routes (no calId).
		huma.Register(subAPI, huma.Operation{
			OperationID: "calendars-list",
			Method:      http.MethodGet,
			Path:        "/workspaces/{wsId}/calendars",
			Summary:     "List calendars in a workspace",
		}, calendars.ListCalendars(calDeps))
		huma.Register(subAPI, huma.Operation{
			OperationID: "calendars-create",
			Method:      http.MethodPost,
			Path:        "/workspaces/{wsId}/calendars",
			Summary:     "Create a calendar",
		}, calendars.CreateCalendar(calDeps))
		huma.Register(subAPI, huma.Operation{
			OperationID: "calendars-subscribe-system",
			Method:      http.MethodPost,
			Path:        "/workspaces/{wsId}/calendars/subscribe-system",
			Summary:     "Subscribe the caller to the holiday feed for a country",
		}, calendars.SubscribeSystemCalendar(calDeps))

		// Cross-calendar event query (no calId, outside calendar member scope).
		huma.Register(subAPI, huma.Operation{
			OperationID: "calendar-events-list",
			Method:      http.MethodGet,
			Path:        "/workspaces/{wsId}/calendar-events",
			Summary:     "List events across all calendars in a workspace",
		}, calendars.ListCalendarEvents(calDeps))

		// Cross-workspace: every visible event across every subscribed
		// calendar in every workspace the caller belongs to. Backs the
		// unified flow-web calendar alongside flow-api's /me/tasks-with-dates.
		huma.Register(subAPI, huma.Operation{
			OperationID: "me-calendar-events-list",
			Method:      http.MethodGet,
			Path:        "/me/calendar-events",
			Summary:     "List events across every workspace the caller belongs to",
		}, calendars.ListMyCalendarEvents(calDeps))

		// Calendar-invite accept is gone; ws joining uses workspace_invites in R5.9.
	})

	// Calendar-scoped routes (RequireAuth + RequireCalendarMember).
	// The calMW middleware resolves {calId}, verifies the actor has an active
	// subscription, and stores CalendarContext + SubscriptionContext on the
	// request context. Handlers can retrieve them via CalendarFromContext and
	// SubscriptionFromContext. Existing handlers still call resolveWorkspace +
	// resolveCalendar internally (redundant but safe); new handlers should
	// prefer the context values to reduce boilerplate.
	var calAPI huma.API
	r.Group(func(sub chi.Router) {
		sub.Use(authMW)
		sub.Use(calMW)
		calAPI = newSubAPI(sub)

		// Single calendar CRUD.
		huma.Register(calAPI, huma.Operation{
			OperationID: "calendars-get",
			Method:      http.MethodGet,
			Path:        "/workspaces/{wsId}/calendars/{calId}",
			Summary:     "Get a calendar",
		}, calendars.GetCalendar(calDeps))
		huma.Register(calAPI, huma.Operation{
			OperationID: "calendars-patch",
			Method:      http.MethodPatch,
			Path:        "/workspaces/{wsId}/calendars/{calId}",
			Summary:     "Update a calendar",
		}, calendars.PatchCalendar(calDeps))
		huma.Register(calAPI, huma.Operation{
			OperationID: "calendars-delete",
			Method:      http.MethodDelete,
			Path:        "/workspaces/{wsId}/calendars/{calId}",
			Summary:     "Delete a calendar",
		}, calendars.DeleteCalendar(calDeps))

		// Events within a calendar.
		huma.Register(calAPI, huma.Operation{
			OperationID: "events-list",
			Method:      http.MethodGet,
			Path:        "/workspaces/{wsId}/calendars/{calId}/events",
			Summary:     "List events in a calendar",
		}, calendars.ListEvents(calDeps))
		huma.Register(calAPI, huma.Operation{
			OperationID: "events-create",
			Method:      http.MethodPost,
			Path:        "/workspaces/{wsId}/calendars/{calId}/events",
			Summary:     "Create an event",
		}, calendars.CreateEvent(calDeps))
		huma.Register(calAPI, huma.Operation{
			OperationID: "events-get",
			Method:      http.MethodGet,
			Path:        "/workspaces/{wsId}/calendars/{calId}/events/{evtId}",
			Summary:     "Get an event",
		}, calendars.GetEvent(calDeps))
		huma.Register(calAPI, huma.Operation{
			OperationID: "events-patch",
			Method:      http.MethodPatch,
			Path:        "/workspaces/{wsId}/calendars/{calId}/events/{evtId}",
			Summary:     "Update an event",
		}, calendars.PatchEvent(calDeps))
		huma.Register(calAPI, huma.Operation{
			OperationID: "events-delete",
			Method:      http.MethodDelete,
			Path:        "/workspaces/{wsId}/calendars/{calId}/events/{evtId}",
			Summary:     "Delete an event",
		}, calendars.DeleteEvent(calDeps))

		// Smart event creation (natural language parser).
		huma.Register(calAPI, huma.Operation{
			OperationID: "events-smart-create",
			Method:      http.MethodPost,
			Path:        "/workspaces/{wsId}/calendars/{calId}/events/smart-create",
			Summary:     "Parse natural language text into an event proposal",
		}, calendars.SmartCreate(calDeps))

		// iCalendar export is rebuilt in R5.14 against calendar_public_shares.

		// Task-to-calendar sync.
		huma.Register(calAPI, huma.Operation{
			OperationID: "events-from-task",
			Method:      http.MethodPost,
			Path:        "/workspaces/{wsId}/calendars/{calId}/events/from-task",
			Summary:     "Create a calendar event from a task",
		}, calendars.CreateEventFromTask(calDeps))

		// Calendar members.
		huma.Register(calAPI, huma.Operation{
			OperationID: "members-add",
			Method:      http.MethodPost,
			Path:        "/workspaces/{wsId}/calendars/{calId}/members",
			Summary:     "Add a member to a calendar",
		}, calendars.AddMember(calDeps))
		huma.Register(calAPI, huma.Operation{
			OperationID: "members-list",
			Method:      http.MethodGet,
			Path:        "/workspaces/{wsId}/calendars/{calId}/members",
			Summary:     "List members of a calendar",
		}, calendars.ListMembers(calDeps))
		huma.Register(calAPI, huma.Operation{
			OperationID: "members-update-role",
			Method:      http.MethodPatch,
			Path:        "/workspaces/{wsId}/calendars/{calId}/members/{userId}",
			Summary:     "Update a member's role",
		}, calendars.UpdateMemberRole(calDeps))
		huma.Register(calAPI, huma.Operation{
			OperationID: "members-remove",
			Method:      http.MethodDelete,
			Path:        "/workspaces/{wsId}/calendars/{calId}/members/{userId}",
			Summary:     "Remove a member from a calendar",
		}, calendars.RemoveMember(calDeps))

		// Calendar-scoped invite links are gone; public share pages replace
		// them in R5.14 (calendar_public_shares), and workspace-level
		// joining uses workspace_invites.

		// Event attendees.
		huma.Register(calAPI, huma.Operation{
			OperationID: "attendees-add",
			Method:      http.MethodPost,
			Path:        "/workspaces/{wsId}/calendars/{calId}/events/{evtId}/attendees",
			Summary:     "Add attendees to an event",
		}, calendars.AddAttendees(calDeps))
		huma.Register(calAPI, huma.Operation{
			OperationID: "attendees-remove",
			Method:      http.MethodDelete,
			Path:        "/workspaces/{wsId}/calendars/{calId}/events/{evtId}/attendees/{userId}",
			Summary:     "Remove an attendee from an event",
		}, calendars.RemoveAttendee(calDeps))
		huma.Register(calAPI, huma.Operation{
			OperationID: "attendees-rsvp",
			Method:      http.MethodPatch,
			Path:        "/workspaces/{wsId}/calendars/{calId}/events/{evtId}/attendees/rsvp",
			Summary:     "Update own RSVP for an event",
		}, calendars.UpdateRsvp(calDeps))
		huma.Register(calAPI, huma.Operation{
			OperationID: "attendees-can-edit",
			Method:      http.MethodPatch,
			Path:        "/workspaces/{wsId}/calendars/{calId}/events/{evtId}/attendees/{userId}/can-edit",
			Summary:     "Toggle can_edit for an attendee",
		}, calendars.ToggleCanEdit(calDeps))

		// Event comments.
		huma.Register(calAPI, huma.Operation{
			OperationID: "comments-list",
			Method:      http.MethodGet,
			Path:        "/workspaces/{wsId}/calendars/{calId}/events/{evtId}/comments",
			Summary:     "List comments on an event",
		}, calendars.ListComments(calDeps))
		huma.Register(calAPI, huma.Operation{
			OperationID: "comments-create",
			Method:      http.MethodPost,
			Path:        "/workspaces/{wsId}/calendars/{calId}/events/{evtId}/comments",
			Summary:     "Add a comment to an event",
		}, calendars.CreateComment(calDeps))
		huma.Register(calAPI, huma.Operation{
			OperationID: "comments-edit",
			Method:      http.MethodPatch,
			Path:        "/workspaces/{wsId}/calendars/{calId}/events/{evtId}/comments/{cId}",
			Summary:     "Edit a comment",
		}, calendars.EditComment(calDeps))
		huma.Register(calAPI, huma.Operation{
			OperationID: "comments-delete",
			Method:      http.MethodDelete,
			Path:        "/workspaces/{wsId}/calendars/{calId}/events/{evtId}/comments/{cId}",
			Summary:     "Delete a comment",
		}, calendars.DeleteComment(calDeps))

		// Event checklist.
		huma.Register(calAPI, huma.Operation{
			OperationID: "checklist-list",
			Method:      http.MethodGet,
			Path:        "/workspaces/{wsId}/calendars/{calId}/events/{evtId}/checklist",
			Summary:     "List checklist items for an event",
		}, calendars.ListChecklist(calDeps))
		huma.Register(calAPI, huma.Operation{
			OperationID: "checklist-create",
			Method:      http.MethodPost,
			Path:        "/workspaces/{wsId}/calendars/{calId}/events/{evtId}/checklist",
			Summary:     "Add a checklist item to an event",
		}, calendars.CreateChecklistItem(calDeps))
		huma.Register(calAPI, huma.Operation{
			OperationID: "checklist-update",
			Method:      http.MethodPatch,
			Path:        "/workspaces/{wsId}/calendars/{calId}/events/{evtId}/checklist/{itemId}",
			Summary:     "Update a checklist item",
		}, calendars.UpdateChecklistItem(calDeps))
		huma.Register(calAPI, huma.Operation{
			OperationID: "checklist-delete",
			Method:      http.MethodDelete,
			Path:        "/workspaces/{wsId}/calendars/{calId}/events/{evtId}/checklist/{itemId}",
			Summary:     "Delete a checklist item",
		}, calendars.DeleteChecklistItem(calDeps))

		// Memos within a calendar.
		huma.Register(calAPI, huma.Operation{
			OperationID: "memos-list",
			Method:      http.MethodGet,
			Path:        "/workspaces/{wsId}/calendars/{calId}/memos",
			Summary:     "List memos in a calendar",
		}, calendars.ListMemos(calDeps))
		huma.Register(calAPI, huma.Operation{
			OperationID: "memos-create",
			Method:      http.MethodPost,
			Path:        "/workspaces/{wsId}/calendars/{calId}/memos",
			Summary:     "Create a memo",
		}, calendars.CreateMemo(calDeps))
		huma.Register(calAPI, huma.Operation{
			OperationID: "memos-update",
			Method:      http.MethodPatch,
			Path:        "/workspaces/{wsId}/calendars/{calId}/memos/{memoId}",
			Summary:     "Update a memo",
		}, calendars.UpdateMemo(calDeps))
		huma.Register(calAPI, huma.Operation{
			OperationID: "memos-delete",
			Method:      http.MethodDelete,
			Path:        "/workspaces/{wsId}/calendars/{calId}/memos/{memoId}",
			Summary:     "Delete a memo",
		}, calendars.DeleteMemo(calDeps))

		// Event attachments.
		huma.Register(calAPI, huma.Operation{
			OperationID: "attachments-list",
			Method:      http.MethodGet,
			Path:        "/workspaces/{wsId}/calendars/{calId}/events/{evtId}/attachments",
			Summary:     "List attachments on an event",
		}, calendars.ListAttachments(calDeps))
		huma.Register(calAPI, huma.Operation{
			OperationID: "attachments-create",
			Method:      http.MethodPost,
			Path:        "/workspaces/{wsId}/calendars/{calId}/events/{evtId}/attachments",
			Summary:     "Record attachment metadata for an event",
		}, calendars.CreateAttachment(calDeps))
		huma.Register(calAPI, huma.Operation{
			OperationID: "attachments-delete",
			Method:      http.MethodDelete,
			Path:        "/workspaces/{wsId}/calendars/{calId}/events/{evtId}/attachments/{attId}",
			Summary:     "Delete an attachment from an event",
		}, calendars.DeleteAttachment(calDeps))
	})

	return Result{Handler: r, APIs: []huma.API{api, subAPI, calAPI}}
}

// passthroughDB adapts *sql.DB to middleware.ACLDB.
type passthroughDB struct{ db *sql.DB }

func (p passthroughDB) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	return p.db.QueryRowContext(ctx, query, args...)
}
