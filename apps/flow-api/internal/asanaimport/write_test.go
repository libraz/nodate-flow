package asanaimport

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/asanaimport/asana"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/eventbus"
	"github.com/libraz/nodate-flow/apps/flow-api/tests/helpers"
	"github.com/libraz/nodate-flow/packages/go-shared/testhelpers"
)

// memStore is an object store in memory.
type memStore struct {
	mu      sync.Mutex
	objects map[string][]byte
}

func (s *memStore) Put(_ context.Context, key string, r io.Reader, _ int64, _, _ string) error {
	b, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.objects[key] = b
	return nil
}

func (s *memStore) StatObject(_ context.Context, key string) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, ok := s.objects[key]
	if !ok {
		return 0, errors.New("no such object")
	}
	return int64(len(b)), nil
}

// fakeFiles serves attachment bytes by gid.
type fakeFiles map[string][]byte

func (f fakeFiles) DownloadAttachment(_ context.Context, gid string, w io.Writer) (int64, error) {
	b, ok := f[gid]
	if !ok {
		return 0, errors.New("no such attachment")
	}
	return io.Copy(w, bytes.NewReader(b))
}

// writeEnv is one seeded workspace with an owner (the importer) and a member.
type writeEnv struct {
	db                *sql.DB
	ctx               context.Context
	suffix            string
	wsID              uint32
	opsID, hanakoID   uint32
	opsMail, hanaMail string
}

func newWriteEnv(t *testing.T) *writeEnv {
	t.Helper()
	testhelpers.SkipUnlessIntegration(t)
	inst, err := helpers.EnsureShared()
	require.NoError(t, err, "start shared MySQL")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	t.Cleanup(cancel)

	suffix := fmt.Sprintf("%x", time.Now().UnixNano())
	suffix = suffix[len(suffix)-12:]
	e := &writeEnv{db: inst.DB, ctx: ctx, suffix: suffix,
		opsMail: "ops+" + suffix + "@example.test", hanaMail: "hanako+" + suffix + "@example.test"}
	wsPub := types.New()
	e.wsID = e.seed(t, `INSERT INTO workspaces (public_id, slug, name, timezone) VALUES (?, ?, ?, 'UTC')`,
		wsPub, "aw-"+suffix, "Asana write "+suffix)
	t.Cleanup(func() { helpers.PurgeWorkspace(t, e.db, wsPub.String()) })
	e.opsID = e.user(t, e.opsMail, "Ops", "owner")
	e.hanakoID = e.user(t, e.hanaMail, "Hanako", "member")
	return e
}

func (e *writeEnv) seed(t *testing.T, query string, args ...any) uint32 {
	t.Helper()
	res, err := e.db.ExecContext(e.ctx, query, args...)
	require.NoError(t, err, "seed %q", query)
	id, err := res.LastInsertId()
	require.NoError(t, err)
	return uint32(id) //#nosec G115 -- auto-increment ids of fresh rows fit uint32
}

func (e *writeEnv) user(t *testing.T, email, name, role string) uint32 {
	t.Helper()
	id := e.seed(t, `INSERT INTO users (public_id, email, display_name, locale, timezone) VALUES (?, ?, ?, 'en', 'UTC')`,
		types.New(), email, name)
	e.seed(t, `INSERT INTO workspace_members (public_id, workspace_id, user_id, role) VALUES (?, ?, ?, ?)`,
		types.New(), e.wsID, id, role)
	return id
}

func (e *writeEnv) count(t *testing.T, query string, args ...any) int {
	t.Helper()
	var n int
	require.NoError(t, e.db.QueryRowContext(e.ctx, query, args...).Scan(&n), query)
	return n
}

func (e *writeEnv) taskID(t *testing.T, gid string) uint32 {
	t.Helper()
	var id uint32
	require.NoError(t, e.db.QueryRowContext(e.ctx,
		`SELECT id FROM tasks WHERE workspace_id = ? AND source_uid = ? AND enabled = TRUE`, e.wsID, SourceUID(gid)).Scan(&id))
	return id
}

func (e *writeEnv) prepare(t *testing.T, m *Mapping, snap *asana.Snapshot, withSync bool) *Plan {
	t.Helper()
	p, err := Prepare(e.ctx, dbLookup{q: generated.New(e.db)}, Input{
		Mapping: m, Sync: withSync, Snapshot: snap, StorageConfigured: true,
		Workspaces: []asana.Workspace{{GID: snap.WorkspaceGID, Name: "Acme"}},
	})
	require.NoError(t, err)
	require.Empty(t, errorTexts(p.Errors))
	return p
}

