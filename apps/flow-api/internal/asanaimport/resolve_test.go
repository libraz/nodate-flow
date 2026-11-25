package asanaimport

import (
	"context"
	"database/sql"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/asanaimport/asana"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated"
)

// fakeLookup is an in-memory deployment.
type fakeLookup struct {
	ws          *generated.FindWorkspaceBySlugRow
	users       []generated.ListUsersByEmailsForImportRow
	roles       map[uint32]generated.WorkspaceMembersRole
	projectRows []generated.ListProjectsForImportRow
	members     []generated.ListProjectMembersForImportRow
	labelRows   []generated.ListLabelsForImportRow
	allowlist   int64
	ex          *Existing
}

func (f *fakeLookup) workspace(_ context.Context, slug string) (generated.FindWorkspaceBySlugRow, bool, error) {
	if f.ws == nil || f.ws.Slug != slug {
		return generated.FindWorkspaceBySlugRow{}, false, nil
	}
	return *f.ws, true, nil
}

func (f *fakeLookup) usersByEmail(_ context.Context, emails []string) ([]generated.ListUsersByEmailsForImportRow, error) {
	var out []generated.ListUsersByEmailsForImportRow
	for _, u := range f.users {
		if slices.Contains(emails, u.Email) {
			out = append(out, u)
		}
	}
	return out, nil
}

func (f *fakeLookup) workspaceRole(_ context.Context, _, userID uint32) (generated.WorkspaceMembersRole, bool, error) {
	r, ok := f.roles[userID]
	return r, ok, nil
}

func (f *fakeLookup) projects(context.Context, uint32) ([]generated.ListProjectsForImportRow, error) {
	return f.projectRows, nil
}

func (f *fakeLookup) projectMembers(_ context.Context, _ uint32, ids []uint32) ([]generated.ListProjectMembersForImportRow, error) {
	var out []generated.ListProjectMembersForImportRow
	for _, m := range f.members {
		if slices.Contains(ids, m.ProjectID) {
			out = append(out, m)
		}
	}
	return out, nil
}

func (f *fakeLookup) labels(context.Context, uint32) ([]generated.ListLabelsForImportRow, error) {
	return f.labelRows, nil
}

func (f *fakeLookup) allowlistCount(context.Context) (int64, error) { return f.allowlist, nil }

func (f *fakeLookup) existing(context.Context, uint32) (*Existing, error) { return f.ex, nil }

// world is one deployment plus one Asana workspace.
type world struct {
	m    *Mapping
	in   Input
	lk   *fakeLookup
	snap *asana.Snapshot
}

func user(id uint32, email string, enabled bool) generated.ListUsersByEmailsForImportRow {
	return generated.ListUsersByEmailsForImportRow{ID: id, Email: email, DisplayName: email, Enabled: enabled}
}

// newWorld: accounts ops(1, owner), hanako(2), jiro(3), off(4, disabled),
// out(5, not a member), saburo(6). Asana users 100 Ops, 101 Hanako,
// 102 Jiro (no email), 103 Taro (no account), 104 Saburo.
func newWorld() *world {
	snap := &asana.Snapshot{
		WorkspaceGID: "1",
		Users: []asana.User{
			{GID: "100", Name: "Ops", Email: "ops@example.com"},
			{GID: "101", Name: "Hanako", Email: "Hanako@Example.com"},
			{GID: "102", Name: "Jiro"},
			{GID: "103", Name: "Taro", Email: "taro@example.com"},
			{GID: "104", Name: "Saburo", Email: "saburo@example.com"},
		},
		Tags: []asana.Tag{{GID: "800", Name: "Bug"}},
		Projects: []asana.Project{
			{GID: "900", Name: "Marketing"}, {GID: "901", Name: "Sales"},
			{GID: "902", Name: "Dup"}, {GID: "905", Name: "Dup"}, {GID: "903", Name: "Ops", Archived: true},
		},
		Mapped: map[string]*asana.ProjectSnapshot{
			"900": {Project: asana.Project{GID: "900", Name: "Marketing"}, Direct: map[string]bool{},
				Sections: []asana.Section{{GID: "S1", Name: "Todo"}, {GID: "S2", Name: "Untitled section"}}},
			"901": {Project: asana.Project{GID: "901", Name: "Sales"}, Direct: map[string]bool{},
				Sections: []asana.Section{{GID: "S3", Name: "Todo"}}},
		},
		Tasks: map[string]*asana.TaskDetail{},
	}
	m := &Mapping{
		Workspace: "acme",
		Importer:  "ops@example.com",
		Projects: []MappingProject{
			{Source: "900", Members: []MappingMember{{Email: "hanako@example.com", Role: "editor"}}},
			{Source: "901", Members: []MappingMember{{Email: "hanako@example.com", Role: "editor"}}},
		},
	}
	lk := &fakeLookup{
		ws: &generated.FindWorkspaceBySlugRow{ID: 1, Slug: "acme", Name: "Acme", Timezone: "Asia/Tokyo", Enabled: true},
		users: []generated.ListUsersByEmailsForImportRow{
			user(1, "ops@example.com", true), user(2, "hanako@example.com", true), user(3, "jiro@example.com", true),
			user(4, "off@example.com", false), user(5, "out@example.com", true), user(6, "saburo@example.com", true),
		},
		roles: map[uint32]generated.WorkspaceMembersRole{
			1: generated.WorkspaceMembersRoleOwner, 2: generated.WorkspaceMembersRoleMember,
			3: generated.WorkspaceMembersRoleMember, 4: generated.WorkspaceMembersRoleMember,
			6: generated.WorkspaceMembersRoleMember,
		},
		ex: emptyExisting(),
	}
	return &world{
		m:    m,
		lk:   lk,
		snap: snap,
		in: Input{
			Mapping: m, Workspaces: []asana.Workspace{{GID: "1", Name: "Acme"}},
			Snapshot: snap, StorageConfigured: true,
		},
	}
}

