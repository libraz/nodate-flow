package execution

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/acl"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
)

func TestToolGrants_Parse(t *testing.T) {
	for _, tc := range []struct {
		name    string
		raw     []byte
		want    ToolGrants
		invalid bool
	}{
		{name: "sql null", raw: nil, want: nil},
		{name: "json null", raw: []byte(`null`), want: nil},
		{name: "empty object", raw: []byte(`{}`), want: ToolGrants{}},
		{name: "modes", raw: []byte(`{"add_comment":"auto","set_priority":"approval"}`),
			want: ToolGrants{ToolAddComment: GrantModeAuto, ToolSetPriority: GrantModeApproval}},
		{name: "all tools", raw: []byte(`{"add_comment":"approval","set_priority":"approval","set_due_on":"approval","transition_task":"approval","create_subtask":"approval"}`),
			want: DefaultTaskAgentTools()},
		{name: "array", raw: []byte(`["add_comment"]`), invalid: true},
		{name: "empty array", raw: []byte(`[]`), invalid: true},
		{name: "string scalar", raw: []byte(`"add_comment"`), invalid: true},
		{name: "number scalar", raw: []byte(`1`), invalid: true},
		{name: "malformed", raw: []byte(`{`), invalid: true},
		{name: "unknown key", raw: []byte(`{"delete_task":"auto"}`), invalid: true},
		{name: "read_task is not grantable", raw: []byte(`{"read_task":"auto"}`), invalid: true},
		{name: "unknown mode", raw: []byte(`{"add_comment":"always"}`), invalid: true},
		{name: "empty mode", raw: []byte(`{"add_comment":""}`), invalid: true},
		{name: "null mode", raw: []byte(`{"add_comment":null}`), invalid: true},
		{name: "non-string mode", raw: []byte(`{"add_comment":true}`), invalid: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseToolGrants(tc.raw)
			if tc.invalid {
				if !errors.Is(err, ErrInvalidToolGrants) {
					t.Fatalf("ParseToolGrants error = %v, want ErrInvalidToolGrants", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseToolGrants: %v", err)
			}
			if (got == nil) != (tc.want == nil) || len(got) != len(tc.want) {
				t.Fatalf("ParseToolGrants = %#v, want %#v", got, tc.want)
			}
			for tool, mode := range tc.want {
				if got[tool] != mode {
					t.Fatalf("ParseToolGrants[%s] = %q, want %q", tool, got[tool], mode)
				}
			}
		})
	}
}

func TestToolGrants_Validate(t *testing.T) {
	if _, err := ValidateToolGrants(map[string]string{}); err != nil {
		t.Fatalf("empty map: %v", err)
	}
	for name, raw := range map[string]map[string]string{
		"unknown tool": {"rename_task": "auto"},
		"unknown mode": {"set_due_on": "manual"},
		"null mode":    {"set_due_on": ""},
	} {
		if _, err := ValidateToolGrants(raw); !errors.Is(err, ErrInvalidToolGrants) {
			t.Fatalf("%s: error = %v, want ErrInvalidToolGrants", name, err)
		}
	}
}

func TestToolGrants_DefaultAndMarshal(t *testing.T) {
	defaults := DefaultTaskAgentTools()
	if len(defaults) != 5 {
		t.Fatalf("default grants = %v, want 5 tools", defaults)
	}
	for tool, mode := range defaults {
		if mode != GrantModeApproval {
			t.Fatalf("default mode for %s = %q, want approval", tool, mode)
		}
	}
	defaults[ToolAddComment] = GrantModeAuto
	if DefaultTaskAgentTools()[ToolAddComment] != GrantModeApproval {
		t.Fatal("DefaultTaskAgentTools returned shared state")
	}

	raw, err := MarshalToolGrants(DefaultTaskAgentTools())
	if err != nil {
		t.Fatalf("MarshalToolGrants: %v", err)
	}
	const want = `{"add_comment":"approval","create_subtask":"approval","set_due_on":"approval","set_priority":"approval","transition_task":"approval"}`
	if string(raw) != want {
		t.Fatalf("MarshalToolGrants = %s, want %s", raw, want)
	}
	if raw, err := MarshalToolGrants(nil); err != nil || string(raw) != `{}` {
		t.Fatalf("MarshalToolGrants(nil) = %s, %v; want {}", raw, err)
	}
	if _, err := MarshalToolGrants(ToolGrants{"delete_task": GrantModeAuto}); !errors.Is(err, ErrInvalidToolGrants) {
		t.Fatalf("MarshalToolGrants unknown tool error = %v", err)
	}
}

func TestToolGrants_CapabilityFloors(t *testing.T) {
	for _, tc := range []struct {
		tool  Tool
		floor acl.ProjectRole
	}{
		{ToolAddComment, acl.ProjectRoleCommenter},
		{ToolSetPriority, acl.ProjectRoleEditor},
		{ToolSetDueOn, acl.ProjectRoleEditor},
		{ToolTransitionTask, acl.ProjectRoleEditor},
		{ToolCreateSubtask, acl.ProjectRoleEditor},
	} {
		req, err := requirementFor(tc.tool.Capability())
		if err != nil {
			t.Fatalf("requirementFor(%s): %v", tc.tool, err)
		}
		if req.tool != tc.tool || req.floor != tc.floor || req.scope != ScopeWriteWorkspace {
			t.Fatalf("requirementFor(%s) = %+v, want tool %s floor %s write scope", tc.tool, req, tc.tool, tc.floor)
		}
	}
	req, err := requirementFor(CapabilityReadTask)
	if err != nil || req.tool != "" {
		t.Fatalf("read requirement = %+v, %v; want no tool", req, err)
	}
}

func TestAuthorize_ModeReturned(t *testing.T) {
	target := types.New()
	rec := acl.TaskRecord{ID: 3, WorkspaceID: 1, ProjectID: 4, Visibility: acl.TaskVisibilityPublic}
	allTools := []string{ToolAddComment, ToolSetPriority, ToolSetDueOn, ToolTransitionTask, ToolCreateSubtask}

	for _, tc := range []struct {
		name       string
		toolsJSON  []byte
		capability Capability
		tools      []string
		wantMode   GrantMode
		wantErr    *apierrors.Spec
	}{
		{name: "auto", toolsJSON: []byte(`{"set_priority":"auto"}`), capability: CapabilitySetPriority, tools: allTools, wantMode: GrantModeAuto},
		{name: "approval", toolsJSON: []byte(`{"add_comment":"approval"}`), capability: CapabilityAddComment, tools: allTools, wantMode: GrantModeApproval},
		{name: "new tool", toolsJSON: []byte(`{"create_subtask":"auto"}`), capability: CapabilityCreateSubtask, tools: allTools, wantMode: GrantModeAuto},
		{name: "read needs no grant", toolsJSON: []byte(`{}`), capability: CapabilityReadTask, tools: nil, wantMode: ""},
		{name: "read with sql null grants", toolsJSON: nil, capability: CapabilityReadTask, tools: nil, wantMode: ""},
		{name: "tool absent from grant", toolsJSON: []byte(`{"add_comment":"auto"}`), capability: CapabilitySetDueOn, tools: allTools, wantErr: apierrors.McpScopeInsufficient},
		{name: "json null grants", toolsJSON: []byte(`null`), capability: CapabilityTransitionTask, tools: allTools, wantErr: apierrors.McpScopeInsufficient},
		{name: "tool absent from principal", toolsJSON: []byte(`{"set_due_on":"auto"}`), capability: CapabilitySetDueOn, tools: []string{ToolAddComment}, wantErr: apierrors.McpScopeInsufficient},
		{name: "array fails closed", toolsJSON: []byte(`["set_priority"]`), capability: CapabilitySetPriority, tools: allTools, wantErr: apierrors.InternalUnexpected},
		{name: "array fails closed for read", toolsJSON: []byte(`["read_task"]`), capability: CapabilityReadTask, tools: nil, wantErr: apierrors.InternalUnexpected},
		{name: "unknown mode fails closed", toolsJSON: []byte(`{"set_priority":"yes"}`), capability: CapabilitySetPriority, tools: allTools, wantErr: apierrors.InternalUnexpected},
	} {
		t.Run(tc.name, func(t *testing.T) {
			principal, err := NewAgentPrincipal(rec.WorkspaceID, 9, []string{ScopeWriteWorkspace}, tc.tools, []types.PublicID{target}, 0)
			if err != nil {
				t.Fatalf("NewAgentPrincipal: %v", err)
			}
			req, err := requirementFor(tc.capability)
			if err != nil {
				t.Fatalf("requirementFor: %v", err)
			}
			db := sql.OpenDB(&fakeConnector{toolsJSON: tc.toolsJSON})
			t.Cleanup(func() { _ = db.Close() })

			authz, err := authorizeAgentTaskTx(context.Background(), db, principal, rec, target, req)
			if tc.wantErr != nil {
				if !errors.Is(err, apierrors.New(tc.wantErr)) {
					t.Fatalf("error = %v, want %s", err, tc.wantErr.Code)
				}
				return
			}
			if err != nil {
				t.Fatalf("authorizeAgentTaskTx: %v", err)
			}
			if authz.Mode != tc.wantMode {
				t.Fatalf("Mode = %q, want %q", authz.Mode, tc.wantMode)
			}
			if authz.Agent == nil || authz.Agent.ID != 9 {
				t.Fatalf("Agent = %+v, want agent 9", authz.Agent)
			}
		})
	}
}

// fakeConnector answers the agent row and assignment reads made by
// authorizeAgentTaskTx: an enabled, unpaused, assigned agent.
type fakeConnector struct{ toolsJSON []byte }

func (c *fakeConnector) Connect(context.Context) (driver.Conn, error) { return &fakeConn{c}, nil }
func (c *fakeConnector) Driver() driver.Driver                        { return fakeDriver{} }

type fakeDriver struct{}

func (fakeDriver) Open(string) (driver.Conn, error) {
	return nil, errors.New("fake driver: use connector")
}

type fakeConn struct{ c *fakeConnector }

func (*fakeConn) Prepare(string) (driver.Stmt, error) { return nil, errors.New("fake driver: prepare") }
func (*fakeConn) Close() error                        { return nil }
func (*fakeConn) Begin() (driver.Tx, error)           { return nil, errors.New("fake driver: begin") }

func (f *fakeConn) QueryContext(_ context.Context, query string, _ []driver.NamedValue) (driver.Rows, error) {
	switch {
	case strings.Contains(query, "FROM ai_agents"):
		publicID := types.New()
		var tools driver.Value
		if f.c.toolsJSON != nil {
			tools = f.c.toolsJSON
		}
		return &fakeRows{
			cols: []string{"public_id", "name", "enabled", "paused", "tools_json", "allowed_scopes_json"},
			rows: [][]driver.Value{{publicID[:], "Agent", true, false, tools, nil}},
		}, nil
	case strings.Contains(query, "FROM task_actors"):
		return &fakeRows{cols: []string{"1"}, rows: [][]driver.Value{{int64(1)}}}, nil
	default:
		return nil, errors.New("fake driver: unexpected query " + query)
	}
}

type fakeRows struct {
	cols []string
	rows [][]driver.Value
}

func (r *fakeRows) Columns() []string { return r.cols }
func (r *fakeRows) Close() error      { return nil }
func (r *fakeRows) Next(dest []driver.Value) error {
	if len(r.rows) == 0 {
		return io.EOF
	}
	copy(dest, r.rows[0])
	r.rows = r.rows[1:]
	return nil
}
