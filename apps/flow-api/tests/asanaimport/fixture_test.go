package asanaimporttests

import (
	"strings"
	"testing"
	"time"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/asanaimport"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/asanaimport/asana"
)

// Slugs of the projects the importer creates (asana-<gid>).
const (
	marketing = "asana-900"
	sales     = "asana-901"
)

func at(s string) time.Time {
	v, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return v
}

func ptr[T any](v T) *T { return &v }

// Asana people of the base fixture.
func (e *env) ops() asana.User {
	return asana.User{GID: "100", Name: "Ops Lead", Email: strings.ToUpper(e.owner.Email)}
}
func (e *env) hanakoUser() asana.User {
	// The Asana address differs from the flow one: source_users links them.
	return asana.User{GID: "101", Name: "Hanako Yamada", Email: "hanako+" + e.sfx + "@asana.example"}
}
func (e *env) taro() asana.User {
	return asana.User{GID: "102", Name: "Taro Suzuki", Email: e.taroMail}
}
func (e *env) ghost() asana.User {
	return asana.User{GID: "103", Name: "Ghost Writer", Email: e.ghostMail}
}
func (e *env) kenji() asana.User {
	return asana.User{GID: "104", Name: "Kenji Sato", Email: e.kenjiMail}
}

var (
	tagBug  = asana.Tag{GID: "800", Name: "Bug"}
	tagDocs = asana.Tag{GID: "801", Name: "Docs"}
	secTodo = &asana.Ref{GID: "S1", Name: "Todo"}
	secDone = &asana.Ref{GID: "S2", Name: "Done"}
	secNone = &asana.Ref{GID: "S3", Name: "Untitled section"}
)

func in900(section *asana.Ref) []asana.Membership {
	return []asana.Membership{{Project: asana.Ref{GID: "900", Name: "Marketing"}, Section: section}}
}

func comment(gid string, by *asana.User, html, created string) asana.Story {
	return asana.Story{GID: gid, ResourceSubtype: asana.StoryCommentAdded, HTMLText: html, CreatedBy: by, CreatedAt: at(created)}
}

