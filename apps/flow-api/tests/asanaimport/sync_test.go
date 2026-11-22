package asanaimporttests

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/asanaimport"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/asanaimport/asana"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/eventbus"
	"github.com/libraz/nodate-flow/apps/flow-api/tests/helpers"
)

var syncOpts = runOpts{sync: true}

// changeRow is one pairwise case of what Asana changed on a synced task.
type changeRow struct {
	title, description, due, assignee, watchers, labels, completion string
}

// changeRows is `pict testdata/changes.pict /e:testdata/changes-seed.txt /r:7`;
// the seed row pins the all-unchanged case.
var changeRows = []changeRow{
	{"same", "same", "same", "same", "same", "same", "same"},
	{"changed", "changed", "changed", "removed", "added", "sectionChanged", "complete"},
	{"same", "same", "cleared", "changed", "removed", "tagAdded", "complete"},
	{"changed", "changed", "changed", "changed", "removed", "same", "reopen"},
	{"changed", "changed", "same", "same", "added", "tagAdded", "reopen"},
	{"same", "changed", "cleared", "removed", "same", "same", "complete"},
	{"changed", "same", "cleared", "changed", "same", "sectionChanged", "reopen"},
	{"same", "same", "changed", "removed", "same", "tagAdded", "same"},
	{"same", "same", "cleared", "changed", "added", "same", "same"},
	{"changed", "changed", "same", "changed", "removed", "tagRemoved", "same"},
	{"same", "same", "cleared", "same", "same", "tagRemoved", "complete"},
	{"same", "same", "changed", "removed", "added", "tagRemoved", "reopen"},
	{"same", "changed", "changed", "same", "removed", "sectionChanged", "same"},
	{"same", "changed", "same", "removed", "removed", "sectionChanged", "complete"},
}

// clearTasks empties the task side of the fake, keeping people, tags and projects.
func clearTasks(s *fakeState) {
	for _, g := range append([]string(nil), s.order...) {
		s.removeTask(g)
	}
	s.stories = map[string][]asana.Story{}
	s.files = map[string][]fakeFile{}
}

func (e *env) transition(t *testing.T, taskPub, transition string) {
	t.Helper()
	e.rest(t, "/tasks/"+taskPub+"/transitions", map[string]any{"transition": transition}, nil)
}

