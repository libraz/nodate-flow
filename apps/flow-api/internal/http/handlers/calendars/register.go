package calendars

import (
	"net/http"

	"github.com/danielgtaylor/huma/v2"
)

// Register wires the calendar operations onto the given Huma API.
func Register(api huma.API, deps Deps) {
	huma.Register(api, huma.Operation{
		OperationID: "calendars-list",
		Method:      http.MethodGet,
		Path:        "/workspaces/{wsId}/calendars",
		Summary:     "List calendars visible to the caller",
	}, List(deps))

	huma.Register(api, huma.Operation{
		OperationID: "calendar-events-list",
		Method:      http.MethodGet,
		Path:        "/workspaces/{wsId}/calendar-events",
		Summary:     "List events across all subscribed calendars",
	}, ListEvents(deps))

	huma.Register(api, huma.Operation{
		OperationID: "calendar-events-create",
		Method:      http.MethodPost,
		Path:        "/workspaces/{wsId}/calendars/{calId}/events",
		Summary:     "Create a calendar event",
	}, CreateEvent(deps))

	huma.Register(api, huma.Operation{
		OperationID: "calendar-events-patch",
		Method:      http.MethodPatch,
		Path:        "/workspaces/{wsId}/calendars/{calId}/events/{eventId}",
		Summary:     "Update a calendar event",
	}, PatchEvent(deps))

	huma.Register(api, huma.Operation{
		OperationID: "calendar-events-delete",
		Method:      http.MethodDelete,
		Path:        "/workspaces/{wsId}/calendars/{calId}/events/{eventId}",
		Summary:     "Delete a calendar event",
	}, DeleteEvent(deps))
}
