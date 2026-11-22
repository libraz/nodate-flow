package asanaimport

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/asanaimport/asana"
)

// TestRunRefusesBadInput: input problems end in exit code 2 before any
// request leaves for Asana. The nil database proves nothing could be written.
func TestRunRefusesBadInput(t *testing.T) {
	const token = "secret-pat-value"
	const valid = "workspace: acme\nimporter: ops@example.com\nprojects:\n  - source: \"900\"\n"
	cases := []struct {
		name    string
		token   string
		mapping string
		dryRun  bool
		out     []string // substrings of the report
		absent  []string // substrings the report must not hold
		errText string
	}{
		{name: "empty mapping", token: token, mapping: "", out: []string{"validation errors: 1", "mapping is empty"}, errText: "mapping has 1 error"},
		{name: "unknown key", token: token, mapping: "workspace: acme\nauthors: {}\n", out: []string{"validation errors: 1", "authors"}, errText: "mapping has 1 error"},
		{name: "missing token", token: "", mapping: valid, errText: "ASANA_TOKEN"},
		{
			name: "validation errors", token: token, mapping: "projects: []\n",
			out:     []string{"validation errors: ", "  - workspace: is required", "  - importer: is required", "projects: must list at least one project"},
			errText: "mapping has",
		},
		{
			name: "dry run lists no empty projects", token: token, mapping: "projects: []\n", dryRun: true,
			out:     []string{"  - workspace: is required", "  - importer: is required"},
			absent:  []string{"must list at least one"},
			errText: "mapping has",
		},
		{
			name: "invalid slug", token: token, errText: "mapping has 1 error",
			mapping: "workspace: acme\nimporter: ops@example.com\nprojects:\n  - source: \"900\"\n    slug: Bad_Slug\n",
			out:     []string{`  - projects[0].slug: "Bad_Slug" is not a valid slug`},
		},
		{name: "no database", token: token, mapping: valid, errText: "database is required"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var hits atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				hits.Add(1)
				http.NotFound(w, r)
			}))
			defer srv.Close()

			var out bytes.Buffer
			code, err := Run(context.Background(), Options{
				BaseURL: srv.URL, Token: tc.token, Mapping: strings.NewReader(tc.mapping), DryRun: tc.dryRun, Out: &out,
			})
			require.Error(t, err)
			assert.Equal(t, ExitNoWrites, code)
			assert.Contains(t, err.Error(), tc.errText)
			assert.NotContains(t, err.Error(), token)
			assert.NotContains(t, out.String(), token)
			assert.Zero(t, hits.Load(), "no request may leave before the input is valid")
			if len(tc.out) == 0 {
				assert.Empty(t, out.String())
			}
			for _, s := range tc.out {
				assert.Contains(t, out.String(), s)
			}
			for _, s := range tc.absent {
				assert.NotContains(t, out.String(), s)
			}
		})
	}
}

// existingTask is a sourced task row of the flow project id.
func existingTask(gid string, id, project uint32) *ExistingTask {
	r := &ExistingTask{GID: gid}
	r.ID, r.ProjectID = id, project
	return r
}

// reportPlan is a hand-built plan: two mapped projects, one reused and one
// created, with sync plan inputs in every counter.
func reportPlan() *Plan {
	snap := &asana.Snapshot{
		Users: make([]asana.User, 3),
		Tags:  make([]asana.Tag, 2),
		Projects: []asana.Project{
			{GID: "900", Name: "Marketing"}, {GID: "901", Name: "Sales", Archived: true}, {GID: "902", Name: "Ops"},
		},
		Mapped: map[string]*asana.ProjectSnapshot{
			"900": {Repeated: []asana.Repeat{{GID: "7"}, {GID: "7"}}},
			"901": {},
		},
	}
	ex := emptyExisting()
	for i := range 5 {
		ex.Tasks = append(ex.Tasks, existingTask(strconv.Itoa(i), uint32(500+i), 11)) //#nosec G115 -- small fixture ids
	}
	ex.SourceUIDCounts[11] = 6
	ex.Comments[500] = make([]*ExistingComment, 8)

	updated := existingTask("0", 500, 11)
	p := &Plan{
		Sync:            true,
		SourceWorkspace: asana.Workspace{GID: "1", Name: "Acme"},
		SignIn:          SignIn{Domains: 1, Allowlist: 2},
		NewUsers:        []MappingUser{{Email: "a@example.com"}, {Email: "b@example.com"}},
		snap:            snap,
		existing:        ex,
		Projects: []*ProjectPlan{
			{
				Index: 0, Mapping: MappingProject{Source: "Marketing"}, Source: snap.Projects[0], Name: "Marketing", ExistingID: 11,
				Stats: ProjectStats{
					Tasks: 6, TopLevel: 4, Subtasks: 2, Default: 4, Milestone: 1, Approval: 1, Completed: 2,
					BySection:           []NameCount{{Name: "Todo", Count: 3}, {Name: "", Count: 2}, {Name: "Done", Count: 1}},
					Tags:                []NameCount{{Name: "Bug", Count: 2}, {Name: "Urgent", Count: 1}},
					Comments:            8,
					AttachmentsAsana:    2,
					AttachmentsExternal: 1,
					Blocks:              2,
					Skipped: map[string]int{
						SkipEmptyTitle: 1, SkipEmptyBody: 2, SkipTooLarge: 1, SkipTypeNotAllowed: 1, SkipOutsideMapping: 1, SkipCycle: 1,
					},
					People: []PersonStat{
						{GID: "101", Name: "Hanako", Kind: PersonEmail, Email: "hanako@example.com", Assignee: 3, Creator: 2, Commenter: 4, Follower: 1},
						{GID: "103", Name: "Taro", Kind: PersonUnmatched, Commenter: 1, Follower: 2},
					},
					CustomFields: 3, Likes: 4, SystemStories: 5,
				},
			},
			{
				Index: 1, Mapping: MappingProject{Source: "901"}, Source: snap.Projects[1], Name: "Sales", Slug: "asana-901",
				Stats: ProjectStats{Tasks: 2, TopLevel: 2, Default: 2},
			},
		},
		Tasks: []*TaskPlan{
			{GID: "a", Project: 0, Action: ActionCreate},
			{GID: "b", Project: 0, Action: ActionUpdate, Row: updated},
			{GID: "c", Project: 0, Action: ActionUnchanged},
			{GID: "d", Project: 0, Action: ActionUnchanged},
			{GID: "e", Project: 1, Action: ActionCreate},
			{GID: "f", Project: 1, Action: ActionCreate},
		},
		TaskDeletions: []*TaskDeletion{
			{Project: 0, Candidate: true, Probed: true, Delete: true},
			{Project: 0, Candidate: true, Probed: true},
			{Project: 0, Candidate: true, Moved: true},
		},
		Comments: []*CommentPlan{
			{Project: 0, Action: ActionCreate}, {Project: 0, Action: ActionCreate}, {Project: 0, Action: ActionCreate},
			{Project: 0, Action: ActionUpdate},
			{Project: 0, Action: ActionUnchanged}, {Project: 0, Action: ActionUnchanged},
			{Project: 0, Action: ActionUnchanged}, {Project: 0, Action: ActionUnchanged},
		},
		CommentDeletions: []*CommentDeletion{
			{Project: 0, Probed: true, Delete: true},
			{Project: 0, Probed: true},
		},
	}
	return p
}

