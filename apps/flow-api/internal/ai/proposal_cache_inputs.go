package ai

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/acl"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
)

func proposalCacheInputReader(reader SmartCreateReader, fallback any) ProposalCacheInputReader {
	if current, ok := reader.(ProposalCacheInputReader); ok {
		return current
	}
	if current, ok := fallback.(ProposalCacheInputReader); ok {
		return current
	}
	return nil
}

func currentProposalCacheVisibility(
	ctx context.Context,
	reader ProposalCacheInputReader,
	workspaceID uint32,
	actorUserID int64,
) (acl.VisibilityArgs, error) {
	role, err := reader.GetProposalCacheWorkspaceAccess(ctx, generated.GetProposalCacheWorkspaceAccessParams{
		ID:     workspaceID,
		UserID: uint32(actorUserID), //#nosec G115 -- actor ids originate from the uint32 user key.
	})
	if errors.Is(err, sql.ErrNoRows) {
		return acl.VisibilityArgs{}, ErrProposalInputsChanged
	}
	if err != nil {
		return acl.VisibilityArgs{}, err
	}
	return acl.ListVisibilityArgs(uint32(actorUserID), acl.WorkspaceRole(proposalCacheSQLString(role))), nil //#nosec G115 -- actor ids originate from the uint32 user key.
}

func sameProposalVisibility(left, right acl.VisibilityArgs) bool {
	return left.ActorUserID == right.ActorUserID && left.IsElevated == right.IsElevated
}

func proposalCacheResources(snapshot proposalCacheSnapshot) []InvocationResource {
	resources := make([]InvocationResource, 0, len(snapshot.TaskInputs))
	for _, input := range snapshot.TaskInputs {
		resources = append(resources, TaskInputResource(input.PublicID))
	}
	return resources
}

// loadProposalCacheTaskInputs checks workspace authority even when there are
// no task references, then loads references under visibility derived from the
// current database role. missingIsError distinguishes a prompt that became
// stale before a provider call from a cached envelope that should be evicted.
func loadProposalCacheTaskInputs(
	ctx context.Context,
	reader ProposalCacheInputReader,
	workspaceID uint32,
	actorUserID int64,
	resources []InvocationResource,
	missingIsError bool,
) ([]generated.ListProposalCacheTaskInputsRow, acl.VisibilityArgs, bool, error) {
	vis, err := currentProposalCacheVisibility(ctx, reader, workspaceID, actorUserID)
	if err != nil {
		return nil, acl.VisibilityArgs{}, false, err
	}
	unique := uniqueInvocationResources(resources)
	publicIDs := make([]types.PublicID, 0, len(unique))
	seen := make(map[string]struct{}, len(unique))
	for _, resource := range unique {
		if resource.Kind != "task" {
			return nil, vis, false, fmt.Errorf("unsupported proposal cache input kind %q", resource.Kind)
		}
		publicID, parseErr := types.Parse(resource.PublicID)
		if parseErr != nil {
			return nil, vis, false, fmt.Errorf("invalid proposal cache task reference: %w", parseErr)
		}
		canonical := publicID.String()
		if _, exists := seen[canonical]; exists {
			continue
		}
		seen[canonical] = struct{}{}
		publicIDs = append(publicIDs, publicID)
	}
	if len(publicIDs) == 0 {
		return nil, vis, true, nil
	}
	rows, err := reader.ListProposalCacheTaskInputs(ctx, generated.ListProposalCacheTaskInputsParams{
		WorkspaceID: workspaceID,
		ActorUserID: actorUserID,
		PublicIds:   publicIDs,
	})
	if err != nil {
		return nil, vis, false, err
	}
	if len(rows) != len(publicIDs) {
		if missingIsError {
			return nil, vis, false, ErrProposalInputsChanged
		}
		return nil, vis, false, nil
	}
	expected := make(map[string]struct{}, len(publicIDs))
	for _, publicID := range publicIDs {
		expected[publicID.String()] = struct{}{}
	}
	for _, row := range rows {
		key := row.PublicID.String()
		if _, ok := expected[key]; !ok {
			if missingIsError {
				return nil, vis, false, ErrProposalInputsChanged
			}
			return nil, vis, false, nil
		}
		delete(expected, key)
	}
	if len(expected) != 0 {
		if missingIsError {
			return nil, vis, false, ErrProposalInputsChanged
		}
		return nil, vis, false, nil
	}
	return rows, vis, true, nil
}

func proposalCacheSnapshotFromRows(rows []generated.ListProposalCacheTaskInputsRow) proposalCacheSnapshot {
	inputs := make([]proposalCacheTaskFingerprint, 0, len(rows))
	for _, row := range rows {
		assigneeNameValid := "0"
		if row.AssigneeName.Valid {
			assigneeNameValid = "1"
		}
		fingerprint := ProposalCacheKey(0,
			row.PublicID.String(),
			row.Title,
			row.Description.String,
			fmt.Sprint(row.DerivedState),
			proposalCacheSQLString(row.AssigneePublicID),
			assigneeNameValid,
			row.AssigneeName.String,
		)
		inputs = append(inputs, proposalCacheTaskFingerprint{
			PublicID:    row.PublicID.String(),
			Fingerprint: fingerprint,
		})
	}
	sort.Slice(inputs, func(i, j int) bool { return inputs[i].PublicID < inputs[j].PublicID })
	return proposalCacheSnapshot{TaskInputs: inputs}
}

