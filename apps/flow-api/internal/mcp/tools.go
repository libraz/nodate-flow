// Package mcp tool registry and tool implementations.
package mcp

import (
	"context"
	"database/sql"
	"encoding/json"
	stderrors "errors"
	"time"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/auth"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated/calendar"
	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
)

// toolRun is the signature every tool body has.
type toolRun func(ctx context.Context, deps Deps, s *session, args json.RawMessage) (any, error)

// tool is the internal descriptor for a registered MCP tool.
type tool struct {
	name          string
	description   string
	requiredScope string
	// floor is the minimum role the caller has to hold. It is filled in by
	// [Handler.register] from that function's first argument rather than
	// written in the literal below, so a tool cannot be registered without
	// one.
	floor       auth.Floor
	inputSchema map[string]any
	run         toolRun
}

// toolDescriptor is the public shape returned by tools/list.
type toolDescriptor struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
}

func (h *Handler) listTools() []toolDescriptor {
	out := make([]toolDescriptor, 0, len(h.tools))
	for _, t := range h.tools {
		out = append(out, toolDescriptor{
			Name:        t.name,
			Description: t.description,
			InputSchema: t.inputSchema,
		})
	}
	return out
}

// register records a tool under the role floor its callers have to clear.
//
// The floor is a positional argument rather than another field on the
// literal below because a keyed composite literal that omits a field still
// compiles: a tool registered without a floor would read as "no floor at
// all" and nothing would say so. Leaving the argument out does not compile.
//
// The floor is also bound to the call here, so it is the value the ACL
// helpers apply rather than a label each tool body separately remembers to
// honour. A floor that no longer matches what the tool enforces therefore
// changes behaviour instead of going stale.
func (h *Handler) register(floor auth.Floor, t tool) {
	t.floor = floor
	t.run = withFloor(floor, t.run)
	h.tools[t.name] = t
}

// withFloor binds the declared floor to the session the tool body runs
// with. The session is copied rather than mutated so a caller's value is
// never left carrying the floor of a tool it has finished invoking.
func withFloor(floor auth.Floor, run toolRun) toolRun {
	return func(ctx context.Context, deps Deps, s *session, args json.RawMessage) (any, error) {
		if s == nil {
			return nil, apierrors.New(apierrors.McpTokenUnknown)
		}
		scoped := *s
		scoped.floor = floor
		return run(ctx, deps, &scoped, args)
	}
}

