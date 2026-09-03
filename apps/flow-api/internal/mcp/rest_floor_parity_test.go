// Package mcp_test cross-transport floor checks.
//
// The MCP tool table and the REST operation inventory answer for the same
// workspace. Nothing joined them, so every rule REST gained had to be
// carried across by hand, and the tool that was missed stayed missed: an
// agent could reach through MCP what the same account was refused over
// HTTP, and the only thing that would have said so was somebody reading
// both tables side by side.
//
// The join lives here. Each tool names the REST operation it answers for,
// by OperationID, and the operation has to exist in the inventory the
// router hands back — a renamed or deleted operation stops resolving
// instead of quietly describing nothing. The tool's floor is then held to
// that operation's, and a tool that admits callers the operation refuses
// fails.
//
// Tools with no REST counterpart are listed separately rather than mapped
// to whichever operation looks closest. A loose mapping is worse than none:
// it reports a floor comparison that was never a comparison.
package mcp_test

import (
	"sort"
	"testing"
	"time"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/auth"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/router"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/mcp"
)

// mcpRESTCounterparts maps each registered MCP tool onto the REST
// operation that performs the same logical change.
//
// The value is an OperationID, resolved against the router's own
// inventory. It is not a description of one: an id that names no
// registered operation fails, which is the property a prose note listing a
// file path never had.
//
// Where several REST operations cover one tool, the strictest is named —
// the comparison is about what a caller has to hold, so borrowing the
// laxest of a set would let the tool sit below the route a caller would
// actually have to use.
var mcpRESTCounterparts = map[string]string{
	// Tasks.
	"list_tasks":                  "tasks-list",
	"search_tasks":                "tasks-list",
	"get_task":                    "tasks-get",
	"create_task":                 "tasks-create",
	"update_task":                 "tasks-patch",
	"transition_task":             "tasks-transitions-apply",
	"archive_task":                "tasks-archive",
	"unarchive_task":              "tasks-unarchive",
	"add_comment":                 "tasks-comments-add",
	"add_reaction":                "tasks-reactions-create",
	"list_reactions":              "tasks-reactions-list",
	"add_task_label":              "tasks-labels-add",
	"remove_task_label":           "tasks-labels-remove",
	"list_description_versions":   "tasks-description-history-list",
	"restore_description_version": "tasks-description-history-restore",
	"propose_steps":               "tasks-propose-steps",
	"apply_steps":                 "tasks-apply-steps",
	"smart_create_task":           "tasks-apply-smart",
	"propose_duplicates":          "tasks-duplicates-list",
	"propose_relations":           "relation-suggestions-list-task",
	"export_tasks":                "export-tasks",

	// Projects.
	"list_projects": "projects-list",

	// Labels, lenses, timeboxes, pages: workspace-shared state.
	"list_labels":         "labels-list",
	"create_label":        "labels-create",
	"propose_lens":        "ai-compile-lens",
	"list_timeboxes":      "timeboxes-list",
	"create_timebox":      "timeboxes-create",
	"add_task_to_timebox": "timeboxes-add-task",
	"list_pages":          "pages-list",
	"get_page":            "pages-get",
	"create_page":         "pages-create",
	"update_page":         "pages-update",
	"generate_page":       "pages-generate",

	// Intake and imports.
	"list_intake_items":      "intake-list",
	"triage_intake_item":     "intake-triage",
	"convert_intake_to_task": "intake-convert",
	"list_import_jobs":       "imports-list",
	"create_import_job":      "imports-create",

	// AI proposals that read one task.
	"propose_priority": "ai-priority-suggestions-list",

	// Favorites.
	"list_favorites": "favorites-list",
	"add_favorite":   "favorites-create",

	// Calendar.
	"list_calendars":         "calendars-list",
	"list_calendar_events":   "calendar-events-list",
	"create_calendar_event":  "events-create",
	"update_calendar_event":  "events-patch",
	"delete_calendar_event":  "events-delete",
	"create_event_from_task": "events-from-task",
	"list_calendar_memos":    "memos-list",
	"toggle_calendar_memo":   "memos-update",
}

// mcpToolsWithoutRESTOperation lists the tools that answer for no REST
// operation, with what they do instead of a counterpart's floor.
//
// Each still declares a floor of its own — the reason below is why there is
// nothing to compare it against, never a reason to skip declaring one.
//
// The reverse direction is deliberately not enumerated. REST registers
// roughly two hundred operations and MCP exposes a few dozen; a table of
// every operation with no tool would be noise, and an operation MCP does
// not expose is not a way into the workspace. What matters is the
// direction that opens one: a tool nothing on the REST side reviews.
var mcpToolsWithoutRESTOperation = map[string]string{
	"propose_tasks_from": "turns free text into task candidates and persists nothing; the REST proposal routes all start from an existing project or task, so none of them is the same request",
	"resolve_task_ref":   "translates a human-readable reference into a public id; REST clients already hold the id by the time they call, so no route does this",
	"list_recent":        "the caller's own recently visited entities, recorded by the transport rather than by a route",
	"list_free_slots":    "derives free time from events the caller can already list; REST exposes the events and leaves the arithmetic to the client",
}

