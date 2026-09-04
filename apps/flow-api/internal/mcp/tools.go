// Package mcp tool registry and tool implementations.
package mcp

import (
	"context"
	"database/sql"
	"encoding/json"
	stderrors "errors"
	"fmt"
	"time"

	"sort"
	"strconv"
	"strings"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/acl"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/ai"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/ai/embed"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/ai/nlquery"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/auth"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/calendarrules"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/dbretry"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated/calendar"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/eventbus"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/itemkit"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/taskcreate"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/taskstate"
	"github.com/libraz/nodate-flow/packages/go-shared/recurrence"
	"github.com/libraz/nodate-flow/packages/go-shared/region"
	"github.com/libraz/nodate-flow/packages/go-shared/stringutil"
)

// countLinkedEvents returns how many enabled calendar_events are
// attached to a task. Used to decide when an MCP mutation must route
// through itemkit to keep the task / event pair consistent.
//
// The same question decides the same thing on the REST task read path,
// so both ask it with the same generated query.
func countLinkedEvents(ctx context.Context, deps Deps, taskID uint32) (int, error) {
	n, err := deps.Queries.CountActiveCalendarEventsByTaskId(ctx, sql.NullInt32{
		Int32: int32(taskID), //#nosec G115 -- internal row id, bounded by realistic deployments
		Valid: true,
	})
	if err != nil {
		return 0, err
	}
	return int(n), nil
}

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
		description:   "List projects in the caller's workspace.",
		requiredScope: "read:workspace",
		inputSchema:   objectSchema(nil, nil),
		run:           runListProjects,
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
		description:   "Create a new task in a project.",
		requiredScope: "write:workspace",
		inputSchema: objectSchema(map[string]any{
			"projectId":   stringSchema("Project public id (UUID v7).", Constraints{Pattern: publicIDPattern}),
			"title":       stringSchema("Task title.", Constraints{MinLength: intPtr(1), MaxLength: intPtr(500)}),
			"description": stringSchema("Optional description."),
			"priority":    intSchema("Priority 0..4.", Constraints{Min: intPtr(0), Max: intPtr(4)}),
			"dueOn":       stringSchema("YYYY-MM-DD.", Constraints{Pattern: `^\d{4}-\d{2}-\d{2}$`}),
			"startOn":     stringSchema("YYYY-MM-DD.", Constraints{Pattern: `^\d{4}-\d{2}-\d{2}$`}),
		}, []string{"projectId", "title"}),
		run: runCreateTask,
	})
	h.register(auth.FloorProjectEditor, tool{
		name:          "update_task",
		description:   "Update mutable fields of a task.",
		requiredScope: "write:workspace",
		inputSchema: objectSchema(map[string]any{
			"taskId":      stringSchema("Task public id (UUID v7).", Constraints{Pattern: publicIDPattern}),
			"title":       stringSchema("New title.", Constraints{MinLength: intPtr(1), MaxLength: intPtr(500)}),
			"description": stringSchema("New description."),
			"priority":    intSchema("New priority 0..4.", Constraints{Min: intPtr(0), Max: intPtr(4)}),
			"dueOn":       stringSchema("YYYY-MM-DD.", Constraints{Pattern: `^\d{4}-\d{2}-\d{2}$`}),
			"startOn":     stringSchema("YYYY-MM-DD.", Constraints{Pattern: `^\d{4}-\d{2}-\d{2}$`}),
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
			"reason":     stringSchema("Optional reason for the transition."),
		}, []string{"taskId", "transition"}),
		run: runTransitionTask,
	})
	h.register(auth.FloorProjectCommenter, tool{
		name:          "add_comment",
		description:   "Append a comment to a task.",
		requiredScope: "write:workspace",
		inputSchema: objectSchema(map[string]any{
			"taskId": stringSchema("Task public id (UUID v7).", Constraints{Pattern: publicIDPattern}),
			"body":   stringSchema("Comment body.", Constraints{MinLength: intPtr(1)}),
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
			"source": stringSchema("Input text to propose tasks from.", Constraints{MinLength: intPtr(1)}),
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
			"steps": map[string]any{
				"type":        "array",
				"description": "Step definitions to create as child tasks.",
				"items": objectSchema(map[string]any{
					"title":       stringSchema("Step title.", Constraints{MinLength: intPtr(1), MaxLength: intPtr(500)}),
					"description": stringSchema("Step description (optional)."),
					"priority":    intSchema("Step priority 0..4 (optional).", Constraints{Min: intPtr(0), Max: intPtr(4)}),
				}, []string{"title"}),
			},
		}, []string{"parentTaskId", "steps"}),
		run: runApplySteps,
	})
	h.register(auth.FloorWorkspaceMember, tool{
		name:          "propose_duplicates",
		description:   "Return likely-duplicate tasks for a given task by embedding similarity (ADR 0003).",
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
			"prompt": stringSchema("Natural-language description of the desired view.", Constraints{MinLength: intPtr(1)}),
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
	h.register(auth.FloorWorkspaceMember, tool{
		name:          "create_timebox",
		description:   "Create a new timebox (sprint / iteration) in the workspace.",
		requiredScope: "write:workspace",
		inputSchema: objectSchema(map[string]any{
			"name":        stringSchema("Timebox name.", Constraints{MinLength: intPtr(1), MaxLength: intPtr(200)}),
			"startsOn":    stringSchema("Start date YYYY-MM-DD.", Constraints{Pattern: `^\d{4}-\d{2}-\d{2}$`}),
			"endsOn":      stringSchema("End date YYYY-MM-DD.", Constraints{Pattern: `^\d{4}-\d{2}-\d{2}$`}),
			"description": stringSchema("Optional description."),
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
		description:   "List wiki pages for the current workspace. Returns root-level pages by default.",
		requiredScope: "read:workspace",
		inputSchema: objectSchema(map[string]any{
			"projectId":    stringSchema("Optional project public id (UUID v7) to scope pages.", Constraints{Pattern: publicIDPattern}),
			"parentPageId": stringSchema("Optional parent page public id (UUID v7) to list child pages.", Constraints{Pattern: publicIDPattern}),
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
	h.register(auth.FloorWorkspaceMember, tool{
		name:          "create_page",
		description:   "Create a new wiki page.",
		requiredScope: "write:workspace",
		inputSchema: objectSchema(map[string]any{
			"title":        stringSchema("Page title.", Constraints{MinLength: intPtr(1), MaxLength: intPtr(200)}),
			"body":         stringSchema("Optional page body content."),
			"parentPageId": stringSchema("Optional parent page public id (UUID v7).", Constraints{Pattern: publicIDPattern}),
			"projectId":    stringSchema("Optional project public id (UUID v7).", Constraints{Pattern: publicIDPattern}),
		}, []string{"title"}),
		run: runCreatePage,
	})
	h.register(auth.FloorWorkspaceMember, tool{
		name:          "update_page",
		description:   "Update a wiki page's title or content.",
		requiredScope: "write:workspace",
		inputSchema: objectSchema(map[string]any{
			"pageId": stringSchema("Page public id (UUID v7).", Constraints{Pattern: publicIDPattern}),
			"title":  stringSchema("New title (optional).", Constraints{MaxLength: intPtr(200)}),
			"body":   stringSchema("New body content (optional)."),
		}, []string{"pageId"}),
		run: runUpdatePage,
	})
	h.register(auth.FloorWorkspaceMember, tool{
		name:          "generate_page",
		description:   "Generate a wiki page using AI based on project or task context.",
		requiredScope: "write:workspace",
		inputSchema: objectSchema(map[string]any{
			"contextDescription": stringSchema("What the page should be about.", Constraints{MinLength: intPtr(1)}),
			"projectId":          stringSchema("Optional project public id (UUID v7).", Constraints{Pattern: publicIDPattern}),
			"taskIds": map[string]any{
				"type":        "array",
				"description": "Optional task public ids (UUID v7) to include as context.",
				"items":       stringSchema("Task public id (UUID v7).", Constraints{Pattern: publicIDPattern}),
			},
		}, []string{"contextDescription"}),
		run: runGeneratePage,
	})
	h.register(auth.FloorProjectEditor, tool{
		name:          "smart_create_task",
		description:   "Create a task with AI-suggested subtask breakdown and assignees based on past ticket patterns.",
		requiredScope: "write:workspace",
		inputSchema: objectSchema(map[string]any{
			"projectId":   stringSchema("Project public id (UUID v7).", Constraints{Pattern: publicIDPattern}),
			"title":       stringSchema("Task title.", Constraints{MinLength: intPtr(1), MaxLength: intPtr(500)}),
			"description": stringSchema("Optional task description for better AI analysis."),
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
			"title":       stringSchema("Event title.", Constraints{MinLength: intPtr(1), MaxLength: intPtr(500)}),
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
			"location":    stringSchema("Event location."),
			"memo":        stringSchema("Event memo/notes."),
			"blockLabel":  stringSchema("Label for block-type events."),
		}, []string{"calendarId", "title"}),
		run: runCreateCalendarEvent,
	})
	h.register(auth.FloorWorkspaceMember, tool{
		name:          "update_calendar_event",
		description:   "Update mutable fields of a calendar event. Times are unix seconds since epoch (startAt/endAt); use startDate/endDate (YYYY-MM-DD) for all-day events.",
		requiredScope: "write:workspace",
		inputSchema: objectSchema(map[string]any{
			"eventId":     stringSchema("Event public id (UUID v7).", Constraints{Pattern: publicIDPattern}),
			"title":       stringSchema("New title.", Constraints{MinLength: intPtr(1), MaxLength: intPtr(500)}),
			"startAt":     intSchema("New start time, unix seconds since epoch.", Constraints{Min: intPtr(0)}),
			"endAt":       intSchema("New end time, unix seconds since epoch.", Constraints{Min: intPtr(0)}),
			"startDate":   stringSchema("New start date YYYY-MM-DD (all-day events).", Constraints{Pattern: `^\d{4}-\d{2}-\d{2}$`}),
			"endDate":     stringSchema("New end date YYYY-MM-DD (all-day events).", Constraints{Pattern: `^\d{4}-\d{2}-\d{2}$`}),
			"kind":        stringSchema("New event kind.", Constraints{Enum: calendarEventKinds}),
			"showAs":      stringSchema("New reading of the time to others.", Constraints{Enum: calendarEventShowAs}),
			"flexibility": stringSchema("New answer to whether the commitment can be moved. Independent of showAs, which only says whether the time reads as taken.", Constraints{Enum: calendarEventFlexibility}),
			"visibility":  stringSchema("New answer to who may see the event's details.", Constraints{Enum: calendarEventVisibility}),
			"location":    stringSchema("New location."),
			"memo":        stringSchema("New memo/notes."),
			"blockLabel":  stringSchema("New block label."),
		}, []string{"eventId"}),
		run: runUpdateCalendarEvent,
	})
	h.register(auth.FloorWorkspaceMember, tool{
		name:          "delete_calendar_event",
		description:   "Delete a calendar event (soft-delete).",
		requiredScope: "write:workspace",
		inputSchema: objectSchema(map[string]any{
			"eventId": stringSchema("Event public id (UUID v7).", Constraints{Pattern: publicIDPattern}),
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
			"name":        stringSchema("Label name.", Constraints{MinLength: intPtr(1), MaxLength: intPtr(100)}),
			"color":       stringSchema("Hex color (e.g. #ef4444). Optional.", Constraints{Pattern: `^#[0-9a-fA-F]{6}$`}),
			"description": stringSchema("Optional description."),
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

	// Favorites, reactions, and recent visits.
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
	h.register(auth.FloorWorkspaceMember, tool{
		name:          "list_recent",
		description:   "List the user's recently visited entities in the workspace.",
		requiredScope: "read:workspace",
		inputSchema: objectSchema(map[string]any{
			"limit": intSchema("Max results (1..20, default 20).", Constraints{Min: intPtr(1), Max: intPtr(20)}),
		}, nil),
		run: runListRecent,
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
	h.register(auth.FloorWorkspaceMember, tool{
		name:          "create_import_job",
		description:   "Create a new import job.",
		requiredScope: "write:workspace",
		inputSchema: objectSchema(map[string]any{
			"source":     stringSchema("Import source: github, jira, linear, or csv.", Constraints{Pattern: `^(github|jira|linear|csv)$`}),
			"projectId":  stringSchema("Project public id (UUID v7) to import into. Optional.", Constraints{Pattern: publicIDPattern}),
			"configJson": stringSchema("JSON string with source-specific configuration. Optional."),
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

func runListProjects(ctx context.Context, deps Deps, s *session, _ json.RawMessage) (any, error) {
	if _, err := requireWorkspaceMember(ctx, deps, s); err != nil {
		return nil, err
	}
	rows, err := deps.Queries.ListProjectsForWorkspace(ctx, generated.ListProjectsForWorkspaceParams{
		WorkspaceID: s.workspaceID,
		Limit:       200,
		Offset:      0,
	})
	if err != nil {
		return nil, apierrors.Wrap(apierrors.McpToolExecutionFailed, err)
	}
	items := make([]map[string]any, 0, len(rows))
	for _, r := range rows {
		items = append(items, map[string]any{
			"id":   r.PublicID.String(),
			"slug": r.Slug,
			"name": r.Name,
		})
	}
	return map[string]any{"projects": items}, nil
}

func runListTasks(ctx context.Context, deps Deps, s *session, raw json.RawMessage) (any, error) {
	var in struct {
		ProjectID string `json:"projectId"`
		Limit     int32  `json:"limit"`
		Offset    int32  `json:"offset"`
	}
	if err := parseArgs(raw, &in); err != nil {
		return nil, err
	}
	if in.Limit <= 0 || in.Limit > 200 {
		in.Limit = 50
	}
	wsRole, err := requireWorkspaceMember(ctx, deps, s)
	if err != nil {
		return nil, err
	}
	var projectPublicID []byte
	if in.ProjectID != "" {
		prjPub, err := types.Parse(in.ProjectID)
		if err != nil {
			return nil, apierrors.New(apierrors.WsProjectNotFound)
		}
		if _, err := resolveProject(ctx, deps, s, in.ProjectID); err != nil {
			return nil, err
		}
		pb := prjPub.UUID()
		projectPublicID = pb[:]
	}
	rows, err := listMCPTasks(ctx, deps.DB, s.workspaceID, projectPublicID, s.userID, wsRole, in.Limit, in.Offset)
	if err != nil {
		return nil, apierrors.Wrap(apierrors.McpToolExecutionFailed, err)
	}
	items := make([]map[string]any, 0, len(rows))
	for _, r := range rows {
		items = append(items, taskListRowToMap(r.publicID, r.title, r.derivedState, r.priority, r.dueOn))
	}
	return map[string]any{"tasks": items}, nil
}

type mcpTaskListRow struct {
	publicID     types.PublicID
	title        string
	derivedState string
	priority     int32
	dueOn        sql.NullTime
}

func listMCPTasks(
	ctx context.Context,
	db *sql.DB,
	workspaceID uint32,
	projectPublicID []byte,
	actorID uint32,
	wsRole acl.WorkspaceRole,
	limit int32,
	offset int32,
) ([]mcpTaskListRow, error) {
	where := []string{"v.workspace_id = ?"}
	args := []any{workspaceID}
	if len(projectPublicID) > 0 {
		where = append(where, "v.project_public_id = ?")
		args = append(args, projectPublicID)
	}
	if visFrag, visArgs := acl.TaskVisibilityFilter(actorID, wsRole); visFrag != "" {
		where = append(where, visFrag)
		args = append(args, visArgs...)
	}

	//#nosec G201 -- WHERE fragments are static literals; user values are bound.
	query := fmt.Sprintf(`SELECT
  v.public_id,
  v.title,
  v.derived_state,
  v.priority,
  v.due_on
FROM v_task_list v
WHERE %s
ORDER BY v.sort_weight ASC, v.priority DESC, v.due_on ASC, v.created_at DESC, v.public_id DESC
LIMIT ? OFFSET ?`, strings.Join(where, " AND "))
	args = append(args, limit, offset)

	// no-generated-query: the WHERE clause is not fixed. acl.TaskVisibilityFilter
	// returns a different fragment, with a different number of binds, depending on
	// the caller's workspace role, and sqlc compiles one statement per query. A
	// static version would have to spell the visibility predicate out a second
	// time, which is how a listing ends up projecting rows the caller may not see.
	// Replacing this needs a way to compose the filter into a generated statement,
	// not another copy of the predicate.
	rows, err := db.QueryContext(ctx, query, args...) //#nosec G701 -- query is assembled from static WHERE fragments; all user values are bound args.
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []mcpTaskListRow{}
	for rows.Next() {
		var r mcpTaskListRow
		if err := rows.Scan(&r.publicID, &r.title, &r.derivedState, &r.priority, &r.dueOn); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func taskListRowToMap(pub types.PublicID, title, state string, priority int32, due sql.NullTime) map[string]any {
	m := map[string]any{
		"id":           pub.String(),
		"title":        title,
		"derivedState": state,
		"priority":     priority,
	}
	if due.Valid {
		m["dueOn"] = due.Time.UTC().Format("2006-01-02")
	}
	return m
}

func runGetTask(ctx context.Context, deps Deps, s *session, raw json.RawMessage) (any, error) {
	var in struct {
		TaskID string `json:"taskId"`
	}
	if err := parseArgs(raw, &in); err != nil {
		return nil, err
	}
	if _, err := requireWorkspaceMember(ctx, deps, s); err != nil {
		return nil, err
	}
	_, row, err := resolveTaskRow(ctx, deps, s, in.TaskID)
	if err != nil {
		return nil, err
	}
	out := map[string]any{
		"id":           row.PublicID.String(),
		"title":        row.Title,
		"derivedState": string(row.DerivedState),
		"priority":     row.Priority,
		"projectName":  row.ProjectName,
	}
	if row.Description.Valid {
		out["description"] = row.Description.String
	}
	if row.DueOn.Valid {
		out["dueOn"] = row.DueOn.Time.UTC().Format("2006-01-02")
	}
	return out, nil
}

func runCreateTask(ctx context.Context, deps Deps, s *session, raw json.RawMessage) (any, error) {
	var in struct {
		ProjectID   string `json:"projectId"`
		Title       string `json:"title"`
		Description string `json:"description"`
		Priority    int32  `json:"priority"`
		DueOn       string `json:"dueOn"`
		StartOn     string `json:"startOn"`
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
	prjID, err := resolveProjectForWrite(ctx, deps, s, in.ProjectID)
	if err != nil {
		return nil, err
	}
	due, err := parseDateOrNull(in.DueOn)
	if err != nil {
		return nil, apierrors.New(apierrors.McpToolArgumentsInvalid)
	}
	start, err := parseDateOrNull(in.StartOn)
	if err != nil {
		return nil, apierrors.New(apierrors.McpToolArgumentsInvalid)
	}
	var (
		pub    types.PublicID
		taskID int64
	)
	if txErr := dbretry.InTx(ctx, deps.DB, "mcp.create_task", nil, func(ctx context.Context, tx *dbretry.Tx) error {
		created, err := taskcreate.New(ctx, tx, taskcreate.Args{
			WorkspaceID: s.workspaceID,
			ProjectID:   prjID,
			ActorUserID: sql.NullInt32{Int32: int32(s.userID), Valid: true}, //#nosec G115 -- session user id is users.id (BIGINT UNSIGNED), fits int32 within realistic deployments
			Title:       in.Title,
			Description: sql.NullString{String: in.Description, Valid: in.Description != ""},
			Priority:    in.Priority,
			DueOn:       due,
			StartedOn:   start,
		})
		if err != nil {
			return err
		}
		pub = created.PublicID
		taskID = created.ID
		return nil
	}); txErr != nil {
		return nil, apierrors.Wrap(apierrors.McpToolExecutionFailed, txErr)
	}
	noteInvocationTask(ctx, uint32(taskID)) //#nosec G115 -- task id is tasks.id (BIGINT UNSIGNED), fits uint32 within realistic deployments
	// The task row is committed. Reporting a failure here would tell the
	// caller nothing was created and invite a duplicate on retry.
	recordMutation(ctx, deps, s, mutation{
		EventType:    eventbus.TaskCreated,
		AuditAction:  "task.create",
		ResourceType: "task",
		ResourceID:   pub.String(),
		TaskID:       &taskID,
		Payload: map[string]any{
			"taskId": pub.String(),
			"title":  in.Title,
			"via":    "mcp",
		},
		CallSite: "mcp.create_task",
	})
	return map[string]any{"id": pub.String()}, nil
}

func runUpdateTask(ctx context.Context, deps Deps, s *session, raw json.RawMessage) (any, error) {
	var in struct {
		TaskID      string  `json:"taskId"`
		Title       *string `json:"title"`
		Description *string `json:"description"`
		Priority    *int32  `json:"priority"`
		DueOn       *string `json:"dueOn"`
		StartOn     *string `json:"startOn"`
	}
	if err := parseArgs(raw, &in); err != nil {
		return nil, err
	}
	if _, err := requireWorkspaceMember(ctx, deps, s); err != nil {
		return nil, err
	}
	taskInternal, current, err := resolveTaskRowForWrite(ctx, deps, s, in.TaskID)
	if err != nil {
		return nil, err
	}
	title := current.Title
	if in.Title != nil && *in.Title != "" {
		title = *in.Title
	}
	desc := current.Description
	if in.Description != nil {
		desc = sql.NullString{String: *in.Description, Valid: *in.Description != ""}
	}
	prio := current.Priority
	if in.Priority != nil {
		prio = *in.Priority
	}
	due := current.DueOn
	if in.DueOn != nil {
		p, err := parseDateOrNull(*in.DueOn)
		if err != nil {
			return nil, apierrors.New(apierrors.McpToolArgumentsInvalid)
		}
		due = p
	}
	start := current.StartedOn
	if in.StartOn != nil {
		p, err := parseDateOrNull(*in.StartOn)
		if err != nil {
			return nil, apierrors.New(apierrors.McpToolArgumentsInvalid)
		}
		start = p
	}

	titleChanged := in.Title != nil && *in.Title != "" && title != current.Title
	dueOnChanged := in.DueOn != nil && due != current.DueOn

	needsItemkit := false
	if titleChanged || dueOnChanged {
		n, cerr := countLinkedEvents(ctx, deps, taskInternal)
		if cerr != nil {
			return nil, apierrors.Wrap(apierrors.McpToolExecutionFailed, cerr)
		}
		needsItemkit = n > 0
	}

	updateParams := generated.UpdateTaskParams{
		Title:           title,
		Description:     desc,
		Priority:        prio,
		DueOn:           due,
		StartedOn:       start,
		SortWeight:      current.SortWeight,
		Visibility:      current.Visibility,
		UpdatedByUserID: sql.NullInt32{Int32: int32(s.userID), Valid: true}, //#nosec G115 -- session user id is users.id (BIGINT UNSIGNED), fits int32 within realistic deployments
		WorkspaceID:     s.workspaceID,
		PublicID:        current.PublicID,
	}

	if !needsItemkit {
		// Not an existence check: MySQL counts changed rows, so an update
		// carrying the task's current values reports zero. The task is
		// resolved into `current` above.
		if _, err := deps.Queries.UpdateTask(ctx, updateParams); err != nil {
			return nil, apierrors.Wrap(apierrors.McpToolExecutionFailed, err)
		}
	} else {
		// itemkit writes an event row through eventlog inside this
		// transaction, and a deadlock there rolls the whole transaction
		// back — re-running one statement inside a dead transaction
		// would achieve nothing. dbretry.InTx retries the unit that
		// actually failed.
		var answered error
		txErr := dbretry.InTx(ctx, deps.DB, "mcp.update_task", nil, func(ctx context.Context, tx *dbretry.Tx) error {
			answered = nil
			qtx := deps.Queries.WithTx(tx.RawTx())
			if _, err := qtx.UpdateTask(ctx, updateParams); err != nil {
				return err
			}
			if titleChanged {
				if err := itemkit.RenameItem(ctx, tx, itemkit.RenameItemArgs{
					WorkspaceID: s.workspaceID,
					ActorUserID: s.userID,
					TaskID:      taskInternal,
					NewTitle:    title,
				}); err != nil {
					answered = translateItemkitMCPError(err)
					return err
				}
			}
			if dueOnChanged {
				var t time.Time
				if due.Valid {
					t = due.Time
				}
				if err := itemkit.RescheduleTask(ctx, tx, itemkit.RescheduleTaskArgs{
					WorkspaceID: s.workspaceID,
					TaskID:      taskInternal,
					ActorUserID: s.userID,
					SetDueOn:    true,
					DueOn:       t,
				}); err != nil {
					answered = translateItemkitMCPError(err)
					return err
				}
			}
			return nil
		})
		if answered != nil {
			return nil, answered
		}
		if txErr != nil {
			return nil, apierrors.Wrap(apierrors.McpToolExecutionFailed, txErr)
		}
	}

	taskID64 := int64(taskInternal)
	// Propagated rather than absorbed: re-running this tool with the same
	// arguments re-applies the same field values and re-appends the
	// event, so the caller's retry is what repairs the log.
	if err := recordMutationStrict(ctx, deps, s, mutation{
		EventType:    eventbus.TaskUpdated,
		AuditAction:  "task.update",
		ResourceType: "task",
		ResourceID:   current.PublicID.String(),
		TaskID:       &taskID64,
		Payload:      map[string]any{"taskId": current.PublicID.String(), "via": "mcp"},
		CallSite:     "mcp.update_task",
	}); err != nil {
		return nil, apierrors.Wrap(apierrors.McpToolExecutionFailed, err)
	}
	return map[string]any{"id": current.PublicID.String()}, nil
}

// translateItemkitMCPError maps an itemkit invariant into a stable MCP
// error code so the tool response is 422-style for recoverable cases
// and generic for the rest.
func translateItemkitMCPError(err error) error {
	if err == nil {
		return nil
	}
	if strings.Contains(err.Error(), "itemkit invariant") {
		return apierrors.New(apierrors.ItemItemkitInvariantViolation)
	}
	return apierrors.Wrap(apierrors.McpToolExecutionFailed, err)
}

func runTransitionTask(ctx context.Context, deps Deps, s *session, raw json.RawMessage) (any, error) {
	var in struct {
		TaskID     string  `json:"taskId"`
		Transition string  `json:"transition"`
		Reason     *string `json:"reason"`
	}
	if err := parseArgs(raw, &in); err != nil {
		return nil, err
	}
	if !taskstate.IsKnownTransition(in.Transition) {
		return nil, apierrors.New(apierrors.McpToolArgumentsInvalid)
	}
	if _, err := requireWorkspaceMember(ctx, deps, s); err != nil {
		return nil, err
	}
	taskInternal, pub, err := resolveTaskForWrite(ctx, deps, s, in.TaskID)
	if err != nil {
		return nil, err
	}

	actor := int64(s.userID)
	reason := ""
	if in.Reason != nil {
		reason = *in.Reason
	}
	// dbretry.InTx, not a hand-rolled transaction: the transition event
	// is appended inside it, and only InTx gives the eventbus a commit
	// boundary to hang the fan-out on. Opening the tx directly here is
	// what stopped webhook deliveries and notifications from being
	// created for agent-driven transitions while the same transition
	// over REST delivered normally.
	var (
		result taskstate.ApplyResult
		spec   *apierrors.Spec
	)
	txErr := dbretry.InTx(ctx, deps.DB, "mcp.transition_task", nil, func(ctx context.Context, tx *dbretry.Tx) error {
		var applyErr error
		result, spec, applyErr = taskstate.ApplyTransitionTx(ctx, tx, taskstate.ApplyParams{
			WorkspaceID:  s.workspaceID,
			TaskID:       taskInternal,
			PublicID:     pub,
			Transition:   in.Transition,
			ActorUserID:  &actor,
			Reason:       reason,
			Via:          "mcp",
			ExtraPayload: nil,
		})
		if applyErr != nil {
			return applyErr
		}
		if spec != nil {
			// A rejected transition is a decision, not a failure: roll
			// the attempt back and let the caller answer with the spec.
			return errTransitionRejected
		}
		return nil
	})
	if spec != nil {
		return nil, apierrors.New(spec)
	}
	if txErr != nil {
		return nil, apierrors.Wrap(apierrors.McpToolExecutionFailed, txErr)
	}
	// taskstate appended the transition event inside the transaction so
	// the event and the state change commit together; only the audit row
	// belongs to the transport.
	recordTxMutationAudit(ctx, deps, s, mutation{
		AuditAction:  "task.transition",
		ResourceType: "task",
		ResourceID:   pub.String(),
		Payload: map[string]any{
			"taskId":     pub.String(),
			"transition": in.Transition,
			"fromState":  string(result.FromState),
			"toState":    string(result.ToState),
			"via":        "mcp",
		},
		CallSite: "mcp.transition_task",
	})
	return map[string]any{
		"id":         pub.String(),
		"fromState":  string(result.FromState),
		"toState":    string(result.ToState),
		"transition": in.Transition,
	}, nil
}

func runAddComment(ctx context.Context, deps Deps, s *session, raw json.RawMessage) (any, error) {
	var in struct {
		TaskID string `json:"taskId"`
		Body   string `json:"body"`
	}
	if err := parseArgs(raw, &in); err != nil {
		return nil, err
	}
	if in.Body == "" {
		return nil, apierrors.New(apierrors.McpToolArgumentsInvalid)
	}
	if _, err := requireWorkspaceMember(ctx, deps, s); err != nil {
		return nil, err
	}
	taskInternal, pub, err := resolveTaskForWrite(ctx, deps, s, in.TaskID)
	if err != nil {
		return nil, err
	}
	cpub := newPublicID()
	if _, err := deps.Queries.AddComment(ctx, generated.AddCommentParams{
		PublicID:    cpub,
		WorkspaceID: s.workspaceID,
		TaskID:      sql.NullInt32{Int32: int32(taskInternal), Valid: true}, //#nosec G115 -- internal row id, bounded by realistic deployments
		AuthorID:    s.userID,
		Body:        in.Body,
	}); err != nil {
		return nil, apierrors.Wrap(apierrors.McpToolExecutionFailed, err)
	}
	taskID64 := int64(taskInternal)
	// The comment row is committed; a retry would post it twice.
	recordMutation(ctx, deps, s, mutation{
		EventType:    eventbus.CommentAddedLegacy,
		AuditAction:  "comment.create",
		ResourceType: "comment",
		ResourceID:   cpub.String(),
		TaskID:       &taskID64,
		Payload: map[string]any{
			"taskId":    pub.String(),
			"commentId": cpub.String(),
			"via":       "mcp",
		},
		CallSite: "mcp.add_comment",
	})
	return map[string]any{"id": cpub.String()}, nil
}

// runSearchTasks searches tasks by title or description using LIKE.
func runSearchTasks(ctx context.Context, deps Deps, s *session, raw json.RawMessage) (any, error) {
	var in struct {
		Query  string `json:"query"`
		Limit  int32  `json:"limit"`
		Offset int32  `json:"offset"`
	}
	if err := parseArgs(raw, &in); err != nil {
		return nil, err
	}
	wsRole, err := requireWorkspaceMember(ctx, deps, s)
	if err != nil {
		return nil, err
	}
	if in.Query == "" {
		return nil, apierrors.New(apierrors.McpToolArgumentsInvalid)
	}
	if in.Limit <= 0 || in.Limit > 200 {
		in.Limit = 50
	}

	pattern := "%" + in.Query + "%"
	rows, err := searchMCPTasks(ctx, deps.DB, s.workspaceID, s.userID, wsRole, pattern, in.Limit, in.Offset)
	if err != nil {
		return nil, apierrors.Wrap(apierrors.McpToolExecutionFailed, err)
	}

	items := make([]map[string]any, 0, len(rows))
	for _, r := range rows {
		items = append(items, taskListRowToMap(r.publicID, r.title, r.derivedState, r.priority, r.dueOn))
	}
	return map[string]any{"tasks": items}, nil
}

func searchMCPTasks(
	ctx context.Context,
	db *sql.DB,
	workspaceID uint32,
	actorID uint32,
	wsRole acl.WorkspaceRole,
	pattern string,
	limit int32,
	offset int32,
) ([]mcpTaskListRow, error) {
	where := []string{"v.workspace_id = ?", "(v.title LIKE ? OR t.description LIKE ?)"}
	args := []any{workspaceID, pattern, pattern}
	if visFrag, visArgs := acl.TaskVisibilityFilter(actorID, wsRole); visFrag != "" {
		where = append(where, visFrag)
		args = append(args, visArgs...)
	}

	//#nosec G201 -- WHERE fragments are static literals; user values are bound.
	query := fmt.Sprintf(`SELECT
  v.public_id,
  v.title,
  v.derived_state,
  v.priority,
  v.due_on
FROM v_task_list v
INNER JOIN tasks t
  ON t.public_id = v.public_id AND t.workspace_id = v.workspace_id
WHERE %s
ORDER BY v.priority DESC, v.due_on ASC, v.created_at DESC, v.public_id DESC
LIMIT ? OFFSET ?`, strings.Join(where, " AND "))
	args = append(args, limit, offset)

	// no-generated-query: same reason as listMCPTasks. The search predicate is
	// fixed, but acl.TaskVisibilityFilter AND-ed into it is not: its fragment and
	// its bind count follow the caller's workspace role. Search is the path where
	// omitting it matters most, since a LIKE over titles and descriptions is
	// exactly how a task nobody may see would surface.
	rows, err := db.QueryContext(ctx, query, args...) //#nosec G701 -- query is assembled from static WHERE fragments; all user values are bound args.
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []mcpTaskListRow{}
	for rows.Next() {
		var r mcpTaskListRow
		if err := rows.Scan(&r.publicID, &r.title, &r.derivedState, &r.priority, &r.dueOn); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// runProposeTasksFrom asks the workspace's LLM provider to turn a free-text
// source (meeting notes, email, etc.) into a list of candidate tasks. The
// orchestrator handles cost guard, provider resolution, redaction, and
// invocation logging.
func runProposeTasksFrom(ctx context.Context, deps Deps, s *session, raw json.RawMessage) (any, error) {
	var in struct {
		Source string `json:"source"`
	}
	if err := parseArgs(raw, &in); err != nil {
		return nil, err
	}
	if in.Source == "" {
		return nil, apierrors.New(apierrors.McpToolArgumentsInvalid)
	}
	if _, err := requireWorkspaceMember(ctx, deps, s); err != nil {
		return nil, err
	}
	if deps.AI == nil {
		return nil, apierrors.New(apierrors.AiProviderNotConfigured)
	}
	tasks, err := deps.AI.ProposeTasksFrom(ctx, s.workspaceID, in.Source)
	if err != nil {
		return nil, mapAiError(err)
	}
	out := make([]map[string]any, 0, len(tasks))
	for _, t := range tasks {
		out = append(out, map[string]any{
			"title":       t.Title,
			"description": t.Description,
			"priority":    t.Priority,
		})
	}
	return map[string]any{"tasks": out}, nil
}

// runProposePriority asks the workspace LLM to suggest a priority for an
// existing task identified by public id.
func runProposePriority(ctx context.Context, deps Deps, s *session, raw json.RawMessage) (any, error) {
	var in struct {
		TaskID string `json:"taskId"`
	}
	if err := parseArgs(raw, &in); err != nil {
		return nil, err
	}
	if _, err := requireWorkspaceMember(ctx, deps, s); err != nil {
		return nil, err
	}
	_, row, err := resolveTaskRow(ctx, deps, s, in.TaskID)
	if err != nil {
		return nil, err
	}
	if deps.AI == nil {
		return nil, apierrors.New(apierrors.AiProviderNotConfigured)
	}
	summary := row.Title
	if row.Description.Valid && row.Description.String != "" {
		summary = row.Title + "\n\n" + row.Description.String
	}
	priority, err := deps.AI.ProposePriority(ctx, s.workspaceID, summary)
	if err != nil {
		return nil, mapAiError(err)
	}
	return map[string]any{
		"priority":  priority,
		"rationale": "",
	}, nil
}

// runProposeDuplicates ranks stored embeddings against the source task
// via Go-side cosine similarity (MySQL 9.6 Community lacks
// VEC_DISTANCE_COSINE). Thresholds come from ai_settings, with ADR 0003
// defaults when the workspace has no row yet.
func runProposeDuplicates(ctx context.Context, deps Deps, s *session, raw json.RawMessage) (any, error) {
	var in struct {
		TaskID string `json:"taskId"`
	}
	if err := parseArgs(raw, &in); err != nil {
		return nil, err
	}
	wsRole, err := requireWorkspaceMember(ctx, deps, s)
	if err != nil {
		return nil, err
	}
	vis := acl.ListVisibilityArgs(s.userID, wsRole)
	if deps.Embedder == nil {
		return nil, apierrors.New(apierrors.AiProviderNotConfigured)
	}
	taskInternal, row, err := resolveTaskRow(ctx, deps, s, in.TaskID)
	if err != nil {
		return nil, err
	}

	// The lookup key is the embedder's own model, which is what wrote
	// every row: see [embed.Client.Model]. Thresholds come from
	// ai_settings, with the ADR 0003 defaults on miss.
	model := deps.Embedder.Model()
	high := 0.870
	low := 0.750
	if settings, serr := deps.Queries.GetAiSettings(ctx, s.workspaceID); serr == nil {
		if v, perr := strconv.ParseFloat(settings.DuplicateThresholdHigh, 64); perr == nil {
			high = v
		}
		if v, perr := strconv.ParseFloat(settings.DuplicateThresholdLow, 64); perr == nil {
			low = v
		}
	}

	src, err := deps.Queries.GetTaskEmbedding(ctx, generated.GetTaskEmbeddingParams{
		TaskID: taskInternal,
		Model:  model,
	})
	if stderrors.Is(err, sql.ErrNoRows) {
		desc := ""
		if row.Description.Valid {
			desc = row.Description.String
		}
		if eerr := deps.Embedder.EmbedTask(ctx, s.workspaceID, taskInternal, row.Title, desc); eerr != nil {
			return map[string]any{"candidates": []any{}, "model": model}, nil
		}
		src, err = deps.Queries.GetTaskEmbedding(ctx, generated.GetTaskEmbeddingParams{
			TaskID: taskInternal,
			Model:  model,
		})
	}
	if err != nil {
		return map[string]any{"candidates": []any{}, "model": model}, nil
	}
	srcVec, err := embed.Decode(toBytes(src.Vector))
	if err != nil || len(srcVec) == 0 {
		return map[string]any{"candidates": []any{}, "model": model}, nil
	}

	rows, err := deps.Queries.ListCandidateTaskEmbeddings(ctx, generated.ListCandidateTaskEmbeddingsParams{
		WorkspaceID:   s.workspaceID,
		Model:         model,
		TaskID:        taskInternal,
		IsElevated:    vis.IsElevated,
		ActorUserID:   vis.ActorUserID,
		ActorUserID_2: vis.ActorUserID,
		ActorUserID_3: vis.ActorUserID,
		Limit:         200,
	})
	if err != nil {
		return nil, apierrors.Wrap(apierrors.McpToolExecutionFailed, err)
	}
	type cand struct {
		id       string
		title    string
		score    float64
		classify string
	}
	ranked := make([]cand, 0, len(rows))
	for _, r := range rows {
		v, derr := embed.Decode(toBytes(r.Vector))
		if derr != nil || len(v) != len(srcVec) {
			continue
		}
		score := float64(embed.Cosine(srcVec, v))
		if score < low {
			continue
		}
		classification := "related"
		if score >= high {
			classification = "duplicate"
		}
		ranked = append(ranked, cand{
			id:       r.PublicID.String(),
			title:    r.Title,
			score:    score,
			classify: classification,
		})
	}
	sort.SliceStable(ranked, func(i, j int) bool { return ranked[i].score > ranked[j].score })
	if len(ranked) > 20 {
		ranked = ranked[:20]
	}
	out := make([]map[string]any, 0, len(ranked))
	for _, c := range ranked {
		out = append(out, map[string]any{
			"taskId":         c.id,
			"title":          c.title,
			"score":          c.score,
			"classification": c.classify,
		})
	}
	return map[string]any{"candidates": out, "model": model}, nil
}

// runProposeSteps asks the workspace LLM to break an existing task
// into concrete execution steps. It reuses ProposeTasksFrom by
// feeding the task's title + description back in as the source
// signal, which keeps the prompt path and cost guard identical to
// propose_tasks_from.
//
// The project-editor floor is the one REST holds the same request to: a
// breakdown is the first half of apply_steps, it spends the workspace's
// model budget on a task the caller may only be able to read, and the
// decomposition it returns is meant for whoever may restructure the task.
// Reading the task was the whole gate here, so anyone who could see a task
// could bill the workspace for a plan they could not act on.
func runProposeSteps(ctx context.Context, deps Deps, s *session, raw json.RawMessage) (any, error) {
	var in struct {
		TaskID string `json:"taskId"`
	}
	if err := parseArgs(raw, &in); err != nil {
		return nil, err
	}
	if _, err := requireWorkspaceMember(ctx, deps, s); err != nil {
		return nil, err
	}
	_, row, err := resolveTaskRowForWrite(ctx, deps, s, in.TaskID)
	if err != nil {
		return nil, err
	}
	if deps.AI == nil {
		return nil, apierrors.New(apierrors.AiProviderNotConfigured)
	}
	source := "Break this task into concrete execution steps.\n\nTitle: " + row.Title
	if row.Description.Valid && row.Description.String != "" {
		source += "\n\nDescription:\n" + row.Description.String
	}
	steps, err := deps.AI.ProposeTasksFrom(ctx, s.workspaceID, source)
	if err != nil {
		return nil, mapAiError(err)
	}
	out := make([]map[string]any, 0, len(steps))
	for _, st := range steps {
		out = append(out, map[string]any{
			"title":       st.Title,
			"description": st.Description,
			"priority":    st.Priority,
		})
	}
	return map[string]any{"parentTaskId": row.PublicID.String(), "steps": out}, nil
}

// runApplySteps creates the given steps as child tasks under an
// existing parent task. Each step becomes a new tasks row with
// parent_task_id set to the parent's internal id. Returns the list of
// created child public ids.
func runApplySteps(ctx context.Context, deps Deps, s *session, raw json.RawMessage) (any, error) {
	var in struct {
		ParentTaskID string `json:"parentTaskId"`
		Steps        []struct {
			Title       string `json:"title"`
			Description string `json:"description"`
			Priority    int32  `json:"priority"`
		} `json:"steps"`
	}
	if err := parseArgs(raw, &in); err != nil {
		return nil, err
	}
	if len(in.Steps) == 0 {
		return nil, apierrors.New(apierrors.McpToolArgumentsInvalid)
	}
	if _, err := requireWorkspaceMember(ctx, deps, s); err != nil {
		return nil, err
	}
	parentInternal, parentPub, err := resolveTaskForWrite(ctx, deps, s, in.ParentTaskID)
	if err != nil {
		return nil, err
	}
	// no-generated-query: the child rows need the parent's internal project_id as
	// an FK value, and no generated read hands it over. The single-task lookup in
	// acl.go projects the project's public id, which would have to be resolved
	// back to an internal one; LockTaskForTransition does return project_id but
	// is FOR UPDATE and belongs inside the transition transaction, so reading
	// through it here would take a row lock this path has no reason to hold.
	// Replacing this takes a plain generated read of tasks.project_id keyed on
	// the internal id.
	var parentProjectID uint32
	if err := deps.DB.QueryRowContext(ctx,
		`SELECT project_id FROM tasks WHERE id = ? AND workspace_id = ? LIMIT 1`,
		parentInternal, s.workspaceID,
	).Scan(&parentProjectID); err != nil {
		return nil, apierrors.Wrap(apierrors.McpToolExecutionFailed, err)
	}
	for _, st := range in.Steps {
		if st.Title == "" {
			return nil, apierrors.New(apierrors.McpToolArgumentsInvalid)
		}
	}

	// All children are created in one transaction. Task-number allocation
	// locks the project row, and it reads MAX(task_number) within the
	// transaction so each step sees its predecessors and the children get
	// distinct numbers. A failure partway through now rolls the whole batch
	// back instead of leaving orphan children behind.
	created := make([]string, 0, len(in.Steps))
	childIDs := make([]int64, 0, len(in.Steps))
	if txErr := dbretry.InTx(ctx, deps.DB, "mcp.apply_steps", nil, func(ctx context.Context, tx *dbretry.Tx) error {
		created = created[:0]
		childIDs = childIDs[:0]
		for _, st := range in.Steps {
			child, err := taskcreate.New(ctx, tx, taskcreate.Args{
				WorkspaceID:  s.workspaceID,
				ProjectID:    parentProjectID,
				ParentTaskID: sql.NullInt32{Int32: int32(parentInternal), Valid: true}, //#nosec G115 -- parent task id is tasks.id (BIGINT UNSIGNED), fits int32 within realistic deployments
				ActorUserID:  sql.NullInt32{Int32: int32(s.userID), Valid: true},       //#nosec G115 -- session user id is users.id (BIGINT UNSIGNED), fits int32 within realistic deployments
				Title:        st.Title,
				Description:  sql.NullString{String: st.Description, Valid: st.Description != ""},
				Priority:     st.Priority,
			})
			if err != nil {
				return err
			}
			created = append(created, child.PublicID.String())
			childIDs = append(childIDs, child.ID)
		}
		return nil
	}); txErr != nil {
		return nil, apierrors.Wrap(apierrors.McpToolExecutionFailed, txErr)
	}

	// Events are appended after commit so they reference committed rows and
	// a retried transaction cannot publish a child that was rolled back.
	for i, st := range in.Steps {
		childID := childIDs[i]
		// The children are committed; a retry would create them again.
		recordMutation(ctx, deps, s, mutation{
			EventType:    eventbus.TaskCreated,
			AuditAction:  "task.apply_steps",
			ResourceType: "task",
			ResourceID:   created[i],
			TaskID:       &childID,
			Payload: map[string]any{
				"taskId":       created[i],
				"title":        st.Title,
				"parentTaskId": parentPub.String(),
				"via":          "mcp:apply_steps",
			},
			CallSite: "mcp.apply_steps",
		})
	}
	return map[string]any{"created": created}, nil
}

// mapAiError maps ai package sentinel errors to the appropriate API
// error spec for the MCP boundary.
func mapAiError(err error) error {
	switch {
	case stderrors.Is(err, ai.ErrNoProvider):
		return apierrors.New(apierrors.AiProviderNotConfigured)
	case stderrors.Is(err, ai.ErrDailyBudgetExceeded):
		return apierrors.New(apierrors.AiCostGuardExceeded)
	case stderrors.Is(err, ai.ErrParse):
		return apierrors.New(apierrors.AiResponseInvalidJson)
	}
	return apierrors.Wrap(apierrors.AiProviderUpstreamUnreachable, err)
}

// toBytes coerces an interface{} column (returned by sqlc for VECTOR
// columns read back through STRING_TO_VECTOR) into a []byte, or nil
// when the underlying value is neither []byte nor string.
func toBytes(v any) []byte {
	switch x := v.(type) {
	case []byte:
		return x
	case string:
		return []byte(x)
	}
	return nil
}

// runListTimeboxes lists timeboxes in the caller's workspace.
func runListTimeboxes(ctx context.Context, deps Deps, s *session, raw json.RawMessage) (any, error) {
	var in struct {
		Limit  int32 `json:"limit"`
		Offset int32 `json:"offset"`
	}
	if err := parseArgs(raw, &in); err != nil {
		return nil, err
	}
	if _, err := requireWorkspaceMember(ctx, deps, s); err != nil {
		return nil, err
	}
	if in.Limit <= 0 || in.Limit > 200 {
		in.Limit = 50
	}
	rows, err := deps.Queries.ListTimeboxesForWorkspace(ctx, generated.ListTimeboxesForWorkspaceParams{
		WorkspaceID: s.workspaceID,
		Limit:       in.Limit,
		Offset:      in.Offset,
	})
	if err != nil {
		return nil, apierrors.Wrap(apierrors.McpToolExecutionFailed, err)
	}
	items := make([]map[string]any, 0, len(rows))
	for _, r := range rows {
		m := map[string]any{
			"id":       r.PublicID.String(),
			"name":     r.Name,
			"startsOn": r.StartsOn.UTC().Format("2006-01-02"),
			"endsOn":   r.EndsOn.UTC().Format("2006-01-02"),
			"status":   string(r.Status),
		}
		if r.Description.Valid {
			m["description"] = r.Description.String
		}
		if r.ProjectName.Valid {
			m["projectId"] = r.ProjectPublicID.String()
			m["projectName"] = r.ProjectName.String
		}
		items = append(items, m)
	}
	return map[string]any{"timeboxes": items}, nil
}

// runCreateTimebox creates a new timebox in the workspace.
func runCreateTimebox(ctx context.Context, deps Deps, s *session, raw json.RawMessage) (any, error) {
	var in struct {
		Name        string `json:"name"`
		StartsOn    string `json:"startsOn"`
		EndsOn      string `json:"endsOn"`
		Description string `json:"description"`
		ProjectID   string `json:"projectId"`
	}
	if err := parseArgs(raw, &in); err != nil {
		return nil, err
	}
	if in.Name == "" || in.StartsOn == "" || in.EndsOn == "" {
		return nil, apierrors.New(apierrors.McpToolArgumentsInvalid)
	}
	if _, err := requireWorkspaceMember(ctx, deps, s); err != nil {
		return nil, err
	}
	starts, err := time.Parse("2006-01-02", in.StartsOn)
	if err != nil {
		return nil, apierrors.New(apierrors.McpToolArgumentsInvalid)
	}
	ends, err := time.Parse("2006-01-02", in.EndsOn)
	if err != nil {
		return nil, apierrors.New(apierrors.McpToolArgumentsInvalid)
	}
	if !ends.After(starts) {
		return nil, apierrors.New(apierrors.TimeboxTimeboxInvalidDates)
	}
	var projectID sql.NullInt32
	if in.ProjectID != "" {
		prjID, err := resolveProject(ctx, deps, s, in.ProjectID)
		if err != nil {
			return nil, err
		}
		projectID = sql.NullInt32{Int32: int32(prjID), Valid: true} //#nosec G115 -- project id is projects.id (BIGINT UNSIGNED), fits int32 within realistic deployments
	}
	pub := newPublicID()
	desc := sql.NullString{String: in.Description, Valid: in.Description != ""}
	timeboxID, err := deps.Queries.CreateTimebox(ctx, generated.CreateTimeboxParams{
		PublicID:    pub,
		WorkspaceID: s.workspaceID,
		ProjectID:   projectID,
		CreatorID:   s.userID,
		Name:        in.Name,
		Description: desc,
		StartsOn:    starts,
		EndsOn:      ends,
	})
	if err != nil {
		return nil, apierrors.Wrap(apierrors.McpToolExecutionFailed, err)
	}
	// The timebox row is committed; a retry would create a second one.
	recordMutation(ctx, deps, s, mutation{
		EventType:    eventbus.TimeboxCreated,
		AuditAction:  "timebox.create",
		ResourceType: "timebox",
		ResourceID:   pub.String(),
		Payload: map[string]any{
			"timeboxId": pub.String(),
			"name":      in.Name,
			"via":       "mcp",
		},
		CallSite: "mcp.create_timebox",
	})
	_ = timeboxID // internal id not exposed
	return map[string]any{"id": pub.String()}, nil
}

// runAddTaskToTimebox associates a task with a timebox by resolving
// both public UUIDs to internal IDs.
func runAddTaskToTimebox(ctx context.Context, deps Deps, s *session, raw json.RawMessage) (any, error) {
	var in struct {
		TimeboxID string `json:"timeboxId"`
		TaskID    string `json:"taskId"`
	}
	if err := parseArgs(raw, &in); err != nil {
		return nil, err
	}
	if in.TimeboxID == "" || in.TaskID == "" {
		return nil, apierrors.New(apierrors.McpToolArgumentsInvalid)
	}
	if _, err := requireWorkspaceMember(ctx, deps, s); err != nil {
		return nil, err
	}
	// Resolve timebox public_id -> internal id.
	tbPub, err := types.Parse(in.TimeboxID)
	if err != nil {
		return nil, apierrors.New(apierrors.TimeboxTimeboxNotFound)
	}
	tbRow, err := deps.Queries.GetTimeboxByPublicId(ctx, generated.GetTimeboxByPublicIdParams{
		WorkspaceID: s.workspaceID,
		PublicID:    tbPub,
	})
	if err != nil {
		if stderrors.Is(err, sql.ErrNoRows) {
			return nil, apierrors.New(apierrors.TimeboxTimeboxNotFound)
		}
		return nil, apierrors.Wrap(apierrors.McpToolExecutionFailed, err)
	}
	// Resolve task public_id -> internal id.
	taskInternal, taskPub, err := resolveTask(ctx, deps, s, in.TaskID)
	if err != nil {
		return nil, err
	}
	linkPub := newPublicID()
	if err := deps.Queries.AddTaskToTimebox(ctx, generated.AddTaskToTimeboxParams{
		PublicID:    linkPub,
		WorkspaceID: s.workspaceID,
		TimeboxID:   tbRow.ID,
		TaskID:      taskInternal,
	}); err != nil {
		return nil, apierrors.Wrap(apierrors.McpToolExecutionFailed, err)
	}
	taskID64 := int64(taskInternal)
	// The link row is committed and carries a unique key, so a retry
	// would be rejected before it could re-append the event.
	recordMutation(ctx, deps, s, mutation{
		EventType:    eventbus.TimeboxTaskAdded,
		AuditAction:  "timebox.task.add",
		ResourceType: "timebox",
		ResourceID:   tbPub.String(),
		TaskID:       &taskID64,
		Payload: map[string]any{
			"timeboxId": tbPub.String(),
			"taskId":    taskPub.String(),
			"via":       "mcp",
		},
		CallSite: "mcp.add_task_to_timebox",
	})
	return map[string]any{"ok": true}, nil
}

// runExportTasks exports tasks as JSON for MCP consumers. When
// projectId is provided it scopes to that project using
// ExportTasksForLens; otherwise it exports workspace-wide.
func runExportTasks(ctx context.Context, deps Deps, s *session, raw json.RawMessage) (any, error) {
	var in struct {
		ProjectID string `json:"projectId"`
		Limit     int32  `json:"limit"`
	}
	if err := parseArgs(raw, &in); err != nil {
		return nil, err
	}
	wsRole, err := requireWorkspaceMember(ctx, deps, s)
	if err != nil {
		return nil, err
	}
	visibility := mcpExportVisibilityParams(s.userID, wsRole)
	if in.Limit <= 0 || in.Limit > 200 {
		in.Limit = 200
	}
	type exportRow struct {
		PublicID            types.PublicID
		Title               string
		Description         sql.NullString
		DerivedState        string
		Priority            int32
		DueOn               sql.NullTime
		StartedOn           sql.NullTime
		ProjectName         string
		AssigneeDisplayName sql.NullString
	}
	var rows []exportRow
	if in.ProjectID != "" {
		prjID, err := resolveProject(ctx, deps, s, in.ProjectID)
		if err != nil {
			return nil, err
		}
		dbRows, err := deps.Queries.ExportTasksForLens(ctx, generated.ExportTasksForLensParams{
			WorkspaceID:   s.workspaceID,
			ProjectID:     prjID,
			IsElevated:    visibility.isElevated,
			ActorUserID:   visibility.actorUserID,
			ActorUserID_2: visibility.actorUserID,
			ActorUserID_3: visibility.actorUserID,
			Limit:         in.Limit,
		})
		if err != nil {
			return nil, apierrors.Wrap(apierrors.McpToolExecutionFailed, err)
		}
		for _, r := range dbRows {
			rows = append(rows, exportRow{
				PublicID:            r.PublicID,
				Title:               r.Title,
				Description:         r.Description,
				DerivedState:        string(r.DerivedState),
				Priority:            r.Priority,
				DueOn:               r.DueOn,
				StartedOn:           r.StartedOn,
				ProjectName:         r.ProjectName,
				AssigneeDisplayName: r.AssigneeDisplayName,
			})
		}
	} else {
		dbRows, err := deps.Queries.ExportTasksForWorkspace(ctx, generated.ExportTasksForWorkspaceParams{
			WorkspaceID:   s.workspaceID,
			IsElevated:    visibility.isElevated,
			ActorUserID:   visibility.actorUserID,
			ActorUserID_2: visibility.actorUserID,
			ActorUserID_3: visibility.actorUserID,
			Limit:         in.Limit,
		})
		if err != nil {
			return nil, apierrors.Wrap(apierrors.McpToolExecutionFailed, err)
		}
		for _, r := range dbRows {
			rows = append(rows, exportRow{
				PublicID:            r.PublicID,
				Title:               r.Title,
				Description:         r.Description,
				DerivedState:        string(r.DerivedState),
				Priority:            r.Priority,
				DueOn:               r.DueOn,
				StartedOn:           r.StartedOn,
				ProjectName:         r.ProjectName,
				AssigneeDisplayName: r.AssigneeDisplayName,
			})
		}
	}
	items := make([]map[string]any, 0, len(rows))
	for _, r := range rows {
		m := map[string]any{
			"id":          r.PublicID.String(),
			"title":       r.Title,
			"state":       r.DerivedState,
			"priority":    r.Priority,
			"projectName": r.ProjectName,
		}
		if r.Description.Valid {
			m["description"] = r.Description.String
		}
		if r.DueOn.Valid {
			m["dueOn"] = r.DueOn.Time.UTC().Format("2006-01-02")
		}
		if r.AssigneeDisplayName.Valid {
			m["assignee"] = r.AssigneeDisplayName.String
		}
		items = append(items, m)
	}
	// An export is a read, but it is also the moment a workspace's task
	// data leaves it in bulk. REST records both halves for exactly that
	// reason; leaving MCP out meant an administrator investigating a leak
	// saw only the exports made by people using a browser, and none of
	// the ones made by the automated callers that use this surface most.
	recordMutation(ctx, deps, s, mutation{
		EventType:    eventbus.ExportRequested,
		AuditAction:  "export.create",
		ResourceType: "export",
		Payload: map[string]any{
			"format": "json",
			"count":  len(items),
			"via":    "mcp",
		},
		CallSite: "mcp.export_tasks",
	})
	return map[string]any{"tasks": items}, nil
}

type mcpExportVisibility struct {
	isElevated  int64
	actorUserID int64
}

func mcpExportVisibilityParams(actorID uint32, wsRole acl.WorkspaceRole) mcpExportVisibility {
	var elevated int64
	if wsRole.AtLeast(acl.WorkspaceRoleAdmin) {
		elevated = 1
	}
	return mcpExportVisibility{
		isElevated:  elevated,
		actorUserID: int64(actorID),
	}
}

// runProposeRelations finds related or duplicate tasks for a given task
// by embedding similarity, returning structured suggestions with a
// suggestedKind field ("duplicate" or "related"). This is similar to
// propose_duplicates but returns richer structured output.
func runProposeRelations(ctx context.Context, deps Deps, s *session, raw json.RawMessage) (any, error) {
	var in struct {
		TaskID string `json:"taskId"`
	}
	if err := parseArgs(raw, &in); err != nil {
		return nil, err
	}
	wsRole, err := requireWorkspaceMember(ctx, deps, s)
	if err != nil {
		return nil, err
	}
	vis := acl.ListVisibilityArgs(s.userID, wsRole)
	if deps.Embedder == nil {
		return nil, apierrors.New(apierrors.AiProviderNotConfigured)
	}
	taskInternal, row, err := resolveTaskRow(ctx, deps, s, in.TaskID)
	if err != nil {
		return nil, err
	}

	// The lookup key is the embedder's own model, which is what wrote
	// every row: see [embed.Client.Model]. Thresholds come from
	// ai_settings, with the ADR 0003 defaults on miss.
	model := deps.Embedder.Model()
	high := 0.870
	low := 0.750
	if settings, serr := deps.Queries.GetAiSettings(ctx, s.workspaceID); serr == nil {
		if v, perr := strconv.ParseFloat(settings.DuplicateThresholdHigh, 64); perr == nil {
			high = v
		}
		if v, perr := strconv.ParseFloat(settings.DuplicateThresholdLow, 64); perr == nil {
			low = v
		}
	}

	src, err := deps.Queries.GetTaskEmbedding(ctx, generated.GetTaskEmbeddingParams{
		TaskID: taskInternal,
		Model:  model,
	})
	if stderrors.Is(err, sql.ErrNoRows) {
		desc := ""
		if row.Description.Valid {
			desc = row.Description.String
		}
		if eerr := deps.Embedder.EmbedTask(ctx, s.workspaceID, taskInternal, row.Title, desc); eerr != nil {
			return map[string]any{"candidates": []any{}, "model": model}, nil
		}
		src, err = deps.Queries.GetTaskEmbedding(ctx, generated.GetTaskEmbeddingParams{
			TaskID: taskInternal,
			Model:  model,
		})
	}
	if err != nil {
		return map[string]any{"candidates": []any{}, "model": model}, nil
	}
	srcVec, err := embed.Decode(toBytes(src.Vector))
	if err != nil || len(srcVec) == 0 {
		return map[string]any{"candidates": []any{}, "model": model}, nil
	}

	rows, err := deps.Queries.ListCandidateTaskEmbeddings(ctx, generated.ListCandidateTaskEmbeddingsParams{
		WorkspaceID:   s.workspaceID,
		Model:         model,
		TaskID:        taskInternal,
		IsElevated:    vis.IsElevated,
		ActorUserID:   vis.ActorUserID,
		ActorUserID_2: vis.ActorUserID,
		ActorUserID_3: vis.ActorUserID,
		Limit:         200,
	})
	if err != nil {
		return nil, apierrors.Wrap(apierrors.McpToolExecutionFailed, err)
	}
	type cand struct {
		id    string
		title string
		score float64
		kind  string
	}
	ranked := make([]cand, 0, len(rows))
	for _, r := range rows {
		v, derr := embed.Decode(toBytes(r.Vector))
		if derr != nil || len(v) != len(srcVec) {
			continue
		}
		score := float64(embed.Cosine(srcVec, v))
		if score < low {
			continue
		}
		suggestedKind := "related"
		if score >= high {
			suggestedKind = "duplicate"
		}
		ranked = append(ranked, cand{
			id:    r.PublicID.String(),
			title: r.Title,
			score: score,
			kind:  suggestedKind,
		})
	}
	sort.SliceStable(ranked, func(i, j int) bool { return ranked[i].score > ranked[j].score })
	if len(ranked) > 20 {
		ranked = ranked[:20]
	}
	out := make([]map[string]any, 0, len(ranked))
	for _, c := range ranked {
		out = append(out, map[string]any{
			"taskId":        c.id,
			"title":         c.title,
			"score":         c.score,
			"suggestedKind": c.kind,
		})
	}
	return map[string]any{"candidates": out, "model": model}, nil
}

// runProposeLens compiles a prose prompt into a validated Lens via
// the NL query compiler. When no compiler is wired (e.g., no AI mock
// or provider configured) it returns AI.PROVIDER.NOT_CONFIGURED.
func runProposeLens(ctx context.Context, deps Deps, s *session, raw json.RawMessage) (any, error) {
	var in struct {
		Prompt string `json:"prompt"`
	}
	if err := parseArgs(raw, &in); err != nil {
		return nil, err
	}
	if _, err := requireWorkspaceMember(ctx, deps, s); err != nil {
		return nil, err
	}
	if deps.NlQuery == nil {
		return nil, apierrors.New(apierrors.AiProviderNotConfigured)
	}
	lens, err := deps.NlQuery.Compile(ctx, in.Prompt)
	if err != nil {
		if stderrors.Is(err, nlquery.ErrUnparseable) {
			return nil, apierrors.New(apierrors.AiResponseInvalidJson)
		}
		return nil, apierrors.Wrap(apierrors.AiProviderUpstreamUnreachable, err)
	}
	return map[string]any{"lens": lens}, nil
}

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
	}
	if err := parseArgs(raw, &in); err != nil {
		return nil, err
	}
	if _, err := requireWorkspaceMember(ctx, deps, s); err != nil {
		return nil, err
	}

	items := []map[string]any{}

	if in.ParentPageID != "" {
		parentInternal, _, err := resolvePage(ctx, deps, s, in.ParentPageID)
		if err != nil {
			return nil, err
		}
		rows, err := deps.Queries.ListChildPages(ctx, generated.ListChildPagesParams{
			WorkspaceID:  s.workspaceID,
			ParentPageID: sql.NullInt32{Int32: int32(parentInternal), Valid: true}, //#nosec G115 -- parent page id is pages.id (BIGINT UNSIGNED), fits int32 within realistic deployments
			Limit:        200,
			Offset:       0,
		})
		if err != nil {
			return nil, apierrors.Wrap(apierrors.McpToolExecutionFailed, err)
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
			Limit:       200,
			Offset:      0,
		})
		if err != nil {
			return nil, apierrors.Wrap(apierrors.McpToolExecutionFailed, err)
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
			Limit:       200,
			Offset:      0,
		})
		if err != nil {
			return nil, apierrors.Wrap(apierrors.McpToolExecutionFailed, err)
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
	return map[string]any{"pages": items}, nil
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
		prjID, err := resolveProject(ctx, deps, s, in.ProjectID)
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
	pageID, err := deps.Queries.CreatePage(ctx, generated.CreatePageParams{
		PublicID:      pub,
		WorkspaceID:   s.workspaceID,
		ProjectID:     projectID,
		CreatorID:     s.userID,
		ParentPageID:  parentPageID,
		Title:         in.Title,
		Body:          in.Body,
		IsAiGenerated: false,
	})
	if err != nil {
		return nil, apierrors.Wrap(apierrors.McpToolExecutionFailed, err)
	}

	// The page row is committed; a retry would create a second one.
	recordMutation(ctx, deps, s, mutation{
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
	_ = pageID // internal id not exposed
	return map[string]any{"id": pub.String()}, nil
}

// runUpdatePage updates a wiki page's title or content.
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
	pageInternal, pub, err := resolvePage(ctx, deps, s, in.PageID)
	if err != nil {
		return nil, err
	}

	// Fetch current project_id and parent_page_id to preserve them: UpdatePage
	// takes both as parameters, so a value not read back here is a value the
	// update would clear.
	//
	// no-generated-query: UpdatePage takes the internal project_id and
	// parent_page_id, and GetPageByPublicId — the only generated read of a single
	// page — projects ProjectPublicID and ParentPagePublicID instead. Going
	// through it would mean resolving two public ids back to internal ones to
	// write back values that were never meant to change. Replacing this takes a
	// generated read that returns the two FK columns as they are stored.
	var projectID sql.NullInt32
	var parentPageID sql.NullInt32
	const qPage = `SELECT project_id, parent_page_id FROM pages WHERE id = ? AND workspace_id = ? LIMIT 1`
	if err := deps.DB.QueryRowContext(ctx, qPage, pageInternal, s.workspaceID).Scan(&projectID, &parentPageID); err != nil {
		return nil, apierrors.Wrap(apierrors.McpToolExecutionFailed, err)
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

	// Not an existence check: an update carrying the page's current
	// title and body reports zero. resolvePage above is what answers for
	// a page that is not there.
	if _, err := deps.Queries.UpdatePage(ctx, generated.UpdatePageParams{
		Title:        titleParam,
		Body:         bodyParam,
		ProjectID:    projectID,
		ParentPageID: parentPageID,
		WorkspaceID:  s.workspaceID,
		PublicID:     pub,
	}); err != nil {
		return nil, apierrors.Wrap(apierrors.McpToolExecutionFailed, err)
	}

	pageID64 := int64(pageInternal)
	// Propagated: the update is idempotent, so a retry re-applies the
	// same values and re-appends the event.
	if err := recordMutationStrict(ctx, deps, s, mutation{
		EventType:    eventbus.PageUpdated,
		AuditAction:  "page.update",
		ResourceType: "page",
		ResourceID:   pub.String(),
		Payload: map[string]any{
			"pageId": pub.String(),
			"via":    "mcp",
		},
		CallSite: "mcp.update_page",
	}); err != nil {
		return nil, apierrors.Wrap(apierrors.McpToolExecutionFailed, err)
	}
	_ = pageID64 // used only for the event; no page-specific event field yet
	return map[string]any{"id": pub.String()}, nil
}

// generatedPageTitleMaxLen caps the page title this tool produces,
// whether it came from the caller's description or from the model. The
// pages.title column is wider; this is the tool's own bound, and it
// matches the maxLength the create_page and update_page tools declare on
// a caller-supplied title.
const generatedPageTitleMaxLen = 200

// generatedPageTitle builds the fallback page title from the caller's
// context description.
//
// The cut lands on a rune boundary: the description arrives over MCP and
// is free-form, so a byte-indexed cut can sever a multi-byte character
// and hand the utf8mb4 column a fragment it rejects under
// STRICT_TRANS_TABLES — the page then fails to create with nothing in
// the request to explain why.
func generatedPageTitle(contextDescription string) string {
	return stringutil.TruncateBytes("Generated: "+contextDescription, generatedPageTitleMaxLen)
}

// runGeneratePage generates a wiki page using AI. When no AI provider
// is configured, it creates a page with the context description as body
// and marks is_ai_generated=false.
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

	var projectID sql.NullInt32
	if in.ProjectID != "" {
		prjID, err := resolveProject(ctx, deps, s, in.ProjectID)
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
	contextParts := []string{in.ContextDescription}
	for _, tid := range in.TaskIDs {
		_, tRow, err := resolveTaskRow(ctx, deps, s, tid)
		if err != nil {
			return nil, err
		}
		taskCtx := "\n\nTask: " + tRow.Title
		if tRow.Description.Valid && tRow.Description.String != "" {
			taskCtx += "\nDescription: " + tRow.Description.String
		}
		contextParts = append(contextParts, taskCtx)
	}

	// Attempt AI generation.
	title := generatedPageTitle(in.ContextDescription)
	body := in.ContextDescription
	isAI := false

	if deps.AI != nil {
		signal := "Generate a wiki page about the following topic.\n\n"
		for _, part := range contextParts {
			signal += part
		}
		proposed, err := deps.AI.ProposeTasksFrom(ctx, s.workspaceID, signal)
		if err == nil && len(proposed) > 0 {
			// Use the first proposed task's title and description as page
			// content. ProposeTasksFrom is reused as the general-purpose
			// LLM call; the response is repurposed here.
			//
			// The model's title gets the same bound as the fallback one.
			// Nothing constrains what a model returns, so an overlong title
			// reached pages.title unclipped and the insert failed, losing
			// the body the call had already paid for. The cut is rune-aware
			// for the same reason as in generatedPageTitle.
			title = stringutil.TruncateBytes(proposed[0].Title, generatedPageTitleMaxLen)
			body = proposed[0].Description
			isAI = true
		}
		// On AI error, fall through to non-AI creation.
	}

	pub := newPublicID()
	pageID, err := deps.Queries.CreatePage(ctx, generated.CreatePageParams{
		PublicID:      pub,
		WorkspaceID:   s.workspaceID,
		ProjectID:     projectID,
		CreatorID:     s.userID,
		ParentPageID:  sql.NullInt32{},
		Title:         title,
		Body:          body,
		IsAiGenerated: isAI,
	})
	if err != nil {
		return nil, apierrors.Wrap(apierrors.McpToolExecutionFailed, err)
	}

	// The page row is committed, and a retry would repeat the model call
	// that produced its body as well as duplicating the page.
	recordMutation(ctx, deps, s, mutation{
		EventType:    eventbus.PageCreated,
		AuditAction:  "page.generate",
		ResourceType: "page",
		ResourceID:   pub.String(),
		Payload: map[string]any{
			"pageId":        pub.String(),
			"title":         title,
			"isAiGenerated": isAI,
			"via":           "mcp:generate_page",
		},
		CallSite: "mcp.generate_page",
	})
	_ = pageID // internal id not exposed
	return map[string]any{
		"id":            pub.String(),
		"isAiGenerated": isAI,
	}, nil
}

// taskTitleMaxLen is the width of tasks.title. Model-produced titles are
// cut to it in bytes, which is conservative for a utf8mb4 column counted
// in characters.
const taskTitleMaxLen = 255

// runSmartCreateTask creates a parent task and AI-suggested subtasks with
// assignee recommendations. It delegates to the AI orchestrator's
// ProposeSmartCreate method to get a structured proposal, then persists
// the parent task and each subtask as child tasks. When the proposal
// includes assignee suggestions, the tool adds task actors for valid
// workspace members.
func runSmartCreateTask(ctx context.Context, deps Deps, s *session, raw json.RawMessage) (any, error) {
	var in struct {
		ProjectID   string `json:"projectId"`
		Title       string `json:"title"`
		Description string `json:"description"`
	}
	if err := parseArgs(raw, &in); err != nil {
		return nil, err
	}
	if in.Title == "" {
		return nil, apierrors.New(apierrors.McpToolArgumentsInvalid)
	}
	wsRole, err := requireWorkspaceMember(ctx, deps, s)
	if err != nil {
		return nil, err
	}
	vis := acl.ListVisibilityArgs(s.userID, wsRole)
	prjID, err := resolveProjectForWrite(ctx, deps, s, in.ProjectID)
	if err != nil {
		return nil, err
	}
	if deps.AI == nil {
		return nil, apierrors.New(apierrors.AiProviderNotConfigured)
	}
	if deps.Embedder == nil {
		return nil, apierrors.New(apierrors.AiProviderNotConfigured)
	}

	// Call the smart create orchestrator. The embed provider satisfies
	// ai.EmbedClient and *generated.Queries satisfies SmartCreateReader.
	proposal, err := deps.AI.ProposeSmartCreate(
		ctx, s.workspaceID,
		in.Title, in.Description,
		deps.Embedder.Provider, deps.Queries,
		vis,
	)
	if err != nil {
		return nil, mapAiError(err)
	}

	// Bound every title the model produced before any of them reaches a
	// column. Nothing constrains what a model returns, and tasks.title is
	// finite, so an overlong subtask title failed the insert and took the
	// whole batch — parent included — down with it. The caller's own title
	// is bounded by the tool's input schema; this is the same bound applied
	// to the half of the payload no schema covers. The cut is rune-aware:
	// a byte-indexed cut can sever a multi-byte character and hand the
	// utf8mb4 column a fragment it rejects under STRICT_TRANS_TABLES.
	for i := range proposal.Subtasks {
		proposal.Subtasks[i].Title = stringutil.TruncateBytes(proposal.Subtasks[i].Title, taskTitleMaxLen)
	}

	// Create the parent and every proposed subtask in one transaction, so a
	// failure partway through the batch does not leave a parent with a
	// truncated set of children.
	type createdChild struct {
		id    int64
		pub   string
		title string
	}
	var (
		parentPub types.PublicID
		parentID  int64
		children  []createdChild
	)
	if txErr := dbretry.InTx(ctx, deps.DB, "mcp.smart_create_task", nil, func(ctx context.Context, tx *dbretry.Tx) error {
		children = children[:0]
		parent, err := taskcreate.New(ctx, tx, taskcreate.Args{
			WorkspaceID: s.workspaceID,
			ProjectID:   prjID,
			ActorUserID: sql.NullInt32{Int32: int32(s.userID), Valid: true}, //#nosec G115 -- session user id is users.id (BIGINT UNSIGNED), fits int32 within realistic deployments
			Title:       in.Title,
			Description: sql.NullString{String: in.Description, Valid: in.Description != ""},
		})
		if err != nil {
			return err
		}
		parentPub = parent.PublicID
		parentID = parent.ID

		for _, st := range proposal.Subtasks {
			if st.Title == "" {
				continue
			}
			child, cerr := taskcreate.New(ctx, tx, taskcreate.Args{
				WorkspaceID:  s.workspaceID,
				ProjectID:    prjID,
				ParentTaskID: sql.NullInt32{Int32: int32(parent.ID), Valid: true}, //#nosec G115 -- parent_task_id is tasks.id (BIGINT UNSIGNED), fits int32 within realistic deployments
				ActorUserID:  sql.NullInt32{Int32: int32(s.userID), Valid: true},  //#nosec G115 -- session user id is users.id (BIGINT UNSIGNED), fits int32 within realistic deployments
				Title:        st.Title,
				Description:  sql.NullString{String: st.Description, Valid: st.Description != ""},
				Priority:     smartCreatePriorityToInt(st.Priority),
			})
			if cerr != nil {
				return cerr
			}
			children = append(children, createdChild{id: child.ID, pub: child.PublicID.String(), title: st.Title})
		}
		return nil
	}); txErr != nil {
		return nil, apierrors.Wrap(apierrors.McpToolExecutionFailed, txErr)
	}

	// Events are appended after commit so they reference committed rows.
	// The whole tree is durable by now, so a failure here is absorbed:
	// a retry would build a second copy of it.
	noteInvocationTask(ctx, uint32(parentID)) //#nosec G115 -- task id is tasks.id (BIGINT UNSIGNED), fits uint32 within realistic deployments
	recordMutation(ctx, deps, s, mutation{
		EventType:    eventbus.TaskCreated,
		AuditAction:  "task.smart_create",
		ResourceType: "task",
		ResourceID:   parentPub.String(),
		TaskID:       &parentID,
		Payload: map[string]any{
			"taskId": parentPub.String(),
			"title":  in.Title,
			"via":    "mcp:smart_create_task",
		},
		CallSite: "mcp.smart_create_task",
	})
	subtaskIDs := make([]string, 0, len(children))
	for i := range children {
		childID := children[i].id
		recordMutation(ctx, deps, s, mutation{
			EventType:    eventbus.TaskCreated,
			AuditAction:  "task.create",
			ResourceType: "task",
			ResourceID:   children[i].pub,
			TaskID:       &childID,
			Payload: map[string]any{
				"taskId":       children[i].pub,
				"title":        children[i].title,
				"parentTaskId": parentPub.String(),
				"via":          "mcp:smart_create_task",
			},
			CallSite: "mcp.smart_create_task",
		})
		subtaskIDs = append(subtaskIDs, children[i].pub)
	}

	// Build assignee suggestions for the response (we do not auto-assign
	// because the caller should review and confirm the suggestions).
	assignees := make([]map[string]any, 0, len(proposal.SuggestedAssignees))
	for _, a := range proposal.SuggestedAssignees {
		assignees = append(assignees, map[string]any{
			"userPublicId": a.UserPublicID,
			"displayName":  a.DisplayName,
			"confidence":   a.Confidence,
			"reason":       a.Reason,
		})
	}

	subtaskSuggestions := make([]map[string]any, 0, len(proposal.Subtasks))
	for i, st := range proposal.Subtasks {
		m := map[string]any{
			"title":    st.Title,
			"priority": st.Priority,
		}
		if i < len(subtaskIDs) {
			m["id"] = subtaskIDs[i]
		}
		if st.Assignee != nil {
			m["suggestedAssignee"] = map[string]any{
				"userPublicId": st.Assignee.UserPublicID,
				"displayName":  st.Assignee.DisplayName,
				"confidence":   st.Assignee.Confidence,
				"reason":       st.Assignee.Reason,
			}
		}
		subtaskSuggestions = append(subtaskSuggestions, m)
	}

	return map[string]any{
		"id":                 parentPub.String(),
		"subtasks":           subtaskSuggestions,
		"suggestedAssignees": assignees,
	}, nil
}

// smartCreatePriorityToInt maps the LLM priority string to the DB int32
// value used in the tasks table. Unknown values default to 0 (none).
func smartCreatePriorityToInt(s string) int32 {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "low":
		return 1
	case "medium":
		return 2
	case "high":
		return 3
	default:
		return 0
	}
}

// ----------------------------------------------------------------------------
// Calendar tools
// ----------------------------------------------------------------------------

// calendarRoleFor derives the MCP-exposed role string for a calendar
// row. calendar_subscriptions.role is gone; we mirror the HTTP handler
// convention (personal owner -> "owner", system -> "viewer", otherwise
// "editor" since every ws member has edit access).
func calendarRoleFor(kind calendar.CalendarsKind, ownerUserID sql.NullInt32, actorUserID uint32) string {
	if ownerUserID.Valid && uint32(ownerUserID.Int32) == actorUserID { //#nosec G115 -- owner_user_id is users.id (BIGINT UNSIGNED), fits uint32 within realistic deployments
		return "owner"
	}
	if kind == calendar.CalendarsKindSystem {
		return "viewer"
	}
	return "editor"
}

func runListCalendars(ctx context.Context, deps Deps, s *session, _ json.RawMessage) (any, error) {
	if _, err := requireWorkspaceMember(ctx, deps, s); err != nil {
		return nil, err
	}
	rows, err := deps.CalendarQueries.ListCalendarsForUser(ctx, calendar.ListCalendarsForUserParams{
		UserID:      s.userID,
		WorkspaceID: s.workspaceID,
	})
	if err != nil {
		return nil, apierrors.Wrap(apierrors.McpToolExecutionFailed, err)
	}
	items := make([]map[string]any, 0, len(rows))
	for _, r := range rows {
		items = append(items, map[string]any{
			"id":   r.PublicID.String(),
			"kind": string(r.Kind),
			"name": r.Name,
			// calendar_subscriptions.role has been dropped. Derive
			// the role from ownership so existing MCP clients keep a
			// stable shape.
			"role":  calendarRoleFor(r.Kind, r.OwnerUserID, s.userID),
			"color": r.Color,
			// member_color has been dropped from calendar_subscriptions;
			// fall back to display_color.
			"memberColor": r.DisplayColor,
			"visible":     r.Visible,
		})
	}
	return map[string]any{"calendars": items}, nil
}

// parseDayBoundary parses a YYYY-MM-DD string and returns the time at
// 00:00:00 UTC on that calendar day. Used for all-day events at the MCP
// boundary, mirroring the API/MCP convention from
// docs/conventions/api-types.md.
func parseDayBoundary(s string) (time.Time, error) {
	return time.Parse("2006-01-02", s)
}

// mcpCalendarOccurrence is one thing on the calendar during a window:
// either a plain event or one occurrence of a series, flattened so the
// tools downstream do not have to care which.
type mcpCalendarOccurrence struct {
	row     calendar.ListCalendarEventsAcrossCalendarsRow
	startAt time.Time
	endAt   time.Time
	dated   bool
}

// listCalendarOccurrences returns everything on the caller's calendars
// between startTime and endTime, with recurring series expanded into
// their concrete occurrences.
//
// Both calendar tools go through here. Reading only the non-recurring
// query — which is what they did — meant the standing meetings were
// absent from every answer: an agent asked for Tuesday's schedule got a
// day with the weekly one-to-ones missing, and the free-slot search
// built its busy map from the same truncated set and offered those hours
// as available.
func listCalendarOccurrences(
	ctx context.Context,
	deps Deps,
	membershipUserID uint32,
	viewerUserID uint32,
	workspaceID uint32,
	startTime, endTime time.Time,
) ([]mcpCalendarOccurrence, error) {
	plain, err := deps.CalendarQueries.ListCalendarEventsAcrossCalendars(ctx, calendar.ListCalendarEventsAcrossCalendarsParams{
		ViewerUserID: viewerUserID,
		UserID:       membershipUserID,
		WorkspaceID:  workspaceID,
		RangeEnd:     sql.NullTime{Time: endTime, Valid: true},
		RangeStart:   sql.NullTime{Time: startTime, Valid: true},
	})
	if err != nil {
		return nil, apierrors.Wrap(apierrors.McpToolExecutionFailed, err)
	}

	out := make([]mcpCalendarOccurrence, 0, len(plain))
	for _, r := range plain {
		occ := mcpCalendarOccurrence{row: r}
		if r.StartAt.Valid && r.EndAt.Valid {
			occ.startAt, occ.endAt, occ.dated = r.StartAt.Time, r.EndAt.Time, true
		}
		out = append(out, occ)
	}

	series, err := deps.CalendarQueries.ListRecurringCalendarEventsAcrossCalendars(ctx, calendar.ListRecurringCalendarEventsAcrossCalendarsParams{
		ViewerUserID:  viewerUserID,
		UserID:        membershipUserID,
		WorkspaceID:   workspaceID,
		StartAt:       sql.NullTime{Time: endTime, Valid: true},
		RecurrenceEnd: sql.NullTime{Time: startTime, Valid: true},
	})
	if err != nil {
		return nil, apierrors.Wrap(apierrors.McpToolExecutionFailed, err)
	}
	for _, r := range series {
		if !r.StartAt.Valid || !r.EndAt.Valid {
			continue
		}
		rule, perr := recurrence.ParseRule(r.RecurrenceRule)
		if perr != nil || rule == nil {
			continue
		}
		var seriesEnd *time.Time
		if r.RecurrenceEnd.Valid {
			seriesEnd = &r.RecurrenceEnd.Time
		}
		for _, inst := range recurrence.Expand(recurrence.Event{
			StartAt:       r.StartAt.Time,
			EndAt:         r.EndAt.Time,
			Timezone:      r.Timezone,
			Rule:          rule,
			Exceptions:    recurrence.ParseExceptions(r.RecurrenceExceptions),
			RecurrenceEnd: seriesEnd,
		}, startTime, endTime) {
			out = append(out, mcpCalendarOccurrence{
				row:     recurringRowAsPlain(r),
				startAt: inst.StartAt,
				endAt:   inst.EndAt,
				dated:   true,
			})
		}
	}

	sort.Slice(out, func(i, j int) bool {
		switch {
		case out[i].dated && out[j].dated && !out[i].startAt.Equal(out[j].startAt):
			return out[i].startAt.Before(out[j].startAt)
		case out[i].dated != out[j].dated:
			return out[i].dated
		default:
			return out[i].row.PublicID.String() < out[j].row.PublicID.String()
		}
	})
	return out, nil
}

// recurringRowAsPlain narrows a recurring row to the shape the callers
// project. The two queries select the same columns; sqlc names the row
// types separately, so the copy is mechanical.
func recurringRowAsPlain(r calendar.ListRecurringCalendarEventsAcrossCalendarsRow) calendar.ListCalendarEventsAcrossCalendarsRow {
	return calendar.ListCalendarEventsAcrossCalendarsRow{
		PublicID:                  r.PublicID,
		CalendarID:                r.CalendarID,
		CalendarPublicID:          r.CalendarPublicID,
		Kind:                      r.Kind,
		Visibility:                r.Visibility,
		ShowAs:                    r.ShowAs,
		Flexibility:               r.Flexibility,
		Title:                     r.Title,
		AllDay:                    r.AllDay,
		StartAt:                   r.StartAt,
		EndAt:                     r.EndAt,
		Timezone:                  r.Timezone,
		Location:                  r.Location,
		OwnerUserID:               r.OwnerUserID,
		BlockLabel:                r.BlockLabel,
		TaskID:                    r.TaskID,
		UpdatedAt:                 r.UpdatedAt,
		CreatedAt:                 r.CreatedAt,
		IsAttendee:                r.IsAttendee,
		CalendarDefaultVisibility: r.CalendarDefaultVisibility,
	}
}

func runListCalendarEvents(ctx context.Context, deps Deps, s *session, raw json.RawMessage) (any, error) {
	var in struct {
		StartDate string `json:"startDate"`
		EndDate   string `json:"endDate"`
		StartAt   *int64 `json:"startAt"`
		EndAt     *int64 `json:"endAt"`
	}
	if err := parseArgs(raw, &in); err != nil {
		return nil, err
	}
	if _, err := requireWorkspaceMember(ctx, deps, s); err != nil {
		return nil, err
	}
	var startTime, endTime time.Time
	switch {
	case in.StartAt != nil:
		startTime = time.Unix(*in.StartAt, 0).UTC()
	case in.StartDate != "":
		t, err := parseDayBoundary(in.StartDate)
		if err != nil {
			return nil, apierrors.Newf(apierrors.McpToolArgumentsInvalid, "invalid startDate: %v", err)
		}
		startTime = t
	default:
		return nil, apierrors.New(apierrors.McpToolArgumentsInvalid)
	}
	switch {
	case in.EndAt != nil:
		endTime = time.Unix(*in.EndAt, 0).UTC()
	case in.EndDate != "":
		t, err := parseDayBoundary(in.EndDate)
		if err != nil {
			return nil, apierrors.Newf(apierrors.McpToolArgumentsInvalid, "invalid endDate: %v", err)
		}
		endTime = t
	default:
		return nil, apierrors.New(apierrors.McpToolArgumentsInvalid)
	}
	occurrences, err := listCalendarOccurrences(ctx, deps, s.userID, s.userID, s.workspaceID, startTime, endTime)
	if err != nil {
		return nil, err
	}
	items := make([]map[string]any, 0, len(occurrences))
	for _, occ := range occurrences {
		r := occ.row
		item := map[string]any{
			"id":          r.PublicID.String(),
			"calendarId":  r.CalendarPublicID.String(),
			"kind":        string(r.Kind),
			"showAs":      string(r.ShowAs),
			"flexibility": string(r.Flexibility),
			"title":       r.Title,
			"allDay":      r.AllDay,
		}
		// Each occurrence carries its own times. A series is reported as
		// the meetings it produces in the window rather than as one row
		// plus a rule the caller would have to expand, because the
		// caller here is a model answering "what is on Tuesday".
		if occ.dated {
			if r.AllDay {
				item["startDate"] = occ.startAt.UTC().Format("2006-01-02")
				item["endDate"] = occ.endAt.UTC().Format("2006-01-02")
			} else {
				item["startAt"] = occ.startAt.Unix()
				item["endAt"] = occ.endAt.Unix()
			}
		}
		items = append(items, item)
	}
	return map[string]any{"events": items}, nil
}

func runCreateCalendarEvent(ctx context.Context, deps Deps, s *session, raw json.RawMessage) (any, error) {
	var in struct {
		CalendarID  string `json:"calendarId"`
		Title       string `json:"title"`
		StartAt     *int64 `json:"startAt"`
		EndAt       *int64 `json:"endAt"`
		StartDate   string `json:"startDate"`
		EndDate     string `json:"endDate"`
		Kind        string `json:"kind"`
		ShowAs      string `json:"showAs"`
		Flexibility string `json:"flexibility"`
		Visibility  string `json:"visibility"`
		OwnerUserID string `json:"ownerUserId"`
		AllDay      *bool  `json:"allDay"`
		Location    string `json:"location"`
		Memo        string `json:"memo"`
		BlockLabel  string `json:"blockLabel"`
	}
	if err := parseArgs(raw, &in); err != nil {
		return nil, err
	}
	if in.CalendarID == "" || in.Title == "" {
		return nil, apierrors.New(apierrors.McpToolArgumentsInvalid)
	}
	if _, err := requireWorkspaceMember(ctx, deps, s); err != nil {
		return nil, err
	}
	calID, err := resolveCalendarWrite(ctx, deps, s, in.CalendarID)
	if err != nil {
		return nil, err
	}
	allDay := false
	if in.AllDay != nil {
		allDay = *in.AllDay
	}
	var startAt, endAt time.Time
	if allDay {
		if in.StartDate == "" || in.EndDate == "" {
			return nil, apierrors.Newf(apierrors.McpToolArgumentsInvalid, "all-day events require startDate and endDate")
		}
		startAt, err = parseDayBoundary(in.StartDate)
		if err != nil {
			return nil, apierrors.Newf(apierrors.McpToolArgumentsInvalid, "invalid startDate: %v", err)
		}
		endAt, err = parseDayBoundary(in.EndDate)
		if err != nil {
			return nil, apierrors.Newf(apierrors.McpToolArgumentsInvalid, "invalid endDate: %v", err)
		}
	} else {
		if in.StartAt == nil || in.EndAt == nil {
			return nil, apierrors.Newf(apierrors.McpToolArgumentsInvalid, "timed events require startAt and endAt (unix seconds)")
		}
		startAt = time.Unix(*in.StartAt, 0).UTC()
		endAt = time.Unix(*in.EndAt, 0).UTC()
	}
	// The window is checked before it is normalised, and through the same
	// rule the REST create applies: an agent that inverts it is told which
	// way round it goes, instead of being handed the CHECK constraint's
	// refusal as an unexplained execution failure.
	if rangeErr := calendarrules.RequireEventChronology(
		calendarrules.UnixSeconds(startAt), calendarrules.UnixSeconds(endAt)); rangeErr != nil {
		return nil, rangeErr
	}
	// An all-day row is stored as UTC midnight on the author's date.
	// parseDayBoundary already lands there, so this changes nothing today
	// — it is here because the canonical form is one decision for every
	// transport, and reading it off the shared rule is what stops the two
	// from drifting apart the way they did before.
	normStart, normEnd := calendarrules.NormalizeAllDayBounds(allDay,
		sql.NullTime{Time: startAt, Valid: true}, sql.NullTime{Time: endAt, Valid: true})
	startAt, endAt = normStart.Time, normEnd.Time

	kind := calendar.CalendarEventsKindEvent
	if in.Kind != "" {
		kind = calendar.CalendarEventsKind(in.Kind)
	}
	showAs := calendar.CalendarEventsShowAsBusy
	if in.ShowAs != "" {
		showAs = calendar.CalendarEventsShowAs(in.ShowAs)
	}
	// Default to 'fixed'. An agent that says nothing about movability has
	// not been told the event can move, and guessing otherwise would offer
	// up the owner's time on their behalf.
	flexibility := calendar.CalendarEventsFlexibilityFixed
	if in.Flexibility != "" {
		flexibility = calendar.CalendarEventsFlexibility(in.Flexibility)
	}
	visibility := calendar.CalendarEventsVisibilityDefault
	if in.Visibility != "" {
		visibility = calendar.CalendarEventsVisibility(in.Visibility)
	}

	ownerUserID := s.userID
	if in.OwnerUserID != "" {
		// Resolve the target user by public id, scoped to workspace
		// membership so a non-member cannot be assigned as owner.
		resolved, uerr := resolveWorkspaceUser(ctx, deps, s, in.OwnerUserID)
		if uerr != nil {
			return nil, uerr
		}
		// Same rule and same refusal REST gives: delegation takes a
		// manager or owner role on the calendar, and filing an event
		// under yourself takes nothing.
		allowed, aerr := canSetCalendarEventOwner(ctx, deps, s, resolved, calID)
		if aerr != nil {
			return nil, aerr
		}
		if !allowed {
			return nil, apierrors.New(apierrors.CalendarEventEditPermissionRequired)
		}
		ownerUserID = resolved
	}

	pub := newPublicID()
	_, err = deps.CalendarQueries.CreateCalendarEvent(ctx, calendar.CreateCalendarEventParams{
		PublicID:           pub,
		WorkspaceID:        s.workspaceID,
		CalendarID:         calID,
		Kind:               kind,
		Visibility:         visibility,
		ShowAs:             showAs,
		Flexibility:        flexibility,
		Title:              in.Title,
		AllDay:             allDay,
		StartAt:            sql.NullTime{Time: startAt, Valid: true},
		EndAt:              sql.NullTime{Time: endAt, Valid: true},
		Timezone:           "UTC",
		Location:           sql.NullString{String: in.Location, Valid: in.Location != ""},
		Memo:               sql.NullString{String: in.Memo, Valid: in.Memo != ""},
		Url:                sql.NullString{},
		OwnerUserID:        ownerUserID,
		CreatedByUserID:    s.userID,
		BlockLabel:         sql.NullString{String: in.BlockLabel, Valid: in.BlockLabel != ""},
		RecurrenceRule:     nil,
		RecurrenceEnd:      sql.NullTime{},
		NotificationOffset: sql.NullInt32{},
		TaskID:             sql.NullInt32{},
	})
	if err != nil {
		return nil, apierrors.Wrap(apierrors.McpToolExecutionFailed, err)
	}
	// The event row is committed; a retry would create a second entry on
	// somebody's calendar.
	recordMutation(ctx, deps, s, mutation{
		EventType:    eventbus.CalEventCreated,
		AuditAction:  "calendar.event.create",
		ResourceType: "calendar.event",
		ResourceID:   pub.String(),
		Payload: map[string]any{
			"eventId":    pub.String(),
			"calendarId": in.CalendarID,
			"title":      in.Title,
			"kind":       string(kind),
			"startAt":    startAt.Unix(),
			"endAt":      endAt.Unix(),
			"via":        "mcp",
		},
		CallSite: "mcp.create_calendar_event",
	})
	out := map[string]any{
		"id":          pub.String(),
		"title":       in.Title,
		"kind":        string(kind),
		"showAs":      string(showAs),
		"flexibility": string(flexibility),
		"allDay":      allDay,
	}
	if allDay {
		out["startDate"] = startAt.UTC().Format("2006-01-02")
		out["endDate"] = endAt.UTC().Format("2006-01-02")
	} else {
		out["startAt"] = startAt.Unix()
		out["endAt"] = endAt.Unix()
	}
	return out, nil
}

func runUpdateCalendarEvent(ctx context.Context, deps Deps, s *session, raw json.RawMessage) (any, error) {
	var in struct {
		EventID     string  `json:"eventId"`
		Title       *string `json:"title"`
		StartAt     *int64  `json:"startAt"`
		EndAt       *int64  `json:"endAt"`
		StartDate   *string `json:"startDate"`
		EndDate     *string `json:"endDate"`
		Kind        *string `json:"kind"`
		ShowAs      *string `json:"showAs"`
		Flexibility *string `json:"flexibility"`
		Visibility  *string `json:"visibility"`
		Location    *string `json:"location"`
		Memo        *string `json:"memo"`
		BlockLabel  *string `json:"blockLabel"`
	}
	if err := parseArgs(raw, &in); err != nil {
		return nil, err
	}
	if in.EventID == "" {
		return nil, apierrors.New(apierrors.McpToolArgumentsInvalid)
	}
	if _, err := requireWorkspaceMember(ctx, deps, s); err != nil {
		return nil, err
	}

	eventPub, err := types.Parse(in.EventID)
	if err != nil {
		return nil, apierrors.Newf(apierrors.McpToolArgumentsInvalid, "invalid eventId")
	}
	owner, err := deps.CalendarQueries.FindCalendarEventOwner(ctx, calendar.FindCalendarEventOwnerParams{
		PublicID:    eventPub,
		WorkspaceID: s.workspaceID,
	})
	if err != nil {
		if stderrors.Is(err, sql.ErrNoRows) {
			return nil, apierrors.Newf(apierrors.McpToolExecutionFailed, "event not found")
		}
		return nil, apierrors.Wrap(apierrors.McpToolExecutionFailed, err)
	}

	// Resolve event internal id for permission check.
	evt, err := deps.CalendarQueries.FindCalendarEventByPublicId(ctx, calendar.FindCalendarEventByPublicIdParams{
		PublicID:    eventPub,
		CalendarID:  owner.CalendarID,
		WorkspaceID: s.workspaceID,
	})
	if err != nil {
		return nil, apierrors.Wrap(apierrors.McpToolExecutionFailed, err)
	}
	if err := requireCalendarWritable(ctx, deps, s, owner.CalendarID); err != nil {
		return nil, err
	}
	ok, err := canEditCalendarEvent(ctx, deps, s, owner.OwnerUserID, evt.ID, owner.CalendarID)
	if err != nil || !ok {
		return nil, apierrors.Newf(apierrors.McpToolExecutionFailed, "permission denied: cannot edit event")
	}

	params := calendar.PatchCalendarEventParams{
		PublicID:    eventPub,
		CalendarID:  owner.CalendarID,
		WorkspaceID: s.workspaceID,
	}

	isLinked := evt.TaskID.Valid
	titleChanged := in.Title != nil && *in.Title != evt.Title

	var newStartAt, newEndAt time.Time
	timeChanged := false
	switch {
	case in.StartAt != nil:
		newStartAt = time.Unix(*in.StartAt, 0).UTC()
		timeChanged = true
	case in.StartDate != nil && *in.StartDate != "":
		t, perr := parseDayBoundary(*in.StartDate)
		if perr != nil {
			return nil, apierrors.Newf(apierrors.McpToolArgumentsInvalid, "invalid startDate")
		}
		newStartAt = t
		timeChanged = true
	}
	switch {
	case in.EndAt != nil:
		newEndAt = time.Unix(*in.EndAt, 0).UTC()
		timeChanged = true
	case in.EndDate != nil && *in.EndDate != "":
		t, perr := parseDayBoundary(*in.EndDate)
		if perr != nil {
			return nil, apierrors.Newf(apierrors.McpToolArgumentsInvalid, "invalid endDate")
		}
		newEndAt = t
		timeChanged = true
	}
	// When only one side of the time window arrived, fall back to the
	// stored value so itemkit / PatchCalendarEvent receive a valid pair.
	if timeChanged {
		if newStartAt.IsZero() && evt.StartAt.Valid {
			newStartAt = evt.StartAt.Time
		}
		if newEndAt.IsZero() && evt.EndAt.Valid {
			newEndAt = evt.EndAt.Time
		}
		// The fall-back above answers "the caller moved one end of a
		// window that already had two". On an undated event there is no
		// other end to borrow, and writing the half pair reached the
		// column as a zero instant the driver rejects. The REST patch
		// refuses that request by name, so this one does too.
		if pairErr := calendarrules.RequireEventStartEndPair(
			calendarrules.UnixSeconds(newStartAt), calendarrules.UnixSeconds(newEndAt)); pairErr != nil {
			return nil, pairErr
		}
		// Both branches below write this pair — the standalone patch and
		// the itemkit reschedule — so the ordering is checked once, here,
		// through the rule the REST patch applies. Without it an inverted
		// window reached chk_calendar_events_chronology and came back as
		// an execution failure that named nothing.
		if rangeErr := calendarrules.RequireEventChronology(
			calendarrules.UnixSeconds(newStartAt), calendarrules.UnixSeconds(newEndAt)); rangeErr != nil {
			return nil, rangeErr
		}
		// The stored row decides whether the pair is a date or an instant:
		// this tool takes no allDay argument, so moving an all-day event by
		// startAt would otherwise store an off-midnight instant and put the
		// event on a different square for readers in another zone.
		normStart, normEnd := calendarrules.NormalizeAllDayBounds(evt.AllDay,
			sql.NullTime{Time: newStartAt, Valid: true}, sql.NullTime{Time: newEndAt, Valid: true})
		newStartAt, newEndAt = normStart.Time, normEnd.Time
	}
	if in.Title != nil {
		params.Title = sql.NullString{String: *in.Title, Valid: true}
	}
	if timeChanged {
		params.StartAt = sql.NullTime{Time: newStartAt, Valid: true}
		params.EndAt = sql.NullTime{Time: newEndAt, Valid: true}
	}
	if in.Kind != nil {
		params.Kind = calendar.NullCalendarEventsKind{
			CalendarEventsKind: calendar.CalendarEventsKind(*in.Kind),
			Valid:              true,
		}
	}
	if in.ShowAs != nil {
		params.ShowAs = calendar.NullCalendarEventsShowAs{
			CalendarEventsShowAs: calendar.CalendarEventsShowAs(*in.ShowAs),
			Valid:                true,
		}
	}
	if in.Flexibility != nil {
		params.Flexibility = calendar.NullCalendarEventsFlexibility{
			CalendarEventsFlexibility: calendar.CalendarEventsFlexibility(*in.Flexibility),
			Valid:                     true,
		}
	}
	if in.Visibility != nil {
		params.Visibility = calendar.NullCalendarEventsVisibility{
			CalendarEventsVisibility: calendar.CalendarEventsVisibility(*in.Visibility),
			Valid:                    true,
		}
	}
	if in.Location != nil {
		params.Location = sql.NullString{String: *in.Location, Valid: true}
	}
	if in.Memo != nil {
		params.Memo = sql.NullString{String: *in.Memo, Valid: true}
	}
	if in.BlockLabel != nil {
		params.BlockLabel = sql.NullString{String: *in.BlockLabel, Valid: true}
	}

	if !isLinked || (!titleChanged && !timeChanged) {
		// Not an existence check: a patch carrying the event's current
		// values reports zero. The event was read above to decide this
		// branch.
		if _, err := deps.CalendarQueries.PatchCalendarEvent(ctx, params); err != nil {
			return nil, apierrors.Wrap(apierrors.McpToolExecutionFailed, err)
		}
		// This branch never reaches itemkit, so nothing else appends the
		// event. That is why an agent editing a standalone event used to
		// leave no trace at all: the linked-event path borrowed itemkit's
		// event row and this one had nobody to borrow from.
		recordMutation(ctx, deps, s, mutation{
			EventType:    eventbus.CalEventUpdated,
			AuditAction:  "calendar.event.update",
			ResourceType: "calendar.event",
			ResourceID:   eventPub.String(),
			Payload: map[string]any{
				"eventId": eventPub.String(),
				"via":     "mcp",
			},
			CallSite: "mcp.update_calendar_event",
		})
		return map[string]any{"success": true}, nil
	}

	// itemkit owns the title and time columns for a linked event, so they
	// drop out of the remaining-fields patch. Decided here rather than
	// inside the transaction because the transaction is retried on a
	// deadlock and would otherwise re-read params its own first attempt
	// had already emptied.
	if titleChanged {
		params.Title = sql.NullString{}
	}
	if timeChanged {
		params.StartAt = sql.NullTime{}
		params.EndAt = sql.NullTime{}
	}
	// Only run the remaining-fields patch if anything is still set. The
	// clearable columns arrive as a bare any rather than a typed Null*,
	// because their SET expression wraps the COALESCE in an IF that
	// reads the matching clear flag; nil is the absent case.
	patchRemaining := params.Title.Valid || params.Kind.Valid || params.ShowAs.Valid ||
		params.Visibility.Valid || params.Location != nil || params.Memo != nil ||
		params.BlockLabel != nil || params.StartAt.Valid || params.EndAt.Valid

	// answered holds a response-shaped error decided inside the
	// transaction, so an itemkit invariant is not reported as a generic
	// tool-execution failure.
	var answered error
	txErr := dbretry.InTx(ctx, deps.DB, "mcp.update_calendar_event", nil, func(ctx context.Context, tx *dbretry.Tx) error {
		answered = nil
		qtxCal := deps.CalendarQueries.WithTx(tx.RawTx())

		if titleChanged {
			if err := itemkit.RenameItem(ctx, tx, itemkit.RenameItemArgs{
				WorkspaceID: s.workspaceID,
				ActorUserID: s.userID,
				EventID:     evt.ID,
				NewTitle:    *in.Title,
			}); err != nil {
				answered = translateItemkitMCPError(err)
				return err
			}
		}
		if timeChanged {
			if err := itemkit.RescheduleEvent(ctx, tx, itemkit.RescheduleEventArgs{
				WorkspaceID: s.workspaceID,
				EventID:     evt.ID,
				ActorUserID: s.userID,
				StartAt:     newStartAt,
				EndAt:       newEndAt,
			}); err != nil {
				answered = translateItemkitMCPError(err)
				return err
			}
		}
		if patchRemaining {
			if _, err := qtxCal.PatchCalendarEvent(ctx, params); err != nil {
				return err
			}
		}
		return nil
	})
	if answered != nil {
		return nil, answered
	}
	if txErr != nil {
		return nil, apierrors.Wrap(apierrors.McpToolExecutionFailed, txErr)
	}
	// itemkit appended calendar.event.updated inside the transaction that
	// just committed, so only the audit half is left to add here.
	recordTxMutationAudit(ctx, deps, s, mutation{
		AuditAction:  "calendar.event.update",
		ResourceType: "calendar.event",
		ResourceID:   eventPub.String(),
		Payload: map[string]any{
			"eventId": eventPub.String(),
			"via":     "mcp",
		},
		CallSite: "mcp.update_calendar_event",
	})
	return map[string]any{"success": true}, nil
}

func runDeleteCalendarEvent(ctx context.Context, deps Deps, s *session, raw json.RawMessage) (any, error) {
	var in struct {
		EventID string `json:"eventId"`
	}
	if err := parseArgs(raw, &in); err != nil {
		return nil, err
	}
	if in.EventID == "" {
		return nil, apierrors.New(apierrors.McpToolArgumentsInvalid)
	}
	if _, err := requireWorkspaceMember(ctx, deps, s); err != nil {
		return nil, err
	}

	eventPub, err := types.Parse(in.EventID)
	if err != nil {
		return nil, apierrors.Newf(apierrors.McpToolArgumentsInvalid, "invalid eventId")
	}
	owner, err := deps.CalendarQueries.FindCalendarEventOwner(ctx, calendar.FindCalendarEventOwnerParams{
		PublicID:    eventPub,
		WorkspaceID: s.workspaceID,
	})
	if err != nil {
		if stderrors.Is(err, sql.ErrNoRows) {
			return nil, apierrors.Newf(apierrors.McpToolExecutionFailed, "event not found")
		}
		return nil, apierrors.Wrap(apierrors.McpToolExecutionFailed, err)
	}

	evt, err := deps.CalendarQueries.FindCalendarEventByPublicId(ctx, calendar.FindCalendarEventByPublicIdParams{
		PublicID:    eventPub,
		CalendarID:  owner.CalendarID,
		WorkspaceID: s.workspaceID,
	})
	if err != nil {
		return nil, apierrors.Wrap(apierrors.McpToolExecutionFailed, err)
	}
	if err := requireCalendarWritable(ctx, deps, s, owner.CalendarID); err != nil {
		return nil, err
	}
	ok, err := canEditCalendarEvent(ctx, deps, s, owner.OwnerUserID, evt.ID, owner.CalendarID)
	if err != nil || !ok {
		return nil, apierrors.Newf(apierrors.McpToolExecutionFailed, "permission denied: cannot delete event")
	}

	var answered error
	txErr := dbretry.InTx(ctx, deps.DB, "mcp.delete_calendar_event", nil, func(ctx context.Context, tx *dbretry.Tx) error {
		answered = nil
		if err := itemkit.DeleteEvent(ctx, tx, s.workspaceID, evt.ID, s.userID); err != nil {
			answered = translateItemkitMCPError(err)
			return err
		}
		return nil
	})
	if answered != nil {
		return nil, answered
	}
	if txErr != nil {
		return nil, apierrors.Wrap(apierrors.McpToolExecutionFailed, txErr)
	}
	// itemkit appended item.unscheduled and the legacy
	// calendar.event.deleted inside the committed transaction; the audit
	// row is the remaining half.
	recordTxMutationAudit(ctx, deps, s, mutation{
		AuditAction:  "calendar.event.delete",
		ResourceType: "calendar.event",
		ResourceID:   eventPub.String(),
		Payload: map[string]any{
			"eventId": eventPub.String(),
			"via":     "mcp",
		},
		CallSite: "mcp.delete_calendar_event",
	})
	return map[string]any{"success": true}, nil
}

// workDayStartHour and workDayEndHour bound the window list_free_slots
// searches. They are a stand-in for a real per-user working-hours
// setting, which does not exist yet; naming them makes the assumption
// visible rather than leaving two literals in the middle of a function.
const (
	workDayStartHour = 9
	workDayEndHour   = 18
)

// resolveUserTimezone returns the zone to interpret a user's day in:
// their own preference, else the workspace default, else UTC.
//
// Same chain as the REST handlers' resolveEffectiveTimezone, minus the
// explicit-request tier, because no MCP tool takes a timezone argument.
// Lookup failures fall through rather than erroring: a missing profile
// row should degrade to the workspace's timezone, not fail the tool. A
// stored name the zoneinfo database cannot resolve falls through for the
// same reason, so the tiers below it still apply.
func resolveUserTimezone(ctx context.Context, deps Deps, workspaceID, userID uint32) region.Zone {
	var userTz string
	if profile, err := deps.Queries.FindUserProfileById(ctx, userID); err == nil {
		userTz = profile.Timezone
	}
	var wsTz string
	if row, err := deps.Queries.FindWorkspaceTimezoneCountryById(ctx, workspaceID); err == nil {
		wsTz = row.Timezone
	}
	if region.ValidateTimezone(userTz) != nil {
		userTz = ""
	}
	if region.ValidateTimezone(wsTz) != nil {
		wsTz = ""
	}
	// The chain ends at region.DefaultTimezone, which always resolves,
	// so the surviving candidates cannot fail.
	z, err := region.Resolve(userTz, wsTz)
	if err != nil {
		return region.UTC()
	}
	return z
}

func runListFreeSlots(ctx context.Context, deps Deps, s *session, raw json.RawMessage) (any, error) {
	var in struct {
		UserID          string `json:"userId"`
		Date            string `json:"date"`
		DurationMinutes int    `json:"durationMinutes"`
	}
	if err := parseArgs(raw, &in); err != nil {
		return nil, err
	}
	if in.Date == "" {
		return nil, apierrors.New(apierrors.McpToolArgumentsInvalid)
	}
	if _, err := requireWorkspaceMember(ctx, deps, s); err != nil {
		return nil, err
	}

	targetUserID := s.userID
	if in.UserID != "" {
		// Scope the lookup to workspace membership so the free/busy
		// probe cannot act as an existence oracle for users outside
		// the workspace.
		resolved, uerr := resolveWorkspaceUser(ctx, deps, s, in.UserID)
		if uerr != nil {
			return nil, uerr
		}
		targetUserID = resolved
	}

	day, err := region.ParseDay(in.Date)
	if err != nil {
		return nil, apierrors.Newf(apierrors.McpToolArgumentsInvalid, "invalid date: %v", err)
	}
	if in.DurationMinutes <= 0 {
		in.DurationMinutes = 60
	}

	// The working day belongs to the person whose day it is, so the
	// window is built in their timezone rather than in UTC. Fixed at UTC
	// it named 18:00–03:00 for a Tokyo user: the real working day fell
	// outside the query window, so every meeting in it was invisible,
	// the day was reported wholly free, and the agent booked the middle
	// of the night.
	zone := resolveUserTimezone(ctx, deps, s.workspaceID, targetUserID)
	workStart := day.At(zone, workDayStartHour, 0, 0)
	workEnd := day.At(zone, workDayEndHour, 0, 0)

	// The viewer here is the target, not the caller. This tool returns
	// free windows and never any event data, so the question it has to
	// answer is which of the target's own commitments occupy the day —
	// filtering by the caller's rights would drop the target's
	// confidential events and report an occupied slot as free. What the
	// caller learns is availability, which is what show_as governs: an
	// owner who does not want a block to read as taken marks it free.
	occurrences, err := listCalendarOccurrences(ctx, deps, targetUserID, targetUserID, s.workspaceID, workStart, workEnd)
	if err != nil {
		return nil, err
	}

	// Collect busy intervals, clamped to working hours.
	type interval struct{ start, end time.Time }
	busy := make([]interval, 0, len(occurrences))
	for _, occ := range occurrences {
		r := occ.row
		if r.AllDay {
			continue
		}
		if string(r.ShowAs) == "free" {
			continue
		}
		// Undated events (planning-stage placeholders) occupy no time,
		// so they contribute nothing to the busy map.
		if !occ.dated {
			continue
		}
		s := occ.startAt
		e := occ.endAt
		if s.Before(workStart) {
			s = workStart
		}
		if e.After(workEnd) {
			e = workEnd
		}
		if s.Before(e) {
			busy = append(busy, interval{s, e})
		}
	}
	sort.Slice(busy, func(i, j int) bool { return busy[i].start.Before(busy[j].start) })

	// Find gaps.
	minDur := time.Duration(in.DurationMinutes) * time.Minute
	slots := []map[string]any{}
	cursor := workStart
	for _, b := range busy {
		if b.start.After(cursor) {
			gap := b.start.Sub(cursor)
			if gap >= minDur {
				slots = append(slots, map[string]any{
					"startAt":         cursor.Unix(),
					"endAt":           b.start.Unix(),
					"durationMinutes": int(gap.Minutes()),
				})
			}
		}
		if b.end.After(cursor) {
			cursor = b.end
		}
	}
	if cursor.Before(workEnd) {
		gap := workEnd.Sub(cursor)
		if gap >= minDur {
			slots = append(slots, map[string]any{
				"startAt":         cursor.Unix(),
				"endAt":           workEnd.Unix(),
				"durationMinutes": int(gap.Minutes()),
			})
		}
	}
	return map[string]any{"slots": slots}, nil
}

// runCreateEventFromTask projects a task onto a calendar as a timed
// event.
//
// calendar-precondition: all-day-bounds not-applicable — the row is
// written with all_day false and the tool takes no date arguments, so
// there is no calendar square to pin to UTC midnight
func runCreateEventFromTask(ctx context.Context, deps Deps, s *session, raw json.RawMessage) (any, error) {
	var in struct {
		TaskID     string `json:"taskId"`
		CalendarID string `json:"calendarId"`
		StartAt    *int64 `json:"startAt"`
		EndAt      *int64 `json:"endAt"`
	}
	if err := parseArgs(raw, &in); err != nil {
		return nil, err
	}
	if in.TaskID == "" || in.CalendarID == "" || in.StartAt == nil || in.EndAt == nil {
		return nil, apierrors.New(apierrors.McpToolArgumentsInvalid)
	}
	if _, err := requireWorkspaceMember(ctx, deps, s); err != nil {
		return nil, err
	}

	// The created event carries calendar_events.task_id, i.e. it links itself
	// to the task. REST reaches the same end state with two calls, the second
	// of which (tasks-event-links-create) is project-editor gated, so the
	// editor floor applies here too on top of the calendar_members grant.
	taskInternal, task, err := resolveTaskRowForWrite(ctx, deps, s, in.TaskID)
	if err != nil {
		return nil, err
	}
	calID, err := resolveCalendarWrite(ctx, deps, s, in.CalendarID)
	if err != nil {
		return nil, err
	}
	startAt := time.Unix(*in.StartAt, 0).UTC()
	endAt := time.Unix(*in.EndAt, 0).UTC()
	// This tool takes a window the caller chose, which the REST route it
	// answers for does not — that one derives the window from the task's
	// due date. Taking the window means owing the same refusal the event
	// routes give when it is inverted.
	if rangeErr := calendarrules.RequireEventChronology(
		calendarrules.UnixSeconds(startAt), calendarrules.UnixSeconds(endAt)); rangeErr != nil {
		return nil, rangeErr
	}

	pub := newPublicID()
	_, err = deps.CalendarQueries.CreateCalendarEvent(ctx, calendar.CreateCalendarEventParams{
		PublicID:           pub,
		WorkspaceID:        s.workspaceID,
		CalendarID:         calID,
		Kind:               calendar.CalendarEventsKindEvent,
		Visibility:         calendar.CalendarEventsVisibilityDefault,
		ShowAs:             calendar.CalendarEventsShowAsBusy,
		Flexibility:        calendar.CalendarEventsFlexibilityFixed,
		Title:              task.Title,
		AllDay:             false,
		StartAt:            sql.NullTime{Time: startAt, Valid: true},
		EndAt:              sql.NullTime{Time: endAt, Valid: true},
		Timezone:           "UTC",
		Location:           sql.NullString{},
		Memo:               sql.NullString{},
		Url:                sql.NullString{},
		OwnerUserID:        s.userID,
		CreatedByUserID:    s.userID,
		BlockLabel:         sql.NullString{},
		RecurrenceRule:     nil,
		RecurrenceEnd:      sql.NullTime{},
		NotificationOffset: sql.NullInt32{},
		TaskID:             sql.NullInt32{Int32: int32(taskInternal), Valid: true}, //#nosec G115 -- task id is tasks.id (BIGINT UNSIGNED), fits int32 within realistic deployments
	})
	if err != nil {
		return nil, apierrors.Wrap(apierrors.McpToolExecutionFailed, err)
	}
	taskID64 := int64(taskInternal)
	recordMutation(ctx, deps, s, mutation{
		EventType:    eventbus.CalEventCreated,
		AuditAction:  "calendar.event.create",
		ResourceType: "calendar.event",
		ResourceID:   pub.String(),
		TaskID:       &taskID64,
		Payload: map[string]any{
			"eventId":    pub.String(),
			"calendarId": in.CalendarID,
			"taskId":     task.PublicID.String(),
			"title":      task.Title,
			"startAt":    startAt.Unix(),
			"endAt":      endAt.Unix(),
			"via":        "mcp:create_event_from_task",
		},
		CallSite: "mcp.create_event_from_task",
	})
	return map[string]any{
		"id":      pub.String(),
		"title":   task.Title,
		"startAt": startAt.Unix(),
		"endAt":   endAt.Unix(),
		"taskId":  task.PublicID.String(),
	}, nil
}

func runListCalendarMemos(ctx context.Context, deps Deps, s *session, raw json.RawMessage) (any, error) {
	var in struct {
		CalendarID string `json:"calendarId"`
	}
	if err := parseArgs(raw, &in); err != nil {
		return nil, err
	}
	if in.CalendarID == "" {
		return nil, apierrors.New(apierrors.McpToolArgumentsInvalid)
	}
	if _, err := requireWorkspaceMember(ctx, deps, s); err != nil {
		return nil, err
	}
	calID, err := resolveCalendar(ctx, deps, s, in.CalendarID)
	if err != nil {
		return nil, err
	}
	rows, err := deps.CalendarQueries.ListCalendarMemos(ctx, calendar.ListCalendarMemosParams{
		CalendarID:  calID,
		WorkspaceID: s.workspaceID,
	})
	if err != nil {
		return nil, apierrors.Wrap(apierrors.McpToolExecutionFailed, err)
	}
	items := make([]map[string]any, 0, len(rows))
	for _, r := range rows {
		items = append(items, map[string]any{
			"id":        r.PublicID.String(),
			"title":     r.Title,
			"done":      r.Done,
			"createdBy": r.DisplayName,
		})
	}
	return map[string]any{"memos": items}, nil
}

func runToggleCalendarMemo(ctx context.Context, deps Deps, s *session, raw json.RawMessage) (any, error) {
	var in struct {
		MemoID     string `json:"memoId"`
		CalendarID string `json:"calendarId"`
		Done       *bool  `json:"done"`
	}
	if err := parseArgs(raw, &in); err != nil {
		return nil, err
	}
	if in.MemoID == "" || in.CalendarID == "" || in.Done == nil {
		return nil, apierrors.New(apierrors.McpToolArgumentsInvalid)
	}
	if _, err := requireWorkspaceMember(ctx, deps, s); err != nil {
		return nil, err
	}
	calID, err := resolveCalendarWrite(ctx, deps, s, in.CalendarID)
	if err != nil {
		return nil, err
	}
	memoPub, err := types.Parse(in.MemoID)
	if err != nil {
		return nil, apierrors.Newf(apierrors.McpToolArgumentsInvalid, "invalid memoId")
	}
	// Not an existence check: toggling a memo to the done state it
	// already holds changes nothing and counts zero.
	if _, err := deps.CalendarQueries.UpdateCalendarMemo(ctx, calendar.UpdateCalendarMemoParams{
		Done:        sql.NullBool{Bool: *in.Done, Valid: true},
		PublicID:    memoPub,
		CalendarID:  calID,
		WorkspaceID: s.workspaceID,
	}); err != nil {
		return nil, apierrors.Wrap(apierrors.McpToolExecutionFailed, err)
	}
	// Same kind and action the REST memo patch uses, so a shared memo
	// ticked off by an agent reads the same on the timeline as one ticked
	// off in the web app.
	if err := recordMutationStrict(ctx, deps, s, mutation{
		EventType:    eventbus.CalMemoUpdated,
		AuditAction:  "calendar.memo.update",
		ResourceType: "calendar.memo",
		ResourceID:   in.MemoID,
		Payload: map[string]any{
			"memoId":     in.MemoID,
			"calendarId": in.CalendarID,
			"done":       *in.Done,
			"via":        "mcp",
		},
		CallSite: "mcp.toggle_calendar_memo",
	}); err != nil {
		return nil, apierrors.Wrap(apierrors.McpToolExecutionFailed, err)
	}
	return map[string]any{"success": true}, nil
}