func registerTools(h *Handler) {
	h.register(auth.FloorWorkspaceMember, tool{
		name:          "list_projects",
		description:   "List projects in the caller's workspace that the caller can open. Page with limit/offset while hasMore is true. Each project carries isArchived; archived projects are hidden from the app's pickers.",
		requiredScope: "read:workspace",
		inputSchema: objectSchema(map[string]any{
			"limit":  intSchema("Max number of rows (1..200, default 50).", Constraints{Min: intPtr(1), Max: intPtr(200)}),
			"offset": intSchema("Row offset.", Constraints{Min: intPtr(0)}),
		}, nil),
		run: runListProjects,
	})
	h.register(auth.FloorWorkspaceMember, tool{
		name:          "list_tasks",
		description:   "List tasks, optionally scoped to a project.",
		requiredScope: "read:workspace",
		inputSchema: objectSchema(map[string]any{
			"projectId": stringSchema("Project public id (UUID v7). Optional.", Constraints{Pattern: publicIDPattern}),
			"limit":     intSchema("Max number of rows (1..200).", Constraints{Min: intPtr(1), Max: intPtr(200)}),
			"offset":    intSchema("Row offset.", Constraints{Min: intPtr(0)}),
		}, nil),
		run: runListTasks,
	})
	h.register(auth.FloorWorkspaceMember, tool{
		name:          "get_task",
		description:   "Fetch a single task by public id.",
		requiredScope: "read:workspace",
		inputSchema: objectSchema(map[string]any{
			"taskId": stringSchema("Task public id (UUID v7).", Constraints{Pattern: publicIDPattern}),
		}, []string{"taskId"}),
		run: runGetTask,
	})
	h.register(auth.FloorProjectEditor, tool{
		name:          "create_task",
		description:   "Create a new task in a project. The task is recorded as created by the user whose token made the call, and starts with no assignee.",
		requiredScope: "write:workspace",
		inputSchema: objectSchema(map[string]any{
			"projectId":   stringSchema("Project public id (UUID v7).", Constraints{Pattern: publicIDPattern}),
			"title":       stringSchema("Task title.", Constraints{MinLength: intPtr(1), MaxLength: intPtr(taskTitleLimit)}),
			"description": stringSchema("Optional description.", Constraints{MaxLength: intPtr(50000)}),
			"priority":    intSchema("Priority 0..4.", Constraints{Min: intPtr(0), Max: intPtr(4)}),
			"dueOn":       stringSchema("YYYY-MM-DD.", Constraints{Pattern: `^\d{4}-\d{2}-\d{2}$`}),
			"startOn":     stringSchema("YYYY-MM-DD.", Constraints{Pattern: `^\d{4}-\d{2}-\d{2}$`}),
		}, []string{"projectId", "title"}),
		run: runCreateTask,
	})
	h.register(auth.FloorProjectEditor, tool{
		name:          "update_task",
		description:   "Update mutable fields of a task. Omitted fields are left as they are; an empty dueOn or startOn clears that date.",
		requiredScope: "write:workspace",
		inputSchema: objectSchema(map[string]any{
			"taskId":      stringSchema("Task public id (UUID v7).", Constraints{Pattern: publicIDPattern}),
			"title":       stringSchema("New title.", Constraints{MinLength: intPtr(1), MaxLength: intPtr(taskTitleLimit)}),
			"description": stringSchema("New description.", Constraints{MaxLength: intPtr(50000)}),
			"priority":    intSchema("New priority 0..4.", Constraints{Min: intPtr(0), Max: intPtr(4)}),
			"dueOn":       stringSchema("YYYY-MM-DD, or an empty string to clear the due date.", Constraints{Pattern: `^(\d{4}-\d{2}-\d{2})?$`}),
			"startOn":     stringSchema("YYYY-MM-DD, or an empty string to clear the start date.", Constraints{Pattern: `^(\d{4}-\d{2}-\d{2})?$`}),
		}, []string{"taskId"}),
		run: runUpdateTask,
	})
	h.register(auth.FloorProjectEditor, tool{
		name:          "transition_task",
		description:   "Apply a state machine transition to a task. Valid transitions: start, block, unblock, submit, complete, reopen, cancel.",
		requiredScope: "write:workspace",
		inputSchema: objectSchema(map[string]any{
			"taskId":     stringSchema("Task public id (UUID v7).", Constraints{Pattern: publicIDPattern}),
			"transition": stringSchema("Transition name: start | block | unblock | submit | complete | reopen | cancel.", Constraints{Pattern: `^(start|block|unblock|submit|complete|reopen|cancel)$`}),
			"reason":     stringSchema("Optional reason for the transition.", Constraints{MaxLength: intPtr(2000)}),
		}, []string{"taskId", "transition"}),
		run: runTransitionTask,
	})
	h.register(auth.FloorProjectCommenter, tool{
		name:          "add_comment",
		description:   "Append a comment to a task.",
		requiredScope: "write:workspace",
		inputSchema: objectSchema(map[string]any{
			// The body bound is what the REST comment input declares. The column
			// is MEDIUMTEXT and would take more, but a comment one surface
			// accepts has to be one the other accepts too.
			"taskId": stringSchema("Task public id (UUID v7).", Constraints{Pattern: publicIDPattern}),
			"body":   stringSchema("Comment body.", Constraints{MinLength: intPtr(1), MaxLength: intPtr(50000)}),
		}, []string{"taskId", "body"}),
		run: runAddComment,
	})
	h.register(auth.FloorWorkspaceMember, tool{
		name:          "search_tasks",
		description:   "Search tasks by title or description within the workspace.",
		requiredScope: "read:workspace",
		inputSchema: objectSchema(map[string]any{
			"query":  stringSchema("Search term (matched against title and description).", Constraints{MinLength: intPtr(1), MaxLength: intPtr(200)}),
			"limit":  intSchema("Max results (1-200, default 50).", Constraints{Min: intPtr(1), Max: intPtr(200)}),
			"offset": intSchema("Pagination offset (default 0).", Constraints{Min: intPtr(0)}),
		}, []string{"query"}),
		run: runSearchTasks,
	})
	h.register(auth.FloorWorkspaceMember, tool{
		name:          "propose_tasks_from",
		description:   "Ask the workspace LLM to propose tasks from free text. Requires a configured AI provider.",
		requiredScope: "write:workspace",
		inputSchema: objectSchema(map[string]any{
			// The whole text is sent to the provider as one prompt, so the
			// bound is what a caller may spend in a single call.
			"source": stringSchema("Input text to propose tasks from.", Constraints{MinLength: intPtr(1), MaxLength: intPtr(50000)}),
		}, []string{"source"}),
		run: runProposeTasksFrom,
	})
	h.register(auth.FloorWorkspaceMember, tool{
		name:          "propose_priority",
		description:   "Ask the workspace LLM to propose a priority for a task. Requires a configured AI provider.",
		requiredScope: "write:workspace",
		inputSchema: objectSchema(map[string]any{
			"taskId": stringSchema("Task public id (UUID v7).", Constraints{Pattern: publicIDPattern}),
		}, []string{"taskId"}),
		run: runProposePriority,
	})
	h.register(auth.FloorProjectEditor, tool{
		name:          "propose_steps",
		description:   "Ask the workspace LLM to break an existing task into concrete execution steps.",
		requiredScope: "write:workspace",
		inputSchema: objectSchema(map[string]any{
			"taskId": stringSchema("Task public id (UUID v7).", Constraints{Pattern: publicIDPattern}),
		}, []string{"taskId"}),
		run: runProposeSteps,
	})
	h.register(auth.FloorProjectEditor, tool{
		name:          "apply_steps",
		description:   "Create the given steps as child tasks under an existing parent task.",
		requiredScope: "write:workspace",
		inputSchema: objectSchema(map[string]any{
			"parentTaskId": stringSchema("Parent task public id (UUID v7).", Constraints{Pattern: publicIDPattern}),
			// Each step becomes a child task created inside one transaction
			// that holds the project row to number them, so the length of
			// this array is how long every other writer of the project
			// waits. The lower bound is the one the REST apply-steps body
			// states: a call with no steps asks for nothing.
			"steps": arraySchema("Step definitions to create as child tasks.",
				objectSchema(map[string]any{
					"title":       stringSchema("Step title.", Constraints{MinLength: intPtr(1), MaxLength: intPtr(taskTitleLimit)}),
					"description": stringSchema("Step description (optional).", Constraints{MaxLength: intPtr(50000)}),
					"priority":    intSchema("Step priority 0..4 (optional).", Constraints{Min: intPtr(0), Max: intPtr(4)}),
				}, []string{"title"}),
				Constraints{MinItems: intPtr(1), MaxItems: intPtr(100)}),
		}, []string{"parentTaskId", "steps"}),
		run: runApplySteps,
	})
	h.register(auth.FloorWorkspaceMember, tool{
		name:          "propose_duplicates",
		description:   "Return likely-duplicate tasks for a given task by embedding similarity.",
		requiredScope: "write:workspace",
		inputSchema: objectSchema(map[string]any{
			"taskId": stringSchema("Task public id (UUID v7).", Constraints{Pattern: publicIDPattern}),
		}, []string{"taskId"}),
		run: runProposeDuplicates,
	})
	h.register(auth.FloorWorkspaceMember, tool{
		name:          "propose_lens",
		description:   "Compile a natural-language query into a validated Lens view JSON.",
		requiredScope: "write:workspace",
		inputSchema: objectSchema(map[string]any{
			// The bound is what the REST compile-lens body declares. Both
			// routes hand the prompt to the same compiler, so a prompt one
			// accepts has to be one the other accepts too.
			"prompt": stringSchema("Natural-language description of the desired view.", Constraints{MinLength: intPtr(1), MaxLength: intPtr(500)}),
		}, []string{"prompt"}),
		run: runProposeLens,
	})
	h.register(auth.FloorWorkspaceMember, tool{
		name:          "list_timeboxes",
		description:   "List timeboxes (sprints / iterations) in the caller's workspace.",
		requiredScope: "read:workspace",
		inputSchema: objectSchema(map[string]any{
			"limit":  intSchema("Max number of rows (1..200).", Constraints{Min: intPtr(1), Max: intPtr(200)}),
			"offset": intSchema("Row offset.", Constraints{Min: intPtr(0)}),
		}, nil),
		run: runListTimeboxes,
	})
	h.register(auth.FloorProjectEditor, tool{
		name:          "create_timebox",
		description:   "Create a new timebox (sprint / iteration) in the workspace.",
		requiredScope: "write:workspace",
		inputSchema: objectSchema(map[string]any{
			"name":        stringSchema("Timebox name.", Constraints{MinLength: intPtr(1), MaxLength: intPtr(200)}),
			"startsOn":    stringSchema("Start date YYYY-MM-DD.", Constraints{Pattern: `^\d{4}-\d{2}-\d{2}$`}),
			"endsOn":      stringSchema("End date YYYY-MM-DD.", Constraints{Pattern: `^\d{4}-\d{2}-\d{2}$`}),
			"description": stringSchema("Optional description.", Constraints{MaxLength: intPtr(4000)}),
			"projectId":   stringSchema("Optional project public id (UUID v7) to scope the timebox.", Constraints{Pattern: publicIDPattern}),
		}, []string{"name", "startsOn", "endsOn"}),
		run: runCreateTimebox,
	})
	h.register(auth.FloorWorkspaceMember, tool{
		name:          "add_task_to_timebox",
		description:   "Add a task to a timebox.",
		requiredScope: "write:workspace",
		inputSchema: objectSchema(map[string]any{
			"timeboxId": stringSchema("Timebox public id (UUID v7).", Constraints{Pattern: publicIDPattern}),
			"taskId":    stringSchema("Task public id (UUID v7).", Constraints{Pattern: publicIDPattern}),
		}, []string{"timeboxId", "taskId"}),
		run: runAddTaskToTimebox,
	})
	h.register(auth.FloorWorkspaceMember, tool{
		name:          "export_tasks",
		description:   "Export tasks as JSON for MCP consumers. Optionally scoped to a project.",
		requiredScope: "read:workspace",
		inputSchema: objectSchema(map[string]any{
			"projectId": stringSchema("Optional project public id (UUID v7) to scope export.", Constraints{Pattern: publicIDPattern}),
			"limit":     intSchema("Max tasks to export (1..200, default 200).", Constraints{Min: intPtr(1), Max: intPtr(200)}),
		}, nil),
		run: runExportTasks,
	})
	h.register(auth.FloorWorkspaceMember, tool{
		name:          "propose_relations",
		description:   "Given a task, find related or duplicate tasks by embedding similarity. Returns structured suggestions with kind.",
		requiredScope: "write:workspace",
		inputSchema: objectSchema(map[string]any{
			"taskId": stringSchema("Task public id (UUID v7).", Constraints{Pattern: publicIDPattern}),
		}, []string{"taskId"}),
		run: runProposeRelations,
	})
	h.register(auth.FloorWorkspaceMember, tool{
		name:          "list_pages",
		description:   "List wiki pages for the current workspace. Returns root-level pages by default; total counts every page the listing covers, so page with limit/offset until it is reached.",
		requiredScope: "read:workspace",
		inputSchema: objectSchema(map[string]any{
			"projectId":    stringSchema("Optional project public id (UUID v7) to scope pages.", Constraints{Pattern: publicIDPattern}),
			"parentPageId": stringSchema("Optional parent page public id (UUID v7) to list child pages.", Constraints{Pattern: publicIDPattern}),
			"limit":        intSchema("Max number of rows (1..200, default 50).", Constraints{Min: intPtr(1), Max: intPtr(200)}),
			"offset":       intSchema("Row offset.", Constraints{Min: intPtr(0)}),
		}, nil),
		run: runListPages,
	})
	h.register(auth.FloorWorkspaceMember, tool{
		name:          "get_page",
		description:   "Get a wiki page by ID, including its content.",
		requiredScope: "read:workspace",
		inputSchema: objectSchema(map[string]any{
			"pageId": stringSchema("Page public id (UUID v7).", Constraints{Pattern: publicIDPattern}),
		}, []string{"pageId"}),
		run: runGetPage,
	})
	h.register(auth.FloorProjectEditor, tool{
		name:          "create_page",
		description:   "Create a new wiki page.",
		requiredScope: "write:workspace",
		inputSchema: objectSchema(map[string]any{
			// The title bound is the width of pages.title, which is also what
			// the REST page inputs declare. A narrower bound here would refuse
			// a title the same column accepts from the other surface.
			"title":        stringSchema("Page title.", Constraints{MinLength: intPtr(1), MaxLength: intPtr(pageTitleLimit)}),
			"body":         stringSchema("Optional page body content.", Constraints{MaxLength: intPtr(100000)}),
			"parentPageId": stringSchema("Optional parent page public id (UUID v7).", Constraints{Pattern: publicIDPattern}),
			"projectId":    stringSchema("Optional project public id (UUID v7).", Constraints{Pattern: publicIDPattern}),
		}, []string{"title"}),
		run: runCreatePage,
	})
	h.register(auth.FloorProjectEditor, tool{
		name:          "update_page",
		description:   "Update a wiki page's title or content.",
		requiredScope: "write:workspace",
		inputSchema: objectSchema(map[string]any{
			"pageId": stringSchema("Page public id (UUID v7).", Constraints{Pattern: publicIDPattern}),
			"title":  stringSchema("New title (optional).", Constraints{MaxLength: intPtr(pageTitleLimit)}),
			"body":   stringSchema("New body content (optional).", Constraints{MaxLength: intPtr(100000)}),
		}, []string{"pageId"}),
		run: runUpdatePage,
	})
	h.register(auth.FloorProjectEditor, tool{
		name:          "generate_page",
		description:   "Generate a wiki page using AI based on project or task context. Refuses and writes nothing when the workspace has no AI provider or the provider returns no usable draft; a success always means the stored body was drafted, never echoed back from the request.",
		requiredScope: "write:workspace",
		inputSchema: objectSchema(map[string]any{
			// The instruction bound is what the REST page-generation body
			// declares for the same prompt.
			"contextDescription": stringSchema("What the page should be about.", Constraints{MinLength: intPtr(1), MaxLength: intPtr(10000)}),
			"projectId":          stringSchema("Optional project public id (UUID v7).", Constraints{Pattern: publicIDPattern}),
			// Every id here is fetched and its content goes into the prompt
			// sent to the provider, so the length of this array is what the
			// workspace is billed for on a call it did not size.
			"taskIds": arraySchema("Optional task public ids (UUID v7) to include as context.",
				stringSchema("Task public id (UUID v7).", Constraints{Pattern: publicIDPattern}),
				Constraints{MaxItems: intPtr(50)}),
		}, []string{"contextDescription"}),
		run: runGeneratePage,
	})
	h.register(auth.FloorProjectEditor, tool{
		name:          "smart_create_task",
		description:   "Create a task with AI-suggested subtask breakdown and assignees based on past ticket patterns.",
		requiredScope: "write:workspace",
		inputSchema: objectSchema(map[string]any{
			"projectId":   stringSchema("Project public id (UUID v7).", Constraints{Pattern: publicIDPattern}),
			"title":       stringSchema("Task title.", Constraints{MinLength: intPtr(1), MaxLength: intPtr(taskTitleLimit)}),
			"description": stringSchema("Optional task description for better AI analysis.", Constraints{MaxLength: intPtr(50000)}),
		}, []string{"projectId", "title"}),
		run: runSmartCreateTask,
	})

	// Calendar tools.
	h.register(auth.FloorWorkspaceMember, tool{
		name:          "list_calendars",
		description:   "List calendars the caller subscribes to in the workspace.",
		requiredScope: "read:workspace",
		inputSchema:   objectSchema(nil, nil),
		run:           runListCalendars,
	})
	h.register(auth.FloorWorkspaceMember, tool{
		name:          "list_calendar_events",
		description:   "List events in a date range across all subscribed calendars. Use startDate/endDate (YYYY-MM-DD) for day ranges, or startAt/endAt (unix seconds since epoch) for sub-day windows.",
		requiredScope: "read:workspace",
		inputSchema: objectSchema(map[string]any{
			"startDate": stringSchema("Range start as YYYY-MM-DD (mutually exclusive with startAt).", Constraints{Pattern: `^\d{4}-\d{2}-\d{2}$`}),
			"endDate":   stringSchema("Range end as YYYY-MM-DD (mutually exclusive with endAt).", Constraints{Pattern: `^\d{4}-\d{2}-\d{2}$`}),
			"startAt":   intSchema("Range start, unix seconds since epoch (mutually exclusive with startDate).", Constraints{Min: intPtr(0)}),
			"endAt":     intSchema("Range end, unix seconds since epoch (mutually exclusive with endDate).", Constraints{Min: intPtr(0)}),
		}, nil),
		run: runListCalendarEvents,
	})
	h.register(auth.FloorWorkspaceMember, tool{
		name:          "create_calendar_event",
		description:   "Create a calendar event. Use startAt/endAt (unix seconds since epoch) for timed events, or startDate/endDate (YYYY-MM-DD) when allDay=true.",
		requiredScope: "write:workspace",
		inputSchema: objectSchema(map[string]any{
			"calendarId":  stringSchema("Calendar public id (UUID v7).", Constraints{Pattern: publicIDPattern}),
			"title":       stringSchema("Event title.", Constraints{MinLength: intPtr(1), MaxLength: intPtr(eventTitleLimit)}),
			"startAt":     intSchema("Start time, unix seconds since epoch (timed events).", Constraints{Min: intPtr(0)}),
			"endAt":       intSchema("End time, unix seconds since epoch (timed events).", Constraints{Min: intPtr(0)}),
			"startDate":   stringSchema("Start date YYYY-MM-DD (all-day events).", Constraints{Pattern: `^\d{4}-\d{2}-\d{2}$`}),
			"endDate":     stringSchema("End date YYYY-MM-DD (all-day events, inclusive).", Constraints{Pattern: `^\d{4}-\d{2}-\d{2}$`}),
			"kind":        stringSchema("Event kind (default event).", Constraints{Enum: calendarEventKinds}),
			"showAs":      stringSchema("How the time reads to others (default busy).", Constraints{Enum: calendarEventShowAs}),
			"flexibility": stringSchema("Whether the commitment can be moved (default fixed). Independent of showAs, which only says whether the time reads as taken.", Constraints{Enum: calendarEventFlexibility}),
			"visibility":  stringSchema("Who may see the event's details.", Constraints{Enum: calendarEventVisibility}),
			"ownerUserId": stringSchema("Owner user public id (UUID v7). Defaults to caller.", Constraints{Pattern: publicIDPattern}),
			"allDay":      boolSchema("Whether this is an all-day event."),
			// location and blockLabel are bounded by the widths of
			// calendar_events.location and calendar_events.block_label. Both are
			// narrow columns, so a value past them is refused by the driver
			// rather than by the schema unless the bound is advertised here.
			"location": stringSchema("Event location.", Constraints{MaxLength: intPtr(500)}),
			// The memo column is MEDIUMTEXT and would take far more, so this
			// bound is on what one call may carry rather than on the column.
			// update_calendar_event states the same one: a memo one write
			// tool accepts has to be one the other accepts too.
			"memo":       stringSchema("Event memo/notes.", Constraints{MaxLength: intPtr(10000)}),
			"blockLabel": stringSchema("Label for block-type events.", Constraints{MaxLength: intPtr(100)}),
		}, []string{"calendarId", "title"}),
		run: runCreateCalendarEvent,
	})
	h.register(auth.FloorWorkspaceMember, tool{
		name:          "update_calendar_event",
		description:   "Update mutable fields of a calendar event. Times are unix seconds since epoch (startAt/endAt); use startDate/endDate (YYYY-MM-DD) for all-day events. For a recurring event, scope says how much of the series the change reaches; omitting it changes the whole series.",
		requiredScope: "write:workspace",
		inputSchema: objectSchema(map[string]any{
			"eventId":     stringSchema("Event public id (UUID v7).", Constraints{Pattern: publicIDPattern}),
			"title":       stringSchema("New title.", Constraints{MinLength: intPtr(1), MaxLength: intPtr(eventTitleLimit)}),
			"startAt":     intSchema("New start time, unix seconds since epoch.", Constraints{Min: intPtr(0)}),
			"endAt":       intSchema("New end time, unix seconds since epoch.", Constraints{Min: intPtr(0)}),
			"startDate":   stringSchema("New start date YYYY-MM-DD (all-day events).", Constraints{Pattern: `^\d{4}-\d{2}-\d{2}$`}),
			"endDate":     stringSchema("New last day YYYY-MM-DD (all-day events, inclusive, as list_calendar_events returns it).", Constraints{Pattern: `^\d{4}-\d{2}-\d{2}$`}),
			"kind":        stringSchema("New event kind.", Constraints{Enum: calendarEventKinds}),
			"showAs":      stringSchema("New reading of the time to others.", Constraints{Enum: calendarEventShowAs}),
			"flexibility": stringSchema("New answer to whether the commitment can be moved. Independent of showAs, which only says whether the time reads as taken.", Constraints{Enum: calendarEventFlexibility}),
			"visibility":  stringSchema("New answer to who may see the event's details.", Constraints{Enum: calendarEventVisibility}),
			"location":    stringSchema("New location; an empty string clears it.", Constraints{MaxLength: intPtr(500)}),
			"memo":        stringSchema("New memo/notes; an empty string clears it.", Constraints{MaxLength: intPtr(10000)}),
			"blockLabel":  stringSchema("New block label; an empty string clears it.", Constraints{MaxLength: intPtr(100)}),
			// The same two arguments the REST patch route takes, under the
			// same names and with the same closed set of values: a
			// recurring series is one master row, so without them every
			// edit rewrites every occurrence.
			"scope":           stringSchema("Which occurrences of a recurring series this update reaches. Omitted means the whole series.", Constraints{Enum: occurrenceScopes}),
			"occurrenceStart": intSchema("The occurrence's start under the series rule, unix seconds since epoch (UTC). Required when scope is not series; it identifies the occurrence even after the edit moves it.", Constraints{Min: intPtr(0)}),
		}, []string{"eventId"}),
		run: runUpdateCalendarEvent,
	})
	h.register(auth.FloorWorkspaceMember, tool{
		name:          "delete_calendar_event",
		description:   "Delete a calendar event (soft-delete). For a recurring event, scope says how much of the series the delete reaches; omitting it deletes the whole series.",
		requiredScope: "write:workspace",
		inputSchema: objectSchema(map[string]any{
			"eventId":         stringSchema("Event public id (UUID v7).", Constraints{Pattern: publicIDPattern}),
			"scope":           stringSchema("Which occurrences of a recurring series this delete reaches. Omitted means the whole series.", Constraints{Enum: occurrenceScopes}),
			"occurrenceStart": intSchema("The occurrence's start under the series rule, unix seconds since epoch (UTC). Required when scope is not series.", Constraints{Min: intPtr(0)}),
		}, []string{"eventId"}),
		run: runDeleteCalendarEvent,
	})
	h.register(auth.FloorWorkspaceMember, tool{
		name:          "list_free_slots",
		description:   "Find available time slots for a user on a given date within working hours (09:00-18:00).",
		requiredScope: "read:workspace",
		inputSchema: objectSchema(map[string]any{
			"userId":          stringSchema("User public id (UUID v7). Defaults to caller.", Constraints{Pattern: publicIDPattern}),
			"date":            stringSchema("Date to check (YYYY-MM-DD).", Constraints{Pattern: `^\d{4}-\d{2}-\d{2}$`}),
			"durationMinutes": intSchema("Minimum slot duration in minutes (default 60).", Constraints{Min: intPtr(1), Max: intPtr(1440)}),
		}, []string{"date"}),
		run: runListFreeSlots,
	})
	h.register(auth.FloorProjectEditor, tool{
		name:          "create_event_from_task",
		description:   "Create a calendar event linked to an existing task. Times are unix seconds since epoch.",
		requiredScope: "write:workspace",
		inputSchema: objectSchema(map[string]any{
			"taskId":     stringSchema("Task public id (UUID v7).", Constraints{Pattern: publicIDPattern}),
			"calendarId": stringSchema("Calendar public id (UUID v7).", Constraints{Pattern: publicIDPattern}),
			"startAt":    intSchema("Start time, unix seconds since epoch.", Constraints{Min: intPtr(0)}),
			"endAt":      intSchema("End time, unix seconds since epoch.", Constraints{Min: intPtr(0)}),
		}, []string{"taskId", "calendarId", "startAt", "endAt"}),
		run: runCreateEventFromTask,
	})
	h.register(auth.FloorWorkspaceMember, tool{
		name:          "list_calendar_memos",
		description:   "List memos (shared to-do items) in a calendar.",
		requiredScope: "read:workspace",
		inputSchema: objectSchema(map[string]any{
			"calendarId": stringSchema("Calendar public id (UUID v7).", Constraints{Pattern: publicIDPattern}),
		}, []string{"calendarId"}),
		run: runListCalendarMemos,
	})
	h.register(auth.FloorWorkspaceMember, tool{
		name:          "toggle_calendar_memo",
		description:   "Toggle the done/undone state of a calendar memo.",
		requiredScope: "write:workspace",
		inputSchema: objectSchema(map[string]any{
			"memoId":     stringSchema("Memo public id (UUID v7).", Constraints{Pattern: publicIDPattern}),
			"calendarId": stringSchema("Calendar public id (UUID v7).", Constraints{Pattern: publicIDPattern}),
			"done":       boolSchema("Set to true to mark done, false to unmark."),
		}, []string{"memoId", "calendarId", "done"}),
		run: runToggleCalendarMemo,
	})
	// Label & archive tools.
	h.register(auth.FloorWorkspaceMember, tool{
		name:          "list_labels",
		description:   "List labels in the caller's workspace.",
		requiredScope: "read:workspace",
		inputSchema: objectSchema(map[string]any{
			"limit":  intSchema("Max number of rows (1..200).", Constraints{Min: intPtr(1), Max: intPtr(200)}),
			"offset": intSchema("Row offset.", Constraints{Min: intPtr(0)}),
		}, nil),
		run: runListLabels,
	})
	h.register(auth.FloorWorkspaceMember, tool{
		name:          "create_label",
		description:   "Create a new label in the workspace.",
		requiredScope: "write:workspace",
		inputSchema: objectSchema(map[string]any{
			// The name bound is the width of labels.name, which is also what the
			// REST label inputs declare. A wider bound here would accept a name
			// the column cannot store and fail in the driver instead.
			"name": stringSchema("Label name.", Constraints{MinLength: intPtr(1), MaxLength: intPtr(64)}),
			// The pattern already fixes the length at seven, but the bound is
			// stated so that a check reading declared numbers can see it
			// without inferring a length out of a regular expression. The
			// number is the one the REST label inputs declare for the same
			// column, since two surfaces stating different bounds for one
			// column means one of them is wrong.
			"color":       stringSchema("Hex color (e.g. #ef4444). Optional.", Constraints{Pattern: `^#[0-9a-fA-F]{6}$`, MaxLength: intPtr(16)}),
			"description": stringSchema("Optional description.", Constraints{MaxLength: intPtr(255)}),
		}, []string{"name"}),
		run: runCreateLabel,
	})
	h.register(auth.FloorProjectEditor, tool{
		name:          "add_task_label",
		description:   "Attach a label to a task.",
		requiredScope: "write:workspace",
		inputSchema: objectSchema(map[string]any{
			"taskId":  stringSchema("Task public id (UUID v7).", Constraints{Pattern: publicIDPattern}),
			"labelId": stringSchema("Label public id (UUID v7).", Constraints{Pattern: publicIDPattern}),
		}, []string{"taskId", "labelId"}),
		run: runAddTaskLabel,
	})
	h.register(auth.FloorProjectEditor, tool{
		name:          "remove_task_label",
		description:   "Remove a label from a task.",
		requiredScope: "write:workspace",
		inputSchema: objectSchema(map[string]any{
			"taskId":  stringSchema("Task public id (UUID v7).", Constraints{Pattern: publicIDPattern}),
			"labelId": stringSchema("Label public id (UUID v7).", Constraints{Pattern: publicIDPattern}),
		}, []string{"taskId", "labelId"}),
		run: runRemoveTaskLabel,
	})
	h.register(auth.FloorWorkspaceMember, tool{
		name:          "resolve_task_ref",
		description:   "Resolve a human-readable task reference (e.g. NF-42) to a task public id.",
		requiredScope: "read:workspace",
		inputSchema: objectSchema(map[string]any{
			"ref": stringSchema("Task reference in PROJECT_IDENTIFIER-NUMBER format (e.g. NF-42).", Constraints{Pattern: `^[A-Za-z][A-Za-z0-9_]*-[0-9]+$`}),
		}, []string{"ref"}),
		run: runResolveTaskRef,
	})
	h.register(auth.FloorProjectEditor, tool{
		name:          "archive_task",
		description:   "Archive a task.",
		requiredScope: "write:workspace",
		inputSchema: objectSchema(map[string]any{
			"taskId": stringSchema("Task public id (UUID v7).", Constraints{Pattern: publicIDPattern}),
		}, []string{"taskId"}),
		run: runArchiveTask,
	})
	h.register(auth.FloorProjectEditor, tool{
		name:          "unarchive_task",
		description:   "Unarchive a task.",
		requiredScope: "write:workspace",
		inputSchema: objectSchema(map[string]any{
			"taskId": stringSchema("Task public id (UUID v7).", Constraints{Pattern: publicIDPattern}),
		}, []string{"taskId"}),
		run: runUnarchiveTask,
	})

	// Favorites and reactions.
	h.register(auth.FloorWorkspaceMember, tool{
		name:          "list_favorites",
		description:   "List the current user's favorite items in the workspace.",
		requiredScope: "read:workspace",
		inputSchema: objectSchema(map[string]any{
			"limit":  intSchema("Max results (1..50, default 50).", Constraints{Min: intPtr(1), Max: intPtr(50)}),
			"offset": intSchema("Skip N results.", Constraints{Min: intPtr(0)}),
		}, nil),
		run: runListFavorites,
	})
	h.register(auth.FloorWorkspaceMember, tool{
		name:          "add_favorite",
		description:   "Add a task, project, page, lens, or timebox to the user's favorites.",
		requiredScope: "write:workspace",
		inputSchema: objectSchema(map[string]any{
			"targetType": stringSchema("Entity type: project, task, page, lens, or timebox.", Constraints{Pattern: `^(project|task|page|lens|timebox)$`}),
			"targetId":   stringSchema("Public ID of the entity to favorite.", Constraints{Pattern: publicIDPattern}),
		}, []string{"targetType", "targetId"}),
		run: runAddFavorite,
	})
	h.register(auth.FloorProjectCommenter, tool{
		name:          "add_reaction",
		description:   "Add an emoji reaction to a task.",
		requiredScope: "write:workspace",
		inputSchema: objectSchema(map[string]any{
			"taskId": stringSchema("Task public id (UUID v7).", Constraints{Pattern: publicIDPattern}),
			"emoji":  stringSchema("Unicode emoji character.", Constraints{MinLength: intPtr(1), MaxLength: intPtr(32)}),
		}, []string{"taskId", "emoji"}),
		run: runAddReaction,
	})
	h.register(auth.FloorWorkspaceMember, tool{
		name:          "list_reactions",
		description:   "List emoji reactions on a task.",
		requiredScope: "read:workspace",
		inputSchema: objectSchema(map[string]any{
			"taskId": stringSchema("Task public id (UUID v7).", Constraints{Pattern: publicIDPattern}),
		}, []string{"taskId"}),
		run: runListReactions,
	})

	// Intake triage and description version history.
	h.register(auth.FloorWorkspaceMember, tool{
		name:          "list_intake_items",
		description:   "List intake items in the workspace triage queue.",
		requiredScope: "read:workspace",
		inputSchema: objectSchema(map[string]any{
			"status": stringSchema("Filter by triage status: pending, accepted, rejected, snoozed, duplicate. Optional.", Constraints{Pattern: `^(pending|accepted|rejected|snoozed|duplicate)$`}),
			"limit":  intSchema("Max results (1..200, default 50).", Constraints{Min: intPtr(1), Max: intPtr(200)}),
			"offset": intSchema("Skip N results.", Constraints{Min: intPtr(0)}),
		}, nil),
		run: runListIntakeItems,
	})
	h.register(auth.FloorWorkspaceMember, tool{
		name:          "triage_intake_item",
		description:   "Accept, reject, snooze, or mark as duplicate an intake item.",
		requiredScope: "write:workspace",
		inputSchema: objectSchema(map[string]any{
			"intakeItemId": stringSchema("Intake item public id (UUID v7).", Constraints{Pattern: publicIDPattern}),
			"status":       stringSchema("Triage decision: accepted, rejected, snoozed, or duplicate.", Constraints{Pattern: `^(accepted|rejected|snoozed|duplicate)$`}),
			"snoozeUntil":  intSchema("Unix seconds timestamp for snooze expiry (required when status is snoozed).", Constraints{Min: intPtr(1)}),
		}, []string{"intakeItemId", "status"}),
		run: runTriageIntakeItem,
	})
	h.register(auth.FloorProjectEditor, tool{
		name:          "convert_intake_to_task",
		description:   "Convert an intake item into a task in a specified project.",
		requiredScope: "write:workspace",
		inputSchema: objectSchema(map[string]any{
			"intakeItemId": stringSchema("Intake item public id (UUID v7).", Constraints{Pattern: publicIDPattern}),
			"projectId":    stringSchema("Project public id (UUID v7) to create the task in.", Constraints{Pattern: publicIDPattern}),
		}, []string{"intakeItemId", "projectId"}),
		run: runConvertIntakeToTask,
	})
	h.register(auth.FloorWorkspaceMember, tool{
		name:          "list_description_versions",
		description:   "List description version history for a task (newest first, without body).",
		requiredScope: "read:workspace",
		inputSchema: objectSchema(map[string]any{
			"taskId": stringSchema("Task public id (UUID v7).", Constraints{Pattern: publicIDPattern}),
		}, []string{"taskId"}),
		run: runListDescriptionVersions,
	})
	h.register(auth.FloorProjectEditor, tool{
		name:          "restore_description_version",
		description:   "Restore a previous description version, updating the task description and creating a new version snapshot.",
		requiredScope: "write:workspace",
		inputSchema: objectSchema(map[string]any{
			"taskId":    stringSchema("Task public id (UUID v7).", Constraints{Pattern: publicIDPattern}),
			"versionId": stringSchema("Description version public id (UUID v7) to restore.", Constraints{Pattern: publicIDPattern}),
		}, []string{"taskId", "versionId"}),
		run: runRestoreDescriptionVersion,
	})

	// Import job management.
	h.register(auth.FloorWorkspaceMember, tool{
		name:          "list_import_jobs",
		description:   "List import jobs for the workspace.",
		requiredScope: "read:workspace",
		inputSchema: objectSchema(map[string]any{
			"status": stringSchema("Filter by status: pending, running, completed, failed, cancelled. Optional.", Constraints{Pattern: `^(pending|running|completed|failed|cancelled)$`}),
			"limit":  intSchema("Max results (1..200, default 50).", Constraints{Min: intPtr(1), Max: intPtr(200)}),
			"offset": intSchema("Skip N results.", Constraints{Min: intPtr(0)}),
		}, nil),
		run: runListImportJobs,
	})
	h.register(auth.FloorProjectEditor, tool{
		name:          "create_import_job",
		description:   "Create a new import job.",
		requiredScope: "write:workspace",
		inputSchema: objectSchema(map[string]any{
			"source":    stringSchema("Import source: github, jira, linear, or csv.", Constraints{Pattern: `^(github|jira|linear|csv)$`}),
			"projectId": stringSchema("Project public id (UUID v7) to import into. Optional.", Constraints{Pattern: publicIDPattern}),
			// The configuration is stored as JSON and read back by the
			// importer, so the bound is on the document one call may carry.
			"configJson": stringSchema("JSON string with source-specific configuration. Optional.", Constraints{MaxLength: intPtr(20000)}),
		}, []string{"source"}),
		run: runCreateImportJob,
	})
}