func currentProposalCacheSnapshot(
	ctx context.Context,
	reader ProposalCacheInputReader,
	rosterReader SmartCreateReader,
	workspaceID uint32,
	vis acl.VisibilityArgs,
	resources []InvocationResource,
	includeRoster bool,
) (proposalCacheSnapshot, acl.VisibilityArgs, bool, error) {
	rows, currentVis, cacheable, err := loadProposalCacheTaskInputs(
		ctx, reader, workspaceID, vis.ActorUserID, resources, false,
	)
	if err != nil || !cacheable {
		return proposalCacheSnapshot{}, currentVis, cacheable, err
	}
	snapshot := proposalCacheSnapshotFromRows(rows)
	if !includeRoster {
		return snapshot, currentVis, true, nil
	}
	members, err := rosterReader.ListWorkspaceMembersForSmartCreate(ctx, workspaceID)
	if err != nil {
		return proposalCacheSnapshot{}, currentVis, false, err
	}
	snapshot.RosterFingerprint = smartCreateRosterFingerprint(members)
	return snapshot, currentVis, true, nil
}

func validateProposalCacheSnapshot(
	ctx context.Context,
	reader ProposalCacheInputReader,
	rosterReader SmartCreateReader,
	workspaceID uint32,
	actorUserID int64,
	expected proposalCacheSnapshot,
	includeRoster bool,
) error {
	current, _, cacheable, err := currentProposalCacheSnapshot(
		ctx, reader, rosterReader, workspaceID,
		acl.VisibilityArgs{ActorUserID: actorUserID}, proposalCacheResources(expected), includeRoster,
	)
	if err != nil {
		return err
	}
	if !cacheable || !sameProposalCacheSnapshot(expected, current) {
		return ErrProposalInputsChanged
	}
	return nil
}

func proposalCacheSQLString(value any) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		return typed
	case []byte:
		return string(typed)
	default:
		return fmt.Sprint(typed)
	}
}

func smartCreateRosterFingerprint(members []generated.ListWorkspaceMembersForSmartCreateRow) string {
	ordered := append([]generated.ListWorkspaceMembersForSmartCreateRow(nil), members...)
	sort.Slice(ordered, func(i, j int) bool {
		if ordered[i].UserPublicID.String() == ordered[j].UserPublicID.String() {
			return ordered[i].DisplayName < ordered[j].DisplayName
		}
		return ordered[i].UserPublicID.String() < ordered[j].UserPublicID.String()
	})
	parts := make([]string, 0, 1+len(ordered)*2)
	parts = append(parts, fmt.Sprintf("members:%d", len(ordered)))
	for _, member := range ordered {
		parts = append(parts, member.UserPublicID.String(), member.DisplayName)
	}
	return ProposalCacheKey(0, parts...)
}

func sameProposalCacheSnapshot(left, right proposalCacheSnapshot) bool {
	if left.RosterFingerprint != right.RosterFingerprint || len(left.TaskInputs) != len(right.TaskInputs) {
		return false
	}
	for i := range left.TaskInputs {
		if left.TaskInputs[i] != right.TaskInputs[i] {
			return false
		}
	}
	return true
}

func sameSmartCreatePromptInputs(
	ranked []scoredTask,
	capturedAssignees []generated.ListTasksWithAssigneesForSmartCreateRow,
	current []generated.ListProposalCacheTaskInputsRow,
) bool {
	currentByID := make(map[string]generated.ListProposalCacheTaskInputsRow, len(current))
	for _, row := range current {
		currentByID[row.PublicID.String()] = row
	}
	limit := min(len(ranked), smartCreateTopN)
	for _, candidate := range ranked[:limit] {
		row, ok := currentByID[candidate.publicID]
		if !ok || row.ID != candidate.id || row.Title != candidate.title {
			return false
		}
		var captured generated.ListTasksWithAssigneesForSmartCreateRow
		matches := 0
		for _, assignee := range capturedAssignees {
			if assignee.ID == candidate.id {
				captured = assignee
				matches++
			}
		}
		if matches != 1 {
			return false
		}
		currentAssigneeID := proposalCacheSQLString(row.AssigneePublicID)
		if captured.AssigneeName.Valid {
			if !row.AssigneeName.Valid || currentAssigneeID != captured.AssigneePublicID.String() || row.AssigneeName.String != captured.AssigneeName.String {
				return false
			}
		} else if row.AssigneeName.Valid || currentAssigneeID != "" {
			return false
		}
	}
	return true
}

func sameStepsPromptInputs(
	children []ChildTaskSummary,
	ranked []scoredTask,
	current []generated.ListProposalCacheTaskInputsRow,
	taskID uint32,
	title, description string,
) bool {
	currentByPublicID := make(map[string]generated.ListProposalCacheTaskInputsRow, len(current))
	var parent *generated.ListProposalCacheTaskInputsRow
	for i := range current {
		row := &current[i]
		currentByPublicID[row.PublicID.String()] = *row
		if taskID != 0 && row.ID == taskID {
			parent = row
		}
	}
	for _, child := range children {
		row, ok := currentByPublicID[child.PublicID]
		if !ok || row.Title != child.Title || string(row.DerivedState) != child.State {
			return false
		}
	}
	for _, candidate := range ranked {
		row, ok := currentByPublicID[candidate.publicID]
		if !ok || row.ID != candidate.id || row.Title != candidate.title {
			return false
		}
	}
	if taskID != 0 {
		if parent == nil || parent.Title != title || parent.Description.String != description {
			return false
		}
	}
	return true
}
