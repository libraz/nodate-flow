// Package asanaimporttests drives importasana end to end: the real Run
// entry point against a real MySQL and object store, with Asana served by
// an in-process fake speaking the REST paths the client reads.
package asanaimporttests

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"go.yaml.in/yaml/v3"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/asanaimport"
	"github.com/libraz/nodate-flow/apps/flow-api/tests/helpers"
	"github.com/libraz/nodate-flow/packages/go-shared/testhelpers"
)

var (
	testSrv     *helpers.TestServer
	testDB      *sql.DB
	testStorage *helpers.StorageBundle
)

func TestMain(m *testing.M) {
	if os.Getenv("NF_TEST_INTEGRATION") == "" {
		os.Exit(m.Run())
	}
	inst, err := helpers.EnsureShared()
	if err != nil {
		fmt.Fprintln(os.Stderr, "asanaimport: start shared mysql:", err)
		os.Exit(1)
	}
	st, err := helpers.EnsureSharedStorage()
	if err != nil {
		fmt.Fprintln(os.Stderr, "asanaimport: start shared storage:", err)
		os.Exit(1)
	}
	bundle, err := helpers.NewStorageBundle(st)
	if err != nil {
		fmt.Fprintln(os.Stderr, "asanaimport: storage bundle:", err)
		os.Exit(1)
	}
	srv, cleanup, err := helpers.NewTestServer(inst.DB)
	if err != nil {
		fmt.Fprintln(os.Stderr, "asanaimport: start test server:", err)
		os.Exit(1)
	}
	testSrv, testDB, testStorage = srv, inst.DB, bundle
	helpers.RegisterCleanupDB(inst.DB)
	code := m.Run()
	cleanup()
	os.Exit(code)
}

// bootstrap gates on integration mode and fails when the harness is missing.
func bootstrap(t *testing.T) {
	t.Helper()
	testhelpers.SkipUnlessIntegration(t)
	if testSrv == nil || testDB == nil || testStorage == nil {
		t.Fatal("integration mode is on but the shared harness was never bootstrapped")
	}
}

// env is one tenant (the importer, "Ops Lead" in Asana), a second member
// ("Hanako" in Asana) and a fake Asana loaded with a fixture.
type env struct {
	owner, hanako *helpers.TestTenant
	wsID          uint32
	opsID         uint32
	hanakoID      uint32
	sfx           string
	// Asana-side addresses of people who have no flow account yet.
	taroMail, kenjiMail, ghostMail string
	fake                           *fakeAsana
}

func newEnv(t *testing.T) *env {
	t.Helper()
	owner := helpers.CreateTestTenant(t, testSrv.BaseURL)
	e := &env{
		owner:  owner,
		hanako: inviteMember(t, owner),
		sfx:    helpers.RandomHex(6),
		fake:   newFakeAsana(t),
	}
	e.wsID = helpers.ResolveWorkspaceInternalID(t, testDB, owner.WorkspacePublicID)
	e.opsID = helpers.ResolveUserInternalID(t, testDB, owner.UserPublicID)
	e.hanakoID = helpers.ResolveUserInternalID(t, testDB, e.hanako.UserPublicID)
	e.taroMail = "taro+" + e.sfx + "@example.test"
	e.kenjiMail = "kenji+" + e.sfx + "@example.test"
	e.ghostMail = "ghost+" + e.sfx + "@elsewhere.test"
	return e
}

// inviteMember adds a freshly registered user to owner's workspace.
func inviteMember(t *testing.T, owner *helpers.TestTenant) *helpers.TestTenant {
	t.Helper()
	member := helpers.CreateTestTenant(t, testSrv.BaseURL)
	var invite struct {
		Token string `json:"token"`
	}
	helpers.DoJSON(t, http.MethodPost, testSrv.BaseURL+"/workspaces/"+owner.WorkspacePublicID+"/invites",
		owner.AccessToken, map[string]any{"role": "member"}, &invite)
	require.NotEmpty(t, invite.Token)
	helpers.DoJSON(t, http.MethodPost, testSrv.BaseURL+"/invites/"+invite.Token+"/accept", member.AccessToken, nil, nil)
	return member
}