// ----------------------------------------------------------------------------
// JSON Schema helpers (minimal hand-rolled)
// ----------------------------------------------------------------------------

// Constraints carries optional JSONSchema validation hints for the
// minimal hand-rolled string/int helpers below. Zero-valued fields are
// omitted from the emitted schema map.
type Constraints struct {
	Min       *int
	Max       *int
	Pattern   string
	MinLength *int
	MaxLength *int
	// Enum is the closed set of permitted string values. Prefer it over
	// Pattern for a real enumeration: the client shows the caller what
	// the choices are instead of a regular expression, and a rejection
	// names them. Build it from the generated column constants so the
	// advertised set cannot fall behind the column.
	Enum []string
	// MinItems and MaxItems bound how many elements an array argument
	// carries. A length is not decoration on an array the way it is on
	// a string: the tool does work per element inside the one call —
	// a row inserted under a lock, an id fetched and pasted into an LLM
	// prompt — so an unbounded array lets the caller choose how much
	// work, and how long a lock is held, on the server's behalf.
	MinItems *int
	MaxItems *int
}

func intPtr(v int) *int { return &v }

// The calendar_events enum columns, spelled from the generated
// constants rather than by hand. An agent that guessed a value used to
// get an opaque execution failure from the driver's rejected INSERT;
// now the value is refused against the same set the column accepts, and
// the client can see the choices before it sends anything.
var (
	calendarEventKinds = []string{
		string(calendar.CalendarEventsKindEvent),
		string(calendar.CalendarEventsKindBlock),
		string(calendar.CalendarEventsKindFree),
		string(calendar.CalendarEventsKindMilestone),
	}
	calendarEventShowAs = []string{
		string(calendar.CalendarEventsShowAsBusy),
		string(calendar.CalendarEventsShowAsFree),
		string(calendar.CalendarEventsShowAsTentative),
		string(calendar.CalendarEventsShowAsOof),
	}
	calendarEventFlexibility = []string{
		string(calendar.CalendarEventsFlexibilityFixed),
		string(calendar.CalendarEventsFlexibilityNegotiable),
		string(calendar.CalendarEventsFlexibilityConditional),
	}
	calendarEventVisibility = []string{
		string(calendar.CalendarEventsVisibilityDefault),
		string(calendar.CalendarEventsVisibilityPublic),
		string(calendar.CalendarEventsVisibilityPrivate),
		string(calendar.CalendarEventsVisibilityConfidential),
	}
)

