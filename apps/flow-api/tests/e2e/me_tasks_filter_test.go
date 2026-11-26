package e2e

import (
	"database/sql"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// myTasksFixtureRow is one /me/tasks row (a task paired with one of the
// caller's roles) as the reference implementation sees it.
type myTasksFixtureRow struct {
	ID          string
	Workspace   string // workspace public id
	Title       string
	State       string
	Priority    int32
	DueOn       string // "" = no due date
	Role        string
	UpdatedMs   sql.NullInt64
	CreatedMs   int64
	workspaceOf string // "own" | "joined"
}

func (r myTasksFixtureRow) key() string { return r.ID + ":" + r.Role }

// myTasksActorRank is the ENUM definition order of task_actors.role.
var myTasksActorRank = map[string]int{"assignee": 0, "reviewer": 1, "watcher": 2, "approver": 3}

// myTasksFixture is the shared read-only world of one test: rows are the
// reference rows; own/joined are the two workspace public ids.
type myTasksFixture struct {
	token  string
	own    string
	joined string
	rows   []myTasksFixtureRow
	titles map[string]string // task id -> title, for failure messages
}

// myTasksSpec declares one fixture task.
type myTasksSpec struct {
	ws          string // "own" | "joined"
	title       string
	roles       []string // roles the caller holds; first one is set at creation
	transitions []string
	priority    int32
	dueOn       string
	// patchPriority/patchDueOn, when set, are applied by PATCH after creation.
	patchPriority *int32
	patchDueOn    string
	// plain: created with only a title; no PATCH, no transition.
	plain bool
}

func i32(v int32) *int32 { return &v }

// buildMyTasksFixture creates user U (own workspace) and a joined
// workspace of another tenant, then ten tasks across them via the API.
//
// Layout (ws, title, roles, state, priority, due):
//
//	own    Alpha report       assignee            open       4  2026-12-05
//	own    alpha REVIEW       assignee+reviewer   review     2  -
//	own    100% done          watcher             done       0  2026-01-10 (priority patched 1 -> 0)
//	own    100x done          approver            cancelled  0  2026-12-05
//	own    設計レビュー        reviewer            waiting    1  -
//	own    Zeta plain         assignee            open       0  -          (untouched)
//	joined Beta alpha notes   assignee            open       4  2027-03-01
//	joined Gamma              reviewer            waiting    3  2026-12-05 (priority/due patched)
//	joined delta task         watcher             review     2  2026-06-01
//	joined Epsilon            approver            done       2  2027-03-01
func buildMyTasksFixture(t *testing.T) *myTasksFixture {
	t.Helper()
	u := newTenant(t)
	o := newTenant(t)

	var invite struct {
		Token string `json:"token"`
	}
	doJSON(t, http.MethodPost, testServerURL+"/workspaces/"+o.WorkspacePublicID+"/invites",
		o.AccessToken, map[string]any{"role": "member"}, &invite)
	doJSON(t, http.MethodPost, testServerURL+"/invites/"+invite.Token+"/accept",
		u.AccessToken, nil, nil)

	specs := []myTasksSpec{
		{ws: "own", title: "Alpha report", roles: []string{"assignee"}, priority: 4, dueOn: "2026-12-05"},
		{ws: "own", title: "alpha REVIEW", roles: []string{"assignee", "reviewer"}, transitions: []string{"start", "submit"}, priority: 2},
		{ws: "own", title: "100% done", roles: []string{"watcher"}, transitions: []string{"complete"}, priority: 3, dueOn: "2026-01-10", patchPriority: i32(0)},
		{ws: "own", title: "100x done", roles: []string{"approver"}, transitions: []string{"cancel"}, priority: 0, dueOn: "2026-12-05"},
		{ws: "own", title: "設計レビュー", roles: []string{"reviewer"}, transitions: []string{"block"}, priority: 1},
		{ws: "own", title: "Zeta plain", roles: []string{"assignee"}, plain: true},
		{ws: "joined", title: "Beta alpha notes", roles: []string{"assignee"}, priority: 4, dueOn: "2027-03-01"},
		{ws: "joined", title: "Gamma", roles: []string{"reviewer"}, transitions: []string{"start"}, priority: 0, patchPriority: i32(3), patchDueOn: "2026-12-05"},
		{ws: "joined", title: "delta task", roles: []string{"watcher"}, transitions: []string{"start", "submit"}, priority: 2, dueOn: "2026-06-01"},
		{ws: "joined", title: "Epsilon", roles: []string{"approver"}, transitions: []string{"complete"}, priority: 2, dueOn: "2027-03-01"},
	}

	fx := &myTasksFixture{
		token: u.AccessToken, own: u.WorkspacePublicID, joined: o.WorkspacePublicID,
		titles: map[string]string{},
	}
	type created struct {
		spec myTasksSpec
		id   string
	}
	var made []created

	for _, sp := range specs {
		tenant := u
		if sp.ws == "joined" {
			tenant = o
		}
		body := map[string]any{"projectId": tenant.ProjectPublicID, "title": sp.title}
		if !sp.plain {
			if sp.priority != 0 {
				body["priority"] = sp.priority
			}
			if sp.dueOn != "" {
				body["dueOn"] = sp.dueOn
			}
		}
		// Own-workspace tasks auto-attach U as assignee. For any other first
		// role, and for every joined-workspace task (created by the other
		// tenant), the actor list is explicit.
		if sp.ws == "joined" || sp.roles[0] != "assignee" {
			body["actors"] = []map[string]any{{"userId": u.UserPublicID, "role": sp.roles[0]}}
		}
		var task struct {
			ID string `json:"id"`
		}
		doJSON(t, http.MethodPost, testServerURL+"/tasks", tenant.AccessToken, body, &task)
		for _, extra := range sp.roles[1:] {
			doJSON(t, http.MethodPost, testServerURL+"/tasks/"+task.ID+"/actors", tenant.AccessToken,
				map[string]any{"userId": u.UserPublicID, "role": extra}, nil)
		}
		for _, tr := range sp.transitions {
			doJSON(t, http.MethodPost, testServerURL+"/tasks/"+task.ID+"/transitions",
				tenant.AccessToken, map[string]any{"transition": tr}, nil)
		}
		patch := map[string]any{}
		if sp.patchPriority != nil {
			patch["priority"] = *sp.patchPriority
		}
		if sp.patchDueOn != "" {
			patch["dueOn"] = sp.patchDueOn
		}
		if len(patch) > 0 {
			doJSON(t, http.MethodPatch, testServerURL+"/tasks/"+task.ID, tenant.AccessToken, patch, nil)
		}
		made = append(made, created{spec: sp, id: task.ID})
		fx.titles[task.ID] = sp.title
	}

	// Millisecond timestamps straight from the DB: the API rounds to seconds
	// while MySQL orders by milliseconds.
	holders := make([]string, 0, len(made))
	args := make([]any, 0, len(made))
	for _, m := range made {
		holders = append(holders, "UUID_TO_BIN(?, 0)")
		args = append(args, m.id)
	}
	dbRows, err := testDB.Query(
		`SELECT BIN_TO_UUID(public_id, 0),
		        CAST(UNIX_TIMESTAMP(updated_at) * 1000 AS SIGNED),
		        CAST(UNIX_TIMESTAMP(created_at) * 1000 AS SIGNED)
		   FROM tasks WHERE public_id IN (`+strings.Join(holders, ",")+`)`, args...)
	require.NoError(t, err)
	defer func() { _ = dbRows.Close() }()
	type stamps struct {
		upd sql.NullInt64
		cre int64
	}
	byID := map[string]stamps{}
	for dbRows.Next() {
		var id string
		var s stamps
		require.NoError(t, dbRows.Scan(&id, &s.upd, &s.cre))
		byID[id] = s
	}
	require.NoError(t, dbRows.Err())
	require.Len(t, byID, len(made), "every fixture task must be readable from the tasks table")

	finalState := map[string]string{"start": "waiting", "block": "waiting", "submit": "review", "complete": "done", "cancel": "cancelled"}
	for _, m := range made {
		state := "open"
		for _, tr := range m.spec.transitions {
			state = finalState[tr]
		}
		prio := m.spec.priority
		due := m.spec.dueOn
		if m.spec.patchPriority != nil {
			prio = *m.spec.patchPriority
		}
		if m.spec.patchDueOn != "" {
			due = m.spec.patchDueOn
		}
		ws := fx.own
		if m.spec.ws == "joined" {
			ws = fx.joined
		}
		for _, role := range m.spec.roles {
			fx.rows = append(fx.rows, myTasksFixtureRow{
				ID: m.id, Workspace: ws, workspaceOf: m.spec.ws, Title: m.spec.title, State: state,
				Priority: prio, DueOn: due, Role: role,
				UpdatedMs: byID[m.id].upd, CreatedMs: byID[m.id].cre,
			})
		}
	}
	return fx
}

// myTasksCase is one request shape. Each field uses the vocabulary of the
// design table: "absent" omits the parameter.
type myTasksCase struct {
	q, state, priority, workspaceID, role, sort string
}

func (c myTasksCase) values(fx *myTasksFixture) url.Values {
	v := url.Values{}
	switch c.q {
	case "match":
		v.Set("q", "alpha")
	case "nomatch":
		v.Set("q", "zzz-no-such")
	case "wildcard":
		v.Set("q", "%")
	}
	switch c.state {
	case "open":
		v.Set("state", "open")
	case "open+done":
		v.Set("state", "open,done")
	case "bogus":
		v.Set("state", "bogus")
	}
	switch c.priority {
	case "p4":
		v.Set("priority", "4")
	case "p0+p2":
		v.Set("priority", "0,2")
	}
	switch c.workspaceID {
	case "own":
		v.Set("workspaceId", fx.own)
	case "joined":
		v.Set("workspaceId", fx.joined)
	case "malformed":
		v.Set("workspaceId", "not-a-uuid")
	}
	switch c.role {
	case "assignee":
		v.Set("role", "assignee")
	case "reviewer+watcher":
		v.Set("role", "reviewer,watcher")
	case "bogus":
		v.Set("role", "bogus")
	}
	if c.sort != "absent" && c.sort != "" {
		v.Set("sort", c.sort)
	}
	return v
}

// refFilter applies the filter semantics: a present parameter whose values
// are all invalid matches nothing.
func (c myTasksCase) refFilter(fx *myTasksFixture) []myTasksFixtureRow {
	var states, roles map[string]bool
	switch c.state {
	case "open":
		states = map[string]bool{"open": true}
	case "open+done":
		states = map[string]bool{"open": true, "done": true}
	case "bogus":
		states = map[string]bool{}
	}
	switch c.role {
	case "assignee":
		roles = map[string]bool{"assignee": true}
	case "reviewer+watcher":
		roles = map[string]bool{"reviewer": true, "watcher": true}
	case "bogus":
		roles = map[string]bool{}
	}
	var prios map[int32]bool
	switch c.priority {
	case "p4":
		prios = map[int32]bool{4: true}
	case "p0+p2":
		prios = map[int32]bool{0: true, 2: true}
	}
	var out []myTasksFixtureRow
	for _, r := range fx.rows {
		if c.q != "absent" && c.q != "" {
			needle := map[string]string{"match": "alpha", "nomatch": "zzz-no-such", "wildcard": "%"}[c.q]
			if !strings.Contains(strings.ToLower(r.Title), needle) {
				continue
			}
		}
		if states != nil && !states[r.State] {
			continue
		}
		if prios != nil && !prios[r.Priority] {
			continue
		}
		switch c.workspaceID {
		case "own":
			if r.workspaceOf != "own" {
				continue
			}
		case "joined":
			if r.workspaceOf != "joined" {
				continue
			}
		case "malformed":
			continue
		}
		if roles != nil && !roles[r.Role] {
			continue
		}
		out = append(out, r)
	}
	return out
}

func cmpInt(a, b int64) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

// cmpDue orders due dates ascending. nullsFirst places rows without a due
// date before dated rows; otherwise after.
func cmpDue(a, b myTasksFixtureRow, nullsFirst, desc bool) int {
	switch {
	case a.DueOn == "" && b.DueOn == "":
		return 0
	case a.DueOn == "":
		if nullsFirst {
			return -1
		}
		return 1
	case b.DueOn == "":
		if nullsFirst {
			return 1
		}
		return -1
	}
	c := strings.Compare(a.DueOn, b.DueOn)
	if desc {
		c = -c
	}
	return c
}

func (r myTasksFixtureRow) updatedOrCreated() int64 {
	if r.UpdatedMs.Valid {
		return r.UpdatedMs.Int64
	}
	return r.CreatedMs
}

// tailCmp is the shared tiebreak: created_at DESC, public_id DESC,
// actor_role ASC (ENUM order).
func tailCmp(a, b myTasksFixtureRow, withRole bool) int {
	if c := cmpInt(b.CreatedMs, a.CreatedMs); c != 0 {
		return c
	}
	if c := strings.Compare(b.ID, a.ID); c != 0 {
		return c
	}
	if withRole {
		return cmpInt(int64(myTasksActorRank[a.Role]), int64(myTasksActorRank[b.Role]))
	}
	return 0
}

// refSort orders rows by the design's ORDER BY table. withRole=false leaves
// the two rows of a multi-role task equal (legacy no-parameter path).
func refSort(rows []myTasksFixtureRow, key string, withRole bool) []myTasksFixtureRow {
	out := append([]myTasksFixtureRow(nil), rows...)
	less := func(a, b myTasksFixtureRow) int {
		switch key {
		case "absent", "":
			if c := cmpInt(int64(b.Priority), int64(a.Priority)); c != 0 {
				return c
			}
			if c := cmpDue(a, b, true, false); c != 0 {
				return c
			}
		case "dueOn", "-dueOn":
			if c := cmpDue(a, b, false, key == "-dueOn"); c != 0 {
				return c
			}
			if c := cmpInt(int64(b.Priority), int64(a.Priority)); c != 0 {
				return c
			}
		case "priority":
			if c := cmpInt(int64(a.Priority), int64(b.Priority)); c != 0 {
				return c
			}
			if c := cmpDue(a, b, false, false); c != 0 {
				return c
			}
		case "-priority":
			if c := cmpInt(int64(b.Priority), int64(a.Priority)); c != 0 {
				return c
			}
			if c := cmpDue(a, b, false, false); c != 0 {
				return c
			}
		case "updatedAt":
			if c := cmpInt(a.updatedOrCreated(), b.updatedOrCreated()); c != 0 {
				return c
			}
		case "-updatedAt":
			if c := cmpInt(b.updatedOrCreated(), a.updatedOrCreated()); c != 0 {
				return c
			}
		case "createdAt":
			if c := cmpInt(a.CreatedMs, b.CreatedMs); c != 0 {
				return c
			}
			if c := strings.Compare(a.ID, b.ID); c != 0 {
				return c
			}
			if withRole {
				return cmpInt(int64(myTasksActorRank[a.Role]), int64(myTasksActorRank[b.Role]))
			}
			return 0
		case "-createdAt":
			// shared tail only
		case "title":
			if c := strings.Compare(strings.ToLower(a.Title), strings.ToLower(b.Title)); c != 0 {
				return c
			}
		case "-title":
			if c := strings.Compare(strings.ToLower(b.Title), strings.ToLower(a.Title)); c != 0 {
				return c
			}
		default:
			panic("unknown sort key " + key)
		}
		return tailCmp(a, b, withRole)
	}
	sort.SliceStable(out, func(i, j int) bool { return less(out[i], out[j]) < 0 })
	return out
}

// myTasksResponse is the slice of GET /me/tasks the tests read.
type myTasksResponse struct {
	Total      int64   `json:"total"`
	NextCursor *string `json:"nextCursor"`
	Tasks      []struct {
		ID        string `json:"id"`
		ActorRole string `json:"actorRole"`
	} `json:"tasks"`
}

func (r myTasksResponse) keys() []string {
	out := make([]string, 0, len(r.Tasks))
	for _, tk := range r.Tasks {
		out = append(out, tk.ID+":"+tk.ActorRole)
	}
	return out
}

func getMyTasks(t *testing.T, token string, q url.Values) myTasksResponse {
	t.Helper()
	u := testServerURL + "/me/tasks"
	if enc := q.Encode(); enc != "" {
		u += "?" + enc
	}
	var resp myTasksResponse
	doJSON(t, http.MethodGet, u, token, nil, &resp)
	return resp
}

// describeKeys renders "id:role" keys as "title/role" for failure output.
func (fx *myTasksFixture) describeKeys(keys []string) string {
	parts := make([]string, len(keys))
	for i, k := range keys {
		id, role, _ := strings.Cut(k, ":")
		title, ok := fx.titles[id]
		if !ok {
			title = id
		}
		parts[i] = fmt.Sprintf("%d:%s/%s", i, title, role)
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

// setDiff reports keys present in want but not got (missing) and the reverse (extra).
func setDiff(want, got []string) (missing, extra []string) {
	w := map[string]int{}
	for _, k := range want {
		w[k]++
	}
	for _, k := range got {
		w[k]--
	}
	for k, n := range w {
		for ; n > 0; n-- {
			missing = append(missing, k)
		}
		for ; n < 0; n++ {
			extra = append(extra, k)
		}
	}
	sort.Strings(missing)
	sort.Strings(extra)
	return
}

// requireSameRows fails with the set diff, then the order diff.
func (fx *myTasksFixture) requireSameRows(t *testing.T, label string, want []myTasksFixtureRow, got myTasksResponse) {
	t.Helper()
	wantKeys := make([]string, len(want))
	for i, r := range want {
		wantKeys[i] = r.key()
	}
	gotKeys := got.keys()
	missing, extra := setDiff(wantKeys, gotKeys)
	require.Emptyf(t, append(append([]string{}, missing...), extra...),
		"%s: row set differs\n  missing: %s\n  extra:   %s\n  want: %s\n  got:  %s",
		label, fx.describeKeys(missing), fx.describeKeys(extra), fx.describeKeys(wantKeys), fx.describeKeys(gotKeys))
	require.Equalf(t, int64(len(want)), got.Total, "%s: total", label)
	require.Equalf(t, wantKeys, gotKeys, "%s: row order differs\n  want: %s\n  got:  %s",
		label, fx.describeKeys(wantKeys), fx.describeKeys(gotKeys))
	require.Nilf(t, got.NextCursor, "%s: nextCursor must be null for filtered/sorted responses", label)
}

// myTasksPairwise is the 54-row pairwise table, copied verbatim from the
// research case file. Columns: q, state, priority, workspaceId, role, sort.
var myTasksPairwise = [][6]string{
	{"nomatch", "absent", "p0+p2", "own", "reviewer+watcher", "-priority"},
	{"nomatch", "open+done", "absent", "malformed", "assignee", "-dueOn"},
	{"match", "open", "p0+p2", "malformed", "bogus", "dueOn"},
	{"wildcard", "open", "absent", "absent", "reviewer+watcher", "createdAt"},
	{"match", "bogus", "p4", "joined", "reviewer+watcher", "absent"},
	{"absent", "open+done", "p4", "absent", "absent", "updatedAt"},
	{"nomatch", "open", "p0+p2", "joined", "absent", "-updatedAt"},
	{"absent", "bogus", "p0+p2", "own", "assignee", "title"},
	{"wildcard", "open", "p4", "own", "bogus", "-dueOn"},
	{"absent", "open", "absent", "joined", "assignee", "priority"},
	{"wildcard", "open+done", "p0+p2", "joined", "bogus", "-createdAt"},
	{"nomatch", "absent", "p4", "joined", "bogus", "-title"},
	{"match", "bogus", "absent", "own", "absent", "-title"},
	{"wildcard", "absent", "p4", "malformed", "absent", "title"},
	{"match", "absent", "absent", "absent", "assignee", "-createdAt"},
	{"absent", "open+done", "p0+p2", "malformed", "reviewer+watcher", "-title"},
	{"match", "open+done", "p0+p2", "absent", "reviewer+watcher", "priority"},
	{"wildcard", "open+done", "p4", "joined", "assignee", "dueOn"},
	{"match", "open", "absent", "malformed", "bogus", "updatedAt"},
	{"nomatch", "open", "p4", "joined", "absent", "-createdAt"},
	{"nomatch", "bogus", "p0+p2", "absent", "bogus", "updatedAt"},
	{"nomatch", "open", "absent", "absent", "reviewer+watcher", "title"},
	{"absent", "absent", "absent", "malformed", "bogus", "absent"},
	{"wildcard", "bogus", "p4", "malformed", "bogus", "priority"},
	{"nomatch", "open", "p0+p2", "absent", "absent", "absent"},
	{"match", "open+done", "p4", "malformed", "assignee", "-priority"},
	{"match", "absent", "p0+p2", "joined", "reviewer+watcher", "-dueOn"},
	{"nomatch", "absent", "p0+p2", "joined", "absent", "createdAt"},
	{"absent", "open", "absent", "joined", "reviewer+watcher", "-priority"},
	{"wildcard", "open", "absent", "joined", "assignee", "updatedAt"},
	{"match", "bogus", "p4", "absent", "assignee", "-updatedAt"},
	{"match", "open+done", "p4", "own", "assignee", "createdAt"},
	{"absent", "bogus", "p0+p2", "malformed", "bogus", "createdAt"},
	{"match", "open+done", "p0+p2", "joined", "bogus", "title"},
	{"match", "absent", "p4", "own", "reviewer+watcher", "updatedAt"},
	{"absent", "bogus", "p4", "own", "reviewer+watcher", "-createdAt"},
	{"wildcard", "bogus", "p4", "absent", "bogus", "-priority"},
	{"nomatch", "absent", "p4", "own", "absent", "priority"},
	{"nomatch", "open", "p0+p2", "absent", "assignee", "-title"},
	{"absent", "bogus", "absent", "absent", "reviewer+watcher", "dueOn"},
	{"wildcard", "open", "absent", "malformed", "reviewer+watcher", "-createdAt"},
	{"nomatch", "absent", "p4", "joined", "reviewer+watcher", "dueOn"},
	{"match", "open", "p4", "own", "reviewer+watcher", "-updatedAt"},
	{"wildcard", "open", "absent", "joined", "bogus", "-updatedAt"},
	{"absent", "absent", "absent", "malformed", "assignee", "-updatedAt"},
	{"absent", "open", "absent", "absent", "reviewer+watcher", "-dueOn"},
	{"wildcard", "absent", "p4", "joined", "bogus", "absent"},
	{"absent", "open", "p0+p2", "malformed", "absent", "-dueOn"},
	{"absent", "open+done", "p4", "own", "assignee", "absent"},
	{"match", "open", "absent", "own", "absent", "dueOn"},
	{"match", "bogus", "absent", "malformed", "bogus", "-dueOn"},
	{"match", "open+done", "p4", "absent", "absent", "-priority"},
	{"wildcard", "absent", "p0+p2", "absent", "reviewer+watcher", "-title"},
	{"match", "open+done", "absent", "joined", "reviewer+watcher", "-updatedAt"},
}

var myTasksSortKeys = []string{"dueOn", "-dueOn", "priority", "-priority", "updatedAt", "-updatedAt", "createdAt", "-createdAt", "title", "-title"}

// TestMyTasksFilterSortMatrix checks GET /me/tasks against a reference
// implementation of the filter and ORDER BY rules: (a) the pairwise table,
// (b) every sort key with no filters, (c) no parameters (legacy order).
func TestMyTasksFilterSortMatrix(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	fx := buildMyTasksFixture(t)
	require.Len(t, fx.rows, 11, "fixture: 10 tasks, one with two roles")

	t.Run("pairwise", func(t *testing.T) {
		for i, row := range myTasksPairwise {
			c := myTasksCase{q: row[0], state: row[1], priority: row[2], workspaceID: row[3], role: row[4], sort: row[5]}
			t.Run(fmt.Sprintf("row%02d", i), func(t *testing.T) {
				t.Parallel()
				q := c.values(fx)
				label := fmt.Sprintf("pairwise row %d %v query=%q", i, row, q.Encode())
				want := refSort(c.refFilter(fx), c.sort, true)
				fx.requireSameRows(t, label, want, getMyTasks(t, fx.token, q))
			})
		}
	})

	t.Run("sortOnly", func(t *testing.T) {
		for i, key := range myTasksSortKeys {
			t.Run(fmt.Sprintf("row%02d_%s", i, key), func(t *testing.T) {
				t.Parallel()
				c := myTasksCase{q: "absent", state: "absent", priority: "absent", workspaceID: "absent", role: "absent", sort: key}
				label := fmt.Sprintf("sort-only row %d sort=%s", i, key)
				want := refSort(c.refFilter(fx), key, true)
				fx.requireSameRows(t, label, want, getMyTasks(t, fx.token, c.values(fx)))
			})
		}
	})

	t.Run("noParams", func(t *testing.T) {
		t.Parallel()
		// Legacy order. Rows equal on every key (the two rows of the
		// multi-role task) have no defined order and compare as a set.
		sorted := refSort(fx.rows, "absent", false)
		got := getMyTasks(t, fx.token, nil)
		require.Equal(t, int64(len(sorted)), got.Total, "no-param total")
		gotKeys := got.keys()
		require.Lenf(t, gotKeys, len(sorted), "no-param row count\n  got: %s", fx.describeKeys(gotKeys))
		for start := 0; start < len(sorted); {
			end := start + 1
			for end < len(sorted) && sorted[end].ID == sorted[start].ID {
				end++
			}
			wantGroup := make([]string, 0, end-start)
			for _, r := range sorted[start:end] {
				wantGroup = append(wantGroup, r.key())
			}
			missing, extra := setDiff(wantGroup, gotKeys[start:end])
			require.Emptyf(t, append(append([]string{}, missing...), extra...),
				"no-param rows %d..%d: group differs\n  want (any order): %s\n  got: %s\n  full got: %s",
				start, end-1, fx.describeKeys(wantGroup), fx.describeKeys(gotKeys[start:end]), fx.describeKeys(gotKeys))
			start = end
		}
	})
}

// TestMyTasksCursorRejectsFilters checks that a cursor combined with any
// filter or sort parameter is refused, and a cursor alone still pages.
func TestMyTasksCursorRejectsFilters(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	fx := buildMyTasksFixture(t)
	first := getMyTasks(t, fx.token, url.Values{"limit": {"1"}})
	require.NotNil(t, first.NextCursor, "limit=1 over 11 rows must emit nextCursor")
	cursor := *first.NextCursor

	status, body := doJSONStatus(t, http.MethodGet,
		testServerURL+"/me/tasks?"+url.Values{"cursor": {cursor}}.Encode(), fx.token, nil)
	require.Equalf(t, http.StatusOK, status, "cursor alone must page; body=%s", string(body))

	extras := []struct{ name, value string }{
		{"q", "alpha"},
		{"state", "open"},
		{"priority", "4"},
		{"workspaceId", fx.own},
		{"role", "assignee"},
		{"sort", "dueOn"},
	}
	for i, e := range extras {
		t.Run(fmt.Sprintf("%d_%s", i, e.name), func(t *testing.T) {
			t.Parallel()
			q := url.Values{"cursor": {cursor}}
			q.Set(e.name, e.value)
			status, body := doJSONStatus(t, http.MethodGet, testServerURL+"/me/tasks?"+q.Encode(), fx.token, nil)
			requireDenied(t, status, body, http.StatusUnprocessableEntity,
				"VALIDATION.QUERY.FIELD_INVALID", "cursor + "+e.name)
		})
	}
}

// TestMyTasksStateFilterAppliesBeforeLimit checks that the state filter is
// applied before LIMIT: high-priority done rows must not crowd open rows out.
func TestMyTasksStateFilterAppliesBeforeLimit(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	u := newTenant(t)
	create := func(title string, priority int32, transitions ...string) string {
		body := map[string]any{"projectId": u.ProjectPublicID, "title": title}
		if priority != 0 {
			body["priority"] = priority
		}
		var task struct {
			ID string `json:"id"`
		}
		doJSON(t, http.MethodPost, testServerURL+"/tasks", u.AccessToken, body, &task)
		for _, tr := range transitions {
			doJSON(t, http.MethodPost, testServerURL+"/tasks/"+task.ID+"/transitions",
				u.AccessToken, map[string]any{"transition": tr}, nil)
		}
		return task.ID
	}
	done := map[string]bool{}
	for i := 0; i < 3; i++ {
		done[create("done "+strconv.Itoa(i), 4, "complete")] = true
	}
	open := map[string]bool{}
	for i := 0; i < 2; i++ {
		open[create("open "+strconv.Itoa(i), 0)] = true
	}

	ids := func(r myTasksResponse) map[string]bool {
		m := map[string]bool{}
		for _, tk := range r.Tasks {
			m[tk.ID] = true
		}
		return m
	}

	unfiltered := getMyTasks(t, u.AccessToken, url.Values{"limit": {"2"}})
	require.Lenf(t, unfiltered.Tasks, 2, "limit=2 returns two rows; got %v", unfiltered.keys())
	for _, tk := range unfiltered.Tasks {
		require.Truef(t, done[tk.ID], "without a filter the highest-priority rows are done tasks; got %v", unfiltered.keys())
	}

	filtered := getMyTasks(t, u.AccessToken, url.Values{"state": {"open"}, "limit": {"2"}})
	require.Equalf(t, open, ids(filtered), "state=open&limit=2 must return both open tasks; got %v", filtered.keys())
	require.Equal(t, int64(2), filtered.Total, "total counts only the rows matching the filter")
	require.Nil(t, filtered.NextCursor, "filtered response has no cursor")
}
