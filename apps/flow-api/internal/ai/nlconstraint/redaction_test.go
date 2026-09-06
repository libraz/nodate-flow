// Redaction tests for the workspace-backed nlconstraint provider.
//
// The ai_invocations row this path writes is served to every member of
// the workspace, guests included, so a secret a user pastes into the
// constraint editor must not reach it. What the row carries is the
// combined system + user prompt after logutil.Redact, on both the
// success and the failure branch.
package nlconstraint

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/ai/providers"
)

// The prefix is a literal so the scanner's longest-prefix choice is the
// one under test; the token body is concatenated so the source carries
// no complete key-shaped string.
const pastedSecret = "sk-ant-" + "api03exampletokenbody0123456789" //#nosec G101 -- synthetic test fixture, never a real key

const promptWithSecret = "due before new year, auth with " + pastedSecret //#nosec G101 -- synthetic test fixture, never a real key

const secretMarker = "[REDACTED:sk-ant-]" //#nosec G101 -- the redaction marker, not a credential: this is what the scrubbed row must contain

// TestCompileConstraint_PromptSecretNeverReachesTheInvocation pins the
// scrub on the prompt the row stores.
//
// The assertions are three-sided on purpose. Absence of the secret alone
// would pass on a row that carried no prompt at all, so the marker that
// replaced it and the prose around it are asserted too: drop the
// redaction and the marker is gone, stop recording the prompt and the
// prose is gone.
func TestCompileConstraint_PromptSecretNeverReachesTheInvocation(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name        string
		providerErr error
		wantStatus  string
	}{
		{name: "success", wantStatus: "ok"},
		{name: "provider failure", providerErr: errors.New("upstream refused the request"), wantStatus: "error"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			prov := &recordingProvider{
				kind: providers.Kind("mock"),
				resp: &providers.Response{Text: goodConstraint},
				err:  tc.providerErr,
			}
			var logged []InvocationRecord
			wp := NewWorkspaceProvider(&meteringResolver{provider: prov}, extractWSFixed(7)).
				WithMetering(fixedGuard{}, func(_ context.Context, rec InvocationRecord) {
					logged = append(logged, rec)
				}, nil)

			_, err := wp.CompileConstraint(context.Background(), promptWithSecret)
			if tc.providerErr == nil && err != nil {
				t.Fatalf("CompileConstraint: %v", err)
			}
			if tc.providerErr != nil && err == nil {
				t.Fatal("expected the provider error to surface")
			}
			if len(logged) != 1 {
				t.Fatalf("logged %d invocations, want exactly 1", len(logged))
			}
			rec := logged[0]
			if rec.Status != tc.wantStatus {
				t.Errorf("Status = %q, want %q", rec.Status, tc.wantStatus)
			}
			if strings.Contains(rec.PromptRedacted, pastedSecret) {
				t.Errorf("the pasted secret reached ai_invocations: %q", rec.PromptRedacted)
			}
			if !strings.Contains(rec.PromptRedacted, secretMarker) {
				t.Errorf("prompt = %q, want %q where the secret was", rec.PromptRedacted, secretMarker)
			}
			if !strings.Contains(rec.PromptRedacted, "due before new year") {
				t.Errorf("prompt = %q, want the prose the row is kept for", rec.PromptRedacted)
			}
			// One pass only: a second Redact over the first pass's output
			// would match the prefix inside the marker and nest it.
			if strings.Contains(rec.PromptRedacted, "[REDACTED:[REDACTED:") {
				t.Errorf("prompt was redacted more than once: %q", rec.PromptRedacted)
			}
		})
	}
}
