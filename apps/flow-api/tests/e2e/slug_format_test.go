package e2e

import (
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/flow-api/tests/helpers"
)

// malformedSlugs are the shapes that must not reach a workspace or
// project row. A slug is pasted into a URL and stored as a DNS label, so
// the rule is lowercase letters and digits with hyphens only between
// them, and it belongs to the API: a CLI, an MCP client or a plain curl
// never runs the browser form, so a rule that only the form applies is
// not a rule.
//
// The edge-hyphen entries are here because a label is not allowed to
// begin or end with one. A client whose slug generator can emit such a
// value is the thing that has to change; the contract does not widen to
// accommodate it, or the generator would be defining the label.
var malformedSlugs = map[string]string{
	"space":            "hello world",
	"uppercase":        "HelloWorld",
	"path separator":   "../admin",
	"trailing space":   "trailing ",
	"underscore":       "hello_world",
	"dot":              "hello.world",
	"percent escape":   "hello%20world",
	"non-ascii":        "スラッグ",
	"leading hyphen":   "-leading",
	"trailing hyphen":  "trailing-",
	"hyphen both ends": "-both-",
	"lone hyphen":      "-",
}

// TestWorkspaceSlugFormatIsEnforcedByTheAPI pins the character set on
// POST /workspaces and PATCH /workspaces/{wsId}. Both must refuse, and
// refuse identically: an uppercase slug used to be silently lowercased
// on the way in, which meant the row carried a name the caller never
// sent and two clients disagreed about what they had created.
func TestWorkspaceSlugFormatIsEnforcedByTheAPI(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)

	for name, slug := range malformedSlugs {
		t.Run("create/"+name, func(t *testing.T) {
			status, body := doJSONStatus(t, http.MethodPost, testServerURL+"/workspaces",
				tt.AccessToken, map[string]any{"slug": slug, "name": "Malformed"})
			requireSlugRefused(t, status, body, "workspace create with "+name)
		})
		t.Run("patch/"+name, func(t *testing.T) {
			status, body := doJSONStatus(t, http.MethodPatch,
				testServerURL+"/workspaces/"+tt.WorkspacePublicID,
				tt.AccessToken, map[string]any{"slug": slug})
			requireSlugRefused(t, status, body, "workspace patch with "+name)
		})
	}

	// A slug longer than the DNS label limit is refused by the same
	// schema, so the character set cannot be read as replacing the bound.
	status, body := doJSONStatus(t, http.MethodPost, testServerURL+"/workspaces",
		tt.AccessToken, map[string]any{"slug": strings.Repeat("a", 64), "name": "Too long"})
	requireSlugRefused(t, status, body, "workspace create with an over-length slug")
}

// TestProjectSlugFormatIsEnforcedByTheAPI is the same contract for
// POST /workspaces/{wsId}/projects and PATCH /projects/{prjId}.
func TestProjectSlugFormatIsEnforcedByTheAPI(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)

	for name, slug := range malformedSlugs {
		t.Run("create/"+name, func(t *testing.T) {
			status, body := doJSONStatus(t, http.MethodPost,
				testServerURL+"/workspaces/"+tt.WorkspacePublicID+"/projects",
				tt.AccessToken, map[string]any{"slug": slug, "name": "Malformed"})
			requireSlugRefused(t, status, body, "project create with "+name)
		})
		t.Run("patch/"+name, func(t *testing.T) {
			status, body := doJSONStatus(t, http.MethodPatch,
				testServerURL+"/projects/"+tt.ProjectPublicID,
				tt.AccessToken, map[string]any{"slug": slug})
			requireSlugRefused(t, status, body, "project patch with "+name)
		})
	}
}

// TestSlugRelaxationsAreAccepted records the two shapes the label rule
// deliberately leaves open, so neither is narrowed away by a later
// tightening of the pattern. A single character is a whole label, and a
// leading digit is allowed by the RFC 1123 relaxation — slugs like
// "2026-planning" are ordinary. Both are checked on create for a
// workspace and a project, and the stored value is compared to what was
// sent so acceptance cannot quietly mean "rewritten".
func TestSlugRelaxationsAreAccepted(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)

	// A one-character slug is exercised on the project endpoint, whose
	// slug is unique only within a workspace: the tenant's workspace is
	// fresh, so a fixed "a" cannot collide with a parallel test. The
	// workspace endpoint, whose slug is global, takes the leading-digit
	// case, which can carry a random suffix.
	for name, slug := range map[string]string{
		"single character": "a",
		"leading digit":    "2026-planning-" + randomHex(6),
	} {
		var prj struct {
			Slug string `json:"slug"`
		}
		doJSON(t, http.MethodPost,
			testServerURL+"/workspaces/"+tt.WorkspacePublicID+"/projects",
			tt.AccessToken, map[string]any{"slug": slug, "name": "Relaxation " + name}, &prj)
		require.Equalf(t, slug, prj.Slug,
			"a %s slug must be accepted and stored exactly as sent", name)
	}

	wsSlug := "2026-planning-" + randomHex(6)
	var ws struct {
		ID   string `json:"id"`
		Slug string `json:"slug"`
	}
	doJSON(t, http.MethodPost, testServerURL+"/workspaces",
		tt.AccessToken, map[string]any{"slug": wsSlug, "name": "Leading digit"}, &ws)
	require.Equal(t, wsSlug, ws.Slug,
		"a leading-digit slug must be accepted and stored exactly as sent")
	t.Cleanup(func() { helpers.PurgeWorkspace(t, testDB, ws.ID) })
}

// requireSlugRefused asserts the request was turned away by request
// validation rather than by a handler, and that the body says which
// field was at fault. 422 is what the schema layer answers; a 2xx here
// means the value reached a row.
func requireSlugRefused(t *testing.T, status int, body []byte, label string) {
	t.Helper()
	require.Equalf(t, http.StatusUnprocessableEntity, status,
		"%s must be refused by request validation; body=%s", label, string(body))
	require.Containsf(t, string(body), "slug",
		"%s: refusal must name the offending field; body=%s", label, string(body))
}
