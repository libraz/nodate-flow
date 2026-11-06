package execution

import (
	"errors"
	"testing"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
)

func TestNewAgentPrincipalCopiesAndBoundsCapabilities(t *testing.T) {
	target := types.New()
	scopes := []string{ScopeWriteWorkspace}
	tools := []string{ToolReadTask, ToolAddComment}
	targets := []types.PublicID{target}

	principal, err := NewAgentPrincipal(7, 11, scopes, tools, targets, 13)
	if err != nil {
		t.Fatalf("NewAgentPrincipal: %v", err)
	}

	scopes[0] = ScopeReadWorkspace
	tools[0] = ToolSetPriority
	targets[0] = types.New()
	if !principal.HasScope(ScopeWriteWorkspace) || !principal.HasScope(ScopeReadWorkspace) {
		t.Fatal("principal capabilities changed after caller slices were mutated")
	}
	if !principal.HasTool(ToolReadTask) || principal.HasTool(ToolSetPriority) {
		t.Fatal("principal tools changed after caller slices were mutated")
	}
	if !principal.CapturedTarget(target) || principal.CapturedTarget(targets[0]) {
		t.Fatal("principal targets changed after caller slice was mutated")
	}
	if principal.UserID() != 0 || principal.AgentID() != 11 || principal.DelegatedByUserID() != 13 {
		t.Fatalf("unexpected identity tuple: user=%d agent=%d delegator=%d", principal.UserID(), principal.AgentID(), principal.DelegatedByUserID())
	}

	gotScopes := principal.Scopes()
	gotTools := principal.Tools()
	gotTargets := principal.Targets()
	gotScopes[0] = ScopeReadWorkspace
	gotTools[0] = ToolSetPriority
	gotTargets[0] = types.New()
	if !principal.HasScope(ScopeWriteWorkspace) || !principal.HasTool(ToolReadTask) || !principal.CapturedTarget(target) {
		t.Fatal("principal accessors leaked mutable slices")
	}
}

func TestPrincipalConstructorsRejectInvalidIdentityAndCapabilities(t *testing.T) {
	if _, err := NewUserPrincipal(1, 0); !errors.Is(err, ErrInvalidPrincipal) {
		t.Fatalf("NewUserPrincipal zero user error = %v, want ErrInvalidPrincipal", err)
	}
	if err := (Principal{}).Validate(); !errors.Is(err, ErrInvalidPrincipal) {
		t.Fatalf("zero principal Validate error = %v, want ErrInvalidPrincipal", err)
	}
	for _, tc := range []struct {
		name    string
		scopes  []string
		tools   []string
		targets []types.PublicID
	}{
		{name: "unknown scope", scopes: []string{"write:task"}, tools: []string{ToolAddComment}, targets: []types.PublicID{types.New()}},
		{name: "duplicate scope", scopes: []string{ScopeReadWorkspace, ScopeReadWorkspace}, tools: []string{ToolReadTask}, targets: []types.PublicID{types.New()}},
		{name: "empty tool", scopes: []string{ScopeWriteWorkspace}, tools: []string{""}, targets: []types.PublicID{types.New()}},
		{name: "duplicate tool", scopes: []string{ScopeWriteWorkspace}, tools: []string{ToolReadTask, ToolReadTask}, targets: []types.PublicID{types.New()}},
		{name: "empty target", scopes: []string{ScopeReadWorkspace}, tools: []string{ToolReadTask}, targets: []types.PublicID{{}}},
		{name: "duplicate target", scopes: []string{ScopeReadWorkspace}, tools: []string{ToolReadTask}, targets: []types.PublicID{types.New(), types.New()}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.name == "duplicate target" {
				tc.targets[1] = tc.targets[0]
			}
			_, err := NewAgentPrincipal(1, 2, tc.scopes, tc.tools, tc.targets, 0)
			if !errors.Is(err, ErrInvalidPrincipal) {
				t.Fatalf("NewAgentPrincipal error = %v, want ErrInvalidPrincipal", err)
			}
		})
	}
}

func TestNewAgentPrincipalPreservesOtherConfiguredToolsWithoutGrantingThemServiceAccess(t *testing.T) {
	principal, err := NewAgentPrincipal(1, 2, []string{ScopeReadWorkspace}, []string{"propose_steps"}, []types.PublicID{types.New()}, 0)
	if err != nil {
		t.Fatalf("NewAgentPrincipal: %v", err)
	}
	if !principal.HasTool("propose_steps") {
		t.Fatal("principal did not preserve its configured tool list")
	}
	if principal.HasTool(ToolSetPriority) {
		t.Fatal("an unconfigured execution capability was granted")
	}
}