// loadBase fills the fake Asana with the base fixture. Project 900
// "Marketing" holds:
//
//	1001 plain task: assignee, followers, dates, section, tag, comments, files
//	1002 completed subtask of 1001 with a due_at instant
//	1003 completed milestone blocked by 1001 and by 1999 (outside the mapping)
//	1004 blank title (skipped)
//	1005 approval by an Asana user with no flow account, with an .exe
func (e *env) loadBase() {
	ops, hanako, taro, ghost, kenji := e.ops(), e.hanakoUser(), e.taro(), e.ghost(), e.kenji()
	e.fake.edit(func(s *fakeState) {
		s.workspace = asana.Workspace{GID: "1", Name: "Acme Asana"}
		s.users = []asana.User{ops, hanako, taro, ghost, kenji}
		s.tags = []asana.Tag{tagBug, tagDocs}
		s.projects = []asana.Project{{GID: "900", Name: "Marketing"}, {GID: "950", Name: "Archive", Archived: true}}
		s.sections["900"] = []asana.Section{{GID: "S1", Name: "Todo"}, {GID: "S2", Name: "Done"}, {GID: "S3", Name: "Untitled section"}}
		s.sections["950"] = nil

		s.addTask(asana.Task{
			GID: "1001", Name: "Write brief", ResourceSubtype: "default_task",
			HTMLNotes: "<body>Plan the <strong>launch</strong></body>",
			Assignee:  &hanako, Followers: []asana.User{ops, taro}, CreatedBy: &hanako,
			CreatedAt: at("2026-01-02T03:04:05Z"), DueOn: "2026-12-10", StartOn: "2026-12-01",
			Memberships: in900(secTodo), Tags: []asana.Tag{tagBug},
			CustomFields: []asana.CustomField{{GID: "cf1", DisplayValue: ptr("High")}, {GID: "cf2"}},
		})
		s.stories["1001"] = []asana.Story{
			func() asana.Story {
				st := comment("5001", &hanako, "<body>Looks good</body>", "2026-01-03T10:00:00Z")
				st.NumLikes = 1
				return st
			}(),
			comment("5002", &ghost, "<body>From outside</body>", "2026-01-04T11:00:00Z"),
			{GID: "5003", ResourceSubtype: "assigned", HTMLText: "<body>assigned to you</body>", CreatedAt: at("2026-01-04T12:00:00Z"), NumLikes: 2},
			comment("5004", &hanako, "<body></body>", "2026-01-04T13:00:00Z"),
		}
		s.files["1001"] = []fakeFile{
			{Attachment: asana.Attachment{GID: "6001", Name: "brief.pdf", Host: "asana"}, data: []byte("%PDF-1.4 brief")},
			{Attachment: asana.Attachment{GID: "6002", Name: "Spec", Host: "gdrive", ViewURL: "https://drive.example/spec"}},
		}

		s.addTask(asana.Task{
			GID: "1002", Name: "Draft outline", ResourceSubtype: "default_task",
			Assignee: &taro, CreatedBy: &taro, CreatedAt: at("2026-01-02T04:00:00Z"),
			DueAt: ptr(at("2026-12-05T12:00:00Z")), Completed: true, Parent: &asana.Ref{GID: "1001", Name: "Write brief"},
		})

		s.addTask(asana.Task{
			GID: "1003", Name: "Ship it", ResourceSubtype: "milestone",
			Followers: []asana.User{kenji}, CreatedBy: &ops, CreatedAt: at("2026-01-02T05:00:00Z"),
			Completed: true, Memberships: in900(secDone), Tags: []asana.Tag{tagBug, tagDocs},
			Dependencies: []asana.Ref{{GID: "1001"}, {GID: "1999"}},
		})
		s.stories["1003"] = []asana.Story{comment("5010", &ops, "<body>Done here</body>", "2026-01-05T09:00:00Z")}

		s.addTask(asana.Task{
			GID: "1004", Name: "  ", ResourceSubtype: "default_task",
			CreatedAt: at("2026-01-02T06:00:00Z"), Memberships: in900(secTodo),
		})

		s.addTask(asana.Task{
			GID: "1005", Name: "Approve budget", ResourceSubtype: "approval",
			Assignee: &ghost, CreatedBy: &ghost, CreatedAt: at("2026-01-02T07:00:00Z"), Memberships: in900(secNone),
		})
		s.files["1005"] = []fakeFile{{Attachment: asana.Attachment{GID: "6003", Name: "tool.exe", Host: "asana"}, data: []byte("MZ")}}

		s.addTask(asana.Task{
			GID: "1999", Name: "Archived prerequisite", ResourceSubtype: "default_task",
			CreatedAt: at("2026-01-01T00:00:00Z"), Memberships: []asana.Membership{{Project: asana.Ref{GID: "950", Name: "Archive"}}},
		})
	})
}

// baseMapping maps project 900 with Hanako and Taro as editors and creates
// Taro and Kenji. Kenji is created but not a project member.
func (e *env) baseMapping() *asanaimport.Mapping {
	return &asanaimport.Mapping{
		Workspace: e.owner.WorkspaceSlug,
		Importer:  e.owner.Email,
		Users: []asanaimport.MappingUser{
			{Email: e.taroMail, Name: "Taro Suzuki"},
			{Email: e.kenjiMail, Name: "Kenji Sato"},
		},
		SourceUsers: map[string]string{"101": e.hanako.Email},
		Projects: []asanaimport.MappingProject{{
			Source:   "900",
			Members:  []asanaimport.MappingMember{{Email: e.hanako.Email, Role: "editor"}, {Email: e.taroMail, Role: "editor"}},
			Sections: map[string]string{"Untitled section": ""},
		}},
	}
}

// importedEnv is an env after a clean first run of the base fixture.
func importedEnv(t *testing.T) *env {
	t.Helper()
	e := newEnv(t)
	e.loadBase()
	e.mustRun(t, e.baseMapping(), runOpts{})
	e.fake.resetRequests()
	return e
}