// publicIDPattern matches a public id as the API actually deals in one:
// the canonical hyphenated UUID that types.PublicID.String() emits, or
// the unhyphenated 32-hex form that types.Parse also accepts.
//
// It used to read `^[0-9a-f]{32}$` and nothing else — a form the server
// never emits. The mistake survived because the pattern was
// advertisement only: every MCP client was handed a rule that would
// have rejected every id the API had just given it, and no server-side
// check ever disagreed with the tools it was guarding. Enforcing the
// schema is what surfaced it.
//
// The alternation is deliberate rather than tidy. Narrowing to the
// canonical form alone would be a rule the resolvers do not apply —
// types.Parse takes both — and tightening the contract is not this
// change's job. Written with explicit case ranges instead of an inline
// (?i) flag because JSON Schema patterns are ECMA-262, which has no
// inline flags: a client validating locally must read the same rule the
// server does.
//
// [TestPublicIDPatternMatchesEmittedIDs] ties it to a real generated id
// so the next edit cannot drift from the type again.
const publicIDPattern = `^([0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}|[0-9a-fA-F]{32})$`

// errTransitionRejected rolls a transition transaction back when the
// state machine refuses the move. The caller answers from the returned
// spec instead; the sentinel never reaches the client and is not a
// transient error, so the retry loop leaves it alone.
var errTransitionRejected = stderrors.New("mcp: transition rejected")