func TestReportDryRunGolden(t *testing.T) {
	t.Run("sync plan", func(t *testing.T) {
		var out bytes.Buffer
		writeDryRun(&out, reportPlan())
		want, err := os.ReadFile(filepath.Join("testdata", "report-dry-run.txt"))
		require.NoError(t, err)
		assert.Equal(t, string(want), out.String())
	})

	t.Run("errors and no sync", func(t *testing.T) {
		var out bytes.Buffer
		writeDryRun(&out, &Plan{Errors: []MappingError{
			{Path: "workspace", Detail: `no workspace has slug "acme"`},
			{Path: "projects[0].source", Detail: "matches no Asana project"},
		}})
		assert.Equal(t, "sign-in restriction: open\n"+
			"users to create: none\n"+
			"validation errors: 2\n"+
			"  - workspace: no workspace has slug \"acme\"\n"+
			"  - projects[0].source: matches no Asana project\n", out.String())
	})

	t.Run("refusals", func(t *testing.T) {
		var out bytes.Buffer
		writeRefusals(&out, []Refusal{
			{Name: "Marketing", Reason: RefuseMassDelete, Lost: 11, Synced: 20},
			{Name: "Sales", Reason: RefuseEmptySource, Lost: 3, Synced: 4},
		})
		assert.Equal(t, "refusing: project \"Marketing\" would delete 11 of 20 synced tasks\n"+
			"refusing: Asana returned no tasks for project \"Sales\" but 4 are synced\n", out.String())
	})
}

func TestReportRunLines(t *testing.T) {
	p := &Plan{Projects: []*ProjectPlan{{Name: "Marketing", Notes: Notes{Truncated: 1}}}}
	res := &WriteResult{
		Projects: []ProjectResult{{
			Created: true, MembersAdded: 2, MembersExisting: 1,
			Tasks:        Counts{Imported: 5, Duplicates: 1, Skipped: 1, Failed: 1},
			Comments:     Counts{Imported: 3},
			Attachments:  Counts{Imported: 1, Skipped: 1},
			Dependencies: Counts{Imported: 1},
		}},
		ProjectsCreated: 1, UsersCreated: 2,
		Failures: []string{"task \"7\": boom\nsecond line"},
	}
	var out bytes.Buffer
	writeRun(&out, p, res)
	assert.Equal(t,
		"project \"Marketing\" (created): members added=2 existing=1; "+
			"tasks imported=5 updated=0 duplicates=1 deleted=0 kept=0 moved=0 skipped=1 failed=1 rejected=0; "+
			"comments imported=3 updated=0 duplicates=0 deleted=0 kept=0 moved=0 skipped=0 failed=0 rejected=0; "+
			"attachments imported=1 updated=0 duplicates=0 deleted=0 kept=0 moved=0 skipped=1 failed=0 rejected=0; "+
			"dependencies imported=1 updated=0 duplicates=0 deleted=0 kept=0 moved=0 skipped=0 failed=0 rejected=0\n"+
			"project \"Marketing\": notes truncated=1 start_dropped=0 parent_changed=0 attachments_removed_in_asana=0 labels_left=0 completion_gate=0 cancelled_kept=0 slug_ignored=0 identifier_ignored=0\n"+
			"failure: task \"7\": boom second line\n"+
			"total: tasks imported=5 updated=0 duplicates=1 deleted=0 kept=0 moved=0 skipped=1 failed=1 rejected=0; "+
			"comments imported=3 updated=0 duplicates=0 deleted=0 kept=0 moved=0 skipped=0 failed=0 rejected=0; "+
			"attachments imported=1 updated=0 duplicates=0 deleted=0 kept=0 moved=0 skipped=1 failed=0 rejected=0; "+
			"dependencies imported=1 updated=0 duplicates=0 deleted=0 kept=0 moved=0 skipped=0 failed=0 rejected=0; "+
			"projects_created=1 users_created=2\n", out.String())
}
