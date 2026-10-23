package admin

import (
	"context"
	"errors"
	"testing"

	apierrors "github.com/libraz/nodate-flow/apps/auth-api/internal/errors"
	"github.com/libraz/nodate-flow/apps/auth-api/internal/http/handlers/handlerutil"
)

// TestPatchSettings_RefusesKeysNoCodePathReads pins that PATCH
// /admin/settings refuses every key whose behaviour nothing enforces.
// Registration, MFA policy and workspace/member limits are read from
// deployment config, so storing them would report a change that never
// happens. The refusal comes before any write, so no database is needed.
func TestPatchSettings_RefusesKeysNoCodePathReads(t *testing.T) {
	t.Parallel()

	patch := PatchSettings(Deps{})
	for _, kv := range [][2]string{
		{"registration_open", "false"},
		{"mfa_enforcement", "required"},
		{"max_workspaces_per_user", "3"},
		{"max_members_per_workspace", "10"},
	} {
		in := &PatchSettingsInput{}
		in.Body.Settings = map[string]string{kv[0]: kv[1]}
		_, err := patch(context.Background(), in)
		var pd *handlerutil.ProblemDetails
		if !errors.As(err, &pd) || pd.Type != apierrors.InstanceSettingsInvalidKey.Code {
			t.Fatalf("PATCH %s=%s: err = %v, want %s", kv[0], kv[1], err, apierrors.InstanceSettingsInvalidKey.Code)
		}
	}
}