// TestWriteCreatesThroughKits imports a project into an empty workspace and
// checks that every entity lands with its source identity, actors, events
// and audit rows.
func TestWriteCreatesThroughKits(t *testing.T) {
	e := newWriteEnv(t)
	ops := asana.User{GID: "100", Name: "Ops", Email: e.opsMail}
	hanako := asana.User{GID: "101", Name: "Hanako", Email: e.hanaMail}
	created := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	in900 := []asana.Membership{{Project: asana.Ref{GID: "900"}, Section: &asana.Ref{GID: "S1", Name: "Todo"}}}
	snap := &asana.Snapshot{
		WorkspaceGID: "1",
		Users:        []asana.User{ops, hanako},
		Tags:         []asana.Tag{{GID: "800", Name: "Bug"}},
		Projects:     []asana.Project{{GID: "900", Name: "Marketing " + e.suffix}},
		Mapped: map[string]*asana.ProjectSnapshot{"900": {
			Project:  asana.Project{GID: "900", Name: "Marketing " + e.suffix},
			Sections: []asana.Section{{GID: "S1", Name: "Todo"}},
			Order:    []string{"1", "2", "3"},
			Direct:   map[string]bool{"1": true, "3": true},
		}},
		Tasks: map[string]*asana.TaskDetail{
			"1": {
				Task: asana.Task{
					GID: "1", Name: "Parent", ResourceSubtype: "default_task", CreatedAt: created,
					CreatedBy: &hanako, Assignee: &hanako, Followers: []asana.User{ops},
					DueOn: "2026-12-10", Completed: true, Memberships: in900,
					Tags: []asana.Tag{{GID: "800", Name: "Bug"}},
				},
				Comments: []asana.Story{{
					GID: "500", ResourceSubtype: asana.StoryCommentAdded, HTMLText: "<body>Hello</body>",
					CreatedBy: &hanako, CreatedAt: created,
				}},
				Attachments: []asana.Attachment{{GID: "600", Name: "notes.txt", Host: "asana", DownloadURL: "https://files.invalid/600", Size: 5}},
			},
			"2": {Task: asana.Task{GID: "2", Name: "Child", ResourceSubtype: "default_task", CreatedAt: created, Parent: &asana.Ref{GID: "1"}}},
			"3": {Task: asana.Task{
				GID: "3", Name: "Blocked", ResourceSubtype: "default_task", CreatedAt: created,
				Memberships: []asana.Membership{{Project: asana.Ref{GID: "900"}}}, Dependencies: []asana.Ref{{GID: "1"}},
			}},
		},
	}
	m := &Mapping{
		Workspace: "aw-" + e.suffix,
		Importer:  e.opsMail,
		Projects:  []MappingProject{{Source: "900", Members: []MappingMember{{Email: e.hanaMail, Role: "editor"}}}},
	}
	p := e.prepare(t, m, snap, false)

	store := &memStore{objects: map[string][]byte{}}
	res, err := Write(e.ctx, p, WriteDeps{DB: e.db, Storage: store, Source: fakeFiles{"600": []byte("hello")}, TempDir: t.TempDir()})
	require.NoError(t, err)
	require.Empty(t, res.Failures)
	require.Zero(t, res.Failed())
	require.Equal(t, 1, res.ProjectsCreated)
	pr := res.Projects[0]
	require.True(t, pr.Created)
	require.Equal(t, 1, pr.MembersAdded)
	require.Equal(t, Counts{Imported: 3}, pr.Tasks)
	require.Equal(t, Counts{Imported: 1}, pr.Comments)
	require.Equal(t, Counts{Imported: 1}, pr.Attachments)
	require.Equal(t, Counts{Imported: 1}, pr.Dependencies)
	require.Zero(t, p.Projects[0].Notes.CompletionGate)

	var projectID uint32
	require.NoError(t, e.db.QueryRowContext(e.ctx,
		`SELECT id FROM projects WHERE workspace_id = ? AND slug = 'asana-900'`, e.wsID).Scan(&projectID))
	require.Equal(t, 1, e.count(t, `SELECT COUNT(*) FROM project_members WHERE project_id = ? AND user_id = ? AND role = 'lead'`, projectID, e.opsID))
	require.Equal(t, 1, e.count(t, `SELECT COUNT(*) FROM project_members WHERE project_id = ? AND user_id = ? AND role = 'editor'`, projectID, e.hanakoID))

	parentID, childID, blockedID := e.taskID(t, "1"), e.taskID(t, "2"), e.taskID(t, "3")
	require.Equal(t, 1, e.count(t, `SELECT COUNT(*) FROM tasks WHERE id = ? AND project_id = ? AND derived_state = 'done'
		AND created_by_user_id = ? AND due_on = '2026-12-10'`, parentID, projectID, e.hanakoID))
	require.Equal(t, 1, e.count(t, `SELECT COUNT(*) FROM tasks WHERE id = ? AND parent_task_id = ? AND created_by_user_id = ?`,
		childID, parentID, e.opsID))
	require.Equal(t, 1, e.count(t, `SELECT COUNT(*) FROM task_actors WHERE task_id = ? AND user_id = ? AND role = 'assignee' AND enabled = TRUE`, parentID, e.hanakoID))
	require.Equal(t, 1, e.count(t, `SELECT COUNT(*) FROM task_actors WHERE task_id = ? AND user_id = ? AND role = 'watcher' AND enabled = TRUE`, parentID, e.opsID))
	require.Equal(t, 1, e.count(t, `SELECT COUNT(*) FROM task_labels tl JOIN labels l ON l.id = tl.label_id
		WHERE tl.task_id = ? AND tl.enabled = TRUE AND l.name = 'Todo' AND l.project_id = ?`, parentID, projectID))
	require.Equal(t, 1, e.count(t, `SELECT COUNT(*) FROM task_labels tl JOIN labels l ON l.id = tl.label_id
		WHERE tl.task_id = ? AND tl.enabled = TRUE AND l.name = 'Bug' AND l.project_id IS NULL`, parentID))

	require.Equal(t, 1, e.count(t, `SELECT COUNT(*) FROM comments WHERE task_id = ? AND source_uid = ? AND author_id = ?
		AND body LIKE '%Hello%' AND DATE_FORMAT(created_at, '%Y-%m-%d %H:%i:%s') = '2026-01-02 03:04:05'`,
		parentID, SourceUID("500"), e.hanakoID))

	var key string
	require.NoError(t, e.db.QueryRowContext(e.ctx, `SELECT so.storage_key FROM attachments a JOIN storage_objects so ON so.id = a.storage_object_id
		WHERE a.task_id = ? AND a.source_uid = ? AND a.uploader_id = ? AND so.uploaded_at IS NOT NULL`,
		parentID, SourceUID("600"), e.hanakoID).Scan(&key))
	require.Equal(t, []byte("hello"), store.objects[key])

	require.Equal(t, 1, e.count(t, `SELECT COUNT(*) FROM task_dependencies WHERE from_task_id = ? AND to_task_id = ? AND kind = 'blocks' AND enabled = TRUE`,
		parentID, blockedID))

	require.Equal(t, 3, e.count(t, `SELECT COUNT(*) FROM events WHERE workspace_id = ? AND type = ? AND actor_user_id = ?`,
		e.wsID, string(eventbus.TaskCreated), e.opsID))
	for action, want := range map[string]int{
		"project.create": 1, "task.create": 3, "label.create": 2, "task.label.add": 2, "task.transition": 1,
		"comment.create": 1, "attachment.create": 1, "task.dependency.add": 1,
	} {
		require.Equal(t, want, e.count(t, `SELECT COUNT(*) FROM audit_logs WHERE workspace_id = ? AND action = ? AND actor_user_id = ?`,
			e.wsID, action, e.opsID), action)
	}
}

