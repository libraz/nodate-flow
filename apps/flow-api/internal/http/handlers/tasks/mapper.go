// Package-level note on mapper layout:
//
// The five v_task_list-derived sqlc row types
// (FindTaskByPublicIdRow + four ListTasks* variants) share the same
// underlying view but project different column subsets — Find selects
// the rich detail columns (Description, label/constraint counts,
// CreatedByUserPublicID, etc.) while List* selects the leaner
// summary columns and an OFFSET / Total or keyset projection. The
// detail and list paths therefore land on two distinct DTOs (Task vs
// TaskListItem) and the Find→Task mapper is necessarily separate.
//
// Within the four List* mappers the column set is byte-identical, so
// they collapse onto a single rowToTaskListItem helper through a
// taskListRow projection struct with thin per-source adapters
// (taskListRowFrom*). This is the H7 audit fix: before the
// consolidation each adapter ran the same TaskListItem field
// projection inline, which had drifted at least once during the
// keyset rollout.

package tasks

import (
	"database/sql"
	"encoding/json"
	"time"

	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/types"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/http/handlers/handlerutil"
)

// bytesToUUIDString / nullBytesToUUIDString / nullBytesToUUIDPtr /
// rawBytesToUUIDPtr delegate to handlerutil so the four byte→UUID
// conversion shapes share a single implementation across handler
// packages. Aliases keep the call sites readable.
var (
	bytesToUUIDString     = handlerutil.BytesToUUIDString
	nullBytesToUUIDString = handlerutil.NullBytesToUUIDString
	nullBytesToUUIDPtr    = handlerutil.NullBytesToUUIDPtr
	rawBytesToUUIDPtr     = handlerutil.RawBytesToUUIDPtr
)

func rowToTaskFromFind(r generated.FindTaskByPublicIdRow) Task {
	return Task{
		ID:                       r.PublicID.String(),
		WorkspaceID:              bytesToUUIDString(r.WorkspacePublicID),
		ProjectID:                bytesToUUIDString(r.ProjectPublicID),
		ProjectName:              r.ProjectName,
		ParentTaskID:             nullBytesToUUIDString(r.ParentTaskPublicID),
		CreatedByUserID:          nullBytesToUUIDString(r.CreatedByUserPublicID),
		Title:                    r.Title,
		Description:              nullStr(r.Description),
		Visibility:               string(r.Visibility),
		DerivedState:             string(r.DerivedState),
		Priority:                 r.Priority,
		DueOn:                    nullDate(r.DueOn),
		StartedOn:                nullDate(r.StartedOn),
		CompletedAt:              nullTimeUnix(r.CompletedAt),
		ProjectIdentifier:        r.ProjectIdentifier.String,
		TaskNumber:               int32(r.TaskNumber), //#nosec G115 -- task_number is per-project sequence (uint32), fits int32 within realistic deployments
		ArchivedAt:               nullTimeUnix(r.ArchivedAt),
		LabelCount:               r.LabelCount,
		ConstraintCount:          r.ConstraintCount,
		ConstraintSatisfiedCount: r.ConstraintSatisfiedCount,
		DependencyCount:          r.DependencyCount,
		ActorCount:               r.ActorCount,
		SortWeight:               r.SortWeight,
		AgentContext:             buildAgentContext(r.AgentMemo, r.AgentAssigneePublicID, r.AgentAssigneeName),
		UpdatedAt:                nullTimeUnix(r.UpdatedAt),
		CreatedAt:                r.CreatedAt.Unix(),
	}
}

// agentMemoPayload is the persisted shape of tasks.agent_memo as written
// by the orchestrator and handoff endpoints. Only the keys consumed by
// the API DTO are declared here; extra fields are tolerated so the
// orchestrator can evolve the schema without breaking the reader.
type agentMemoPayload struct {
	LastStartedAt int64  `json:"last_started_at"`
	LastRunAt     int64  `json:"last_run_at"`
	LastThought   string `json:"last_thought"`
	Attempts      int    `json:"attempts"`
	HandoffStatus string `json:"handoff_status"`
	HandoffReason string `json:"handoff_reason"`
}

