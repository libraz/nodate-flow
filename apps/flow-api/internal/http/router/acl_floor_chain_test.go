// Package router role-floor chain tests.
//
// TestEveryMutatingOpHasARoleFloor walks the floor label recorded for each
// operation. A label is a ledger entry: it says a floor was chosen, not that
// anything enforces it. Deleting the sub.Use call inside mountGroup while
// leaving the label in place keeps that test green and lets guests write
// again — the exact "the shared guard exists but this caller does not run
// it" shape the role floors were introduced to close.
//
// The tests here close the loop from the other side: they take the
// middleware chain mountGroup actually left on the chi group and drive
// requests through it, so a floor that is recorded but never mounted
// fails here.
//
// What they do not cover: which role a mounted floor demands. Every one
// of these middlewares denies a request whose ACL context was never
// resolved, so lowering a floor from member to guest still refuses the
// empty-context request used here. The role level itself is pinned
// behaviourally against a real database by the guest write-floor e2e
// suite; these tests answer the narrower question that the label ledger
// cannot — whether anything is mounted at all.
package router

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/auth"
)

// mutatingMethods are the verbs a role floor has to stop.
var mutatingMethods = []string{
	http.MethodPost,
	http.MethodPut,
	http.MethodPatch,
	http.MethodDelete,
}

// floorContract describes what a floor label promises once mounted, in
// terms observable without a database: every one of these middlewares
// denies when the ACL context it needs was never resolved, so an empty
// request context is enough to tell an enforcing chain from an empty one.
type floorContract struct {
	// floor is the value production code hands to mountGroup.
	floor aclFloor
	// deniesSafeMethods is true for floors that gate reads as well as
	// writes (the admin and project-role floors), false for the
	// write-only workspace floor that deliberately leaves guests their
	// read access.
	deniesSafeMethods bool
}

func floorContracts() []floorContract {
	return []floorContract{
		{floor: floorWorkspaceMember, deniesSafeMethods: false},
		{floor: floorWorkspaceAdmin, deniesSafeMethods: true},
		{floor: floorProjectCommenter, deniesSafeMethods: true},
		{floor: floorProjectEditor, deniesSafeMethods: true},
		{floor: floorProjectLead, deniesSafeMethods: true},
	}
}

// sentinelHandler records whether the request reached the handler behind
// the middleware chain. Reaching it is the failure the floor exists to
// prevent.
type sentinelHandler struct{ reached bool }

func (s *sentinelHandler) ServeHTTP(w http.ResponseWriter, _ *http.Request) {
	s.reached = true
	w.WriteHeader(http.StatusOK)
}

// mountedFloorChain returns the middleware mountGroup added to a fresh chi
// group for the given floor label, composed into a single handler in front
// of sentinel.
//
// mountGroup is documented as the last Use call on a group, so everything
// it appends to an otherwise-empty router is the floor and nothing else.
func mountedFloorChain(t *testing.T, floor aclFloor, sentinel http.Handler) (http.Handler, int) {
	t.Helper()
	sub := chi.NewRouter()
	before := len(sub.Middlewares())
	var apis []groupAPI
	if api := mountGroup(sub, floor, &apis); api == nil {
		t.Fatalf("mountGroup(%q) returned no huma API", floor.label)
	}
	if len(apis) != 1 || apis[0].floor != floor.label {
		t.Fatalf("mountGroup recorded %+v, want one group carrying label %q", apis, floor.label)
	}
	mws := sub.Middlewares()
	handler := sentinel
	for i := len(mws) - 1; i >= before; i-- {
		handler = mws[i](handler)
	}
	return handler, len(mws) - before
}

// TestMountGroupMountsTheFloorItRecords is the anti-ledger check: for every
// floor label, the middleware chain left behind must actually refuse a
// request that carries no resolved ACL context.
//
// A floor whose sub.Use call is removed shows up here twice — as a chain
// that gained no middleware, and as a mutating request that reached the
// sentinel.
func TestMountGroupMountsTheFloorItRecords(t *testing.T) {
	t.Parallel()

	for _, fc := range floorContracts() {
		t.Run(string(fc.floor.label), func(t *testing.T) {
			t.Parallel()

			for _, method := range mutatingMethods {
				sentinel := &sentinelHandler{}
				chain, mounted := mountedFloorChain(t, fc.floor, sentinel)
				if mounted == 0 {
					t.Fatalf("floor %q records a label but mounts no middleware — the group enforces nothing", fc.floor.label)
				}

				rec := httptest.NewRecorder()
				chain.ServeHTTP(rec, httptest.NewRequest(method, "/workspaces/x/labels", nil))

				if sentinel.reached {
					t.Errorf("%s reached the handler through the %q floor with no ACL context resolved", method, fc.floor.label)
				}
				if rec.Code != http.StatusForbidden {
					t.Errorf("%s through the %q floor = %d, want %d", method, fc.floor.label, rec.Code, http.StatusForbidden)
				}
			}
		})
	}
}