// runOpts selects the mode of one importasana run.
type runOpts struct {
	dry, sync bool
	domains   []string
}

// result is what one run returned and printed.
type result struct {
	code int
	err  error
	out  string
}

func (e *env) run(t *testing.T, m *asanaimport.Mapping, o runOpts) result {
	t.Helper()
	doc, err := yaml.Marshal(m)
	require.NoError(t, err)
	var out bytes.Buffer
	code, err := asanaimport.Run(context.Background(), asanaimport.Options{
		BaseURL: e.fake.URL(), Token: fakeToken, Mapping: bytes.NewReader(doc),
		DryRun: o.dry, Sync: o.sync, DB: testDB, Storage: testStorage.Flow,
		AllowedDomains: o.domains, Out: &out,
	})
	// Reevaluation jobs triggered by the writes must not race the assertions.
	helpers.WaitReevalIdle(t, e.wsID)
	return result{code: code, err: err, out: out.String()}
}

// mustRun runs and requires exit 0 with no error.
func (e *env) mustRun(t *testing.T, m *asanaimport.Mapping, o runOpts) string {
	t.Helper()
	r := e.run(t, m, o)
	require.NoErrorf(t, r.err, "report:\n%s", r.out)
	require.Equalf(t, asanaimport.ExitOK, r.code, "report:\n%s", r.out)
	return r.out
}

// ---- report parsing ----

// line returns the single report line starting with prefix.
func line(t *testing.T, out, prefix string) string {
	t.Helper()
	var hits []string
	for _, l := range strings.Split(out, "\n") {
		if strings.HasPrefix(l, prefix) {
			hits = append(hits, l)
		}
	}
	require.Lenf(t, hits, 1, "lines starting with %q in report:\n%s", prefix, out)
	return hits[0]
}

// kv parses every key=integer token of s.
func kv(s string) map[string]int {
	got := map[string]int{}
	for _, tok := range strings.Fields(strings.ReplaceAll(s, ";", " ")) {
		k, v, ok := strings.Cut(tok, "=")
		if !ok {
			continue
		}
		if n, err := strconv.Atoi(v); err == nil {
			got[k] = n
		}
	}
	return got
}

// requireKV checks that s carries exactly the counters in want ("a=1 b=2")
// and zero for every other counter it prints.
func requireKV(t *testing.T, s, want string) {
	t.Helper()
	got := kv(s)
	exp := kv(want)
	require.NotEmptyf(t, got, "no counters in %q", s)
	for k := range exp {
		require.Containsf(t, got, k, "counter %q missing in %q", k, s)
	}
	for k, v := range got {
		require.Equalf(t, exp[k], v, "counter %q in %q", k, s)
	}
}

// segment returns the "<entity> k=v ..." part of a project run line.
func segment(t *testing.T, projectLine, entity string) string {
	t.Helper()
	for _, part := range strings.Split(projectLine, "; ") {
		if strings.HasPrefix(part, entity+" ") {
			return part
		}
	}
	t.Fatalf("no %q segment in %q", entity, projectLine)
	return ""
}

// runLine is the per-project line of a real run.
func runLine(t *testing.T, out, project string) string {
	t.Helper()
	return line(t, out, fmt.Sprintf("project %q (", project))
}

// requireRun checks the counters of one entity on a project's run line.
func requireRun(t *testing.T, out, project, entity, want string) {
	t.Helper()
	requireKV(t, segment(t, runLine(t, out, project), entity), want)
}

// requireNotes checks the notes line of a project.
func requireNotes(t *testing.T, out, project, want string) {
	t.Helper()
	requireKV(t, line(t, out, fmt.Sprintf("project %q: notes ", project)), want)
}

// requireSyncPlan checks a dry-run sync plan line.
func requireSyncPlan(t *testing.T, out, project, entity, want string) {
	t.Helper()
	requireKV(t, line(t, out, fmt.Sprintf("project %q: sync plan %s ", project, entity)), want)
}

// ---- database reads ----