// restOps returns the authenticated REST operation inventory keyed by
// OperationID, with the floor its chi group applies.
func restOps(t *testing.T) map[string]router.OperationRef {
	t.Helper()
	issuer, err := auth.NewJWTIssuer(nil, "nodate-flow", "api", 15*time.Minute)
	if err != nil {
		t.Fatalf("jwt issuer: %v", err)
	}
	res := router.BuildResult(router.Deps{JWT: issuer})
	out := make(map[string]router.OperationRef, len(res.AuthenticatedOps))
	for _, op := range res.AuthenticatedOps {
		if op.OperationID == "" {
			continue
		}
		// A path and its collection share an id only when the same
		// operation is registered twice; keeping the stricter floor means
		// the comparison below cannot be satisfied by the laxer copy.
		if prev, ok := out[op.OperationID]; ok && prev.WriteFloor.AtLeast(op.WriteFloor) {
			continue
		}
		out[op.OperationID] = op
	}
	return out
}

// TestMCPToolFloorsAreNotBelowREST holds every mapped tool to the floor its
// REST counterpart applies.
//
// A tool below its counterpart is not a difference of opinion between two
// tables — it is a way to perform, through an agent, a change the same
// account is refused in the browser.
func TestMCPToolFloorsAreNotBelowREST(t *testing.T) {
	t.Parallel()

	floors := mcp.ToolFloors()
	if len(floors) == 0 {
		t.Fatal("no tools are registered; the comparison below would be looking at nothing")
	}
	ops := restOps(t)
	if len(ops) < 100 {
		t.Fatalf("the router reported only %d authenticated operations; the comparison below would be looking at nothing", len(ops))
	}

	for _, name := range sortedToolNames(floors) {
		opID, mapped := mcpRESTCounterparts[name]
		if !mapped {
			continue
		}
		op, exists := ops[opID]
		if !exists {
			t.Errorf("tool %q names REST operation %q, which the router does not register; follow the rename, or move the tool to mcpToolsWithoutRESTOperation",
				name, opID)
			continue
		}
		if !floors[name].AtLeast(op.WriteFloor) {
			t.Errorf("tool %q is registered under %q but its REST counterpart %s (%s %s) requires %q; an agent would reach through MCP what the same caller is refused over HTTP — raise the tool's floor",
				name, floors[name], opID, op.Method, op.Path, op.WriteFloor)
		}
	}
}

// TestEveryMCPToolIsAccountedForAgainstREST proves the two tables above
// cover the tool registry exactly.
//
// Without it the comparison silently shrinks: a tool absent from both
// tables is compared against nothing, which is the state the whole surface
// was in.
func TestEveryMCPToolIsAccountedForAgainstREST(t *testing.T) {
	t.Parallel()

	floors := mcp.ToolFloors()
	if len(floors) == 0 {
		t.Fatal("no tools are registered; the check below would be looking at nothing")
	}

	for _, name := range sortedToolNames(floors) {
		_, mapped := mcpRESTCounterparts[name]
		_, standalone := mcpToolsWithoutRESTOperation[name]
		switch {
		case mapped && standalone:
			t.Errorf("tool %q is both mapped to a REST operation and listed as having none; it has one answer or the other", name)
		case !mapped && !standalone:
			t.Errorf("tool %q is in neither table, so its floor is compared against nothing; map it to the OperationID of the REST route that performs the same change, or list it in mcpToolsWithoutRESTOperation with the reason REST has no equivalent", name)
		}
		if floors[name] == auth.FloorNone {
			t.Errorf("tool %q is registered under no floor; every tool reaches a workspace through a token bound to it, so workspace membership is the least it can require", name)
		}
	}

	for name := range mcpRESTCounterparts {
		if _, ok := floors[name]; !ok {
			t.Errorf("mcpRESTCounterparts maps %q, which is no longer a registered tool; drop the stale entry", name)
		}
	}
	for name, reason := range mcpToolsWithoutRESTOperation {
		if _, ok := floors[name]; !ok {
			t.Errorf("mcpToolsWithoutRESTOperation lists %q (%q), which is no longer a registered tool; drop the stale entry", name, reason)
		}
	}
}

func sortedToolNames(floors map[string]auth.Floor) []string {
	out := make([]string, 0, len(floors))
	for name := range floors {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}
