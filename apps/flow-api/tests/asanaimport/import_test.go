package asanaimporttests

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/minio/minio-go/v7"
	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/asanaimport"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/eventbus"
)

// requireTotal checks one segment of the "total:" line; entity "" checks
// the projects_created / users_created tail.
func requireTotal(t *testing.T, out, entity, want string) {
	t.Helper()
	total := line(t, out, "total: ")
	if entity == "" {
		_, tail, ok := strings.Cut(total, "; projects_created=")
		require.Truef(t, ok, "no projects_created in %q", total)
		requireKV(t, "projects_created="+tail, want)
		return
	}
	requireKV(t, segment(t, strings.TrimPrefix(total, "total: "), entity), want)
}

func TestAsanaImport(t *testing.T) {
	bootstrap(t)

	t.Run("dry-run", func(t *testing.T) {
		t.Parallel()
		e := newEnv(t)
		e.loadBase()
		before := e.snapshot(t)

		r := e.run(t, e.baseMapping(), runOpts{dry: true})
		require.NoError(t, r.err)
		require.Equal(t, asanaimport.ExitOK, r.code)

		golden, err := os.ReadFile("testdata/dry-run.txt")
		require.NoError(t, err)
		want := strings.NewReplacer(
			"{{OPS}}", strings.ToLower(e.owner.Email),
			"{{HANAKO}}", e.hanako.Email,
			"{{TARO}}", e.taroMail,
			"{{KENJI}}", e.kenjiMail,
		).Replace(string(golden))
		require.Equal(t, want, r.out)

		require.Equal(t, before, e.snapshot(t), "a dry run writes nothing")
		require.Zero(t, e.fake.requests("probe-"), "a dry run without -sync probes nothing")
		require.Zero(t, e.fake.requests("download:"), "a dry run downloads nothing")
	})

	t.Run("creates", func(t *testing.T) {
		t.Parallel()
		e := newEnv(t)
		e.loadBase()
		memberAdded := e.events(t, string(eventbus.WorkspaceMemberAdded))
		// The tenant fixture already created a project of its own.
		projectCreates := e.audits(t, "project.create")

		out := e.mustRun(t, e.baseMapping(), runOpts{})
		require.Equal(t, `project "Marketing" (created): members added=2 existing=0; `+
			`tasks imported=4 updated=0 duplicates=0 deleted=0 kept=0 moved=0 skipped=1 failed=0 rejected=0; `+
			`comments imported=3 updated=0 duplicates=0 deleted=0 kept=0 moved=0 skipped=1 failed=0 rejected=0; `+
			`attachments imported=1 updated=0 duplicates=0 deleted=0 kept=0 moved=0 skipped=1 failed=0 rejected=0; `+
			`dependencies imported=1 updated=0 duplicates=0 deleted=0 kept=0 moved=0 skipped=1 failed=0 rejected=0`,
			runLine(t, out, "Marketing"))
		requireNotes(t, out, "Marketing", "")
		require.NotContains(t, out, "failure:")
		requireTotal(t, out, "tasks", "imported=4 skipped=1")
		requireTotal(t, out, "", "projects_created=1 users_created=2")

		// Accounts: stub users, members, personal calendar, membership event.
		taroID, kenjiID := userIDByEmail(t, e.taroMail), userIDByEmail(t, e.kenjiMail)
		for name, id := range map[string]uint32{"Taro Suzuki": taroID, "Kenji Sato": kenjiID} {
			require.Equal(t, 1, count(t, `SELECT COUNT(*) FROM users u JOIN workspaces w ON w.id = ?
				WHERE u.id = ? AND u.display_name = ? AND u.locale = 'en' AND u.timezone = w.timezone AND u.enabled = TRUE`,
				e.wsID, id, name), name)
			require.Zero(t, count(t, `SELECT COUNT(*) FROM identities WHERE user_id = ?`, id), "%s has no credential", name)
			require.Equal(t, 1, count(t, `SELECT COUNT(*) FROM workspace_members
				WHERE workspace_id = ? AND user_id = ? AND role = 'member' AND enabled = TRUE`, e.wsID, id), name)
			require.Equal(t, 1, count(t, `SELECT COUNT(*) FROM calendars
				WHERE workspace_id = ? AND kind = 'personal' AND owner_user_id = ? AND enabled = TRUE`, e.wsID, id), name)
		}
		require.Equal(t, memberAdded+2, e.events(t, string(eventbus.WorkspaceMemberAdded)))
		require.Zero(t, count(t, `SELECT COUNT(*) FROM users WHERE email = ?`, e.ghostMail), "unmatched people get no account")

		// Project and members.
		pid := e.projectID(t, marketing)
		require.Equal(t, 1, count(t, `SELECT COUNT(*) FROM projects WHERE id = ? AND name = 'Marketing'`, pid))
		for _, m := range []struct {
			id   uint32
			role string
		}{{e.opsID, "lead"}, {e.hanakoID, "editor"}, {taroID, "editor"}} {
			require.Equal(t, 1, count(t, `SELECT COUNT(*) FROM project_members WHERE project_id = ? AND user_id = ? AND role = ? AND enabled = TRUE`,
				pid, m.id, m.role), m.role)
		}
		require.Equal(t, 3, count(t, `SELECT COUNT(*) FROM project_members WHERE project_id = ? AND enabled = TRUE`, pid))

		// The ordinary task: assignee, watchers, dates, labels, comments, file.
		t1, t2, t3, t5 := e.taskID(t, marketing, "1001"), e.taskID(t, marketing, "1002"), e.taskID(t, marketing, "1003"), e.taskID(t, marketing, "1005")
		require.Equal(t, taskRow{
			Title: "Write brief", Description: "Plan the **launch**\n\n---\n- [Spec](https://drive.example/spec)",
			State: "open", DueOn: "2026-12-10", StartedOn: "2026-12-01", CreatedBy: e.hanakoID, Enabled: true,
		}, readTask(t, t1))
		require.ElementsMatch(t, []string{actor("assignee", e.hanakoID), actor("watcher", e.opsID), actor("watcher", taroID)}, actors(t, t1))
		require.ElementsMatch(t, []string{"Bug", "Todo@" + marketing}, labels(t, t1))

		require.Equal(t, taskRow{Title: "Draft outline", State: "done", DueOn: "2026-12-05", CreatedBy: taroID, Parent: t1, Enabled: true},
			readTask(t, t2))
		require.ElementsMatch(t, []string{actor("assignee", taroID)}, actors(t, t2))

		require.Equal(t, taskRow{Title: "Ship it", State: "done", CreatedBy: e.opsID, Enabled: true}, readTask(t, t3))
		require.Empty(t, actors(t, t3), "a follower outside the project is no watcher")
		require.ElementsMatch(t, []string{"Bug", "Docs", "Done@" + marketing}, labels(t, t3))

		require.Equal(t, taskRow{Title: "Approve budget", State: "open", CreatedBy: e.opsID, Enabled: true}, readTask(t, t5))
		require.Empty(t, actors(t, t5), "an unmatched assignee is dropped")
		require.Empty(t, labels(t, t5), "a section mapped to no label")

		require.Zero(t, count(t, `SELECT COUNT(*) FROM tasks WHERE workspace_id = ? AND source_uid = ?`, e.wsID, asanaimport.SourceUID("1004")))
		require.Equal(t, 4, count(t, `SELECT COUNT(*) FROM tasks WHERE workspace_id = ? AND enabled = TRUE AND project_id = ?`, e.wsID, pid))

		require.Equal(t, commentRow{Author: e.hanakoID, Body: "Looks good", At: "2026-01-03 10:00:00", Enabled: true}, e.comment(t, "5001"))
		require.Equal(t, commentRow{Author: e.opsID, Body: "**Ghost Writer:** From outside", At: "2026-01-04 11:00:00", Enabled: true}, e.comment(t, "5002"))
		require.Equal(t, commentRow{Author: e.opsID, Body: "Done here", At: "2026-01-05 09:00:00", Enabled: true}, e.comment(t, "5010"))
		for _, gid := range []string{"5003", "5004"} {
			require.Zero(t, count(t, `SELECT COUNT(*) FROM comments WHERE workspace_id = ? AND source_uid = ?`, e.wsID, asanaimport.SourceUID(gid)), gid)
		}

		var key, filename, contentType string
		require.NoError(t, testDB.QueryRow(`SELECT so.storage_key, a.filename, so.content_type FROM attachments a
			JOIN storage_objects so ON so.id = a.storage_object_id
			WHERE a.task_id = ? AND a.source_uid = ? AND a.uploader_id = ? AND a.enabled = TRUE AND so.uploaded_at IS NOT NULL`,
			t1, asanaimport.SourceUID("6001"), e.hanakoID).Scan(&key, &filename, &contentType))
		require.Equal(t, "brief.pdf", filename)
		require.Equal(t, "application/pdf", contentType)
		obj, err := testStorage.Raw.GetObject(context.Background(), testStorage.Bucket, key, minio.GetObjectOptions{})
		require.NoError(t, err)
		body, err := io.ReadAll(obj)
		require.NoError(t, err)
		require.Equal(t, "%PDF-1.4 brief", string(body))
		require.Zero(t, count(t, `SELECT COUNT(*) FROM attachments WHERE workspace_id = ? AND source_uid = ?`, e.wsID, asanaimport.SourceUID("6003")),
			"an executable is not imported")
		require.Equal(t, 1, count(t, `SELECT COUNT(*) FROM attachments WHERE workspace_id = ?`, e.wsID))

		require.Equal(t, 1, count(t, `SELECT COUNT(*) FROM task_dependencies
			WHERE from_task_id = ? AND to_task_id = ? AND kind = 'blocks' AND enabled = TRUE`, t1, t3))
		require.Equal(t, 1, count(t, `SELECT COUNT(*) FROM task_dependencies WHERE workspace_id = ?`, e.wsID))

		require.Equal(t, 4, count(t, `SELECT COUNT(*) FROM events WHERE workspace_id = ? AND type = ? AND actor_user_id = ?
			AND JSON_UNQUOTE(JSON_EXTRACT(payload_json, '$.source')) = 'asana'`, e.wsID, string(eventbus.TaskCreated), e.opsID))
		for action, want := range map[string]int{
			"project.create": projectCreates + 1, "task.create": 4, "label.create": 4, "task.label.add": 5, "task.transition": 2,
			"comment.create": 3, "attachment.create": 1, "task.dependency.add": 1,
		} {
			require.Equal(t, want, e.audits(t, action), action)
		}
	})

	t.Run("second-run", func(t *testing.T) {
		t.Parallel()
		e := importedEnv(t)
		before := e.snapshot(t)

		out := e.mustRun(t, e.baseMapping(), runOpts{})
		require.Equal(t, `project "Marketing" (reused): members added=0 existing=2; `+
			`tasks imported=0 updated=0 duplicates=4 deleted=0 kept=0 moved=0 skipped=1 failed=0 rejected=0; `+
			`comments imported=0 updated=0 duplicates=3 deleted=0 kept=0 moved=0 skipped=1 failed=0 rejected=0; `+
			`attachments imported=0 updated=0 duplicates=1 deleted=0 kept=0 moved=0 skipped=1 failed=0 rejected=0; `+
			`dependencies imported=0 updated=0 duplicates=1 deleted=0 kept=0 moved=0 skipped=1 failed=0 rejected=0`,
			runLine(t, out, "Marketing"))
		requireTotal(t, out, "", "projects_created=0 users_created=0")
		require.Equal(t, before, e.snapshot(t), "a repeated run writes no row, event or audit entry")
		require.Zero(t, e.fake.requests("download:"), "an imported file is not downloaded again")
	})

	t.Run("partial-failure-then-resume", func(t *testing.T) {
		t.Parallel()
		e := newEnv(t)
		e.loadBase()
		e.fake.edit(func(s *fakeState) { s.downloadStatus["6001"] = http.StatusInternalServerError })

		r := e.run(t, e.baseMapping(), runOpts{})
		require.NoError(t, r.err, "a failed item is reported, not fatal")
		require.Equalf(t, asanaimport.ExitFailed, r.code, "report:\n%s", r.out)
		require.Equal(t, "failure: attachment 6001: asana: HTTP 500", line(t, r.out, "failure: "))
		requireRun(t, r.out, "Marketing", "attachments", "failed=1 skipped=1")
		requireRun(t, r.out, "Marketing", "tasks", "imported=4 skipped=1")
		requireRun(t, r.out, "Marketing", "comments", "imported=3 skipped=1")
		requireTotal(t, r.out, "attachments", "failed=1 skipped=1")
		t1 := e.taskID(t, marketing, "1001")
		require.Zero(t, count(t, `SELECT COUNT(*) FROM attachments a JOIN storage_objects so ON so.id = a.storage_object_id
			WHERE a.task_id = ? AND a.enabled = TRUE AND so.uploaded_at IS NOT NULL`, t1))

		e.fake.edit(func(s *fakeState) { delete(s.downloadStatus, "6001") })
		out := e.mustRun(t, e.baseMapping(), runOpts{})
		require.NotContains(t, out, "failure:")
		requireRun(t, out, "Marketing", "attachments", "imported=1 skipped=1")
		requireRun(t, out, "Marketing", "tasks", "duplicates=4 skipped=1")
		requireRun(t, out, "Marketing", "comments", "duplicates=3 skipped=1")

		var key string
		require.NoError(t, testDB.QueryRow(`SELECT so.storage_key FROM attachments a JOIN storage_objects so ON so.id = a.storage_object_id
			WHERE a.task_id = ? AND a.source_uid = ? AND a.enabled = TRUE AND so.uploaded_at IS NOT NULL`,
			t1, asanaimport.SourceUID("6001")).Scan(&key))
		testStorage.MustExist(t, key)
		require.Equal(t, 1, count(t, `SELECT COUNT(*) FROM attachments WHERE workspace_id = ? AND source_uid = ? AND enabled = TRUE`,
			e.wsID, asanaimport.SourceUID("6001")))
		require.Equal(t, 4, e.audits(t, "task.create"), "the resumed run creates no second task")
	})

	t.Run("users-open", func(t *testing.T) {
		t.Parallel()
		e := newEnv(t)
		e.loadBase()

		r := e.run(t, e.baseMapping(), runOpts{dry: true})
		require.Equal(t, asanaimport.ExitOK, r.code)
		require.Equal(t, "sign-in restriction: open", line(t, r.out, "sign-in restriction:"))

		out := e.mustRun(t, e.baseMapping(), runOpts{})
		requireTotal(t, out, "", "projects_created=1 users_created=2")
		require.Zero(t, count(t, `SELECT COUNT(*) FROM oauth_signin_allowlist WHERE entry_value IN (?, ?)`, e.taroMail, e.kenjiMail),
			"open sign-in needs no allowlist rows")
	})

	// Sequential: any enabled allowlist row restricts sign-in for the whole
	// instance, so no parallel subtest may register a user meanwhile.
	t.Run("users-restricted", func(t *testing.T) {
		e := newEnv(t)
		e.loadBase()
		t.Cleanup(func() {
			_, err := testDB.Exec(`DELETE FROM oauth_signin_allowlist WHERE entry_value IN (?, ?)`, e.taroMail, e.kenjiMail)
			require.NoError(t, err)
		})
		require.Zero(t, count(t, `SELECT COUNT(*) FROM oauth_signin_allowlist WHERE enabled = TRUE`), "fixture: the allowlist starts empty")
		opts := runOpts{domains: []string{"example.test"}}

		dry := opts
		dry.dry = true
		r := e.run(t, e.baseMapping(), dry)
		require.Equal(t, asanaimport.ExitOK, r.code)
		require.Equal(t, "sign-in restriction: domains=1 allowlist=0", line(t, r.out, "sign-in restriction:"))

		out := e.mustRun(t, e.baseMapping(), opts)
		requireTotal(t, out, "", "projects_created=1 users_created=2")
		for _, mail := range []string{e.taroMail, e.kenjiMail} {
			require.Equal(t, 1, count(t, `SELECT COUNT(*) FROM oauth_signin_allowlist WHERE entry_kind = 'email' AND entry_value = ?
				AND notes = 'created by importasana' AND enabled = TRUE AND added_by_user_id = ?`, mail, e.opsID), mail)
		}

		r = e.run(t, e.baseMapping(), dry)
		require.Equal(t, asanaimport.ExitOK, r.code)
		require.Equal(t, "sign-in restriction: domains=1 allowlist=2", line(t, r.out, "sign-in restriction:"))
		require.Equal(t, "users to create: none", line(t, r.out, "users to create:"))
	})

	t.Run("orphaned", func(t *testing.T) {
		t.Parallel()
		e := importedEnv(t)
		// Another importer's row in the same project: only reachable by SQL,
		// since no API writes a source_uid.
		orphan := e.createTask(t, e.projectPublic(t, marketing), "Local copy")
		_, err := testDB.Exec(`UPDATE tasks SET source_uid = 'csv:1001' WHERE public_id = UUID_TO_BIN(?, 0)`, orphan)
		require.NoError(t, err)

		r := e.run(t, e.baseMapping(), runOpts{dry: true, sync: true})
		require.Equalf(t, asanaimport.ExitOK, r.code, "report:\n%s", r.out)
		requireSyncPlan(t, r.out, "Marketing", "tasks", "unchanged=4 kept=1 synced=4 orphaned=1")

		out := e.mustRun(t, e.baseMapping(), runOpts{sync: true})
		requireRun(t, out, "Marketing", "tasks", "duplicates=4 kept=1 skipped=1")
		require.Equal(t, 1, count(t, `SELECT COUNT(*) FROM tasks WHERE public_id = UUID_TO_BIN(?, 0)
			AND title = 'Local copy' AND source_uid = 'csv:1001' AND enabled = TRUE`, orphan))
	})

	t.Run("locked", func(t *testing.T) {
		t.Parallel()
		e := newEnv(t)
		e.loadBase()
		ctx := context.Background()
		conn, err := testDB.Conn(ctx)
		require.NoError(t, err)
		defer conn.Close()
		lock := fmt.Sprintf("nodate-flow:importasana:%d", e.wsID)
		var got int
		require.NoError(t, conn.QueryRowContext(ctx, `SELECT GET_LOCK(?, 0)`, lock).Scan(&got))
		require.Equal(t, 1, got, "fixture: the test holds the run lock")
		before := e.snapshot(t)

		r := e.run(t, e.baseMapping(), runOpts{})
		require.Equal(t, asanaimport.ExitNoWrites, r.code)
		require.ErrorContains(t, r.err, "another importasana run holds the lock for this workspace")
		require.Zero(t, e.fake.requests(""), "a locked run does not read Asana")
		require.Equal(t, before, e.snapshot(t))

		require.NoError(t, conn.QueryRowContext(ctx, `SELECT RELEASE_LOCK(?)`, lock).Scan(&got))
		r = e.run(t, e.baseMapping(), runOpts{dry: true})
		require.NoError(t, r.err, "the run proceeds once the lock is free")
		require.Equal(t, asanaimport.ExitOK, r.code)
	})

	t.Run("refuses-bad-input", func(t *testing.T) {
		t.Parallel()
		e := newEnv(t)
		e.loadBase()
		before := e.snapshot(t)

		// Stage 1: the document alone is invalid; Asana is never read.
		m := e.baseMapping()
		m.Importer = "not-an-email"
		m.Projects[0].Slug = "Bad_Slug"
		m.Projects[0].Members[0].Role = "boss"
		r := e.run(t, m, runOpts{})
		require.Equal(t, asanaimport.ExitNoWrites, r.code)
		require.Error(t, r.err)
		require.Equal(t, "validation errors: 3\n"+
			`  - importer: "not-an-email" is not a valid ASCII email address`+"\n"+
			`  - projects[0].slug: "Bad_Slug" is not a valid slug`+"\n"+
			`  - projects[0].members[0].role: "boss" is not one of lead, editor, commenter, viewer`+"\n", r.out)
		require.Zero(t, e.fake.requests(""))

		// Stage 2: well-formed, but it disagrees with Asana and the database.
		newbie := "newbie+" + e.sfx + "@example.test"
		outsider := "outsider+" + e.sfx + "@example.test"
		m = e.baseMapping()
		m.Users = append(m.Users, asanaimport.MappingUser{Email: newbie, Name: "Newbie"})
		m.SourceUsers["999"] = e.hanako.Email
		m.Projects[0].Members = append([]asanaimport.MappingMember{{Email: outsider, Role: "editor"}}, m.Projects[0].Members...)
		m.Projects = append(m.Projects, asanaimport.MappingProject{Source: "Nope"})
		r = e.run(t, m, runOpts{})
		require.Equal(t, asanaimport.ExitNoWrites, r.code)
		require.Error(t, r.err)
		require.Equal(t, "validation errors: 3\n"+
			fmt.Sprintf("  - projects[0].members[0].email: %q is not in users and is not an existing account\n", outsider)+
			`  - source_users["999"]: is not a user of Asana workspace "Acme Asana"`+"\n"+
			"  - projects[1].source: matches no Asana project\n", r.out)

		require.Equal(t, before, e.snapshot(t), "a refused run writes nothing")
		require.Zero(t, count(t, `SELECT COUNT(*) FROM users WHERE email IN (?, ?)`, newbie, outsider), "not even the accounts")
	})

	// Sequential: the notification hook is process-wide.
	t.Run("no-fanout", func(t *testing.T) {
		e := newEnv(t)
		e.loadBase()
		var fired atomic.Int64
		handle := eventbus.AddNotifyHook(func(_ context.Context, ws uint32, _ string, _ uint64) {
			if ws == e.wsID {
				fired.Add(1)
			}
		})
		t.Cleanup(func() { eventbus.RemoveNotifyHook(handle) })
		eventbus.BridgeEventlog()
		eventsBefore := count(t, `SELECT COUNT(*) FROM events WHERE workspace_id = ?`, e.wsID)
		memberAdded := e.events(t, string(eventbus.WorkspaceMemberAdded))

		// Import creates accounts, members, tasks, comments and a file.
		e.mustRun(t, e.baseMapping(), runOpts{})
		// Sync deletes a task and edits a comment.
		e.fake.edit(func(s *fakeState) {
			s.removeTask("1005")
			s.story("1001", "5001").HTMLText = "<body>Looks great</body>"
		})
		out := e.mustRun(t, e.baseMapping(), runOpts{sync: true})
		requireRun(t, out, "Marketing", "tasks", "duplicates=3 deleted=1 skipped=1")
		requireRun(t, out, "Marketing", "comments", "updated=1 duplicates=2 skipped=1")

		require.Equal(t, memberAdded+2, e.events(t, string(eventbus.WorkspaceMemberAdded)))
		require.Equal(t, 4, e.events(t, string(eventbus.TaskCreated)))
		require.Equal(t, 1, e.events(t, string(eventbus.ItemDeleted)))
		require.Equal(t, 1, e.events(t, string(eventbus.TaskCommentEdited)))
		require.Greater(t, count(t, `SELECT COUNT(*) FROM events WHERE workspace_id = ?`, e.wsID), eventsBefore)
		require.Zero(t, fired.Load(), "an import or sync notifies nobody")

		// Control: an ordinary REST write in the same workspace does fan out.
		e.rest(t, "/tasks/"+e.taskPublic(t, marketing, "1001")+"/comments", map[string]any{"body": "after the import"}, nil)
		require.Positive(t, fired.Load(), "the hook observes ordinary writes")
	})
}