// define sets an Asana task; name defaults to "T<gid>".
func (w *world) define(gid string, mods ...func(*asana.TaskDetail)) *asana.TaskDetail {
	d := w.snap.Tasks[gid]
	if d == nil {
		d = &asana.TaskDetail{Task: asana.Task{GID: gid, Name: "T" + gid, ResourceSubtype: "default_task"}}
		w.snap.Tasks[gid] = d
	}
	for _, mod := range mods {
		mod(d)
	}
	return d
}

// list appends gids to a project's listing; direct marks them as listed by the project itself.
func (w *world) list(project string, direct bool, gids ...string) {
	ps := w.snap.Mapped[project]
	for _, g := range gids {
		w.define(g)
		ps.Order = append(ps.Order, g)
		if direct {
			ps.Direct[g] = true
		}
	}
}

func parent(gid string) func(*asana.TaskDetail) {
	return func(d *asana.TaskDetail) { d.Task.Parent = &asana.Ref{GID: gid} }
}

// reuse adds an enabled flow project the importer leads.
func (w *world) reuse(name string, id uint32) {
	w.lk.projectRows = append(w.lk.projectRows, generated.ListProjectsForImportRow{ID: id, Name: name, Slug: "p" + name, Enabled: true})
	w.lk.members = append(w.lk.members,
		generated.ListProjectMembersForImportRow{ProjectID: id, UserID: 1, Role: generated.ProjectMembersRoleLead})
}

// row adds a sourced task row; its title matches define's default.
func (w *world) row(gid string, project, id uint32) *ExistingTask {
	r := &ExistingTask{GID: gid}
	r.ID, r.ProjectID, r.Title = id, project, "T"+gid
	r.SourceUid = sql.NullString{String: SourceUID(gid), Valid: true}
	r.DerivedState = generated.TasksDerivedStateOpen
	w.lk.ex.Tasks = append(w.lk.ex.Tasks, r)
	w.lk.ex.SourceUIDCounts[project]++
	return r
}

func (w *world) prepare(t *testing.T) *Plan {
	t.Helper()
	p, err := Prepare(context.Background(), w.lk, w.in)
	require.NoError(t, err)
	return p
}

func (w *world) plan(t *testing.T) *Plan {
	t.Helper()
	p := w.prepare(t)
	require.Empty(t, p.Errors)
	return p
}

func errorTexts(errs []MappingError) []string {
	out := make([]string, len(errs))
	for i, e := range errs {
		out[i] = e.Error()
	}
	return out
}

