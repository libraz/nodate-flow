package asanaimport

import (
	"context"
	"database/sql"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/asanaimport/asana"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/taskstate"
)

type fakeProber struct {
	results map[string]asana.ProbeResult // missing gids answer Error
	calls   int
}

func (f *fakeProber) ProbeTask(_ context.Context, gid string) asana.ProbeResult {
	f.calls++
	return f.results[gid]
}

func (f *fakeProber) ProbeStory(_ context.Context, gid string) asana.ProbeResult {
	f.calls++
	return f.results[gid]
}

func nullID(id uint32) sql.NullInt32 { return sql.NullInt32{Int32: int32(id), Valid: true} } //#nosec G115 -- fixture ids are small positive values

func (w *world) comment(taskID, id uint32, gid string, author uint32, body string) {
	c := &ExistingComment{GID: gid}
	c.ID, c.TaskID, c.AuthorID, c.Body = id, nullID(taskID), nullID(author), body
	w.lk.ex.Comments[taskID] = append(w.lk.ex.Comments[taskID], c)
}

func (w *world) label(id, project uint32, name string) {
	row := generated.ListLabelsForImportRow{ID: id, Name: name, PublicID: types.New()}
	if project != 0 {
		row.ProjectID = nullID(project)
	}
	w.lk.labelRows = append(w.lk.labelRows, row)
}

func taskGIDs(p *Plan) []string {
	var out []string
	for _, tp := range p.Tasks {
		out = append(out, tp.GID)
	}
	return out
}

func TestPlacement(t *testing.T) {
	cases := []struct {
		name  string
		sync  bool
		setup func(w *world)
		want  map[string]int
		order []string
		check func(t *testing.T, p *Plan)
	}{
		{
			name:  "first mapped listing wins",
			setup: func(w *world) { w.list("900", true, "1"); w.list("901", true, "1") },
			want:  map[string]int{"1": 0},
		},
		{
			name: "an existing row stays where it is still listed",
			setup: func(w *world) {
				w.reuse("Sales", 11)
				w.row("1", 11, 500)
				w.list("900", true, "1")
				w.list("901", true, "1")
			},
			want: map[string]int{"1": 1},
			check: func(t *testing.T, p *Plan) {
				assert.Equal(t, ActionUnchanged, p.Task("1").Action)
				assert.False(t, p.Task("1").MidMove)
			},
		},
		{
			name: "a subtask follows its parent",
			setup: func(w *world) {
				w.define("2", parent("1"))
				w.list("900", true, "2")
				w.list("901", true, "1")
				w.list("901", false, "2")
			},
			want:  map[string]int{"1": 1, "2": 1},
			order: []string{"1", "2"},
			check: func(t *testing.T, p *Plan) { assert.Equal(t, "1", p.Task("2").ParentGID) },
		},
		{
			name: "a subtask whose parent is outside the mapping enters at the top",
			setup: func(w *world) {
				w.define("2", parent("99"))
				w.list("900", true, "2")
			},
			want: map[string]int{"2": 0},
			check: func(t *testing.T, p *Plan) {
				assert.Equal(t, ActionCreate, p.Task("2").Action)
				assert.Empty(t, p.Task("2").ParentGID)
			},
		},
		{
			name: "a child listed before its parent is planned after it",
			setup: func(w *world) {
				w.define("2", parent("1"))
				w.list("900", true, "2", "1")
			},
			want:  map[string]int{"1": 0, "2": 0},
			order: []string{"1", "2"},
			check: func(t *testing.T, p *Plan) { assert.Equal(t, "1", p.Task("2").ParentGID) },
		},
		{
			name: "the child of a skipped parent is skipped",
			setup: func(w *world) {
				w.define("1", func(d *asana.TaskDetail) { d.Task.Name = " " })
				w.define("2", parent("1"))
				w.list("900", true, "1")
				w.list("900", false, "2")
			},
			want: map[string]int{"1": 0, "2": 0},
			check: func(t *testing.T, p *Plan) {
				assert.Equal(t, SkipEmptyTitle, p.Task("1").Skip)
				assert.Equal(t, ActionSkip, p.Task("2").Action)
				assert.Equal(t, SkipParentNotImported, p.Task("2").Skip)
				assert.Equal(t, 1, p.Projects[0].Stats.Skipped[SkipParentNotImported])
			},
		},
		{
			name: "a move without -sync leaves the old row",
			setup: func(w *world) {
				w.reuse("Sales", 11)
				w.row("1", 11, 500)
				w.list("900", true, "1")
			},
			want: map[string]int{"1": 0},
			check: func(t *testing.T, p *Plan) {
				tp := p.Task("1")
				assert.Equal(t, ActionUnchanged, tp.Action)
				assert.True(t, tp.MidMove)
				assert.Equal(t, uint32(500), tp.Row.ID)
				assert.Empty(t, p.TaskDeletions)
			},
		},
		{
			name: "a move with -sync recreates the task and deletes the old row",
			sync: true,
			setup: func(w *world) {
				w.reuse("Sales", 11)
				w.row("1", 11, 500)
				w.list("900", true, "1")
			},
			want: map[string]int{"1": 0},
			check: func(t *testing.T, p *Plan) {
				assert.Equal(t, ActionCreate, p.Task("1").Action)
				require.Len(t, p.TaskDeletions, 1)
				d := p.TaskDeletions[0]
				assert.True(t, d.Moved && d.Candidate && d.Delete)
				assert.Equal(t, 1, d.Project)
				assert.Equal(t, 1, p.TaskSync(1).Moved)
				assert.Equal(t, 1, p.TaskSync(0).Create)
				assert.Equal(t, GuardInput{Present: 0, Synced: 1, Delete: 1, Moved: 1}, p.GuardInput(1))
				assert.Empty(t, p.Refusals())
			},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w := newWorld()
			w.in.Sync = c.sync
			c.setup(w)
			p := w.plan(t)
			got := map[string]int{}
			for _, tp := range p.Tasks {
				got[tp.GID] = tp.Project
			}
			assert.Equal(t, c.want, got)
			if c.order != nil {
				assert.Equal(t, c.order, taskGIDs(p))
			}
			if c.check != nil {
				c.check(t, p)
			}
		})
	}
}

