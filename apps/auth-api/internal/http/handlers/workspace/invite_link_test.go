package workspace

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// routeTreePath is flow-web's generated route manifest. It is the only
// place that knows every path the product frontend actually serves, so a
// link built by the backend is checked against it rather than against a
// second copy of the path written out by hand — two hand-written copies
// is how the invite email came to point at a route that does not exist.
const routeTreePath = "apps/flow-web/src/routeTree.gen.ts"

// declaredRoutePaths returns every `path: '...'` entry in flow-web's
// generated route tree.
func declaredRoutePaths(t *testing.T) map[string]bool {
	t.Helper()
	// Tests run in the package directory: apps/auth-api/internal/http/
	// handlers/workspace, six levels below the repository root.
	root, err := filepath.Abs(filepath.Join("..", "..", "..", "..", "..", ".."))
	require.NoError(t, err, "resolve repository root")
	require.FileExists(t, filepath.Join(root, "go.work"),
		"expected the repository root at %s", root)

	raw, err := os.ReadFile(filepath.Join(root, routeTreePath))
	require.NoError(t, err, "read %s; if the frontend moved, this guard needs its path updated", routeTreePath)

	re := regexp.MustCompile(`path:\s*'([^']+)'`)
	paths := map[string]bool{}
	for _, m := range re.FindAllStringSubmatch(string(raw), -1) {
		paths[m[1]] = true
	}
	require.NotEmpty(t, paths, "no routes parsed out of %s", routeTreePath)
	return paths
}

// TestInviteAcceptURL_TargetsALiveRoute proves the workspace-invite link
// the API hands out resolves to a route flow-web serves.
//
// The email used to build `/invites/<token>`, which collides with nothing
// but the calendar-invite RSVP page at the static `/invites/accept`, so
// every invite sent by email — the only path REST, the SDK and
// integrations can take — landed the invitee on a 404. The dialog in the
// web app built the correct singular link, which is exactly why the
// breakage stayed invisible in manual use.
func TestInviteAcceptURL_TargetsALiveRoute(t *testing.T) {
	t.Parallel()
	paths := declaredRoutePaths(t)

	link := InviteAcceptURL("https://flow.example.test", "inv_abc123")
	rest := strings.TrimPrefix(link, "https://flow.example.test")
	require.True(t, strings.HasPrefix(rest, InviteAcceptPathPrefix),
		"the built link must start with the prefix it is derived from, got %q", rest)

	// Reconstruct the route the link resolves to by putting the dynamic
	// segment back, and require flow-web to declare it.
	route := strings.TrimSuffix(InviteAcceptPathPrefix, "/") + "/$token"
	assert.True(t, paths[route],
		"flow-web declares no %q route, so %s leads to a 404; "+
			"InviteAcceptPathPrefix and the frontend route have drifted apart", route, link)
}

// TestInviteAcceptURL_JoinsWithoutDoubledSlash guards the shape of the
// built link against a configured base URL that carries a trailing
// slash, which would otherwise produce `//invite/<token>` — a protocol-
// relative path in some clients.
func TestInviteAcceptURL_JoinsWithoutDoubledSlash(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "https://flow.example.test/invite/tok",
		InviteAcceptURL("https://flow.example.test/", "tok"))
	assert.Equal(t, "https://flow.example.test/invite/tok",
		InviteAcceptURL("https://flow.example.test", "tok"))
}