func TestAsanaSync(t *testing.T) {
	bootstrap(t)

	t.Run("changes", func(t *testing.T) {
		t.Parallel()
		e := newEnv(t)
		e.loadBase()
		hanako, taro, ops := e.hanakoUser(), e.taro(), e.ops()
		gid := func(i int) string { return fmt.Sprintf("%d", 2001+i) }
		e.fake.edit(func(s *fakeState) {
			clearTasks(s)
			for i, row := range changeRows {
				s.addTask(asana.Task{
					GID: gid(i), Name: fmt.Sprintf("Task %d", i+1), ResourceSubtype: "default_task",
					HTMLNotes: fmt.Sprintf("<body>Notes %d</body>", i+1), DueOn: "2026-12-10",
					Assignee: &hanako, Followers: []asana.User{taro}, CreatedBy: &hanako, CreatedAt: at("2026-01-02T03:04:05Z"),
					Memberships: in900(secTodo), Tags: []asana.Tag{tagBug}, Completed: row.completion == "reopen",
				})
			}
		})
		m := e.baseMapping()
		out := e.mustRun(t, m, runOpts{})
		requireRun(t, out, "Marketing", "tasks", fmt.Sprintf("imported=%d", len(changeRows)))
		transitions := e.audits(t, "task.transition")

		e.fake.edit(func(s *fakeState) {
			for i, row := range changeRows {
				task := s.tasks[gid(i)]
				if row.title == "changed" {
					task.Name = fmt.Sprintf("Task %d v2", i+1)
				}
				if row.description == "changed" {
					task.HTMLNotes = fmt.Sprintf("<body>Notes %d v2</body>", i+1)
				}
				switch row.due {
				case "changed":
					task.DueOn = "2026-12-20"
				case "cleared":
					task.DueOn = ""
				}
				switch row.assignee {
				case "changed":
					task.Assignee = &taro
				case "removed":
					task.Assignee = nil
				}
				switch row.watchers {
				case "added":
					task.Followers = []asana.User{taro, ops}
				case "removed":
					task.Followers = nil
				}
				switch row.labels {
				case "tagAdded":
					task.Tags = []asana.Tag{tagBug, tagDocs}
				case "sectionChanged":
					task.Memberships = in900(secDone)
				case "tagRemoved":
					task.Tags = nil
				}
				switch row.completion {
				case "complete":
					task.Completed = true
				case "reopen":
					task.Completed = false
				}
			}
		})
		out = e.mustRun(t, m, syncOpts)
		requireRun(t, out, "Marketing", "tasks", fmt.Sprintf("updated=%d duplicates=1", len(changeRows)-1))

		taroID := userIDByEmail(t, e.taroMail)
		ids := map[string]uint32{"hanako": e.hanakoID, "taro": taroID, "ops": e.opsID}
		wantTransitions := 0
		for i, row := range changeRows {
			name := fmt.Sprintf("row %d %+v", i+1, row)
			id := e.taskID(t, marketing, gid(i))
			want := taskRow{
				Title: fmt.Sprintf("Task %d", i+1), Description: fmt.Sprintf("Notes %d", i+1),
				State: "open", DueOn: "2026-12-10", CreatedBy: e.hanakoID, Enabled: true,
			}
			if row.title == "changed" {
				want.Title += " v2"
			}
			if row.description == "changed" {
				want.Description += " v2"
			}
			switch row.due {
			case "changed":
				want.DueOn = "2026-12-20"
			case "cleared":
				want.DueOn = ""
			}
			switch row.completion {
			case "complete":
				want.State = "done"
				wantTransitions++
			case "reopen":
				want.State = "waiting"
				wantTransitions++
			}
			require.Equal(t, want, readTask(t, id), name)

			assignee := map[string]string{"same": "hanako", "changed": "taro", "removed": ""}[row.assignee]
			followers := map[string][]string{"same": {"taro"}, "added": {"taro", "ops"}, "removed": nil}[row.watchers]
			wantActors := []string{}
			if assignee != "" {
				wantActors = append(wantActors, actor("assignee", ids[assignee]))
			}
			for _, f := range followers {
				if f != assignee {
					wantActors = append(wantActors, actor("watcher", ids[f]))
				}
			}
			require.ElementsMatch(t, wantActors, actors(t, id), name)

			wantLabels := map[string][]string{
				"same":           {"Bug", "Todo@" + marketing},
				"tagAdded":       {"Bug", "Docs", "Todo@" + marketing},
				"sectionChanged": {"Bug", "Done@" + marketing},
				"tagRemoved":     {"Todo@" + marketing},
			}[row.labels]
			require.ElementsMatch(t, wantLabels, labels(t, id), name)
		}
		require.Equal(t, transitions+wantTransitions, e.audits(t, "task.transition"))
	})

	t.Run("idempotent", func(t *testing.T) {
		t.Parallel()
		e := importedEnv(t)
		taro := e.taro()
		e.fake.edit(func(s *fakeState) {
			s.tasks["1001"].Name = "Write brief v2"
			s.tasks["1005"].Tags = []asana.Tag{tagDocs}
			s.stories["1003"] = append(s.stories["1003"], comment("5011", &taro, "<body>Shipped</body>", "2026-01-06T08:00:00Z"))
			s.removeTask("1002")
		})
		m := e.baseMapping()

		out := e.mustRun(t, m, syncOpts)
		requireRun(t, out, "Marketing", "tasks", "updated=2 duplicates=1 deleted=1 skipped=1")
		requireRun(t, out, "Marketing", "comments", "imported=1 duplicates=3 skipped=1")
		require.Equal(t, "Write brief v2", readTask(t, e.taskID(t, marketing, "1001")).Title)
		require.ElementsMatch(t, []string{"Docs"}, labels(t, e.taskID(t, marketing, "1005")))
		require.Equal(t, 1, count(t, `SELECT COUNT(*) FROM tasks WHERE workspace_id = ? AND source_uid = ? AND enabled = FALSE`,
			e.wsID, asanaimport.SourceUID("1002")))
		before := e.snapshot(t)
		e.fake.resetRequests()

		out = e.mustRun(t, m, syncOpts)
		require.Equal(t, `project "Marketing" (reused): members added=0 existing=2; `+
			`tasks imported=0 updated=0 duplicates=3 deleted=0 kept=0 moved=0 skipped=1 failed=0 rejected=0; `+
			`comments imported=0 updated=0 duplicates=4 deleted=0 kept=0 moved=0 skipped=1 failed=0 rejected=0; `+
			`attachments imported=0 updated=0 duplicates=1 deleted=0 kept=0 moved=0 skipped=1 failed=0 rejected=0; `+
			`dependencies imported=0 updated=0 duplicates=1 deleted=0 kept=0 moved=0 skipped=1 failed=0 rejected=0`,
			runLine(t, out, "Marketing"))
		require.Equal(t, before, e.snapshot(t), "a repeated sync writes no row, event or audit entry")
		require.Zero(t, e.fake.requests("probe-"), "nothing is left to probe")
	})

	t.Run("comments", func(t *testing.T) {
		t.Parallel()
		e := importedEnv(t)
		t1 := e.taskID(t, marketing, "1001")
		e.rest(t, "/tasks/"+e.taskPublic(t, marketing, "1001")+"/comments", map[string]any{"body": "local note"}, nil)
		taro := e.taro()
		e.fake.edit(func(s *fakeState) {
			s.story("1001", "5001").HTMLText = "<body>Looks great</body>"
			s.story("1001", "5002").HTMLText = "<body>From outside, edited</body>"
			s.stories["1001"] = append(s.stories["1001"], comment("5005", &taro, "<body>Taro here</body>", "2026-01-06T08:00:00Z"))
			s.removeStory("1003", "5010")
		})
		m := e.baseMapping()
		edited, removed, added := e.events(t, string(eventbus.TaskCommentEdited)), e.events(t, string(eventbus.TaskCommentRemoved)), e.events(t, string(eventbus.TaskCommentAdded))

		r := e.run(t, m, runOpts{dry: true, sync: true})
		require.Equalf(t, asanaimport.ExitOK, r.code, "report:\n%s", r.out)
		requireSyncPlan(t, r.out, "Marketing", "comments", "create=1 update=2 delete=1 synced=3")
		requireSyncPlan(t, r.out, "Marketing", "tasks", "unchanged=4 synced=4")

		out := e.mustRun(t, m, syncOpts)
		requireRun(t, out, "Marketing", "comments", "imported=1 updated=2 deleted=1 skipped=1")

		require.Equal(t, commentRow{Author: e.hanakoID, Body: "Looks great", At: "2026-01-03 10:00:00", Enabled: true}, e.comment(t, "5001"))
		require.Equal(t, commentRow{Author: e.opsID, Body: "**Ghost Writer:** From outside, edited", At: "2026-01-04 11:00:00", Enabled: true},
			e.comment(t, "5002"))
		require.Equal(t, commentRow{Author: userIDByEmail(t, e.taroMail), Body: "Taro here", At: "2026-01-06 08:00:00", Enabled: true},
			e.comment(t, "5005"))
		require.False(t, e.comment(t, "5010").Enabled, "a comment gone from Asana is removed")
		require.Equal(t, 1, count(t, `SELECT COUNT(*) FROM comments WHERE task_id = ? AND body = 'local note'
			AND source_uid IS NULL AND enabled = TRUE`, t1), "a local comment is untouched")

		require.Equal(t, edited+2, e.events(t, string(eventbus.TaskCommentEdited)))
		require.Equal(t, removed+1, e.events(t, string(eventbus.TaskCommentRemoved)))
		require.Equal(t, added+1, e.events(t, string(eventbus.TaskCommentAdded)))
		require.Equal(t, 1, e.audits(t, "comment.delete"))
		require.Equal(t, 2, e.fake.requests("probe-story:5010"), "probed once by the dry run and once by the run")
	})

	t.Run("deletes", func(t *testing.T) {
		t.Parallel()
		e := importedEnv(t)
		e.fake.edit(func(s *fakeState) { s.removeTask("1005") })
		m := e.baseMapping()

		out := e.mustRun(t, m, runOpts{})
		requireRun(t, out, "Marketing", "tasks", "duplicates=3 skipped=1")
		require.True(t, readTask(t, e.taskID(t, marketing, "1005")).Enabled, "without -sync nothing is deleted")
		require.Zero(t, e.fake.requests("probe-"))

		r := e.run(t, m, runOpts{dry: true, sync: true})
		require.Equalf(t, asanaimport.ExitOK, r.code, "report:\n%s", r.out)
		require.Equal(t, `project "Marketing": sync plan tasks create=0 update=0 unchanged=3 delete=1 moved=0 kept=0 synced=4 orphaned=0 repeated=0`,
			line(t, r.out, `project "Marketing": sync plan tasks `))

		out = e.mustRun(t, m, syncOpts)
		requireRun(t, out, "Marketing", "tasks", "duplicates=3 deleted=1 skipped=1")
		require.Equal(t, 1, count(t, `SELECT COUNT(*) FROM tasks WHERE workspace_id = ? AND source_uid = ? AND enabled = FALSE`,
			e.wsID, asanaimport.SourceUID("1005")))
		require.Equal(t, 1, e.events(t, string(eventbus.ItemDeleted)))
		require.Equal(t, 1, e.audits(t, "task.delete"))
		require.Equal(t, 2, e.fake.requests("probe-task:1005"), "probed once by the dry run and once by the run")
	})

	t.Run("left-project", func(t *testing.T) {
		t.Parallel()
		e := importedEnv(t)
		e.fake.edit(func(s *fakeState) {
			s.tasks["1003"].Memberships = []asana.Membership{{Project: asana.Ref{GID: "950", Name: "Archive"}}}
		})

		out := e.mustRun(t, e.baseMapping(), syncOpts)
		requireRun(t, out, "Marketing", "tasks", "duplicates=3 deleted=1 skipped=1")
		require.Equal(t, 1, e.fake.requests("probe-task:1003"), "the probe answers 200: the task lives outside the mapping")
		require.Equal(t, 1, count(t, `SELECT COUNT(*) FROM tasks WHERE workspace_id = ? AND source_uid = ? AND enabled = FALSE`,
			e.wsID, asanaimport.SourceUID("1003")))
		require.Equal(t, 1, e.audits(t, "task.delete"))
	})

	t.Run("forbidden-kept", func(t *testing.T) {
		t.Parallel()
		e := importedEnv(t)
		e.fake.edit(func(s *fakeState) {
			s.removeTask("1005")
			s.taskProbe["1005"] = http.StatusForbidden
			s.removeTask("1003")
			s.taskProbe["1003"] = http.StatusInternalServerError
			s.removeStory("1001", "5001")
			s.storyProbe["5001"] = http.StatusForbidden
			s.removeStory("1001", "5002")
			s.storyProbe["5002"] = http.StatusOK
		})
		m := e.baseMapping()

		r := e.run(t, m, runOpts{dry: true, sync: true})
		require.Equalf(t, asanaimport.ExitOK, r.code, "report:\n%s", r.out)
		requireSyncPlan(t, r.out, "Marketing", "tasks", "unchanged=2 kept=2 synced=4")
		requireSyncPlan(t, r.out, "Marketing", "comments", "kept=2 synced=2")

		out := e.mustRun(t, m, syncOpts)
		requireRun(t, out, "Marketing", "tasks", "duplicates=2 kept=2 skipped=1")
		requireRun(t, out, "Marketing", "comments", "kept=2 skipped=1")
		for _, gid := range []string{"1003", "1005"} {
			require.True(t, readTask(t, e.taskID(t, marketing, gid)).Enabled, gid)
		}
		for _, gid := range []string{"5001", "5002"} {
			require.True(t, e.comment(t, gid).Enabled, gid)
		}
		require.Zero(t, e.audits(t, "task.delete"))
		require.Zero(t, e.audits(t, "comment.delete"))
	})

	t.Run("moved", func(t *testing.T) {
		t.Parallel()
		e := importedEnv(t)
		hanako := e.hanakoUser()
		e.fake.edit(func(s *fakeState) {
			s.projects = append(s.projects, asana.Project{GID: "901", Name: "Sales"})
			s.sections["901"] = nil
			s.addTask(asana.Task{
				GID: "1101", Name: "Pitch deck", ResourceSubtype: "default_task", CreatedBy: &hanako,
				CreatedAt: at("2026-01-07T00:00:00Z"), Memberships: []asana.Membership{{Project: asana.Ref{GID: "901", Name: "Sales"}}},
			})
			t3 := s.tasks["1003"]
			t3.Memberships = []asana.Membership{{Project: asana.Ref{GID: "901", Name: "Sales"}}}
			t3.Dependencies = nil
		})
		m := e.baseMapping()
		m.Projects = append(m.Projects, asanaimport.MappingProject{
			Source: "901", Members: []asanaimport.MappingMember{{Email: e.hanako.Email, Role: "editor"}},
		})

		// Without -sync the move is half seen: neither side is touched, and
		// the moved task counts as a duplicate on the new side.
		out := e.mustRun(t, m, runOpts{})
		requireRun(t, out, "Sales", "tasks", "imported=1 duplicates=1")
		require.True(t, readTask(t, e.taskID(t, marketing, "1003")).Enabled)
		require.Zero(t, count(t, `SELECT COUNT(*) FROM tasks WHERE project_id = ? AND source_uid = ?`,
			e.projectID(t, sales), asanaimport.SourceUID("1003")))

		r := e.run(t, m, runOpts{dry: true, sync: true})
		require.Equalf(t, asanaimport.ExitOK, r.code, "report:\n%s", r.out)
		requireSyncPlan(t, r.out, "Marketing", "tasks", "unchanged=3 moved=1 synced=4")
		requireSyncPlan(t, r.out, "Sales", "tasks", "create=1 unchanged=1 synced=1")
		requireSyncPlan(t, r.out, "Sales", "comments", "create=1")

		out = e.mustRun(t, m, syncOpts)
		requireRun(t, out, "Marketing", "tasks", "duplicates=3 moved=1 skipped=1")
		requireRun(t, out, "Sales", "tasks", "imported=1 duplicates=1")
		require.Zero(t, e.fake.requests("probe-"), "a moved task is not probed")

		require.Equal(t, 1, count(t, `SELECT COUNT(*) FROM tasks WHERE project_id = ? AND source_uid = ? AND enabled = FALSE`,
			e.projectID(t, marketing), asanaimport.SourceUID("1003")))
		moved := e.taskID(t, sales, "1003")
		require.Equal(t, taskRow{Title: "Ship it", State: "done", CreatedBy: e.opsID, Enabled: true}, readTask(t, moved))
		require.ElementsMatch(t, []string{"Bug", "Docs"}, labels(t, moved))
		require.Equal(t, 1, count(t, `SELECT COUNT(*) FROM comments WHERE task_id = ? AND source_uid = ? AND author_id = ?
			AND body = 'Done here' AND enabled = TRUE`, moved, asanaimport.SourceUID("5010"), e.opsID))
	})

	t.Run("reopen", func(t *testing.T) {
		t.Parallel()
		e := importedEnv(t)
		t1 := e.taskID(t, marketing, "1001")
		e.transition(t, e.taskPublic(t, marketing, "1001"), "start")
		started := readTask(t, t1).State
		require.NotEqual(t, "open", started, "fixture: the local start moved the task")
		e.fake.edit(func(s *fakeState) {
			s.tasks["1002"].Completed = false
			s.tasks["1005"].Completed = true
		})

		out := e.mustRun(t, e.baseMapping(), syncOpts)
		requireRun(t, out, "Marketing", "tasks", "updated=2 duplicates=2 skipped=1")
		require.Equal(t, "waiting", readTask(t, e.taskID(t, marketing, "1002")).State, "reopened in Asana")
		require.Equal(t, "done", readTask(t, e.taskID(t, marketing, "1005")).State, "completed in Asana")
		require.Equal(t, started, readTask(t, t1).State, "a local start is not reverted")
		require.Equal(t, "done", readTask(t, e.taskID(t, marketing, "1003")).State)
	})

	t.Run("cancelled-kept", func(t *testing.T) {
		t.Parallel()
		e := importedEnv(t)
		for _, gid := range []string{"1001", "1005"} {
			e.transition(t, e.taskPublic(t, marketing, gid), "cancel")
		}
		e.fake.edit(func(s *fakeState) {
			s.tasks["1001"].Completed = true
			s.tasks["1001"].Name = "Write brief (final)"
		})

		out := e.mustRun(t, e.baseMapping(), syncOpts)
		requireRun(t, out, "Marketing", "tasks", "updated=1 duplicates=3 skipped=1")
		requireNotes(t, out, "Marketing", "cancelled_kept=2")
		t1 := readTask(t, e.taskID(t, marketing, "1001"))
		require.Equal(t, "cancelled", t1.State)
		require.Equal(t, "Write brief (final)", t1.Title)
		require.Equal(t, "cancelled", readTask(t, e.taskID(t, marketing, "1005")).State)
	})

	t.Run("local-labels-untouched", func(t *testing.T) {
		t.Parallel()
		e := importedEnv(t)
		t1pub := e.taskPublic(t, marketing, "1001")
		local := e.createLabel(t, "Local")
		e.rest(t, "/tasks/"+t1pub+"/labels", map[string]any{"labelId": local}, nil)
		e.rest(t, "/tasks/"+t1pub+"/labels", map[string]any{"labelId": e.labelPublic(t, "Docs")}, nil)
		e.fake.edit(func(s *fakeState) { s.tasks["1001"].Tags = nil })

		out := e.mustRun(t, e.baseMapping(), syncOpts)
		requireRun(t, out, "Marketing", "tasks", "updated=1 duplicates=3 skipped=1")
		requireNotes(t, out, "Marketing", "labels_left=1")
		require.ElementsMatch(t, []string{"Local", "Todo@" + marketing}, labels(t, e.taskID(t, marketing, "1001")))
		require.ElementsMatch(t, []string{"Bug", "Docs", "Done@" + marketing}, labels(t, e.taskID(t, marketing, "1003")))
	})

	t.Run("local-actors-untouched", func(t *testing.T) {
		t.Parallel()
		e := importedEnv(t)
		local := inviteMember(t, e.owner)
		localID := helpers.ResolveUserInternalID(t, testDB, local.UserPublicID)
		e.rest(t, "/projects/"+e.projectPublic(t, marketing)+"/members", map[string]any{"userId": local.UserPublicID, "role": "editor"}, nil)
		t1pub, t3pub := e.taskPublic(t, marketing, "1001"), e.taskPublic(t, marketing, "1003")
		for _, role := range []string{"watcher", "reviewer"} {
			e.rest(t, "/tasks/"+t1pub+"/actors", map[string]any{"userId": local.UserPublicID, "role": role}, nil)
		}
		e.rest(t, "/tasks/"+t3pub+"/actors", map[string]any{"userId": e.hanako.UserPublicID, "role": "watcher"}, nil)
		ops := e.ops()
		e.fake.edit(func(s *fakeState) { s.tasks["1001"].Followers = []asana.User{ops} })

		out := e.mustRun(t, e.baseMapping(), syncOpts)
		requireRun(t, out, "Marketing", "tasks", "updated=2 duplicates=2 skipped=1")
		require.ElementsMatch(t, []string{
			actor("assignee", e.hanakoID), actor("watcher", e.opsID), actor("watcher", localID), actor("reviewer", localID),
		}, actors(t, e.taskID(t, marketing, "1001")), "Taro left the Asana followers; the local actor stays")
		require.Empty(t, actors(t, e.taskID(t, marketing, "1003")), "a mapped person Asana does not list as follower is removed")
	})

	t.Run("dependencies", func(t *testing.T) {
		t.Parallel()
		e := importedEnv(t)
		t1, t2, t3, t5 := e.taskID(t, marketing, "1001"), e.taskID(t, marketing, "1002"), e.taskID(t, marketing, "1003"), e.taskID(t, marketing, "1005")
		t2pub, t5pub := e.taskPublic(t, marketing, "1002"), e.taskPublic(t, marketing, "1005")
		for _, kind := range []string{"blocks", "relates"} {
			e.rest(t, "/tasks/"+t2pub+"/dependencies", map[string]any{"toTaskId": t5pub, "kind": kind}, nil)
		}
		edge := func(from, to uint32, kind string) int {
			return count(t, `SELECT COUNT(*) FROM task_dependencies WHERE from_task_id = ? AND to_task_id = ? AND kind = ? AND enabled = TRUE`,
				from, to, kind)
		}
		between := func(a, b uint32, kind string) int { return edge(a, b, kind) + edge(b, a, kind) }
		require.Equal(t, 1, between(t2, t5, "blocks"), "fixture: the local blocks edge")
		require.Equal(t, 1, between(t2, t5, "relates"), "fixture: the local relates edge")
		e.fake.edit(func(s *fakeState) {
			s.tasks["1003"].Dependencies = []asana.Ref{{GID: "1999"}}
			s.tasks["1005"].Dependencies = []asana.Ref{{GID: "1001"}}
			s.tasks["1001"].Dependencies = []asana.Ref{{GID: "1005"}}
		})

		out := e.mustRun(t, e.baseMapping(), syncOpts)
		requireRun(t, out, "Marketing", "dependencies", "imported=1 deleted=2 skipped=2")
		require.Zero(t, edge(t1, t3, "blocks"), "Asana dropped the edge")
		require.Equal(t, 1, edge(t1, t5, "blocks"), "Asana added the edge")
		require.Zero(t, edge(t5, t1, "blocks"), "the reverse edge would close a cycle")
		require.Zero(t, between(t2, t5, "blocks"), "a blocks edge between imported tasks follows Asana")
		require.Equal(t, 1, between(t2, t5, "relates"), "other kinds are local")
		require.Equal(t, 2, e.events(t, string(eventbus.TaskDependencyRemoved)))
	})

	t.Run("attachments-added-only", func(t *testing.T) {
		t.Parallel()
		e := importedEnv(t)
		png := []byte("\x89PNG\r\n\x1a\nfake")
		e.fake.edit(func(s *fakeState) {
			s.files["1001"] = []fakeFile{
				{Attachment: asana.Attachment{GID: "6002", Name: "Spec", Host: "gdrive", ViewURL: "https://drive.example/spec"}},
				{Attachment: asana.Attachment{GID: "6004", Name: "diagram.png", Host: "asana"}, data: png},
			}
		})

		out := e.mustRun(t, e.baseMapping(), syncOpts)
		requireRun(t, out, "Marketing", "attachments", "imported=1 skipped=1")
		requireNotes(t, out, "Marketing", "attachments_removed_in_asana=1")
		t1 := e.taskID(t, marketing, "1001")
		require.Equal(t, 1, count(t, `SELECT COUNT(*) FROM attachments WHERE task_id = ? AND source_uid = ? AND enabled = TRUE`,
			t1, asanaimport.SourceUID("6001")), "a file removed in Asana stays")
		var key string
		require.NoError(t, testDB.QueryRow(`SELECT so.storage_key FROM attachments a JOIN storage_objects so ON so.id = a.storage_object_id
			WHERE a.task_id = ? AND a.source_uid = ? AND a.enabled = TRUE AND so.uploaded_at IS NOT NULL AND so.content_type = 'image/png'`,
			t1, asanaimport.SourceUID("6004")).Scan(&key))
		testStorage.MustExist(t, key)
	})

	t.Run("guard", func(t *testing.T) {
		t.Parallel()
		e := newEnv(t)
		ops := e.ops()
		task := func(gid, project string) asana.Task {
			return asana.Task{
				GID: gid, Name: "Item " + gid, ResourceSubtype: "default_task", CreatedBy: &ops, CreatedAt: at("2026-01-02T00:00:00Z"),
				Memberships: []asana.Membership{{Project: asana.Ref{GID: project}}},
			}
		}
		bulk := func(i int) string { return fmt.Sprintf("%d", 3001+i) }
		e.fake.edit(func(s *fakeState) {
			s.workspace = asana.Workspace{GID: "1", Name: "Acme Asana"}
			s.users = []asana.User{ops}
			s.projects = []asana.Project{{GID: "900", Name: "Bulk"}, {GID: "901", Name: "Pair"}}
			for i := range 12 {
				s.addTask(task(bulk(i), "900"))
			}
			s.addTask(task("3101", "901"))
			s.addTask(task("3102", "901"))
		})
		m := &asanaimport.Mapping{
			Workspace: e.owner.WorkspaceSlug, Importer: e.owner.Email,
			Projects: []asanaimport.MappingProject{{Source: "900"}, {Source: "901"}},
		}
		out := e.mustRun(t, m, runOpts{})
		requireRun(t, out, "Bulk", "tasks", "imported=12")
		requireRun(t, out, "Pair", "tasks", "imported=2")

		e.fake.edit(func(s *fakeState) {
			for i := range 11 {
				s.removeTask(bulk(i))
			}
			s.removeTask("3101")
			s.removeTask("3102")
		})
		e.fake.resetRequests()
		before := e.snapshot(t)
		for _, o := range []runOpts{{dry: true, sync: true}, syncOpts} {
			r := e.run(t, m, o)
			require.Equalf(t, asanaimport.ExitNoWrites, r.code, "%+v report:\n%s", o, r.out)
			require.Error(t, r.err)
			require.Equal(t, `refusing: project "Bulk" would delete 11 of 12 synced tasks`, line(t, r.out, `refusing: project "Bulk"`))
			require.Equal(t, `refusing: Asana returned no tasks for project "Pair" but 2 are synced`, line(t, r.out, `refusing: Asana returned no tasks`))
		}
		require.Zero(t, e.fake.requests("probe-"), "a refused project is not probed")
		require.Equal(t, before, e.snapshot(t), "a refused sync writes nothing")

		// Control: ten losses are below the guard and Pair is back.
		e.fake.edit(func(s *fakeState) {
			s.addTask(task(bulk(10), "900"))
			s.addTask(task("3101", "901"))
			s.addTask(task("3102", "901"))
		})
		out = e.mustRun(t, m, syncOpts)
		requireRun(t, out, "Bulk", "tasks", "duplicates=2 deleted=10")
		requireRun(t, out, "Pair", "tasks", "duplicates=2")
		require.Equal(t, 10, e.fake.requests("probe-task:"))
	})
}