func count(t *testing.T, q string, args ...any) int {
	t.Helper()
	var n int
	require.NoError(t, testDB.QueryRow(q, args...).Scan(&n))
	return n
}

// taskID is the live row importing gid, in the project with slug.
func (e *env) taskID(t *testing.T, slug, gid string) uint32 {
	t.Helper()
	var id uint32
	require.NoErrorf(t, testDB.QueryRow(`SELECT t.id FROM tasks t JOIN projects p ON p.id = t.project_id
		WHERE t.workspace_id = ? AND p.slug = ? AND t.source_uid = ? AND t.enabled = TRUE`,
		e.wsID, slug, asanaimport.SourceUID(gid)).Scan(&id), "task %s in %s", gid, slug)
	return id
}

// taskPublic is the REST id of the live row importing gid.
func (e *env) taskPublic(t *testing.T, slug, gid string) string {
	t.Helper()
	var pub string
	require.NoError(t, testDB.QueryRow(`SELECT BIN_TO_UUID(public_id, 0) FROM tasks WHERE id = ?`,
		e.taskID(t, slug, gid)).Scan(&pub))
	return pub
}

func (e *env) projectID(t *testing.T, slug string) uint32 {
	t.Helper()
	var id uint32
	require.NoError(t, testDB.QueryRow(`SELECT id FROM projects WHERE workspace_id = ? AND slug = ? AND enabled = TRUE`,
		e.wsID, slug).Scan(&id))
	return id
}

func (e *env) projectPublic(t *testing.T, slug string) string {
	t.Helper()
	var pub string
	require.NoError(t, testDB.QueryRow(`SELECT BIN_TO_UUID(public_id, 0) FROM projects WHERE id = ?`,
		e.projectID(t, slug)).Scan(&pub))
	return pub
}

func userIDByEmail(t *testing.T, email string) uint32 {
	t.Helper()
	var id uint32
	require.NoErrorf(t, testDB.QueryRow(`SELECT id FROM users WHERE email = ?`, email).Scan(&id), "user %s", email)
	return id
}

// taskRow is the imported state of one task.
type taskRow struct {
	Title, Description, State string
	DueOn, StartedOn          string // "" when NULL
	CreatedBy                 uint32
	Parent                    uint32 // 0 when top-level
	Enabled                   bool
}

func readTask(t *testing.T, id uint32) taskRow {
	t.Helper()
	var r taskRow
	var due, start sql.NullString
	var parent sql.NullInt64
	require.NoError(t, testDB.QueryRow(`SELECT title, COALESCE(description, ''), derived_state,
		DATE_FORMAT(due_on, '%Y-%m-%d'), DATE_FORMAT(started_on, '%Y-%m-%d'), created_by_user_id, parent_task_id, enabled
		FROM tasks WHERE id = ?`, id).Scan(&r.Title, &r.Description, &r.State, &due, &start, &r.CreatedBy, &parent, &r.Enabled))
	r.DueOn, r.StartedOn, r.Parent = due.String, start.String, uint32(parent.Int64) //#nosec G115 -- tasks.parent_task_id is INT UNSIGNED
	return r
}

// actors lists the live actors of a task as "role:userID", sorted.
func actors(t *testing.T, taskID uint32) []string {
	t.Helper()
	rows, err := testDB.Query(`SELECT CONCAT(role, ':', user_id) FROM task_actors
		WHERE task_id = ? AND enabled = TRUE AND kind = 'user' ORDER BY role, user_id`, taskID)
	require.NoError(t, err)
	return scanStrings(t, rows)
}

func actor(role string, userID uint32) string { return fmt.Sprintf("%s:%d", role, userID) }

// labels lists the live labels of a task as "name@slug" (project label)
// or "name" (workspace label), sorted.
func labels(t *testing.T, taskID uint32) []string {
	t.Helper()
	rows, err := testDB.Query(`SELECT IF(l.project_id IS NULL, l.name, CONCAT(l.name, '@', p.slug))
		FROM task_labels tl JOIN labels l ON l.id = tl.label_id LEFT JOIN projects p ON p.id = l.project_id
		WHERE tl.task_id = ? AND tl.enabled = TRUE ORDER BY 1`, taskID)
	require.NoError(t, err)
	return scanStrings(t, rows)
}

