// Package mcp tool registry and tool implementations.
package mcp

import (
	"context"
	"database/sql"
	"encoding/json"
	stderrors "errors"
	"time"

	"sort"
	"strconv"
	"strings"

	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/ai"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/ai/embed"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/ai/nlquery"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/generated/calendar"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/types"
	apierrors "github.com/nodate-flow/nodate-flow/apps/flow-api/internal/errors"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/eventbus"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/itemkit"
)

// countLinkedEvents returns how many enabled calendar_events are
// attached to a task. Used to decide when an MCP mutation must route
// through itemkit to keep the task / event pair consistent.
func countLinkedEvents(ctx context.Context, deps Deps, taskID uint32) (int, error) {
	var n int
	if err := deps.DB.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM calendar_events WHERE task_id = ? AND enabled = TRUE`,
		taskID,
	).Scan(&n); err != nil {
		return 0, err
	}
	return n, nil
}

// tool is the internal descriptor for a registered MCP tool.
type tool struct {
	name          string
	description   string
	requiredScope string
	inputSchema   map[string]any
	run           func(ctx context.Context, deps Deps, s *session, args json.RawMessage) (any, error)
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

func (h *Handler) register(t tool) { h.tools[t.name] = t }

func registerTools(h *Handler) {
	h.register(tool{
		name:          "list_projects",
		description:   "List projects in the caller's workspace.",
		requiredScope: "read:workspace",
		inputSchema:   objectSchema(nil, nil),
		run:           runListProjects,
	})
	h.register(tool{
		name:          "list_tasks",
		description:   "List tasks, optionally scoped to a project.",
		requiredScope: "read:workspace",
		inputSchema: objectSchema(map[string]any{
			"projectId": stringSchema("Project public id (UUID v7). Optional."),
			"limit":     intSchema("Max number of rows (1..200)."),
			"offset":    intSchema("Row offset."),
		}, nil),
		run: runListTasks,
	})
	h.register(tool{
		name:          "get_task",
		description:   "Fetch a single task by public id.",
		requiredScope: "read:workspace",
		inputSchema: objectSchema(map[string]any{
			"taskId": stringSchema("Task public id (UUID v7)."),
		}, []string{"taskId"}),
		run: runGetTask,
	})
	h.register(tool{
		name:          "create_task",
		description:   "Create a new task in a project.",
		requiredScope: "write:workspace",
		inputSchema: objectSchema(map[string]any{
			"projectId":   stringSchema("Project public id (UUID v7)."),
			"title":       stringSchema("Task title."),
			"description": stringSchema("Optional description."),
			"priority":    intSchema("Priority 0..4."),
			"dueOn":       stringSchema("YYYY-MM-DD."),
			"startOn":     stringSchema("YYYY-MM-DD."),
		}, []string{"projectId", "title"}),
		run: runCreateTask,
	})
	h.register(tool{
		name:          "update_task",
		description:   "Update mutable fields of a task.",
		requiredScope: "write:workspace",
		inputSchema: objectSchema(map[string]any{
			"taskId":      stringSchema("Task public id (UUID v7)."),
			"title":       stringSchema("New title."),
			"description": stringSchema("New description."),
			"priority":    intSchema("New priority 0..4."),
			"dueOn":       stringSchema("YYYY-MM-DD."),
			"startOn":     stringSchema("YYYY-MM-DD."),
		}, []string{"taskId"}),
		run: runUpdateTask,
	})
	h.register(tool{
		name:          "transition_task",
		description:   "Apply a state machine transition to a task. Valid transitions: start, block, unblock, submit, complete, reopen, cancel.",
		requiredScope: "write:workspace",
		inputSchema: objectSchema(map[string]any{
			"taskId":     stringSchema("Task public id (UUID v7)."),
			"transition": stringSchema("Transition name: start | block | unblock | submit | complete | reopen | cancel."),
			"reason":     stringSchema("Optional reason for the transition."),
		}, []string{"taskId", "transition"}),
		run: runTransitionTask,
	})
	h.register(tool{
		name:          "add_comment",
		description:   "Append a comment to a task.",
		requiredScope: "write:workspace",
		inputSchema: objectSchema(map[string]any{
			"taskId": stringSchema("Task public id (UUID v7)."),
			"body":   stringSchema("Comment body."),
		}, []string{"taskId", "body"}),
		run: runAddComment,
	})
	h.register(tool{
		name:          "search_tasks",
		description:   "Search tasks by title or description within the workspace.",
		requiredScope: "read:workspace",
		inputSchema: objectSchema(map[string]any{
			"query":  stringSchema("Search term (matched against title and description)."),
			"limit":  intSchema("Max results (1-200, default 50)."),
			"offset": intSchema("Pagination offset (default 0)."),
		}, []string{"query"}),
		run: runSearchTasks,
	})
	h.register(tool{
		name:          "propose_tasks_from",
		description:   "Ask the workspace LLM to propose tasks from free text. Requires a configured AI provider.",
		requiredScope: "write:workspace",
		inputSchema: objectSchema(map[string]any{
			"source": stringSchema("Input text to propose tasks from."),
		}, []string{"source"}),
		run: runProposeTasksFrom,
	})
	h.register(tool{
		name:          "propose_priority",
		description:   "Ask the workspace LLM to propose a priority for a task. Requires a configured AI provider.",
		requiredScope: "write:workspace",
		inputSchema: objectSchema(map[string]any{
			"taskId": stringSchema("Task public id (UUID v7)."),
		}, []string{"taskId"}),
		run: runProposePriority,
	})
	h.register(tool{
		name:          "propose_steps",
		description:   "Ask the workspace LLM to break an existing task into concrete execution steps.",
		requiredScope: "read:workspace",
		inputSchema: objectSchema(map[string]any{
			"taskId": stringSchema("Task public id (UUID v7)."),
		}, []string{"taskId"}),
		run: runProposeSteps,
	})
	h.register(tool{
		name:          "apply_steps",
		description:   "Create the given steps as child tasks under an existing parent task.",
		requiredScope: "write:workspace",
		inputSchema: objectSchema(map[string]any{
			"parentTaskId": stringSchema("Parent task public id (UUID v7)."),
			"steps": map[string]any{
				"type":        "array",
				"description": "Step definitions to create as child tasks.",
				"items": objectSchema(map[string]any{
					"title":       stringSchema("Step title."),
					"description": stringSchema("Step description (optional)."),
					"priority":    intSchema("Step priority 0..4 (optional)."),
				}, []string{"title"}),
			},
		}, []string{"parentTaskId", "steps"}),
		run: runApplySteps,
	})
	h.register(tool{
		name:          "propose_duplicates",
		description:   "Return likely-duplicate tasks for a given task by embedding similarity (ADR 0003).",
		requiredScope: "read:workspace",
		inputSchema: objectSchema(map[string]any{
			"taskId": stringSchema("Task public id (UUID v7)."),
		}, []string{"taskId"}),
		run: runProposeDuplicates,
	})
	h.register(tool{
		name:          "propose_lens",
		description:   "Compile a natural-language query into a validated Lens view JSON.",
		requiredScope: "read:workspace",
		inputSchema: objectSchema(map[string]any{
			"prompt": stringSchema("Natural-language description of the desired view."),
		}, []string{"prompt"}),
		run: runProposeLens,
	})
	h.register(tool{
		name:          "list_timeboxes",
		description:   "List timeboxes (sprints / iterations) in the caller's workspace.",
		requiredScope: "read:workspace",
		inputSchema: objectSchema(map[string]any{
			"limit":  intSchema("Max number of rows (1..200)."),
			"offset": intSchema("Row offset."),
		}, nil),
		run: runListTimeboxes,
	})
	h.register(tool{
		name:          "create_timebox",
		description:   "Create a new timebox (sprint / iteration) in the workspace.",
		requiredScope: "write:workspace",
		inputSchema: objectSchema(map[string]any{
			"name":        stringSchema("Timebox name."),
			"startsOn":    stringSchema("Start date YYYY-MM-DD."),
			"endsOn":      stringSchema("End date YYYY-MM-DD."),
			"description": stringSchema("Optional description."),
			"projectId":   stringSchema("Optional project public id (UUID v7) to scope the timebox."),
		}, []string{"name", "startsOn", "endsOn"}),
		run: runCreateTimebox,
	})
	h.register(tool{
		name:          "add_task_to_timebox",
		description:   "Add a task to a timebox.",
		requiredScope: "write:workspace",
		inputSchema: objectSchema(map[string]any{
			"timeboxId": stringSchema("Timebox public id (UUID v7)."),
			"taskId":    stringSchema("Task public id (UUID v7)."),
		}, []string{"timeboxId", "taskId"}),
		run: runAddTaskToTimebox,
	})
	h.register(tool{
		name:          "export_tasks",
		description:   "Export tasks as JSON for MCP consumers. Optionally scoped to a project.",
		requiredScope: "read:workspace",
		inputSchema: objectSchema(map[string]any{
			"projectId": stringSchema("Optional project public id (UUID v7) to scope export."),
			"limit":     intSchema("Max tasks to export (1..200, default 200)."),
		}, nil),
		run: runExportTasks,
	})
	h.register(tool{
		name:          "propose_relations",
		description:   "Given a task, find related or duplicate tasks by embedding similarity. Returns structured suggestions with kind.",
		requiredScope: "read:workspace",
		inputSchema: objectSchema(map[string]any{
			"taskId": stringSchema("Task public id (UUID v7)."),
		}, []string{"taskId"}),
		run: runProposeRelations,
	})
	h.register(tool{
		name:          "list_pages",
		description:   "List wiki pages for the current workspace. Returns root-level pages by default.",
		requiredScope: "read:workspace",
		inputSchema: objectSchema(map[string]any{
			"projectId":    stringSchema("Optional project public id (UUID v7) to scope pages."),
			"parentPageId": stringSchema("Optional parent page public id (UUID v7) to list child pages."),
		}, nil),
		run: runListPages,
	})
	h.register(tool{
		name:          "get_page",
		description:   "Get a wiki page by ID, including its content.",
		requiredScope: "read:workspace",
		inputSchema: objectSchema(map[string]any{
			"pageId": stringSchema("Page public id (UUID v7)."),
		}, []string{"pageId"}),
		run: runGetPage,
	})
	h.register(tool{
		name:          "create_page",
		description:   "Create a new wiki page.",
		requiredScope: "write:workspace",
		inputSchema: objectSchema(map[string]any{
			"title":        stringSchema("Page title."),
			"body":         stringSchema("Optional page body content."),
			"parentPageId": stringSchema("Optional parent page public id (UUID v7)."),
			"projectId":    stringSchema("Optional project public id (UUID v7)."),
		}, []string{"title"}),
		run: runCreatePage,
	})
	h.register(tool{
		name:          "update_page",
		description:   "Update a wiki page's title or content.",
		requiredScope: "write:workspace",
		inputSchema: objectSchema(map[string]any{
			"pageId": stringSchema("Page public id (UUID v7)."),
			"title":  stringSchema("New title (optional)."),
			"body":   stringSchema("New body content (optional)."),
		}, []string{"pageId"}),
		run: runUpdatePage,
	})
	h.register(tool{
		name:          "generate_page",
		description:   "Generate a wiki page using AI based on project or task context.",
		requiredScope: "write:workspace",
		inputSchema: objectSchema(map[string]any{
			"contextDescription": stringSchema("What the page should be about."),
			"projectId":          stringSchema("Optional project public id (UUID v7)."),
			"taskIds": map[string]any{
				"type":        "array",
				"description": "Optional task public ids (UUID v7) to include as context.",
				"items":       stringSchema("Task public id (UUID v7)."),
			},
		}, []string{"contextDescription"}),
		run: runGeneratePage,
	})
	h.register(tool{
		name:          "smart_create_task",
		description:   "Create a task with AI-suggested subtask breakdown and assignees based on past ticket patterns.",
		requiredScope: "write:workspace",
		inputSchema: objectSchema(map[string]any{
			"projectId":   stringSchema("Project public id (UUID v7)."),
			"title":       stringSchema("Task title."),
			"description": stringSchema("Optional task description for better AI analysis."),
		}, []string{"projectId", "title"}),
		run: runSmartCreateTask,
	})

	// Calendar tools.
	h.register(tool{
		name:          "list_calendars",
		description:   "List calendars the caller subscribes to in the workspace.",
		requiredScope: "read:workspace",
		inputSchema:   objectSchema(nil, nil),
		run:           runListCalendars,
	})
	h.register(tool{
		name:          "list_calendar_events",
		description:   "List events in a date range across all subscribed calendars.",
		requiredScope: "read:workspace",
		inputSchema: objectSchema(map[string]any{
			"startDate": stringSchema("Range start (RFC 3339 datetime or YYYY-MM-DD)."),
			"endDate":   stringSchema("Range end (RFC 3339 datetime or YYYY-MM-DD)."),
		}, []string{"startDate", "endDate"}),
		run: runListCalendarEvents,
	})
	h.register(tool{
		name:          "create_calendar_event",
		description:   "Create a calendar event.",
		requiredScope: "write:workspace",
		inputSchema: objectSchema(map[string]any{
			"calendarId":  stringSchema("Calendar public id (UUID v7)."),
			"title":       stringSchema("Event title."),
			"startAt":     stringSchema("Start datetime (RFC 3339)."),
			"endAt":       stringSchema("End datetime (RFC 3339)."),
			"kind":        stringSchema("Event kind: event | block | free (default event)."),
			"showAs":      stringSchema("Show as: busy | free | tentative | oof (default busy)."),
			"visibility":  stringSchema("Visibility: default | public | private | confidential."),
			"ownerUserId": stringSchema("Owner user public id (UUID v7). Defaults to caller."),
			"allDay":      boolSchema("Whether this is an all-day event."),
			"location":    stringSchema("Event location."),
			"memo":        stringSchema("Event memo/notes."),
			"blockLabel":  stringSchema("Label for block-type events."),
		}, []string{"calendarId", "title", "startAt", "endAt"}),
		run: runCreateCalendarEvent,
	})
	h.register(tool{
		name:          "update_calendar_event",
		description:   "Update mutable fields of a calendar event.",
		requiredScope: "write:workspace",
		inputSchema: objectSchema(map[string]any{
			"eventId":    stringSchema("Event public id (UUID v7)."),
			"title":      stringSchema("New title."),
			"startAt":    stringSchema("New start datetime (RFC 3339)."),
			"endAt":      stringSchema("New end datetime (RFC 3339)."),
			"kind":       stringSchema("New kind: event | block | free."),
			"showAs":     stringSchema("New show as: busy | free | tentative | oof."),
			"visibility": stringSchema("New visibility: default | public | private | confidential."),
			"location":   stringSchema("New location."),
			"memo":       stringSchema("New memo/notes."),
			"blockLabel": stringSchema("New block label."),
		}, []string{"eventId"}),
		run: runUpdateCalendarEvent,
	})
	h.register(tool{
		name:          "delete_calendar_event",
		description:   "Delete a calendar event (soft-delete).",
		requiredScope: "write:workspace",
		inputSchema: objectSchema(map[string]any{
			"eventId": stringSchema("Event public id (UUID v7)."),
		}, []string{"eventId"}),
		run: runDeleteCalendarEvent,
	})
	h.register(tool{
		name:          "list_free_slots",
		description:   "Find available time slots for a user on a given date within working hours (09:00-18:00).",
		requiredScope: "read:workspace",
		inputSchema: objectSchema(map[string]any{
			"userId":          stringSchema("User public id (UUID v7). Defaults to caller."),
			"date":            stringSchema("Date to check (YYYY-MM-DD)."),
			"durationMinutes": intSchema("Minimum slot duration in minutes (default 60)."),
		}, []string{"date"}),
		run: runListFreeSlots,
	})
	h.register(tool{
		name:          "create_event_from_task",
		description:   "Create a calendar event linked to an existing task.",
		requiredScope: "write:workspace",
		inputSchema: objectSchema(map[string]any{
			"taskId":     stringSchema("Task public id (UUID v7)."),
			"calendarId": stringSchema("Calendar public id (UUID v7)."),
			"startAt":    stringSchema("Start datetime (RFC 3339)."),
			"endAt":      stringSchema("End datetime (RFC 3339)."),
		}, []string{"taskId", "calendarId", "startAt", "endAt"}),
		run: runCreateEventFromTask,
	})
	h.register(tool{
		name:          "list_calendar_memos",
		description:   "List memos (shared to-do items) in a calendar.",
		requiredScope: "read:workspace",
		inputSchema: objectSchema(map[string]any{
			"calendarId": stringSchema("Calendar public id (UUID v7)."),
		}, []string{"calendarId"}),
		run: runListCalendarMemos,
	})
	h.register(tool{
		name:          "toggle_calendar_memo",
		description:   "Toggle the done/undone state of a calendar memo.",
		requiredScope: "write:workspace",
		inputSchema: objectSchema(map[string]any{
			"memoId":     stringSchema("Memo public id (UUID v7)."),
			"calendarId": stringSchema("Calendar public id (UUID v7)."),
			"done":       boolSchema("Set to true to mark done, false to unmark."),
		}, []string{"memoId", "calendarId", "done"}),
		run: runToggleCalendarMemo,
	})
	h.register(tool{
		name:          "smart_create_event",
		description:   "Parse natural language into a calendar event (AI-powered, coming soon).",
		requiredScope: "write:workspace",
		inputSchema: objectSchema(map[string]any{
			"text":       stringSchema("Natural language description of the event."),
			"calendarId": stringSchema("Calendar public id (UUID v7)."),
		}, []string{"text", "calendarId"}),
		run: runSmartCreateEvent,
	})

	// Label & archive tools.
	h.register(tool{
		name:          "list_labels",
		description:   "List labels in the caller's workspace.",
		requiredScope: "read:workspace",
		inputSchema: objectSchema(map[string]any{
			"limit":  intSchema("Max number of rows (1..200)."),
			"offset": intSchema("Row offset."),
		}, nil),
		run: runListLabels,
	})
	h.register(tool{
		name:          "create_label",
		description:   "Create a new label in the workspace.",
		requiredScope: "write:workspace",
		inputSchema: objectSchema(map[string]any{
			"name":        stringSchema("Label name."),
			"color":       stringSchema("Hex color (e.g. #ef4444). Optional."),
			"description": stringSchema("Optional description."),
		}, []string{"name"}),
		run: runCreateLabel,
	})
	h.register(tool{
		name:          "add_task_label",
		description:   "Attach a label to a task.",
		requiredScope: "write:workspace",
		inputSchema: objectSchema(map[string]any{
			"taskId":  stringSchema("Task public id (UUID v7)."),
			"labelId": stringSchema("Label public id (UUID v7)."),
		}, []string{"taskId", "labelId"}),
		run: runAddTaskLabel,
	})
	h.register(tool{
		name:          "remove_task_label",
		description:   "Remove a label from a task.",
		requiredScope: "write:workspace",
		inputSchema: objectSchema(map[string]any{
			"taskId":  stringSchema("Task public id (UUID v7)."),
			"labelId": stringSchema("Label public id (UUID v7)."),
		}, []string{"taskId", "labelId"}),
		run: runRemoveTaskLabel,
	})
	h.register(tool{
		name:          "resolve_task_ref",
		description:   "Resolve a human-readable task reference (e.g. NF-42) to a task public id.",
		requiredScope: "read:workspace",
		inputSchema: objectSchema(map[string]any{
			"ref": stringSchema("Task reference in PROJECT_IDENTIFIER-NUMBER format (e.g. NF-42)."),
		}, []string{"ref"}),
		run: runResolveTaskRef,
	})
	h.register(tool{
		name:          "archive_task",
		description:   "Archive a task.",
		requiredScope: "write:workspace",
		inputSchema: objectSchema(map[string]any{
			"taskId": stringSchema("Task public id (UUID v7)."),
		}, []string{"taskId"}),
		run: runArchiveTask,
	})
	h.register(tool{
		name:          "unarchive_task",
		description:   "Unarchive a task.",
		requiredScope: "write:workspace",
		inputSchema: objectSchema(map[string]any{
			"taskId": stringSchema("Task public id (UUID v7)."),
		}, []string{"taskId"}),
		run: runUnarchiveTask,
	})

	// Favorites, reactions, and recent visits (Wave 2).
	h.register(tool{
		name:          "list_favorites",
		description:   "List the current user's favorite items in the workspace.",
		requiredScope: "read:workspace",
		inputSchema: objectSchema(map[string]any{
			"limit":  intSchema("Max results (1..50, default 50)."),
			"offset": intSchema("Skip N results."),
		}, nil),
		run: runListFavorites,
	})
	h.register(tool{
		name:          "add_favorite",
		description:   "Add a task, project, page, lens, or timebox to the user's favorites.",
		requiredScope: "write:workspace",
		inputSchema: objectSchema(map[string]any{
			"targetType": stringSchema("Entity type: project, task, page, lens, or timebox."),
			"targetId":   stringSchema("Public ID of the entity to favorite."),
		}, []string{"targetType", "targetId"}),
		run: runAddFavorite,
	})
	h.register(tool{
		name:          "add_reaction",
		description:   "Add an emoji reaction to a task.",
		requiredScope: "write:workspace",
		inputSchema: objectSchema(map[string]any{
			"taskId": stringSchema("Task public id (UUID v7)."),
			"emoji":  stringSchema("Unicode emoji character."),
		}, []string{"taskId", "emoji"}),
		run: runAddReaction,
	})
	h.register(tool{
		name:          "list_reactions",
		description:   "List emoji reactions on a task.",
		requiredScope: "read:workspace",
		inputSchema: objectSchema(map[string]any{
			"taskId": stringSchema("Task public id (UUID v7)."),
		}, []string{"taskId"}),
		run: runListReactions,
	})
	h.register(tool{
		name:          "list_recent",
		description:   "List the user's recently visited entities in the workspace.",
		requiredScope: "read:workspace",
		inputSchema: objectSchema(map[string]any{
			"limit": intSchema("Max results (1..20, default 20)."),
		}, nil),
		run: runListRecent,
	})

	// Intake triage and description version history (Wave 3).
	h.register(tool{
		name:          "list_intake_items",
		description:   "List intake items in the workspace triage queue.",
		requiredScope: "read:workspace",
		inputSchema: objectSchema(map[string]any{
			"status": stringSchema("Filter by triage status: pending, accepted, rejected, snoozed, duplicate. Optional."),
			"limit":  intSchema("Max results (1..200, default 50)."),
			"offset": intSchema("Skip N results."),
		}, nil),
		run: runListIntakeItems,
	})
	h.register(tool{
		name:          "triage_intake_item",
		description:   "Accept, reject, snooze, or mark as duplicate an intake item.",
		requiredScope: "write:workspace",
		inputSchema: objectSchema(map[string]any{
			"intakeItemId": stringSchema("Intake item public id (UUID v7)."),
			"status":       stringSchema("Triage decision: accepted, rejected, snoozed, or duplicate."),
			"snoozeUntil":  intSchema("Unix seconds timestamp for snooze expiry (required when status is snoozed)."),
		}, []string{"intakeItemId", "status"}),
		run: runTriageIntakeItem,
	})
	h.register(tool{
		name:          "convert_intake_to_task",
		description:   "Convert an intake item into a task in a specified project.",
		requiredScope: "write:workspace",
		inputSchema: objectSchema(map[string]any{
			"intakeItemId": stringSchema("Intake item public id (UUID v7)."),
			"projectId":    stringSchema("Project public id (UUID v7) to create the task in."),
		}, []string{"intakeItemId", "projectId"}),
		run: runConvertIntakeToTask,
	})
	h.register(tool{
		name:          "list_description_versions",
		description:   "List description version history for a task (newest first, without body).",
		requiredScope: "read:workspace",
		inputSchema: objectSchema(map[string]any{
			"taskId": stringSchema("Task public id (UUID v7)."),
		}, []string{"taskId"}),
		run: runListDescriptionVersions,
	})
	h.register(tool{
		name:          "restore_description_version",
		description:   "Restore a previous description version, updating the task description and creating a new version snapshot.",
		requiredScope: "write:workspace",
		inputSchema: objectSchema(map[string]any{
			"taskId":    stringSchema("Task public id (UUID v7)."),
			"versionId": stringSchema("Description version public id (UUID v7) to restore."),
		}, []string{"taskId", "versionId"}),
		run: runRestoreDescriptionVersion,
	})

	// Import job management (Wave 4).
	h.register(tool{
		name:          "list_import_jobs",
		description:   "List import jobs for the workspace.",
		requiredScope: "read:workspace",
		inputSchema: objectSchema(map[string]any{
			"status": stringSchema("Filter by status: pending, running, completed, failed, cancelled. Optional."),
			"limit":  intSchema("Max results (1..200, default 50)."),
			"offset": intSchema("Skip N results."),
		}, nil),
		run: runListImportJobs,
	})
	h.register(tool{
		name:          "create_import_job",
		description:   "Create a new import job.",
		requiredScope: "write:workspace",
		inputSchema: objectSchema(map[string]any{
			"source":     stringSchema("Import source: github, jira, linear, or csv."),
			"projectId":  stringSchema("Project public id (UUID v7) to import into. Optional."),
			"configJson": stringSchema("JSON string with source-specific configuration. Optional."),
		}, []string{"source"}),
		run: runCreateImportJob,
	})
}

// ----------------------------------------------------------------------------
// JSON Schema helpers (minimal hand-rolled)
// ----------------------------------------------------------------------------

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

func stringSchema(desc string) map[string]any {
	return map[string]any{"type": "string", "description": desc}
}

func intSchema(desc string) map[string]any {
	return map[string]any{"type": "integer", "description": desc}
}

func boolSchema(desc string) map[string]any {
	return map[string]any{"type": "boolean", "description": desc}
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
	if _, err := requireWorkspaceMember(ctx, deps, s); err != nil {
		return nil, err
	}
	if in.Limit <= 0 || in.Limit > 200 {
		in.Limit = 50
	}
	items := []map[string]any{}
	if in.ProjectID != "" {
		prjPub, err := types.Parse(in.ProjectID)
		if err != nil {
			return nil, apierrors.New(apierrors.WsProjectNotFound)
		}
		if _, err := resolveProject(ctx, deps, s, in.ProjectID); err != nil {
			return nil, err
		}
		pb := prjPub.UUID()
		rows, err := deps.Queries.ListTasksForProject(ctx, generated.ListTasksForProjectParams{
			WorkspaceID:     s.workspaceID,
			ProjectPublicID: pb[:],
			Limit:           in.Limit,
			Offset:          in.Offset,
		})
		if err != nil {
			return nil, apierrors.Wrap(apierrors.McpToolExecutionFailed, err)
		}
		for _, r := range rows {
			items = append(items, taskListRowToMap(r.PublicID, r.Title, string(r.DerivedState), r.Priority, r.DueOn))
		}
	} else {
		rows, err := deps.Queries.ListTasksForWorkspace(ctx, generated.ListTasksForWorkspaceParams{
			WorkspaceID: s.workspaceID,
			Limit:       in.Limit,
			Offset:      in.Offset,
		})
		if err != nil {
			return nil, apierrors.Wrap(apierrors.McpToolExecutionFailed, err)
		}
		for _, r := range rows {
			items = append(items, taskListRowToMap(r.PublicID, r.Title, string(r.DerivedState), r.Priority, r.DueOn))
		}
	}
	return map[string]any{"tasks": items}, nil
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
	pub, err := types.Parse(in.TaskID)
	if err != nil {
		return nil, apierrors.New(apierrors.WsTaskNotFound)
	}
	row, err := deps.Queries.FindTaskByPublicId(ctx, generated.FindTaskByPublicIdParams{
		WorkspaceID: s.workspaceID,
		PublicID:    pub,
	})
	if err != nil {
		if stderrors.Is(err, sql.ErrNoRows) {
			return nil, apierrors.New(apierrors.WsTaskNotFound)
		}
		return nil, apierrors.Wrap(apierrors.McpToolExecutionFailed, err)
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
	prjID, err := resolveProject(ctx, deps, s, in.ProjectID)
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
	pub := newPublicID()
	desc := sql.NullString{String: in.Description, Valid: in.Description != ""}
	taskID, err := deps.Queries.CreateTask(ctx, generated.CreateTaskParams{
		PublicID:        pub,
		WorkspaceID:     s.workspaceID,
		ProjectID:       prjID,
		ParentTaskID:    sql.NullInt32{},
		CreatedByUserID: sql.NullInt32{Int32: int32(s.userID), Valid: true},
		UpdatedByUserID: sql.NullInt32{Int32: int32(s.userID), Valid: true},
		Title:           in.Title,
		Description:     desc,
		Priority:        in.Priority,
		DueOn:           due,
		StartedOn:       start,
	})
	if err != nil {
		return nil, apierrors.Wrap(apierrors.McpToolExecutionFailed, err)
	}
	actor := int64(s.userID)
	_ = eventbus.Append(ctx, deps.DB, eventbus.Event{
		Type:        eventbus.TaskCreated,
		WorkspaceID: s.workspaceID,
		ActorUserID: &actor,
		TaskID:      &taskID,
		Payload: map[string]any{
			"taskId": pub.String(),
			"title":  in.Title,
			"via":    "mcp",
		},
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
	taskInternal, pub, err := resolveTask(ctx, deps, s, in.TaskID)
	if err != nil {
		return nil, err
	}
	current, err := deps.Queries.FindTaskByPublicId(ctx, generated.FindTaskByPublicIdParams{
		WorkspaceID: s.workspaceID,
		PublicID:    pub,
	})
	if err != nil {
		return nil, apierrors.Wrap(apierrors.McpToolExecutionFailed, err)
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
		UpdatedByUserID: sql.NullInt32{Int32: int32(s.userID), Valid: true},
		WorkspaceID:     s.workspaceID,
		PublicID:        pub,
	}

	if !needsItemkit {
		if err := deps.Queries.UpdateTask(ctx, updateParams); err != nil {
			return nil, apierrors.Wrap(apierrors.McpToolExecutionFailed, err)
		}
	} else {
		tx, err := deps.DB.BeginTx(ctx, nil)
		if err != nil {
			return nil, apierrors.Wrap(apierrors.McpToolExecutionFailed, err)
		}
		defer tx.Rollback() //nolint:errcheck
		qtx := deps.Queries.WithTx(tx)
		if err := qtx.UpdateTask(ctx, updateParams); err != nil {
			return nil, apierrors.Wrap(apierrors.McpToolExecutionFailed, err)
		}
		if titleChanged {
			if err := itemkit.RenameItem(ctx, tx, itemkit.RenameItemArgs{
				WorkspaceID: s.workspaceID,
				ActorUserID: s.userID,
				TaskID:      taskInternal,
				NewTitle:    title,
			}); err != nil {
				return nil, translateItemkitMCPError(err)
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
				return nil, translateItemkitMCPError(err)
			}
		}
		if err := tx.Commit(); err != nil {
			return nil, apierrors.Wrap(apierrors.McpToolExecutionFailed, err)
		}
	}

	taskID64 := int64(taskInternal)
	actor := int64(s.userID)
	_ = eventbus.Append(ctx, deps.DB, eventbus.Event{
		Type:        eventbus.TaskUpdated,
		WorkspaceID: s.workspaceID,
		ActorUserID: &actor,
		TaskID:      &taskID64,
		Payload:     map[string]any{"taskId": pub.String(), "via": "mcp"},
	})
	return map[string]any{"id": pub.String()}, nil
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

// mcpKnownTransitions is the set of transitions accepted by the
// transition_task MCP tool. Mirrors the HTTP handler's knownTransitions.
var mcpKnownTransitions = map[string]struct{}{
	"start": {}, "block": {}, "unblock": {}, "submit": {},
	"complete": {}, "reopen": {}, "cancel": {},
}

// mcpNextState mirrors the v1 state machine from the HTTP handler layer.
// Duplicated here to avoid importing the handler package from MCP.
func mcpNextState(current generated.TasksDerivedState, transition string) (generated.TasksDerivedState, bool) {
	switch current {
	case generated.TasksDerivedStateOpen:
		switch transition {
		case "start":
			return generated.TasksDerivedStateWaiting, true
		case "cancel":
			return generated.TasksDerivedStateCancelled, true
		case "complete":
			return generated.TasksDerivedStateDone, true
		}
	case generated.TasksDerivedStateWaiting:
		switch transition {
		case "submit":
			return generated.TasksDerivedStateReview, true
		case "block":
			return generated.TasksDerivedStateOpen, true
		case "cancel":
			return generated.TasksDerivedStateCancelled, true
		}
	case generated.TasksDerivedStateReview:
		switch transition {
		case "complete":
			return generated.TasksDerivedStateDone, true
		case "reopen":
			return generated.TasksDerivedStateWaiting, true
		case "cancel":
			return generated.TasksDerivedStateCancelled, true
		}
	case generated.TasksDerivedStateDone:
		if transition == "reopen" {
			return generated.TasksDerivedStateWaiting, true
		}
	case generated.TasksDerivedStateCancelled:
		if transition == "reopen" {
			return generated.TasksDerivedStateOpen, true
		}
	}
	return "", false
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
	if _, ok := mcpKnownTransitions[in.Transition]; !ok {
		return nil, apierrors.New(apierrors.McpToolArgumentsInvalid)
	}
	if _, err := requireWorkspaceMember(ctx, deps, s); err != nil {
		return nil, err
	}
	taskInternal, pub, err := resolveTask(ctx, deps, s, in.TaskID)
	if err != nil {
		return nil, err
	}
	current, err := deps.Queries.FindTaskByPublicId(ctx, generated.FindTaskByPublicIdParams{
		WorkspaceID: s.workspaceID,
		PublicID:    pub,
	})
	if err != nil {
		if stderrors.Is(err, sql.ErrNoRows) {
			return nil, apierrors.New(apierrors.McpToolExecutionFailed)
		}
		return nil, apierrors.Wrap(apierrors.McpToolExecutionFailed, err)
	}
	nextDerived, ok := mcpNextState(current.DerivedState, in.Transition)
	if !ok {
		return nil, apierrors.New(apierrors.WsTaskTransitionRejected)
	}

	tx, err := deps.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, apierrors.Wrap(apierrors.McpToolExecutionFailed, err)
	}
	defer func() { _ = tx.Rollback() }()

	qtx := generated.New(tx)
	if err := qtx.TransitionTaskState(ctx, generated.TransitionTaskStateParams{
		DerivedState:    nextDerived,
		Column2:         string(nextDerived),
		UpdatedByUserID: sql.NullInt32{Int32: int32(s.userID), Valid: true},
		WorkspaceID:     s.workspaceID,
		PublicID:        pub,
	}); err != nil {
		return nil, apierrors.Wrap(apierrors.McpToolExecutionFailed, err)
	}

	taskID64 := int64(taskInternal)
	actor := int64(s.userID)
	reason := ""
	if in.Reason != nil {
		reason = *in.Reason
	}
	if err := eventbus.Append(ctx, tx, eventbus.Event{
		Type:        eventbus.TaskTransition(in.Transition),
		WorkspaceID: s.workspaceID,
		ActorUserID: &actor,
		TaskID:      &taskID64,
		Payload: map[string]any{
			"taskId":     pub.String(),
			"transition": in.Transition,
			"fromState":  string(current.DerivedState),
			"toState":    string(nextDerived),
			"reason":     reason,
			"via":        "mcp",
		},
	}); err != nil {
		return nil, apierrors.Wrap(apierrors.McpToolExecutionFailed, err)
	}

	if err := tx.Commit(); err != nil {
		return nil, apierrors.Wrap(apierrors.McpToolExecutionFailed, err)
	}
	return map[string]any{
		"id":         pub.String(),
		"fromState":  string(current.DerivedState),
		"toState":    string(nextDerived),
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
	taskInternal, pub, err := resolveTask(ctx, deps, s, in.TaskID)
	if err != nil {
		return nil, err
	}
	cpub := newPublicID()
	if _, err := deps.Queries.AddComment(ctx, generated.AddCommentParams{
		PublicID:    cpub,
		WorkspaceID: s.workspaceID,
		TaskID:      taskInternal,
		AuthorID:    s.userID,
		Body:        in.Body,
	}); err != nil {
		return nil, apierrors.Wrap(apierrors.McpToolExecutionFailed, err)
	}
	taskID64 := int64(taskInternal)
	actor := int64(s.userID)
	_ = eventbus.Append(ctx, deps.DB, eventbus.Event{
		Type:        eventbus.CommentAddedLegacy,
		WorkspaceID: s.workspaceID,
		ActorUserID: &actor,
		TaskID:      &taskID64,
		Payload: map[string]any{
			"taskId":    pub.String(),
			"commentId": cpub.String(),
			"via":       "mcp",
		},
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
	if _, err := requireWorkspaceMember(ctx, deps, s); err != nil {
		return nil, err
	}
	if in.Query == "" {
		return nil, apierrors.New(apierrors.McpToolArgumentsInvalid)
	}
	if in.Limit <= 0 || in.Limit > 200 {
		in.Limit = 50
	}

	pattern := "%" + in.Query + "%"
	rows, err := deps.Queries.SearchTasks(ctx, generated.SearchTasksParams{
		WorkspaceID: s.workspaceID,
		Title:       pattern,
		Description: sql.NullString{String: pattern, Valid: true},
		Limit:       in.Limit,
		Offset:      in.Offset,
	})
	if err != nil {
		return nil, apierrors.Wrap(apierrors.McpToolExecutionFailed, err)
	}

	items := make([]map[string]any, 0, len(rows))
	for _, r := range rows {
		items = append(items, taskListRowToMap(r.PublicID, r.Title, string(r.DerivedState), r.Priority, r.DueOn))
	}
	return map[string]any{"tasks": items}, nil
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
	pub, err := types.Parse(in.TaskID)
	if err != nil {
		return nil, apierrors.New(apierrors.WsTaskNotFound)
	}
	row, err := deps.Queries.FindTaskByPublicId(ctx, generated.FindTaskByPublicIdParams{
		WorkspaceID: s.workspaceID,
		PublicID:    pub,
	})
	if err != nil {
		if stderrors.Is(err, sql.ErrNoRows) {
			return nil, apierrors.New(apierrors.WsTaskNotFound)
		}
		return nil, apierrors.Wrap(apierrors.McpToolExecutionFailed, err)
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
	if _, err := requireWorkspaceMember(ctx, deps, s); err != nil {
		return nil, err
	}
	if deps.Embedder == nil {
		return nil, apierrors.New(apierrors.AiProviderNotConfigured)
	}
	taskInternal, pub, err := resolveTask(ctx, deps, s, in.TaskID)
	if err != nil {
		return nil, err
	}

	// Resolve model + thresholds from ai_settings (ADR 0003 defaults on
	// miss).
	model := "mock-768"
	high := 0.870
	low := 0.750
	if settings, serr := deps.Queries.GetAiSettings(ctx, s.workspaceID); serr == nil {
		if settings.EmbedModel != "" {
			model = settings.EmbedModel
		}
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
		row, ferr := deps.Queries.FindTaskByPublicId(ctx, generated.FindTaskByPublicIdParams{
			WorkspaceID: s.workspaceID,
			PublicID:    pub,
		})
		if ferr != nil {
			return map[string]any{"candidates": []any{}, "model": model}, nil
		}
		desc := ""
		if row.Description.Valid {
			desc = row.Description.String
		}
		if eerr := deps.Embedder.EmbedTask(ctx, taskInternal, row.Title, desc); eerr != nil {
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
		WorkspaceID: s.workspaceID,
		Model:       model,
		TaskID:      taskInternal,
		Limit:       200,
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
	pub, err := types.Parse(in.TaskID)
	if err != nil {
		return nil, apierrors.New(apierrors.WsTaskNotFound)
	}
	row, err := deps.Queries.FindTaskByPublicId(ctx, generated.FindTaskByPublicIdParams{
		WorkspaceID: s.workspaceID,
		PublicID:    pub,
	})
	if err != nil {
		if stderrors.Is(err, sql.ErrNoRows) {
			return nil, apierrors.New(apierrors.WsTaskNotFound)
		}
		return nil, apierrors.Wrap(apierrors.McpToolExecutionFailed, err)
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
	parentInternal, parentPub, err := resolveTask(ctx, deps, s, in.ParentTaskID)
	if err != nil {
		return nil, err
	}
	var parentProjectID uint32
	if err := deps.DB.QueryRowContext(ctx,
		`SELECT project_id FROM tasks WHERE id = ? AND workspace_id = ? LIMIT 1`,
		parentInternal, s.workspaceID,
	).Scan(&parentProjectID); err != nil {
		return nil, apierrors.Wrap(apierrors.McpToolExecutionFailed, err)
	}
	created := make([]string, 0, len(in.Steps))
	for _, st := range in.Steps {
		if st.Title == "" {
			return nil, apierrors.New(apierrors.McpToolArgumentsInvalid)
		}
		pub := newPublicID()
		desc := sql.NullString{String: st.Description, Valid: st.Description != ""}
		childID, err := deps.Queries.CreateTask(ctx, generated.CreateTaskParams{
			PublicID:        pub,
			WorkspaceID:     s.workspaceID,
			ProjectID:       parentProjectID,
			ParentTaskID:    sql.NullInt32{Int32: int32(parentInternal), Valid: true},
			CreatedByUserID: sql.NullInt32{Int32: int32(s.userID), Valid: true},
			UpdatedByUserID: sql.NullInt32{Int32: int32(s.userID), Valid: true},
			Title:           st.Title,
			Description:     desc,
			Priority:        st.Priority,
		})
		if err != nil {
			return nil, apierrors.Wrap(apierrors.McpToolExecutionFailed, err)
		}
		actor := int64(s.userID)
		_ = eventbus.Append(ctx, deps.DB, eventbus.Event{
			Type:        eventbus.TaskCreated,
			WorkspaceID: s.workspaceID,
			ActorUserID: &actor,
			TaskID:      &childID,
			Payload: map[string]any{
				"taskId":       pub.String(),
				"title":        st.Title,
				"parentTaskId": parentPub.String(),
				"via":          "mcp:apply_steps",
			},
		})
		created = append(created, pub.String())
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
		projectID = sql.NullInt32{Int32: int32(prjID), Valid: true}
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
	actor := int64(s.userID)
	_ = eventbus.Append(ctx, deps.DB, eventbus.Event{
		Type:        eventbus.TimeboxCreated,
		WorkspaceID: s.workspaceID,
		ActorUserID: &actor,
		Payload: map[string]any{
			"timeboxId": pub.String(),
			"name":      in.Name,
			"via":       "mcp",
		},
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
	actor := int64(s.userID)
	_ = eventbus.Append(ctx, deps.DB, eventbus.Event{
		Type:        eventbus.TimeboxTaskAdded,
		WorkspaceID: s.workspaceID,
		ActorUserID: &actor,
		TaskID:      &taskID64,
		Payload: map[string]any{
			"timeboxId": tbPub.String(),
			"taskId":    taskPub.String(),
			"via":       "mcp",
		},
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
	if _, err := requireWorkspaceMember(ctx, deps, s); err != nil {
		return nil, err
	}
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
			WorkspaceID: s.workspaceID,
			ProjectID:   prjID,
			Limit:       in.Limit,
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
			WorkspaceID: s.workspaceID,
			Limit:       in.Limit,
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
	return map[string]any{"tasks": items}, nil
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
	if _, err := requireWorkspaceMember(ctx, deps, s); err != nil {
		return nil, err
	}
	if deps.Embedder == nil {
		return nil, apierrors.New(apierrors.AiProviderNotConfigured)
	}
	taskInternal, pub, err := resolveTask(ctx, deps, s, in.TaskID)
	if err != nil {
		return nil, err
	}

	// Resolve model + thresholds from ai_settings (ADR 0003 defaults).
	model := "mock-768"
	high := 0.870
	low := 0.750
	if settings, serr := deps.Queries.GetAiSettings(ctx, s.workspaceID); serr == nil {
		if settings.EmbedModel != "" {
			model = settings.EmbedModel
		}
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
		row, ferr := deps.Queries.FindTaskByPublicId(ctx, generated.FindTaskByPublicIdParams{
			WorkspaceID: s.workspaceID,
			PublicID:    pub,
		})
		if ferr != nil {
			return map[string]any{"candidates": []any{}, "model": model}, nil
		}
		desc := ""
		if row.Description.Valid {
			desc = row.Description.String
		}
		if eerr := deps.Embedder.EmbedTask(ctx, taskInternal, row.Title, desc); eerr != nil {
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
		WorkspaceID: s.workspaceID,
		Model:       model,
		TaskID:      taskInternal,
		Limit:       200,
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
			ParentPageID: sql.NullInt32{Int32: int32(parentInternal), Valid: true},
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
			ProjectID:   sql.NullInt32{Int32: int32(prjID), Valid: true},
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
		projectID = sql.NullInt32{Int32: int32(prjID), Valid: true}
	}

	var parentPageID sql.NullInt32
	if in.ParentPageID != "" {
		parentInternal, _, err := resolvePage(ctx, deps, s, in.ParentPageID)
		if err != nil {
			return nil, err
		}
		parentPageID = sql.NullInt32{Int32: int32(parentInternal), Valid: true}
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

	actor := int64(s.userID)
	_ = eventbus.Append(ctx, deps.DB, eventbus.Event{
		Type:        eventbus.PageCreated,
		WorkspaceID: s.workspaceID,
		ActorUserID: &actor,
		Payload: map[string]any{
			"pageId": pub.String(),
			"title":  in.Title,
			"via":    "mcp",
		},
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

	// Fetch current project_id and parent_page_id to preserve them.
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

	if err := deps.Queries.UpdatePage(ctx, generated.UpdatePageParams{
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
	actor := int64(s.userID)
	_ = eventbus.Append(ctx, deps.DB, eventbus.Event{
		Type:        eventbus.PageUpdated,
		WorkspaceID: s.workspaceID,
		ActorUserID: &actor,
		Payload: map[string]any{
			"pageId": pub.String(),
			"via":    "mcp",
		},
	})
	_ = pageID64 // used only for the event; no page-specific event field yet
	return map[string]any{"id": pub.String()}, nil
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
		projectID = sql.NullInt32{Int32: int32(prjID), Valid: true}
	}

	// Build context from task data if task ids are provided.
	contextParts := []string{in.ContextDescription}
	for _, tid := range in.TaskIDs {
		tPub, err := types.Parse(tid)
		if err != nil {
			continue
		}
		tRow, err := deps.Queries.FindTaskByPublicId(ctx, generated.FindTaskByPublicIdParams{
			WorkspaceID: s.workspaceID,
			PublicID:    tPub,
		})
		if err != nil {
			continue
		}
		taskCtx := "\n\nTask: " + tRow.Title
		if tRow.Description.Valid && tRow.Description.String != "" {
			taskCtx += "\nDescription: " + tRow.Description.String
		}
		contextParts = append(contextParts, taskCtx)
	}

	// Attempt AI generation.
	title := "Generated: " + in.ContextDescription
	if len(title) > 200 {
		title = title[:200]
	}
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
			title = proposed[0].Title
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

	actor := int64(s.userID)
	_ = eventbus.Append(ctx, deps.DB, eventbus.Event{
		Type:        eventbus.PageCreated,
		WorkspaceID: s.workspaceID,
		ActorUserID: &actor,
		Payload: map[string]any{
			"pageId":        pub.String(),
			"title":         title,
			"isAiGenerated": isAI,
			"via":           "mcp:generate_page",
		},
	})
	_ = pageID // internal id not exposed
	return map[string]any{
		"id":            pub.String(),
		"isAiGenerated": isAI,
	}, nil
}

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
	if _, err := requireWorkspaceMember(ctx, deps, s); err != nil {
		return nil, err
	}
	prjID, err := resolveProject(ctx, deps, s, in.ProjectID)
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
	)
	if err != nil {
		return nil, mapAiError(err)
	}

	// Create the parent task.
	parentPub := newPublicID()
	desc := sql.NullString{String: in.Description, Valid: in.Description != ""}
	parentID, err := deps.Queries.CreateTask(ctx, generated.CreateTaskParams{
		PublicID:        parentPub,
		WorkspaceID:     s.workspaceID,
		ProjectID:       prjID,
		ParentTaskID:    sql.NullInt32{},
		CreatedByUserID: sql.NullInt32{Int32: int32(s.userID), Valid: true},
		UpdatedByUserID: sql.NullInt32{Int32: int32(s.userID), Valid: true},
		Title:           in.Title,
		Description:     desc,
		Priority:        0,
	})
	if err != nil {
		return nil, apierrors.Wrap(apierrors.McpToolExecutionFailed, err)
	}
	actor := int64(s.userID)
	_ = eventbus.Append(ctx, deps.DB, eventbus.Event{
		Type:        eventbus.TaskCreated,
		WorkspaceID: s.workspaceID,
		ActorUserID: &actor,
		TaskID:      &parentID,
		Payload: map[string]any{
			"taskId": parentPub.String(),
			"title":  in.Title,
			"via":    "mcp:smart_create_task",
		},
	})

	// Build the response.
	subtaskIDs := make([]string, 0, len(proposal.Subtasks))
	for _, st := range proposal.Subtasks {
		if st.Title == "" {
			continue
		}
		childPub := newPublicID()
		childDesc := sql.NullString{String: st.Description, Valid: st.Description != ""}
		childID, cerr := deps.Queries.CreateTask(ctx, generated.CreateTaskParams{
			PublicID:        childPub,
			WorkspaceID:     s.workspaceID,
			ProjectID:       prjID,
			ParentTaskID:    sql.NullInt32{Int32: int32(parentID), Valid: true}, //#nosec G115 -- parent_task_id is tasks.id (BIGINT UNSIGNED), fits int32 within realistic deployments
			CreatedByUserID: sql.NullInt32{Int32: int32(s.userID), Valid: true},
			UpdatedByUserID: sql.NullInt32{Int32: int32(s.userID), Valid: true},
			Title:           st.Title,
			Description:     childDesc,
			Priority:        smartCreatePriorityToInt(st.Priority),
		})
		if cerr != nil {
			return nil, apierrors.Wrap(apierrors.McpToolExecutionFailed, cerr)
		}
		_ = eventbus.Append(ctx, deps.DB, eventbus.Event{
			Type:        eventbus.TaskCreated,
			WorkspaceID: s.workspaceID,
			ActorUserID: &actor,
			TaskID:      &childID,
			Payload: map[string]any{
				"taskId":       childPub.String(),
				"title":        st.Title,
				"parentTaskId": parentPub.String(),
				"via":          "mcp:smart_create_task",
			},
		})
		subtaskIDs = append(subtaskIDs, childPub.String())
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

// nullTimeFormat renders a nullable time as its layout-formatted string,
// or an empty string when the time is NULL. Keeps the MCP DTO stable
// while calendar_events.{start,end}_at are nullable in the schema.
func nullTimeFormat(t sql.NullTime, layout string) string {
	if !t.Valid {
		return ""
	}
	return t.Time.Format(layout)
}

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

// parseFlexibleTime parses an RFC 3339 datetime or a YYYY-MM-DD date string.
func parseFlexibleTime(s string) (time.Time, error) {
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, nil
	}
	t, err := time.Parse("2006-01-02", s)
	if err != nil {
		return time.Time{}, err
	}
	return t, nil
}

func runListCalendarEvents(ctx context.Context, deps Deps, s *session, raw json.RawMessage) (any, error) {
	var in struct {
		StartDate string `json:"startDate"`
		EndDate   string `json:"endDate"`
	}
	if err := parseArgs(raw, &in); err != nil {
		return nil, err
	}
	if in.StartDate == "" || in.EndDate == "" {
		return nil, apierrors.New(apierrors.McpToolArgumentsInvalid)
	}
	if _, err := requireWorkspaceMember(ctx, deps, s); err != nil {
		return nil, err
	}
	startTime, err := parseFlexibleTime(in.StartDate)
	if err != nil {
		return nil, apierrors.Newf(apierrors.McpToolArgumentsInvalid, "invalid startDate: %v", err)
	}
	endTime, err := parseFlexibleTime(in.EndDate)
	if err != nil {
		return nil, apierrors.Newf(apierrors.McpToolArgumentsInvalid, "invalid endDate: %v", err)
	}
	// The SQL query uses start_at < ? AND end_at > ? (overlap check),
	// so we pass endDate as StartAt and startDate as EndAt.
	rows, err := deps.CalendarQueries.ListCalendarEventsAcrossCalendars(ctx, calendar.ListCalendarEventsAcrossCalendarsParams{
		UserID:      s.userID,
		WorkspaceID: s.workspaceID,
		StartAt:     sql.NullTime{Time: endTime, Valid: true},
		EndAt:       sql.NullTime{Time: startTime, Valid: true},
	})
	if err != nil {
		return nil, apierrors.Wrap(apierrors.McpToolExecutionFailed, err)
	}
	items := make([]map[string]any, 0, len(rows))
	for _, r := range rows {
		items = append(items, map[string]any{
			"id":          r.PublicID.String(),
			"calendarId":  r.CalendarID,
			"kind":        string(r.Kind),
			"showAs":      string(r.ShowAs),
			"title":       r.Title,
			"allDay":      r.AllDay,
			"startAt":     nullTimeFormat(r.StartAt, time.RFC3339),
			"endAt":       nullTimeFormat(r.EndAt, time.RFC3339),
			"ownerUserId": r.OwnerUserID,
		})
	}
	return map[string]any{"events": items}, nil
}

func runCreateCalendarEvent(ctx context.Context, deps Deps, s *session, raw json.RawMessage) (any, error) {
	var in struct {
		CalendarID  string `json:"calendarId"`
		Title       string `json:"title"`
		StartAt     string `json:"startAt"`
		EndAt       string `json:"endAt"`
		Kind        string `json:"kind"`
		ShowAs      string `json:"showAs"`
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
	if in.CalendarID == "" || in.Title == "" || in.StartAt == "" || in.EndAt == "" {
		return nil, apierrors.New(apierrors.McpToolArgumentsInvalid)
	}
	if _, err := requireWorkspaceMember(ctx, deps, s); err != nil {
		return nil, err
	}
	calID, err := resolveCalendar(ctx, deps, s, in.CalendarID)
	if err != nil {
		return nil, err
	}
	startAt, err := time.Parse(time.RFC3339, in.StartAt)
	if err != nil {
		return nil, apierrors.Newf(apierrors.McpToolArgumentsInvalid, "invalid startAt: %v", err)
	}
	endAt, err := time.Parse(time.RFC3339, in.EndAt)
	if err != nil {
		return nil, apierrors.Newf(apierrors.McpToolArgumentsInvalid, "invalid endAt: %v", err)
	}

	kind := calendar.CalendarEventsKindEvent
	if in.Kind != "" {
		kind = calendar.CalendarEventsKind(in.Kind)
	}
	showAs := calendar.CalendarEventsShowAsBusy
	if in.ShowAs != "" {
		showAs = calendar.CalendarEventsShowAs(in.ShowAs)
	}
	visibility := calendar.CalendarEventsVisibilityDefault
	if in.Visibility != "" {
		visibility = calendar.CalendarEventsVisibility(in.Visibility)
	}

	ownerUserID := s.userID
	if in.OwnerUserID != "" {
		// calendar_subscriptions.role is gone, so "manager/owner" tiers
		// no longer exist. Only the personal-calendar owner can set a
		// different ownerUserId here. System calendars have no editable
		// owner. Event-level ACL (attendee can_edit) is still TBD.
		const qCalOwner = `SELECT owner_user_id FROM calendars WHERE id = ? AND enabled = TRUE LIMIT 1`
		var calOwner sql.NullInt32
		if serr := deps.DB.QueryRowContext(ctx, qCalOwner, calID).Scan(&calOwner); serr != nil {
			return nil, apierrors.Newf(apierrors.McpToolExecutionFailed, "calendar not found")
		}
		if !calOwner.Valid || uint32(calOwner.Int32) != s.userID {
			return nil, apierrors.Newf(apierrors.McpToolExecutionFailed, "only the calendar owner can set ownerUserId")
		}
		// Resolve the target user by public id.
		ownerPub, perr := types.Parse(in.OwnerUserID)
		if perr != nil {
			return nil, apierrors.Newf(apierrors.McpToolArgumentsInvalid, "invalid ownerUserId")
		}
		const qUser = `SELECT id FROM users WHERE public_id = ? AND enabled = TRUE LIMIT 1`
		if uerr := deps.DB.QueryRowContext(ctx, qUser, ownerPub).Scan(&ownerUserID); uerr != nil {
			return nil, apierrors.Newf(apierrors.McpToolExecutionFailed, "owner user not found")
		}
	}

	allDay := false
	if in.AllDay != nil {
		allDay = *in.AllDay
	}

	pub := newPublicID()
	_, err = deps.CalendarQueries.CreateCalendarEvent(ctx, calendar.CreateCalendarEventParams{
		PublicID:           pub,
		WorkspaceID:        s.workspaceID,
		CalendarID:         calID,
		Kind:               kind,
		Visibility:         visibility,
		ShowAs:             showAs,
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
	return map[string]any{
		"id":      pub.String(),
		"title":   in.Title,
		"startAt": startAt.Format(time.RFC3339),
		"endAt":   endAt.Format(time.RFC3339),
		"kind":    string(kind),
		"showAs":  string(showAs),
	}, nil
}

func runUpdateCalendarEvent(ctx context.Context, deps Deps, s *session, raw json.RawMessage) (any, error) {
	var in struct {
		EventID    string  `json:"eventId"`
		Title      *string `json:"title"`
		StartAt    *string `json:"startAt"`
		EndAt      *string `json:"endAt"`
		Kind       *string `json:"kind"`
		ShowAs     *string `json:"showAs"`
		Visibility *string `json:"visibility"`
		Location   *string `json:"location"`
		Memo       *string `json:"memo"`
		BlockLabel *string `json:"blockLabel"`
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
	if in.StartAt != nil {
		t, perr := time.Parse(time.RFC3339, *in.StartAt)
		if perr != nil {
			return nil, apierrors.Newf(apierrors.McpToolArgumentsInvalid, "invalid startAt")
		}
		newStartAt = t
		timeChanged = true
	}
	if in.EndAt != nil {
		t, perr := time.Parse(time.RFC3339, *in.EndAt)
		if perr != nil {
			return nil, apierrors.Newf(apierrors.McpToolArgumentsInvalid, "invalid endAt")
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
		if err := deps.CalendarQueries.PatchCalendarEvent(ctx, params); err != nil {
			return nil, apierrors.Wrap(apierrors.McpToolExecutionFailed, err)
		}
		return map[string]any{"success": true}, nil
	}

	tx, err := deps.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, apierrors.Wrap(apierrors.McpToolExecutionFailed, err)
	}
	defer tx.Rollback() //nolint:errcheck
	qtxCal := deps.CalendarQueries.WithTx(tx)

	if titleChanged {
		if err := itemkit.RenameItem(ctx, tx, itemkit.RenameItemArgs{
			WorkspaceID: s.workspaceID,
			ActorUserID: s.userID,
			EventID:     evt.ID,
			NewTitle:    *in.Title,
		}); err != nil {
			return nil, translateItemkitMCPError(err)
		}
		params.Title = sql.NullString{}
	}
	if timeChanged {
		if err := itemkit.RescheduleEvent(ctx, tx, itemkit.RescheduleEventArgs{
			WorkspaceID: s.workspaceID,
			EventID:     evt.ID,
			ActorUserID: s.userID,
			StartAt:     newStartAt,
			EndAt:       newEndAt,
		}); err != nil {
			return nil, translateItemkitMCPError(err)
		}
		params.StartAt = sql.NullTime{}
		params.EndAt = sql.NullTime{}
	}
	// Only run the remaining-fields patch if anything is still set.
	if params.Title.Valid || params.Kind.Valid || params.ShowAs.Valid ||
		params.Visibility.Valid || params.Location.Valid || params.Memo.Valid ||
		params.BlockLabel.Valid || params.StartAt.Valid || params.EndAt.Valid {
		if err := qtxCal.PatchCalendarEvent(ctx, params); err != nil {
			return nil, apierrors.Wrap(apierrors.McpToolExecutionFailed, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, apierrors.Wrap(apierrors.McpToolExecutionFailed, err)
	}
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
	ok, err := canEditCalendarEvent(ctx, deps, s, owner.OwnerUserID, evt.ID, owner.CalendarID)
	if err != nil || !ok {
		return nil, apierrors.Newf(apierrors.McpToolExecutionFailed, "permission denied: cannot delete event")
	}

	tx, err := deps.DB.BeginTx(ctx, nil)
	if err != nil {
		return nil, apierrors.Wrap(apierrors.McpToolExecutionFailed, err)
	}
	defer tx.Rollback() //nolint:errcheck
	if err := itemkit.DeleteEvent(ctx, tx, s.workspaceID, evt.ID, s.userID); err != nil {
		return nil, translateItemkitMCPError(err)
	}
	if err := tx.Commit(); err != nil {
		return nil, apierrors.Wrap(apierrors.McpToolExecutionFailed, err)
	}
	return map[string]any{"success": true}, nil
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
		userPub, perr := types.Parse(in.UserID)
		if perr != nil {
			return nil, apierrors.Newf(apierrors.McpToolArgumentsInvalid, "invalid userId")
		}
		const q = `SELECT id FROM users WHERE public_id = ? AND enabled = TRUE LIMIT 1`
		if uerr := deps.DB.QueryRowContext(ctx, q, userPub).Scan(&targetUserID); uerr != nil {
			return nil, apierrors.Newf(apierrors.McpToolExecutionFailed, "user not found")
		}
	}

	date, err := time.Parse("2006-01-02", in.Date)
	if err != nil {
		return nil, apierrors.Newf(apierrors.McpToolArgumentsInvalid, "invalid date: %v", err)
	}
	if in.DurationMinutes <= 0 {
		in.DurationMinutes = 60
	}

	workStart := time.Date(date.Year(), date.Month(), date.Day(), 9, 0, 0, 0, time.UTC)
	workEnd := time.Date(date.Year(), date.Month(), date.Day(), 18, 0, 0, 0, time.UTC)

	rows, err := deps.CalendarQueries.ListCalendarEventsAcrossCalendars(ctx, calendar.ListCalendarEventsAcrossCalendarsParams{
		UserID:      targetUserID,
		WorkspaceID: s.workspaceID,
		StartAt:     sql.NullTime{Time: workEnd, Valid: true},
		EndAt:       sql.NullTime{Time: workStart, Valid: true},
	})
	if err != nil {
		return nil, apierrors.Wrap(apierrors.McpToolExecutionFailed, err)
	}

	// Collect busy intervals, clamped to working hours.
	type interval struct{ start, end time.Time }
	busy := make([]interval, 0, len(rows))
	for _, r := range rows {
		if r.AllDay {
			continue
		}
		if string(r.ShowAs) == "free" {
			continue
		}
		// start_at / end_at are nullable. Undated events
		// (planning-stage placeholders) don't contribute to busy
		// intervals, so skip them.
		if !r.StartAt.Valid || !r.EndAt.Valid {
			continue
		}
		s := r.StartAt.Time
		e := r.EndAt.Time
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
					"startAt":         cursor.Format(time.RFC3339),
					"endAt":           b.start.Format(time.RFC3339),
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
				"startAt":         cursor.Format(time.RFC3339),
				"endAt":           workEnd.Format(time.RFC3339),
				"durationMinutes": int(gap.Minutes()),
			})
		}
	}
	return map[string]any{"slots": slots}, nil
}

func runCreateEventFromTask(ctx context.Context, deps Deps, s *session, raw json.RawMessage) (any, error) {
	var in struct {
		TaskID     string `json:"taskId"`
		CalendarID string `json:"calendarId"`
		StartAt    string `json:"startAt"`
		EndAt      string `json:"endAt"`
	}
	if err := parseArgs(raw, &in); err != nil {
		return nil, err
	}
	if in.TaskID == "" || in.CalendarID == "" || in.StartAt == "" || in.EndAt == "" {
		return nil, apierrors.New(apierrors.McpToolArgumentsInvalid)
	}
	if _, err := requireWorkspaceMember(ctx, deps, s); err != nil {
		return nil, err
	}

	taskInternal, taskPub, err := resolveTask(ctx, deps, s, in.TaskID)
	if err != nil {
		return nil, err
	}
	calID, err := resolveCalendar(ctx, deps, s, in.CalendarID)
	if err != nil {
		return nil, err
	}
	startAt, err := time.Parse(time.RFC3339, in.StartAt)
	if err != nil {
		return nil, apierrors.Newf(apierrors.McpToolArgumentsInvalid, "invalid startAt")
	}
	endAt, err := time.Parse(time.RFC3339, in.EndAt)
	if err != nil {
		return nil, apierrors.Newf(apierrors.McpToolArgumentsInvalid, "invalid endAt")
	}

	// Look up the task to get its title.
	task, err := deps.Queries.FindTaskByPublicId(ctx, generated.FindTaskByPublicIdParams{
		WorkspaceID: s.workspaceID,
		PublicID:    taskPub,
	})
	if err != nil {
		return nil, apierrors.Wrap(apierrors.McpToolExecutionFailed, err)
	}

	pub := newPublicID()
	_, err = deps.CalendarQueries.CreateCalendarEvent(ctx, calendar.CreateCalendarEventParams{
		PublicID:           pub,
		WorkspaceID:        s.workspaceID,
		CalendarID:         calID,
		Kind:               calendar.CalendarEventsKindEvent,
		Visibility:         calendar.CalendarEventsVisibilityDefault,
		ShowAs:             calendar.CalendarEventsShowAsBusy,
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
		TaskID:             sql.NullInt32{Int32: int32(taskInternal), Valid: true},
	})
	if err != nil {
		return nil, apierrors.Wrap(apierrors.McpToolExecutionFailed, err)
	}
	return map[string]any{
		"id":      pub.String(),
		"title":   task.Title,
		"startAt": startAt.Format(time.RFC3339),
		"endAt":   endAt.Format(time.RFC3339),
		"taskId":  taskPub.String(),
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
	rows, err := deps.CalendarQueries.ListCalendarMemos(ctx, calID)
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
	calID, err := resolveCalendar(ctx, deps, s, in.CalendarID)
	if err != nil {
		return nil, err
	}
	memoPub, err := types.Parse(in.MemoID)
	if err != nil {
		return nil, apierrors.Newf(apierrors.McpToolArgumentsInvalid, "invalid memoId")
	}
	if err := deps.CalendarQueries.UpdateCalendarMemo(ctx, calendar.UpdateCalendarMemoParams{
		Done:        sql.NullBool{Bool: *in.Done, Valid: true},
		PublicID:    memoPub,
		CalendarID:  calID,
		WorkspaceID: s.workspaceID,
	}); err != nil {
		return nil, apierrors.Wrap(apierrors.McpToolExecutionFailed, err)
	}
	return map[string]any{"success": true}, nil
}

func runSmartCreateEvent(_ context.Context, _ Deps, _ *session, _ json.RawMessage) (any, error) {
	return nil, apierrors.Newf(apierrors.AiProviderNotConfigured, "AI-powered event creation coming soon")
}