// TestWorkspaceMemberFloorLeavesReadsOpen pins the other half of the
// workspace floor's contract. Guests are the read-only workspace role, so a
// floor that also blocked GET would be a different (and silently
// functionality-breaking) policy from the one the group documents.
func TestWorkspaceMemberFloorLeavesReadsOpen(t *testing.T) {
	t.Parallel()

	for _, method := range []string{http.MethodGet, http.MethodHead, http.MethodOptions} {
		sentinel := &sentinelHandler{}
		chain, _ := mountedFloorChain(t, floorWorkspaceMember, sentinel)

		rec := httptest.NewRecorder()
		chain.ServeHTTP(rec, httptest.NewRequest(method, "/workspaces/x/labels", nil))

		if !sentinel.reached {
			t.Errorf("%s was blocked by the %q floor; reads stay open to every workspace member", method, floorWorkspaceMember.label)
		}
		if rec.Code != http.StatusOK {
			t.Errorf("%s through the %q floor = %d, want %d", method, floorWorkspaceMember.label, rec.Code, http.StatusOK)
		}
	}
}

// TestFloorsGateEveryMethodWhereDeclared covers the floors that are not
// write-only: the admin and project-role middlewares run on reads too, so a
// group that mounts one keeps unresolved callers out entirely.
func TestFloorsGateEveryMethodWhereDeclared(t *testing.T) {
	t.Parallel()

	for _, fc := range floorContracts() {
		if !fc.deniesSafeMethods {
			continue
		}
		t.Run(string(fc.floor.label), func(t *testing.T) {
			t.Parallel()

			sentinel := &sentinelHandler{}
			chain, _ := mountedFloorChain(t, fc.floor, sentinel)

			rec := httptest.NewRecorder()
			chain.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/workspaces/x/labels", nil))

			if sentinel.reached {
				t.Errorf("GET reached the handler through the %q floor with no ACL context resolved", fc.floor.label)
			}
			if rec.Code != http.StatusForbidden {
				t.Errorf("GET through the %q floor = %d, want %d", fc.floor.label, rec.Code, http.StatusForbidden)
			}
		})
	}
}

// TestFloorNoneMountsNothing keeps the empty label honest: it is the label
// groups use when their ACL lives in the handler, and it must not quietly
// acquire a middleware that the operation inventory would then under-report.
func TestFloorNoneMountsNothing(t *testing.T) {
	t.Parallel()

	sentinel := &sentinelHandler{}
	chain, mounted := mountedFloorChain(t, floorNone, sentinel)
	if mounted != 0 {
		t.Fatalf("floorNone mounted %d middlewares, want 0", mounted)
	}

	rec := httptest.NewRecorder()
	chain.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/workspaces/x/labels", nil))
	if !sentinel.reached {
		t.Error("floorNone blocked a request; groups using it enforce their ACL in the handler")
	}
}

// TestEveryRecordedFloorHasAContract makes the contract table exhaustive:
// a new floor label added to mountGroup without a row here would otherwise
// be mounted by production code and checked by nothing.
//
// Exhaustiveness is measured against two sets, because they fail apart. A
// floor can reach a registered operation without a contract (production
// mounts something no test drives), and a floor can be added to the shared
// vocabulary while no group mounts it yet (the next group to pick it up
// inherits a middleware nothing has exercised).
func TestEveryRecordedFloorHasAContract(t *testing.T) {
	t.Parallel()

	covered := map[auth.Floor]bool{floorNone.label: true}
	for _, fc := range floorContracts() {
		covered[fc.floor.label] = true
	}

	res := BuildResult(stubDeps(t))
	for _, op := range res.AuthenticatedOps {
		if !covered[op.WriteFloor] {
			t.Errorf("operation %s carries floor %q, which has no row in floorContracts — add one so the floor's middleware is exercised",
				op.OperationID, op.WriteFloor)
		}
	}

	for _, floor := range auth.Floors() {
		if !covered[floor] {
			t.Errorf("the shared floor vocabulary declares %q, which has no row in floorContracts — add one so the floor is exercised before a group adopts it",
				floor)
		}
	}
}