// buildAgentContext assembles the AgentContext field on the Task DTO
// from the persisted agent_memo JSON and the joined agent assignee
// columns surfaced by v_task_detail. Returns nil when the task has no
// agent memo and no current agent assignee — keeping the field
// `omitempty` so unrelated tasks pay no payload cost.
//
// The agent_assignee_public_id column is bytea ([]byte) because
// view-aliased *_public_id columns do not pick up the sqlc
// types.PublicID override; the conversion to a UUID string happens
// here at the mapper boundary.
func buildAgentContext(memo json.RawMessage, agentPublicID []byte, agentName string) *TaskAgentContext {
	hasAgent := len(agentPublicID) > 0
	hasMemo := len(memo) > 0 && string(memo) != "null" && string(memo) != "{}"
	if !hasAgent && !hasMemo {
		return nil
	}
	ctx := &TaskAgentContext{}
	if hasAgent {
		ctx.Agent = &AgentRef{
			ID:   bytesToUUIDString(agentPublicID),
			Name: agentName,
		}
	}
	if hasMemo {
		var p agentMemoPayload
		if err := json.Unmarshal(memo, &p); err == nil {
			// last_run_at is the canonical key; the orchestrator briefly
			// also writes last_started_at on start. Prefer last_run_at
			// when both are present.
			switch {
			case p.LastRunAt > 0:
				v := p.LastRunAt
				ctx.LastRunAt = &v
			case p.LastStartedAt > 0:
				v := p.LastStartedAt
				ctx.LastRunAt = &v
			}
			ctx.LastThought = p.LastThought
			ctx.Attempts = p.Attempts
			ctx.HandoffStatus = p.HandoffStatus
			ctx.HandoffReason = p.HandoffReason
		}
	}
	return ctx
}

// taskListRow is the union of v_task_list columns that the four
// ListTasks* sqlc rows share. It is the single intermediate
// representation [taskListRowToDTO] consumes; per-source adapters
// (taskListRowFromProject, taskListRowFromProjectKeyset,
// taskListRowFromWorkspace, taskListRowFromWorkspaceKeyset) populate
// it from the matching generated row type.
//
// Unifying through this struct guarantees the four call sites in
// crud.go produce byte-identical TaskListItem JSON regardless of
// which underlying query ran. Adding or removing a column in
// v_task_list now only requires updating one TaskListItem projection
// rather than four parallel inline expressions.
type taskListRow struct {
	PublicID                types.PublicID
	ProjectPublicID         []byte
	ProjectName             string
	ParentTaskPublicID      sql.NullString
	Title                   string
	Visibility              generated.TasksVisibility
	DerivedState            generated.TasksDerivedState
	Priority                int32
	DueOn                   sql.NullTime
	StartedOn               sql.NullTime
	CompletedAt             sql.NullTime
	ProjectIdentifier       sql.NullString
	TaskNumber              uint32
	ArchivedAt              sql.NullTime
	LabelIDs                sql.NullString
	SortWeight              int32
	UpdatedAt               sql.NullTime
	CreatedAt               time.Time
	PrimaryAssigneePublicID interface{}
	AssigneeCount           int64
}

// taskListRowToDTO converts the unified projection into TaskListItem.
// This is the single function responsible for shaping list-row JSON;
// every caller must route through here so OFFSET and keyset variants
// stay observable-identical at the wire level.
func taskListRowToDTO(r taskListRow) TaskListItem {
	return TaskListItem{
		ID:                r.PublicID.String(),
		ProjectID:         bytesToUUIDString(r.ProjectPublicID),
		ProjectName:       r.ProjectName,
		ProjectIdentifier: r.ProjectIdentifier.String,
		TaskNumber:        int32(r.TaskNumber), //#nosec G115 -- task_number is per-project sequence (uint32), fits int32 within realistic deployments
		ParentTaskID:      nullBytesToUUIDString(r.ParentTaskPublicID),
		Title:             r.Title,
		Visibility:        string(r.Visibility),
		DerivedState:      string(r.DerivedState),
		Priority:          r.Priority,
		DueOn:             nullDate(r.DueOn),
		StartedOn:         nullDate(r.StartedOn),
		CompletedAt:       nullTimeUnix(r.CompletedAt),
		ArchivedAt:        nullTimeUnix(r.ArchivedAt),
		LabelIDs:          nullStr(r.LabelIDs),
		SortWeight:        r.SortWeight,
		PrimaryAssigneeID: rawBytesToUUIDPtr(r.PrimaryAssigneePublicID),
		AssigneeCount:     r.AssigneeCount,
		UpdatedAt:         nullTimeUnix(r.UpdatedAt),
		CreatedAt:         r.CreatedAt.Unix(),
	}
}

