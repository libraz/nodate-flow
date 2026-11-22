package asanaimport

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"sync"

	"golang.org/x/text/collate"
	"golang.org/x/text/language"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
)

// SourcePrefix namespaces this importer's source_uid values: "asana:<gid>".
const SourcePrefix = "asana:"

// SourceUID is the source_uid value for an Asana gid.
func SourceUID(gid string) string { return SourcePrefix + gid }

// lookupChunk bounds the IN lists of the per-task queries.
const lookupChunk = 500

// lookup reads the deployment's current state; it never writes.
type lookup interface {
	// workspace finds an enabled workspace by slug.
	workspace(ctx context.Context, slug string) (generated.FindWorkspaceBySlugRow, bool, error)
	// usersByEmail returns the accounts with these addresses, disabled ones included.
	usersByEmail(ctx context.Context, emails []string) ([]generated.ListUsersByEmailsForImportRow, error)
	// workspaceRole reports the account's live membership role.
	workspaceRole(ctx context.Context, workspaceID, userID uint32) (generated.WorkspaceMembersRole, bool, error)
	projects(ctx context.Context, workspaceID uint32) ([]generated.ListProjectsForImportRow, error)
	projectMembers(ctx context.Context, workspaceID uint32, projectIDs []uint32) ([]generated.ListProjectMembersForImportRow, error)
	labels(ctx context.Context, workspaceID uint32) ([]generated.ListLabelsForImportRow, error)
	allowlistCount(ctx context.Context) (int64, error)
	// existing reads every live row this importer wrote in the workspace.
	existing(ctx context.Context, workspaceID uint32) (*Existing, error)
}

// ExistingTask is a live task carrying an "asana:" source_uid.
type ExistingTask struct {
	generated.ListSourcedTasksInWorkspaceRow
	GID string
}

// ExistingComment is a live comment carrying an "asana:" source_uid.
type ExistingComment struct {
	generated.ListSourcedCommentsByTasksRow
	GID string
}

// ExistingAttachment is a live attachment carrying an "asana:" source_uid.
type ExistingAttachment struct {
	generated.ListSourcedAttachmentsByTasksRow
	GID string
}

// Existing is what earlier runs left in the workspace. Maps are keyed by
// the internal task id.
type Existing struct {
	Tasks []*ExistingTask
	// SourceUIDCounts is live tasks with any source_uid, per project id.
	SourceUIDCounts map[uint32]int
	Comments        map[uint32][]*ExistingComment
	Attachments     map[uint32][]*ExistingAttachment
	Actors          map[uint32][]generated.ListTaskActorsForTasksRow
	Labels          map[uint32][]uint32
	// Blocks are blocks edges whose both ends are sourced tasks.
	Blocks []generated.ListBlockingDependenciesForTasksRow
}

func emptyExisting() *Existing {
	return &Existing{
		SourceUIDCounts: map[uint32]int{},
		Comments:        map[uint32][]*ExistingComment{},
		Attachments:     map[uint32][]*ExistingAttachment{},
		Actors:          map[uint32][]generated.ListTaskActorsForTasksRow{},
		Labels:          map[uint32][]uint32{},
	}
}

// dbLookup is lookup over the generated queries.
type dbLookup struct {
	q *generated.Queries
}

func (d dbLookup) workspace(ctx context.Context, slug string) (generated.FindWorkspaceBySlugRow, bool, error) {
	ws, err := d.q.FindWorkspaceBySlug(ctx, slug)
	if errors.Is(err, sql.ErrNoRows) {
		return generated.FindWorkspaceBySlugRow{}, false, nil
	}
	if err != nil {
		return generated.FindWorkspaceBySlugRow{}, false, err
	}
	return ws, ws.Enabled, nil
}

func (d dbLookup) usersByEmail(ctx context.Context, emails []string) ([]generated.ListUsersByEmailsForImportRow, error) {
	var out []generated.ListUsersByEmailsForImportRow
	for _, part := range chunks(emails) {
		rows, err := d.q.ListUsersByEmailsForImport(ctx, part)
		if err != nil {
			return nil, err
		}
		out = append(out, rows...)
	}
	return out, nil
}

func (d dbLookup) workspaceRole(ctx context.Context, workspaceID, userID uint32) (generated.WorkspaceMembersRole, bool, error) {
	row, err := d.q.GetWorkspaceMemberRoleForImport(ctx, generated.GetWorkspaceMemberRoleForImportParams{
		WorkspaceID: workspaceID, UserID: userID,
	})
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return row.Role, row.Enabled, nil
}

func (d dbLookup) projects(ctx context.Context, workspaceID uint32) ([]generated.ListProjectsForImportRow, error) {
	return d.q.ListProjectsForImport(ctx, workspaceID)
}

func (d dbLookup) projectMembers(ctx context.Context, workspaceID uint32, projectIDs []uint32) ([]generated.ListProjectMembersForImportRow, error) {
	var out []generated.ListProjectMembersForImportRow
	for _, part := range chunks(projectIDs) {
		rows, err := d.q.ListProjectMembersForImport(ctx, generated.ListProjectMembersForImportParams{
			WorkspaceID: workspaceID, ProjectIds: part,
		})
		if err != nil {
			return nil, err
		}
		out = append(out, rows...)
	}
	return out, nil
}