func TestResolve(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(w *world)
		want   []string
	}{
		{"consistent mapping", func(*world) {}, nil},
		{"unknown workspace skips the database checks", func(w *world) {
			w.m.Workspace = "nope"
			w.m.Importer = "nobody@example.com"
		}, []string{`workspace: no workspace has slug "nope"`}},
		{"importer must be an owner or admin", func(w *world) {
			w.m.Importer = "jiro@example.com"
		}, []string{`importer: "jiro@example.com" is not an enabled owner or admin of workspace "acme"`}},
		{"importer that does not exist", func(w *world) {
			w.m.Importer = "ghost@example.com"
		}, []string{`importer: "ghost@example.com" is not an enabled owner or admin of workspace "acme"`}},
		{"source workspace matches nothing", func(w *world) {
			w.m.SourceWorkspace = "zzz"
		}, []string{`source_workspace: matches no Asana workspace`}},
		{"omitted source workspace with two visible", func(w *world) {
			w.in.Workspaces = append(w.in.Workspaces, asana.Workspace{GID: "2", Name: "Other"})
		}, []string{`source_workspace: matches more than one Asana workspace`}},
		{"source workspace by name", func(w *world) {
			w.m.SourceWorkspace = "Acme"
			w.in.Workspaces = append(w.in.Workspaces, asana.Workspace{GID: "2", Name: "Other"})
		}, nil},
		{"project source matches nothing", func(w *world) {
			w.m.Projects[1].Source = "nope"
		}, []string{`projects[1].source: matches no Asana project`}},
		{"project source matches two names", func(w *world) {
			w.m.Projects[1].Source = "Dup"
		}, []string{`projects[1].source: matches more than one Asana project`}},
		{"archived project by name", func(w *world) {
			w.m.Projects[1].Source = "Ops"
		}, nil},
		{"gid and name of one project", func(w *world) {
			w.m.Projects[1].Source = "Marketing"
		}, []string{`projects[1].source: resolves to the same Asana project as projects[0]`}},
		{"resolved names that collate equal", func(w *world) {
			w.m.Projects[1].Name = "MARKETING"
		}, []string{`projects[1].name: resolves to the same name as projects[0]`}},
		{"section key Asana does not have", func(w *world) {
			w.m.Projects[0].Sections = map[string]string{"Gone": "x", "Todo": ""}
		}, []string{`projects[0].sections["Gone"]: is not a section of Asana project "Marketing"`}},
		{"unknown address at its first reference", func(w *world) {
			w.m.SourceUsers = map[string]string{"102": "ghost@example.com"}
			w.m.Projects[0].Members = append(w.m.Projects[0].Members, MappingMember{Email: "ghost@example.com", Role: "viewer"})
		}, []string{`source_users["102"]: "ghost@example.com" is not in users and is not an existing account`}},
		{"disabled account", func(w *world) {
			w.m.Projects[0].Members[0].Email = "off@example.com"
		}, []string{`projects[0].members[0].email: "off@example.com" is a disabled account`}},
		{"existing user outside the workspace", func(w *world) {
			w.m.Users = []MappingUser{{Email: "out@example.com"}}
		}, []string{`users[0].email: "out@example.com" is an existing account that is not a member of workspace "acme"`}},
		{"member outside the workspace", func(w *world) {
			w.m.Projects[0].Members[0].Email = "out@example.com"
		}, []string{`projects[0].members[0].email: "out@example.com" is not a member of workspace "acme"`}},
		{"member created by the mapping", func(w *world) {
			w.m.Users = []MappingUser{{Email: "new@example.com"}}
			w.m.Projects[0].Members[0].Email = "New@example.com"
		}, nil},
		{"source user Asana does not have", func(w *world) {
			w.m.SourceUsers = map[string]string{"555": "jiro@example.com", "102": "jiro@example.com"}
		}, []string{`source_users["555"]: is not a user of Asana workspace "Acme"`}},
		{"source user who left, named only by a comment", func(w *world) {
			w.list("900", true, "1")
			w.define("1", func(d *asana.TaskDetail) {
				d.Comments = []asana.Story{{GID: "70", ResourceSubtype: asana.StoryCommentAdded, HTMLText: "<body>hi</body>", CreatedBy: &asana.User{GID: "555", Name: "Gone"}}}
			})
			w.m.SourceUsers = map[string]string{"555": "jiro@example.com"}
		}, nil},
		{"source user who left, named only as a follower", func(w *world) {
			w.list("900", true, "1")
			w.define("1", func(d *asana.TaskDetail) {
				d.Task.Followers = []asana.User{{GID: "555", Name: "Gone"}}
			})
			w.m.SourceUsers = map[string]string{"555": "jiro@example.com"}
		}, nil},
		{"two flow projects of that name", func(w *world) {
			w.reuse("Marketing", 10)
			w.reuse("marketing", 11)
		}, []string{`projects[0].name: matches more than one flow project`}},
		{"flow project led by someone else", func(w *world) {
			w.lk.projectRows = append(w.lk.projectRows, generated.ListProjectsForImportRow{ID: 10, Name: "Marketing", Slug: "m", Enabled: true})
			w.lk.members = append(w.lk.members, generated.ListProjectMembersForImportRow{ProjectID: 10, UserID: 2, Role: generated.ProjectMembersRoleLead})
		}, []string{`projects[0].name: matches flow project "Marketing" whose lead is not the importer`}},
		{"default slug taken", func(w *world) {
			w.lk.projectRows = append(w.lk.projectRows, generated.ListProjectsForImportRow{ID: 10, Name: "Other", Slug: "asana-900", Enabled: true})
		}, []string{`projects[0].slug: "asana-900" is already used in workspace "acme"`}},
		{"identifier taken, compared upper case", func(w *world) {
			w.m.Projects[0].Identifier = "mkt"
			w.lk.projectRows = append(w.lk.projectRows, generated.ListProjectsForImportRow{
				ID: 10, Name: "Other", Slug: "o", Identifier: sql.NullString{String: "MKT", Valid: true}, Enabled: true,
			})
		}, []string{`projects[0].identifier: "mkt" is already used in workspace "acme"`}},
		{"task already imported into an unlisted project", func(w *world) {
			w.list("900", true, "1")
			w.lk.projectRows = append(w.lk.projectRows, generated.ListProjectsForImportRow{ID: 77, Name: "Old", Slug: "old", Enabled: true})
			w.row("1", 77, 500)
		}, []string{`projects[0].source: task 1 is already imported into project "Old", which this mapping does not list`}},
		{"attachments without storage", func(w *world) {
			w.list("900", true, "1")
			w.define("1", func(d *asana.TaskDetail) {
				d.Attachments = []asana.Attachment{{GID: "60", Name: "a.png", Host: "asana", DownloadURL: "https://x", Size: 10}}
			})
			w.in.StorageConfigured = false
		}, []string{`projects[0].source: has 1 attachments but storage is not configured (NF_S3_ENDPOINT)`}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w := newWorld()
			c.mutate(w)
			p := w.prepare(t)
			assert.ElementsMatch(t, c.want, errorTexts(p.Errors))
		})
	}

	t.Run("creation, reuse and sign-in state", func(t *testing.T) {
		w := newWorld()
		w.m.Users = []MappingUser{{Email: "New@example.com"}, {Email: "hanako@example.com"}}
		w.m.Projects[0].Members = append(w.m.Projects[0].Members, MappingMember{Email: "new@example.com", Role: "viewer"})
		w.m.Projects[1].Slug, w.m.Projects[1].Identifier = "sales", "sl"
		w.reuse("Sales", 11)
		w.lk.members = append(w.lk.members, generated.ListProjectMembersForImportRow{ProjectID: 11, UserID: 2, Role: generated.ProjectMembersRoleEditor})
		w.in.AllowedDomains = []string{"example.com"}
		w.lk.allowlist = 3
		p := w.plan(t)

		assert.Equal(t, []MappingUser{{Email: "new@example.com", Name: "New"}}, p.NewUsers)
		assert.Equal(t, SignIn{Domains: 1, Allowlist: 3}, p.SignIn)
		assert.True(t, p.SignIn.Restricted())
		assert.Equal(t, uint32(1), p.Importer.ID)
		assert.Equal(t, "Asia/Tokyo", p.Location.String())

		mk, sales := p.Projects[0], p.Projects[1]
		assert.Equal(t, "Marketing", mk.Name)
		assert.Zero(t, mk.ExistingID)
		assert.Equal(t, "asana-900", mk.Slug)
		assert.Equal(t, []MemberPlan{
			{Email: "hanako@example.com", Role: generated.ProjectMembersRoleEditor},
			{Email: "new@example.com", Role: generated.ProjectMembersRoleViewer},
		}, mk.Members)
		assert.Equal(t, uint32(11), sales.ExistingID)
		assert.Equal(t, 1, sales.Notes.SlugIgnored)
		assert.Equal(t, 1, sales.Notes.IdentifierIgnored)
		assert.True(t, sales.Members[0].Existing)
	})

	t.Run("open sign-in", func(t *testing.T) {
		p := newWorld().plan(t)
		assert.False(t, p.SignIn.Restricted())
	})
}
