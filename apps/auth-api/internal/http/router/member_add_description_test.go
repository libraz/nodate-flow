package router

import (
	"strings"
	"testing"

	"github.com/danielgtaylor/huma/v2"
)

// operationByID finds a registered operation across every sub-API.
func operationByID(t *testing.T, res Result, id string) *huma.Operation {
	t.Helper()
	for _, a := range res.APIs {
		spec := a.OpenAPI()
		if spec == nil || spec.Paths == nil {
			continue
		}
		for _, item := range spec.Paths {
			if item == nil {
				continue
			}
			for _, op := range []*huma.Operation{
				item.Get, item.Post, item.Put, item.Patch, item.Delete, item.Head, item.Options,
			} {
				if op != nil && op.OperationID == id {
					return op
				}
			}
		}
	}
	t.Fatalf("operation %q is not registered", id)
	return nil
}

// TestAddMemberIsNotDescribedAsAnInvite keeps the two membership paths
// distinguishable to anyone reading the spec.
//
// POST /workspaces/{wsId}/members grants a seat immediately: no token is
// minted, nothing is mailed, and the address is never asked. It was
// nevertheless documented as sending an invite the recipient redeems,
// and that description shipped in the spec and the SDK — so every
// integration that wanted to invite somebody by email reached for the
// endpoint that instead handed the address a working seat. The
// description is the only thing an integrator reads before choosing, so
// it is what this guards.
func TestAddMemberIsNotDescribedAsAnInvite(t *testing.T) {
	t.Parallel()
	res := BuildResult(stubDeps(t))

	add := operationByID(t, res, "workspaces-members-add")
	desc := strings.ToLower(add.Description)
	for _, promise := range []string{"redeem", "invite to", "sends a workspace invite", "gets a link"} {
		if strings.Contains(desc, promise) {
			t.Errorf("workspaces-members-add promises %q, but it creates the membership outright: %s",
				promise, add.Description)
		}
	}
	if !strings.Contains(desc, "immediately") {
		t.Errorf("workspaces-members-add must say the grant takes effect without acceptance: %s",
			add.Description)
	}

	// The endpoint the description now points callers at has to exist and
	// has to be the one that actually mints a token.
	invite := operationByID(t, res, "workspaces-invites-create")
	if !strings.Contains(strings.ToLower(invite.Description), "token") {
		t.Errorf("workspaces-invites-create is the consent path; its description must name the token: %s",
			invite.Description)
	}
	if !strings.Contains(add.Description, invite.Path) {
		t.Errorf("workspaces-members-add must point callers who want consent at %s: %s",
			invite.Path, add.Description)
	}
}