func objectSchema(props map[string]any, required []string) map[string]any {
	if props == nil {
		props = map[string]any{}
	}
	out := map[string]any{
		"type":       "object",
		"properties": props,
	}
	if len(required) > 0 {
		out["required"] = required
	}
	return out
}

func stringSchema(desc string, c ...Constraints) map[string]any {
	out := map[string]any{"type": "string", "description": desc}
	if len(c) > 0 {
		applyStringConstraints(out, c[0])
	}
	return out
}

func intSchema(desc string, c ...Constraints) map[string]any {
	out := map[string]any{"type": "integer", "description": desc}
	if len(c) > 0 {
		applyIntConstraints(out, c[0])
	}
	return out
}

func boolSchema(desc string) map[string]any {
	return map[string]any{"type": "boolean", "description": desc}
}

// arraySchema builds an array property from the schema its elements take.
//
// It exists so an array is bounded the same way a string is. Written as a
// bare map literal an array has no constraint surface at all, and every
// one of them was therefore unbounded by construction rather than by
// choice — while the string beside it went through Constraints and got a
// length. Going through a helper is what makes "how many elements" a
// question the next array has to answer.
func arraySchema(desc string, items map[string]any, c ...Constraints) map[string]any {
	out := map[string]any{"type": "array", "description": desc, "items": items}
	if len(c) > 0 {
		applyArrayConstraints(out, c[0])
	}
	return out
}