// TestWriteCompletionGateKeepsTx syncs a review-gated task that Asana
// completed: the refused completion is counted as a note and the title
// change in the same transaction still commits.
func TestWriteCompletionGateKeepsTx(t *testing.T) {
	e := newWriteEnv(t)
	name := "Gated " + e.suffix
	projectID := e.seed(t, `INSERT INTO projects (public_id, workspace_id, name, slug) VALUES (?, ?, ?, ?)`,
		types.New(), e.wsID, name, "gated-"+e.suffix)
	e.seed(t, `INSERT INTO project_members (public_id, workspace_id, project_id, user_id, role) VALUES (?, ?, ?, ?, 'lead')`,
		types.New(), e.wsID, projectID, e.opsID)
	taskID := e.seed(t, `INSERT INTO tasks (public_id, workspace_id, project_id, created_by_user_id, task_number, title, priority,
		visibility, source_uid, completion_policy) VALUES (?, ?, ?, ?, 1, 'Old title', 0, 'public', ?, 'review_required')`,
		types.New(), e.wsID, projectID, e.opsID, SourceUID("1"))

	snap := &asana.Snapshot{
		WorkspaceGID: "1",
		Projects:     []asana.Project{{GID: "900", Name: name}},
		Mapped: map[string]*asana.ProjectSnapshot{"900": {
			Project: asana.Project{GID: "900", Name: name},
			Order:   []string{"1"},
			Direct:  map[string]bool{"1": true},
		}},
		Tasks: map[string]*asana.TaskDetail{"1": {Task: asana.Task{
			GID: "1", Name: "New title", ResourceSubtype: "default_task", Completed: true,
			Memberships: []asana.Membership{{Project: asana.Ref{GID: "900"}}},
		}}},
	}
	m := &Mapping{Workspace: "aw-" + e.suffix, Importer: e.opsMail, Projects: []MappingProject{{Source: "900"}}}
	p := e.prepare(t, m, snap, true)
	require.Equal(t, ActionUpdate, p.Tasks[0].Action)

	res, err := Write(e.ctx, p, WriteDeps{DB: e.db})
	require.NoError(t, err)
	require.Empty(t, res.Failures)
	require.Zero(t, res.Failed())
	require.Equal(t, Counts{Updated: 1}, res.Projects[0].Tasks)
	require.Equal(t, 1, p.Projects[0].Notes.CompletionGate)
	require.Equal(t, 1, e.count(t, `SELECT COUNT(*) FROM tasks WHERE id = ? AND title = 'New title' AND derived_state = 'open'`, taskID))
	require.Equal(t, 1, e.count(t, `SELECT COUNT(*) FROM events WHERE task_id = ? AND type = 'task.updated'`, taskID))
	require.Zero(t, e.count(t, `SELECT COUNT(*) FROM audit_logs WHERE workspace_id = ? AND action = 'task.transition'`, e.wsID))
}
