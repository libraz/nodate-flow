package tasks

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/db/generated"
	apierrors "github.com/nodate-flow/nodate-flow/apps/flow-api/internal/errors"
	"github.com/nodate-flow/nodate-flow/apps/flow-api/internal/http/handlers/handlerutil"
	"github.com/nodate-flow/nodate-flow/packages/go-shared/apierr"
)

// TestParseActorRole_Allowed covers every value in the
// task_actors.role ENUM declaration. A regression that drops one of
// these from the validator switch would surface as a 422 here even
// though the database would still accept it.
func TestParseActorRole_Allowed(t *testing.T) {
	t.Parallel()
	cases := []struct {
		input string
		want  generated.TaskActorsRole
	}{
		{"assignee", generated.TaskActorsRoleAssignee},
		{"reviewer", generated.TaskActorsRoleReviewer},
		{"watcher", generated.TaskActorsRoleWatcher},
		{"approver", generated.TaskActorsRoleApprover},
	}
	for _, tc := range cases {
		t.Run(tc.input, func(t *testing.T) {
			t.Parallel()
			got, err := parseActorRole(tc.input)
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

// TestParseActorRole_Rejects asserts that values outside the catalog
// surface as WS.TASK.ACTOR_ROLE_INVALID with the offending input
// echoed in `received_role`. Without this guard the handler would
// pass an unknown enum value through to MySQL and surface a generic
// 500.
func TestParseActorRole_Rejects(t *testing.T) {
	t.Parallel()
	cases := []string{
		"",
		"invalid",
		"ASSIGNEE", // case-sensitive: enum is lower-case in DDL
		"owner",    // not in the task_actors.role enum
		"Assignee",
	}
	for _, in := range cases {
		t.Run(in, func(t *testing.T) {
			t.Parallel()
			role, err := parseActorRole(in)
			require.Error(t, err, "must reject %q", in)
			assert.Equal(t, generated.TaskActorsRole(""), role)

			var apiErr *apierr.APIError
			require.True(t, errors.As(err, &apiErr), "expected *apierr.APIError, got %T", err)
			require.NotNil(t, apiErr.Spec)
			assert.Equal(t, apierrors.WsTaskActorRoleInvalid.Code, apiErr.Spec.Code)
			assert.Equal(t, apierrors.WsTaskActorRoleInvalid.Status, apiErr.Spec.Status)
			assert.Equal(t, in, apiErr.Details["received_role"],
				"received_role must echo the offending input verbatim")
		})
	}
}

// TestTranslateActorRoleError_PreservesDetails confirms the helper
// converts the apierror into a ProblemDetails envelope while
// surfacing the diagnostic detail through RFC 9457 extensions.
func TestTranslateActorRoleError_PreservesDetails(t *testing.T) {
	t.Parallel()
	_, err := parseActorRole("nope")
	require.Error(t, err)

	out := translateActorRoleError(err)
	require.Error(t, out)

	var problem *handlerutil.ProblemDetails
	require.True(t, errors.As(out, &problem), "expected ProblemDetails, got %T", out)
	assert.Equal(t, apierrors.WsTaskActorRoleInvalid.Code, problem.Type)
	assert.Equal(t, apierrors.WsTaskActorRoleInvalid.Status, problem.Status)
	assert.Equal(t, "nope", problem.Extensions["received_role"],
		"detail map must be propagated through ProblemDetails extensions")
}

// TestTranslateActorRoleError_FallsBackForGenericError covers the
// defensive branch when something other than an *apierr.APIError is
// passed in. The result must still be a usable problem+json envelope
// (status 422) carrying the canonical code so the handler never
// surfaces a raw error.
func TestTranslateActorRoleError_FallsBackForGenericError(t *testing.T) {
	t.Parallel()
	out := translateActorRoleError(errors.New("not an apierror"))
	require.Error(t, out)

	var problem *handlerutil.ProblemDetails
	require.True(t, errors.As(out, &problem))
	assert.Equal(t, apierrors.WsTaskActorRoleInvalid.Code, problem.Type)
	assert.Equal(t, apierrors.WsTaskActorRoleInvalid.Status, problem.Status)
}