func applyStringConstraints(out map[string]any, c Constraints) {
	if len(c.Enum) > 0 {
		out["enum"] = c.Enum
	}
	if c.Pattern != "" {
		out["pattern"] = c.Pattern
	}
	if c.MinLength != nil {
		out["minLength"] = *c.MinLength
	}
	if c.MaxLength != nil {
		out["maxLength"] = *c.MaxLength
	}
}

func applyIntConstraints(out map[string]any, c Constraints) {
	if c.Min != nil {
		out["minimum"] = *c.Min
	}
	if c.Max != nil {
		out["maximum"] = *c.Max
	}
}

func applyArrayConstraints(out map[string]any, c Constraints) {
	if c.MinItems != nil {
		out["minItems"] = *c.MinItems
	}
	if c.MaxItems != nil {
		out["maxItems"] = *c.MaxItems
	}
}

// ----------------------------------------------------------------------------
// Tool implementations
// ----------------------------------------------------------------------------

func parseArgs(raw json.RawMessage, out any) error {
	if len(raw) == 0 {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return apierrors.Wrap(apierrors.McpToolArgumentsInvalid, err)
	}
	return nil
}

func parseDateOrNull(s string) (sql.NullTime, error) {
	if s == "" {
		return sql.NullTime{}, nil
	}
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		return sql.NullTime{}, err
	}
	return sql.NullTime{Time: t, Valid: true}, nil
}

// Widths of tasks.title, pages.title and calendar_events.title in
// characters. They are the maxLength the tool schemas declare and the cut
// applied to model-produced titles, so the two cannot disagree.
const (
	taskTitleLimit  = 500
	pageTitleLimit  = 500
	eventTitleLimit = 500
)