// taskListRowFromProject adapts a ListTasksForProjectRow into the
// unified [taskListRow] projection. The adapter discards the
// COUNT(*) OVER() Total column because it lives on the response
// envelope's `total` field, not on individual task entries.
func taskListRowFromProject(r generated.ListTasksForProjectRow) taskListRow {
	return taskListRow{
		PublicID:                r.PublicID,
		ProjectPublicID:         r.ProjectPublicID,
		ProjectName:             r.ProjectName,
		ParentTaskPublicID:      r.ParentTaskPublicID,
		Title:                   r.Title,
		Visibility:              r.Visibility,
		DerivedState:            r.DerivedState,
		Priority:                r.Priority,
		DueOn:                   r.DueOn,
		StartedOn:               r.StartedOn,
		CompletedAt:             r.CompletedAt,
		ProjectIdentifier:       r.ProjectIdentifier,
		TaskNumber:              r.TaskNumber,
		ArchivedAt:              r.ArchivedAt,
		LabelIDs:                r.LabelIds,
		SortWeight:              r.SortWeight,
		UpdatedAt:               r.UpdatedAt,
		CreatedAt:               r.CreatedAt,
		PrimaryAssigneePublicID: r.PrimaryAssigneePublicID,
		AssigneeCount:           r.AssigneeCount,
	}
}

// taskListRowFromProjectKeyset adapts a ListTasksForProjectKeysetRow.
// The keyset row drops the Total column entirely because keyset
// pagination never carries an absolute total (it answers "more pages?",
// not "page X of Y"); the projection is otherwise identical to
// [taskListRowFromProject].
func taskListRowFromProjectKeyset(r generated.ListTasksForProjectKeysetRow) taskListRow {
	return taskListRow{
		PublicID:                r.PublicID,
		ProjectPublicID:         r.ProjectPublicID,
		ProjectName:             r.ProjectName,
		ParentTaskPublicID:      r.ParentTaskPublicID,
		Title:                   r.Title,
		Visibility:              r.Visibility,
		DerivedState:            r.DerivedState,
		Priority:                r.Priority,
		DueOn:                   r.DueOn,
		StartedOn:               r.StartedOn,
		CompletedAt:             r.CompletedAt,
		ProjectIdentifier:       r.ProjectIdentifier,
		TaskNumber:              r.TaskNumber,
		ArchivedAt:              r.ArchivedAt,
		LabelIDs:                r.LabelIds,
		SortWeight:              r.SortWeight,
		UpdatedAt:               r.UpdatedAt,
		CreatedAt:               r.CreatedAt,
		PrimaryAssigneePublicID: r.PrimaryAssigneePublicID,
		AssigneeCount:           r.AssigneeCount,
	}
}

// taskListRowFromWorkspace adapts a ListTasksForWorkspaceRow.
func taskListRowFromWorkspace(r generated.ListTasksForWorkspaceRow) taskListRow {
	return taskListRow{
		PublicID:                r.PublicID,
		ProjectPublicID:         r.ProjectPublicID,
		ProjectName:             r.ProjectName,
		ParentTaskPublicID:      r.ParentTaskPublicID,
		Title:                   r.Title,
		Visibility:              r.Visibility,
		DerivedState:            r.DerivedState,
		Priority:                r.Priority,
		DueOn:                   r.DueOn,
		StartedOn:               r.StartedOn,
		CompletedAt:             r.CompletedAt,
		ProjectIdentifier:       r.ProjectIdentifier,
		TaskNumber:              r.TaskNumber,
		ArchivedAt:              r.ArchivedAt,
		LabelIDs:                r.LabelIds,
		SortWeight:              r.SortWeight,
		UpdatedAt:               r.UpdatedAt,
		CreatedAt:               r.CreatedAt,
		PrimaryAssigneePublicID: r.PrimaryAssigneePublicID,
		AssigneeCount:           r.AssigneeCount,
	}
}

// taskListRowFromWorkspaceKeyset adapts a ListTasksForWorkspaceKeysetRow.
func taskListRowFromWorkspaceKeyset(r generated.ListTasksForWorkspaceKeysetRow) taskListRow {
	return taskListRow{
		PublicID:                r.PublicID,
		ProjectPublicID:         r.ProjectPublicID,
		ProjectName:             r.ProjectName,
		ParentTaskPublicID:      r.ParentTaskPublicID,
		Title:                   r.Title,
		Visibility:              r.Visibility,
		DerivedState:            r.DerivedState,
		Priority:                r.Priority,
		DueOn:                   r.DueOn,
		StartedOn:               r.StartedOn,
		CompletedAt:             r.CompletedAt,
		ProjectIdentifier:       r.ProjectIdentifier,
		TaskNumber:              r.TaskNumber,
		ArchivedAt:              r.ArchivedAt,
		LabelIDs:                r.LabelIds,
		SortWeight:              r.SortWeight,
		UpdatedAt:               r.UpdatedAt,
		CreatedAt:               r.CreatedAt,
		PrimaryAssigneePublicID: r.PrimaryAssigneePublicID,
		AssigneeCount:           r.AssigneeCount,
	}
}