func (d dbLookup) labels(ctx context.Context, workspaceID uint32) ([]generated.ListLabelsForImportRow, error) {
	return d.q.ListLabelsForImport(ctx, workspaceID)
}

func (d dbLookup) allowlistCount(ctx context.Context) (int64, error) {
	return d.q.CountEnabledSigninAllowlistForImport(ctx)
}

func (d dbLookup) existing(ctx context.Context, workspaceID uint32) (*Existing, error) {
	ex := emptyExisting()
	rows, err := d.q.ListSourcedTasksInWorkspace(ctx, generated.ListSourcedTasksInWorkspaceParams{
		WorkspaceID: workspaceID, Prefix: SourcePrefix,
	})
	if err != nil {
		return nil, err
	}
	ids := make([]uint32, 0, len(rows))
	sourced := map[uint32]bool{}
	for _, r := range rows {
		gid, ok := strings.CutPrefix(r.SourceUid.String, SourcePrefix)
		if !ok {
			continue
		}
		ex.Tasks = append(ex.Tasks, &ExistingTask{ListSourcedTasksInWorkspaceRow: r, GID: gid})
		ids = append(ids, r.ID)
		sourced[r.ID] = true
	}
	counts, err := d.q.CountTasksWithSourceUIDByProject(ctx, workspaceID)
	if err != nil {
		return nil, err
	}
	for _, c := range counts {
		ex.SourceUIDCounts[c.ProjectID] = int(c.Total)
	}

	for _, part := range chunks(ids) {
		nullIDs := make([]sql.NullInt32, len(part))
		for i, id := range part {
			nullIDs[i] = sql.NullInt32{Int32: int32(id), Valid: true} //#nosec G115 -- INT UNSIGNED task ids into the generated NullInt32 parameter
		}
		comments, err := d.q.ListSourcedCommentsByTasks(ctx, generated.ListSourcedCommentsByTasksParams{
			WorkspaceID: workspaceID, TaskIds: nullIDs, Prefix: SourcePrefix,
		})
		if err != nil {
			return nil, err
		}
		for _, c := range comments {
			if gid, ok := strings.CutPrefix(c.SourceUid.String, SourcePrefix); ok && c.TaskID.Valid {
				tid := uint32(c.TaskID.Int32) //#nosec G115 -- generated NullInt32 of an INT UNSIGNED task id
				ex.Comments[tid] = append(ex.Comments[tid], &ExistingComment{ListSourcedCommentsByTasksRow: c, GID: gid})
			}
		}
		atts, err := d.q.ListSourcedAttachmentsByTasks(ctx, generated.ListSourcedAttachmentsByTasksParams{
			WorkspaceID: workspaceID, TaskIds: nullIDs, Prefix: SourcePrefix,
		})
		if err != nil {
			return nil, err
		}
		for _, a := range atts {
			if gid, ok := strings.CutPrefix(a.SourceUid.String, SourcePrefix); ok && a.TaskID.Valid {
				tid := uint32(a.TaskID.Int32) //#nosec G115 -- generated NullInt32 of an INT UNSIGNED task id
				ex.Attachments[tid] = append(ex.Attachments[tid], &ExistingAttachment{ListSourcedAttachmentsByTasksRow: a, GID: gid})
			}
		}
		actors, err := d.q.ListTaskActorsForTasks(ctx, generated.ListTaskActorsForTasksParams{WorkspaceID: workspaceID, TaskIds: part})
		if err != nil {
			return nil, err
		}
		for _, a := range actors {
			ex.Actors[a.TaskID] = append(ex.Actors[a.TaskID], a)
		}
		links, err := d.q.ListTaskLabelsForTasks(ctx, generated.ListTaskLabelsForTasksParams{WorkspaceID: workspaceID, TaskIds: part})
		if err != nil {
			return nil, err
		}
		for _, l := range links {
			ex.Labels[l.TaskID] = append(ex.Labels[l.TaskID], l.LabelID)
		}
		deps, err := d.q.ListBlockingDependenciesForTasks(ctx, generated.ListBlockingDependenciesForTasksParams{WorkspaceID: workspaceID, TaskIds: part})
		if err != nil {
			return nil, err
		}
		for _, e := range deps {
			if sourced[e.ToTaskID] {
				ex.Blocks = append(ex.Blocks, e)
			}
		}
	}
	return ex, nil
}

func chunks[T any](all []T) [][]T {
	var out [][]T
	for len(all) > lookupChunk {
		out = append(out, all[:lookupChunk])
		all = all[lookupChunk:]
	}
	if len(all) > 0 {
		out = append(out, all)
	}
	return out
}

// The labels and projects name columns compare under utf8mb4_0900_ai_ci,
// which ignores case, accents, width and kana type: UCA primary strength.
var (
	collatorMu sync.Mutex
	collator   = collate.New(language.Und, collate.Loose)
)

// nameKey is the form in which two names compare equal exactly when the
// database's collation would call them equal.
func nameKey(s string) string {
	collatorMu.Lock()
	defer collatorMu.Unlock()
	var buf collate.Buffer
	return string(collator.KeyFromString(&buf, s))
}