func scanStrings(t *testing.T, rows *sql.Rows) []string {
	t.Helper()
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var s string
		require.NoError(t, rows.Scan(&s))
		out = append(out, s)
	}
	require.NoError(t, rows.Err())
	return out
}

// commentRow is one imported comment.
type commentRow struct {
	Author  uint32
	Body    string
	At      string
	Enabled bool
}

func (e *env) comment(t *testing.T, gid string) commentRow {
	t.Helper()
	var c commentRow
	require.NoErrorf(t, testDB.QueryRow(`SELECT author_id, body, DATE_FORMAT(created_at, '%Y-%m-%d %H:%i:%s'), enabled
		FROM comments WHERE workspace_id = ? AND source_uid = ? ORDER BY id DESC LIMIT 1`,
		e.wsID, asanaimport.SourceUID(gid)).Scan(&c.Author, &c.Body, &c.At, &c.Enabled), "comment %s", gid)
	return c
}

func (e *env) events(t *testing.T, kind string) int {
	t.Helper()
	return count(t, `SELECT COUNT(*) FROM events WHERE workspace_id = ? AND type = ?`, e.wsID, kind)
}

func (e *env) audits(t *testing.T, action string) int {
	t.Helper()
	return count(t, `SELECT COUNT(*) FROM audit_logs WHERE workspace_id = ? AND action = ? AND actor_user_id = ?`,
		e.wsID, action, e.opsID)
}

// scopedTables are the workspace-scoped tables a run can write.
var scopedTables = []string{
	"projects", "project_members", "tasks", "task_actors", "task_labels", "labels", "task_dependencies",
	"comments", "attachments", "storage_objects", "workspace_members", "calendars", "events", "audit_logs",
}

// snapshot counts every row a run can write for this env, so "nothing was
// written" is checked against the whole write surface, enabled or not.
func (e *env) snapshot(t *testing.T) map[string]int {
	t.Helper()
	snap := map[string]int{}
	for _, tbl := range scopedTables {
		snap[tbl] = count(t, "SELECT COUNT(*) FROM "+tbl+" WHERE workspace_id = ?", e.wsID)
	}
	mails := []any{e.taroMail, e.kenjiMail, e.ghostMail}
	snap["users"] = count(t, `SELECT COUNT(*) FROM users WHERE email IN (?, ?, ?)`, mails...)
	snap["oauth_signin_allowlist"] = count(t, `SELECT COUNT(*) FROM oauth_signin_allowlist WHERE entry_value IN (?, ?, ?)`, mails...)
	return snap
}

// ---- REST writes (local edits the importer must respect) ----

func (e *env) rest(t *testing.T, path string, body any, out any) {
	t.Helper()
	helpers.DoJSON(t, http.MethodPost, testSrv.BaseURL+path, e.owner.AccessToken, body, out)
}

func (e *env) createTask(t *testing.T, projectPub, title string) string {
	t.Helper()
	var task struct {
		ID string `json:"id"`
	}
	e.rest(t, "/tasks", map[string]any{"projectId": projectPub, "title": title}, &task)
	require.NotEmpty(t, task.ID)
	return task.ID
}

func (e *env) createLabel(t *testing.T, name string) string {
	t.Helper()
	var label struct {
		ID string `json:"id"`
	}
	e.rest(t, "/workspaces/"+e.owner.WorkspacePublicID+"/labels", map[string]any{"name": name, "color": "#3366ff"}, &label)
	require.NotEmpty(t, label.ID)
	return label.ID
}

func (e *env) labelPublic(t *testing.T, name string) string {
	t.Helper()
	var pub string
	require.NoError(t, testDB.QueryRow(`SELECT BIN_TO_UUID(public_id, 0) FROM labels
		WHERE workspace_id = ? AND project_id IS NULL AND name = ? AND enabled = TRUE`, e.wsID, name).Scan(&pub))
	return pub
}