// rowToTaskListItemFromProject is the public façade preserved for
// backwards-compatibility with the four crud.go call sites.
func rowToTaskListItemFromProject(r generated.ListTasksForProjectRow) TaskListItem {
	return taskListRowToDTO(taskListRowFromProject(r))
}

// rowToTaskListItemFromProjectKeyset is the keyset-pagination twin of
// rowToTaskListItemFromProject. The adapter handles the row-shape
// difference; the DTO projection runs through the same
// [taskListRowToDTO] so the two paths stay observable-identical.
func rowToTaskListItemFromProjectKeyset(r generated.ListTasksForProjectKeysetRow) TaskListItem {
	return taskListRowToDTO(taskListRowFromProjectKeyset(r))
}

// rowToTaskListItemFromWorkspaceKeyset is the keyset-pagination twin of
// rowToTaskListItemFromWorkspace.
func rowToTaskListItemFromWorkspaceKeyset(r generated.ListTasksForWorkspaceKeysetRow) TaskListItem {
	return taskListRowToDTO(taskListRowFromWorkspaceKeyset(r))
}

// rowToTaskListItemFromWorkspace runs the workspace-scope OFFSET path
// through the unified projection.
func rowToTaskListItemFromWorkspace(r generated.ListTasksForWorkspaceRow) TaskListItem {
	return taskListRowToDTO(taskListRowFromWorkspace(r))
}

func rowToConstraint(r generated.ListConstraintsForTaskRow) TaskConstraint {
	return TaskConstraint{
		ID:          r.PublicID.String(),
		Kind:        string(r.Kind),
		Expression:  r.Expression,
		SatisfiedAt: nullTimeUnix(r.SatisfiedAt),
		FailedAt:    nullTimeUnix(r.FailedAt),
		SortWeight:  r.SortWeight,
		UpdatedAt:   nullTimeUnix(r.UpdatedAt),
		CreatedAt:   r.CreatedAt.Unix(),
	}
}

func rowToDependency(r generated.ListDependenciesForTaskRow) TaskDependency {
	return TaskDependency{
		ID:                 r.PublicID.String(),
		Kind:               string(r.Kind),
		FromTaskID:         r.FromTaskPublicID.String(),
		ToTaskID:           r.ToTaskPublicID.String(),
		ToTaskTitle:        r.ToTaskTitle,
		ToTaskDerivedState: string(r.ToTaskDerivedState),
		UpdatedAt:          nullTimeUnix(r.UpdatedAt),
		CreatedAt:          r.CreatedAt.Unix(),
	}
}

func rowToActor(r generated.ListActorsForTaskRow) TaskActor {
	return TaskActor{
		ID:          r.PublicID.String(),
		UserID:      r.UserPublicID.String(),
		Email:       r.Email,
		DisplayName: r.DisplayName,
		AvatarURL:   nullStr(r.AvatarUrl),
		Role:        string(r.Role),
		UpdatedAt:   nullTimeUnix(r.UpdatedAt),
		CreatedAt:   r.CreatedAt.Unix(),
	}
}

func rowToComment(r generated.ListCommentsForTaskRow) TaskComment {
	return TaskComment{
		ID:                r.PublicID.String(),
		AuthorID:          r.AuthorPublicID.String(),
		AuthorDisplayName: r.AuthorDisplayName,
		AuthorAvatarURL:   nullStr(r.AuthorAvatarUrl),
		Body:              r.Body,
		EditedAt:          nullTimeUnix(r.EditedAt),
		UpdatedAt:         nullTimeUnix(r.UpdatedAt),
		CreatedAt:         r.CreatedAt.Unix(),
	}
}

func rowToAttachment(r generated.ListAttachmentsForTaskRow) TaskAttachment {
	return TaskAttachment{
		ID:                  r.PublicID.String(),
		UploaderID:          r.UploaderPublicID.String(),
		UploaderDisplayName: r.UploaderDisplayName,
		Filename:            r.Filename,
		ContentType:         r.ContentType,
		ByteSize:            r.ByteSize,
		StorageKey:          r.StorageKey,
		ChecksumSHA256:      nullStr(r.ChecksumSha256),
		UpdatedAt:           nullTimeUnix(r.UpdatedAt),
		CreatedAt:           r.CreatedAt.Unix(),
	}
}