func TestPlanSync(t *testing.T) {
	// syncWorld reuses Marketing as project 10 with task 1 imported as row 500.
	syncWorld := func(sync bool) (*world, *ExistingTask) {
		w := newWorld()
		w.in.Sync = sync
		w.reuse("Marketing", 10)
		r := w.row("1", 10, 500)
		w.list("900", true, "1")
		return w, r
	}

	t.Run("task fields, completion and actors", func(t *testing.T) {
		// Pairwise over flow state × Asana completed × title × actor change.
		rows := []struct {
			state     generated.TasksDerivedState
			completed bool
			title     string
			actors    string
		}{
			{"review", false, "changed", "remove"},
			{"cancelled", true, "changed", "same"},
			{"waiting", true, "same", "add"},
			{"open", true, "same", "remove"},
			{"done", false, "same", "same"},
			{"review", true, "same", "add"},
			{"done", true, "changed", "add"},
			{"cancelled", true, "same", "remove"},
			{"cancelled", false, "same", "add"},
			{"waiting", true, "same", "same"},
			{"done", true, "same", "remove"},
			{"open", false, "changed", "add"},
			{"open", true, "same", "same"},
			{"waiting", false, "changed", "add"},
			{"waiting", false, "changed", "remove"},
			{"review", false, "changed", "same"},
		}
		for _, r := range rows {
			t.Run(string(r.state)+"/"+r.title+"/"+r.actors, func(t *testing.T) {
				w, row := syncWorld(true)
				row.DerivedState = r.state
				w.define("1", func(d *asana.TaskDetail) {
					d.Task.Completed = r.completed
					if r.title == "changed" {
						d.Task.Name = "New"
					}
					if r.actors != "remove" {
						d.Task.Assignee = &asana.User{GID: "101", Name: "Hanako"}
					}
				})
				switch r.actors {
				case "same":
					w.lk.ex.Actors[500] = []generated.ListTaskActorsForTasksRow{{TaskID: 500, UserID: nullID(2), Role: generated.TaskActorsRoleAssignee}}
				case "remove":
					w.lk.ex.Actors[500] = []generated.ListTaskActorsForTasksRow{
						{TaskID: 500, UserID: nullID(2), Role: generated.TaskActorsRoleAssignee},
						{TaskID: 500, UserID: nullID(2), Role: generated.TaskActorsRoleReviewer},
						{TaskID: 500, UserID: nullID(9), Role: generated.TaskActorsRoleWatcher},
					}
				}
				p := w.plan(t)
				tp := p.Task("1")

				want := ""
				switch {
				case r.state == generated.TasksDerivedStateCancelled:
				case r.state == generated.TasksDerivedStateDone && !r.completed:
					want = taskstate.TransitionReopen
				case r.state != generated.TasksDerivedStateDone && r.completed:
					want = taskstate.TransitionComplete
				}
				assert.Equal(t, want, tp.Change.Transition)
				if r.state == generated.TasksDerivedStateCancelled {
					assert.Equal(t, 1, p.Projects[0].Notes.CancelledKept)
				}
				if r.title == "changed" {
					require.NotNil(t, tp.Change.Title)
					assert.Equal(t, "New", *tp.Change.Title)
				} else {
					assert.Nil(t, tp.Change.Title)
				}
				switch r.actors {
				case "same":
					assert.Empty(t, tp.Change.AddActors)
					assert.Empty(t, tp.Change.RemoveActors)
				case "add":
					assert.Equal(t, []ActorRef{{"hanako@example.com", generated.TaskActorsRoleAssignee}}, tp.Change.AddActors)
				case "remove":
					assert.Equal(t, []ActorRow{{2, generated.TaskActorsRoleAssignee}}, tp.Change.RemoveActors)
				}
				changed := want != "" || r.title == "changed" || r.actors != "same"
				assert.Equal(t, changed, tp.Action == ActionUpdate)
				assert.Equal(t, !changed, tp.Action == ActionUnchanged)
			})
		}
	})

	t.Run("without -sync an imported task is left alone", func(t *testing.T) {
		w, row := syncWorld(false)
		row.DerivedState = generated.TasksDerivedStateDone
		w.define("1", func(d *asana.TaskDetail) { d.Task.Name = "New" })
		tp := w.plan(t).Task("1")
		assert.Equal(t, ActionUnchanged, tp.Action)
		assert.True(t, tp.Change.empty())
	})

	t.Run("comment bodies and the unresolved-author prefix", func(t *testing.T) {
		// Pairwise over stored author × Asana author × body.
		for _, c := range []struct{ stored, author, body string }{
			{"other", "resolved", "changed"},
			{"importer", "resolved", "same"},
			{"importer", "resolved", "changed"},
			{"other", "unresolved", "same"},
			{"importer", "unresolved", "changed"},
		} {
			t.Run(c.stored+"/"+c.author+"/"+c.body, func(t *testing.T) {
				w, _ := syncWorld(true)
				by := &asana.User{GID: "101", Name: "Hanako"}
				if c.author == "unresolved" {
					by = &asana.User{GID: "103", Name: "Taro"}
				}
				w.define("1", func(d *asana.TaskDetail) {
					d.Comments = []asana.Story{{GID: "70", HTMLText: "<body>hello</body>", CreatedBy: by}}
				})
				want := "hello"
				if c.stored == "importer" && c.author == "unresolved" {
					want = "**Taro:** hello"
				}
				stored := want
				if c.body == "changed" {
					stored = "old"
				}
				author := uint32(2)
				if c.stored == "importer" {
					author = 1
				}
				w.comment(500, 1, "70", author, stored)
				p := w.plan(t)
				require.Len(t, p.Comments, 1)
				cp := p.Comments[0]
				assert.Equal(t, want, cp.Body)
				if c.body == "changed" {
					assert.Equal(t, ActionUpdate, cp.Action)
				} else {
					assert.Equal(t, ActionUnchanged, cp.Action)
				}
			})
		}
	})

	t.Run("comment deletions follow the probe", func(t *testing.T) {
		w, _ := syncWorld(true)
		w.comment(500, 1, "71", 1, "a")
		w.comment(500, 2, "72", 1, "b")
		w.comment(500, 3, "73", 1, "c")
		w.comment(500, 4, "74", 1, "d")
		p := w.plan(t)
		pr := &fakeProber{results: map[string]asana.ProbeResult{"71": asana.NotFound, "72": asana.Forbidden, "73": asana.Exists}}
		p.Probe(context.Background(), pr)
		want := map[string]bool{"71": true, "72": false, "73": false, "74": false}
		require.Len(t, p.CommentDeletions, 4)
		for _, d := range p.CommentDeletions {
			assert.Equal(t, want[d.Row.GID], d.Delete, d.Row.GID)
			assert.True(t, d.Probed)
		}
		assert.Equal(t, SyncCounts{Delete: 1, Kept: 3, Synced: 4}, p.CommentSync(0))
	})

	t.Run("task deletions follow the probe", func(t *testing.T) {
		w, _ := syncWorld(true)
		for i, g := range []string{"2", "3", "4", "5"} {
			w.row(g, 10, uint32(501+i))
		}
		w.lk.ex.SourceUIDCounts[10] += 2 // rows another writer tracked
		w.snap.Mapped["900"].Repeated = []asana.Repeat{{GID: "1"}, {GID: "1"}, {Parent: "1", GID: "9"}}
		p := w.plan(t)
		pr := &fakeProber{results: map[string]asana.ProbeResult{"2": asana.NotFound, "3": asana.Exists, "4": asana.Forbidden}}
		p.Probe(context.Background(), pr)
		want := map[string]bool{"2": true, "3": true, "4": false, "5": false}
		require.Len(t, p.TaskDeletions, 4)
		for _, d := range p.TaskDeletions {
			assert.Equal(t, want[d.Row.GID], d.Delete, d.Row.GID)
			assert.False(t, d.Moved)
		}
		assert.Equal(t, GuardInput{Present: 1, Synced: 5, Delete: 4}, p.GuardInput(0))
		assert.Equal(t, SyncCounts{Unchanged: 1, Delete: 2, Kept: 4, Synced: 5, Orphaned: 2, Repeated: 2}, p.TaskSync(0))
	})

	t.Run("the guard refuses before any probe", func(t *testing.T) {
		for _, c := range []struct {
			name   string
			listed bool
			rows   int
			want   Refusal
		}{
			{"mass delete", true, 11, Refusal{Project: 0, Name: "Marketing", Reason: RefuseMassDelete, Lost: 11, Synced: 12}},
			{"empty source", false, 0, Refusal{Project: 0, Name: "Marketing", Reason: RefuseEmptySource, Lost: 1, Synced: 1}},
		} {
			t.Run(c.name, func(t *testing.T) {
				w := newWorld()
				w.in.Sync = true
				w.reuse("Marketing", 10)
				w.row("1", 10, 500)
				if c.listed {
					w.list("900", true, "1")
				}
				for i := range c.rows {
					w.row("x"+string(rune('a'+i)), 10, uint32(600+i))
				}
				p := w.plan(t)
				assert.Equal(t, []Refusal{c.want}, p.Refusals())
				pr := &fakeProber{}
				p.Probe(context.Background(), pr)
				assert.Zero(t, pr.calls)
			})
		}
	})

	t.Run("labels", func(t *testing.T) {
		for _, c := range []struct {
			name       string
			tags       []string
			attach     []LabelRef
			newLabels  []LabelRef
			detach     []uint32
			labelsLeft int
		}{
			{"tags and sections align by id", []string{"feature"}, []LabelRef{{-1, "feature"}}, nil, []uint32{30}, 1},
			{"a tag with no label yet", []string{"Brand new", "Bug"}, []LabelRef{{-1, "Brand new"}}, []LabelRef{{-1, "Brand new"}}, nil, 1},
		} {
			t.Run(c.name, func(t *testing.T) {
				w, _ := syncWorld(true)
				w.label(30, 0, "Bug")
				w.label(31, 0, "Mine")
				w.label(32, 0, "Feature")
				w.label(33, 10, "Todo")
				w.snap.Tags = append(w.snap.Tags, asana.Tag{GID: "801", Name: "Feature"})
				w.lk.ex.Labels[500] = []uint32{30, 31, 33}
				w.define("1", func(d *asana.TaskDetail) {
					for _, tag := range c.tags {
						d.Task.Tags = append(d.Task.Tags, asana.Tag{Name: tag})
					}
					d.Task.Memberships = []asana.Membership{{Project: asana.Ref{GID: "900"}, Section: &asana.Ref{GID: "S1", Name: "Todo"}}}
				})
				p := w.plan(t)
				ch := p.Task("1").Change
				assert.Equal(t, c.attach, ch.AttachLabels)
				assert.Equal(t, c.detach, ch.DetachLabels)
				assert.Equal(t, c.newLabels, p.NewLabels)
				assert.Equal(t, c.labelsLeft, p.Projects[0].Notes.LabelsLeft)
			})
		}
	})

	t.Run("dependencies", func(t *testing.T) {
		type dep struct {
			from, to string
			action   Action
			skip     string
		}
		for _, c := range []struct {
			name    string
			sync    bool
			edges   map[string][]string // task -> the tasks it depends on
			rows    bool
			blocks  [][2]uint32
			want    []dep
			deleted int
		}{
			{
				name:  "create, outside the mapping and cycles",
				edges: map[string][]string{"1": {"99"}, "2": {"1", "2", "1"}, "3": {"4"}, "4": {"3"}},
				want: []dep{
					{"99", "1", ActionSkip, SkipOutsideMapping},
					{"1", "2", ActionCreate, ""},
					{"2", "2", ActionSkip, SkipCycle},
					{"4", "3", ActionSkip, SkipCycle},
					{"3", "4", ActionCreate, ""},
				},
			},
			{
				name:   "sync removes an edge Asana dropped",
				sync:   true,
				rows:   true,
				blocks: [][2]uint32{{500, 501}},
				want:   nil, deleted: 1,
			},
			{
				name:   "sync keeps an edge Asana still has",
				sync:   true,
				rows:   true,
				edges:  map[string][]string{"3": {"1"}},
				blocks: [][2]uint32{{500, 501}},
				want:   []dep{{"1", "3", ActionUnchanged, ""}},
			},
			{
				name:   "without -sync a dropped edge stays",
				rows:   true,
				blocks: [][2]uint32{{500, 501}},
			},
		} {
			t.Run(c.name, func(t *testing.T) {
				w := newWorld()
				w.in.Sync = c.sync
				if c.rows {
					w.reuse("Marketing", 10)
					w.row("1", 10, 500)
					w.row("3", 10, 501)
				}
				w.list("900", true, "1", "2", "3", "4")
				for task, deps := range c.edges {
					w.define(task, func(d *asana.TaskDetail) {
						for _, g := range deps {
							d.Task.Dependencies = append(d.Task.Dependencies, asana.Ref{GID: g})
						}
					})
				}
				for _, b := range c.blocks {
					w.lk.ex.Blocks = append(w.lk.ex.Blocks, generated.ListBlockingDependenciesForTasksRow{FromTaskID: b[0], ToTaskID: b[1]})
				}
				p := w.plan(t)
				var got []dep
				for _, d := range p.Dependencies {
					got = append(got, dep{d.BlockerGID, d.BlockedGID, d.Action, d.Skip})
				}
				assert.Equal(t, c.want, got)
				assert.Len(t, p.DependencyDeletions, c.deleted)
			})
		}
	})

	t.Run("attachments", func(t *testing.T) {
		w, _ := syncWorld(false)
		uploaded := sql.NullTime{Valid: true}
		for _, a := range []struct {
			gid string
			up  sql.NullTime
		}{{"60", uploaded}, {"61", sql.NullTime{}}, {"64", uploaded}} {
			ea := &ExistingAttachment{GID: a.gid}
			ea.TaskID, ea.UploadedAt = nullID(500), a.up
			w.lk.ex.Attachments[500] = append(w.lk.ex.Attachments[500], ea)
		}
		w.define("1", func(d *asana.TaskDetail) {
			d.Task.CreatedBy = &asana.User{GID: "101", Name: "Hanako"}
			for _, a := range []struct {
				gid  string
				size int64
			}{{"60", 1}, {"61", 1}, {"62", 1}, {"63", 1 << 40}} {
				d.Attachments = append(d.Attachments, asana.Attachment{GID: a.gid, Name: "f.png", Host: "asana", DownloadURL: "https://x", Size: a.size})
			}
		})
		p := w.plan(t)
		want := map[string]struct {
			action Action
			refill bool
		}{"60": {ActionUnchanged, true}, "61": {ActionCreate, true}, "62": {ActionCreate, false}, "63": {ActionSkip, false}}
		require.Len(t, p.Attachments, 4)
		for _, a := range p.Attachments {
			assert.Equal(t, want[a.GID].action, a.Action, a.GID)
			assert.Equal(t, want[a.GID].refill, a.Row != nil, a.GID)
			if a.Action == ActionCreate {
				assert.Equal(t, "hanako@example.com", a.Uploader)
			}
		}
		assert.Equal(t, 1, p.Projects[0].Notes.AttachmentsRemovedInAsana)
		assert.Equal(t, 1, p.Projects[0].Stats.Skipped[SkipTooLarge])
	})

	t.Run("set custom field values are counted as not imported", func(t *testing.T) {
		w, _ := syncWorld(false)
		set, blank := "High", ""
		w.define("1", func(d *asana.TaskDetail) {
			d.Task.CustomFields = []asana.CustomField{{GID: "c1", DisplayValue: &set}, {GID: "c2"}, {GID: "c3", DisplayValue: &blank}}
		})
		w.list("900", true, "2")
		w.define("2", func(d *asana.TaskDetail) { d.Task.CustomFields = []asana.CustomField{{GID: "c1", DisplayValue: &set}} })
		assert.Equal(t, 2, w.plan(t).Projects[0].Stats.CustomFields)
	})

	t.Run("a parent change is noted, never applied", func(t *testing.T) {
		w, _ := syncWorld(true)
		r := w.row("2", 10, 501)
		r.ParentTaskID = nullID(500)
		w.list("900", true, "2")
		p := w.plan(t)
		assert.Equal(t, 1, p.Projects[0].Notes.ParentChanged)
		assert.Equal(t, ActionUnchanged, p.Task("2").Action)
	})
}
