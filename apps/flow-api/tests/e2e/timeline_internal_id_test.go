package e2e

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/flow-api/tests/helpers"
)

// timelinePayloadEnvelope is the subset of the timeline response this
// test reads: the event kind, so a failure names what produced the
// payload, and the payload itself.
type timelinePayloadEnvelope struct {
	Events []struct {
		Type    string          `json:"type"`
		Payload json.RawMessage `json:"payload"`
	} `json:"events"`
}

// TestTimelinePayloadsCarryNoInternalIDs sweeps a worked-in workspace's
// timeline and fails on any payload field that names an identifier with
// a number.
//
// Internal auto-increment keys are not identifiers a client can resolve:
// the API answers in public_id (UUID v7) everywhere else, so a number
// here is unusable to the caller, splits the field's type in the
// generated SDK, and — being monotonic — reports how many rows the whole
// instance holds. This walks the delivered JSON rather than the builders
// because a payload is assembled at runtime; the response body is the
// only place the finished value can be seen.
func TestTimelinePayloadsCarryNoInternalIDs(t *testing.T) {
	bootstrap(t)
	t.Parallel()

	tt := newTenant(t)
	exerciseWorkspaceForTimeline(t, tt)

	var resp timelinePayloadEnvelope
	doJSON(t, http.MethodGet,
		fmt.Sprintf("%s/workspaces/%s/timeline?limit=200", testServerURL, tt.WorkspacePublicID),
		tt.AccessToken, nil, &resp)
	require.NotEmpty(t, resp.Events, "the tenant setup must have produced timeline events")

	checked := 0
	for _, e := range resp.Events {
		if len(e.Payload) == 0 {
			continue
		}
		var doc any
		require.NoErrorf(t, json.Unmarshal(e.Payload, &doc),
			"%s: payload is not valid JSON: %s", e.Type, e.Payload)
		for _, field := range numericIDFields("", doc) {
			t.Errorf("%s: payload field %q carries the internal key %s — resolve it to public_id before appending; payload=%s",
				e.Type, field.path, field.value, e.Payload)
		}
		checked++
	}
	require.NotZero(t, checked, "no payload was inspected; the sweep proved nothing")
}

// numericIDField names one offending payload field.
type numericIDField struct {
	path  string
	value string
}

// numericIDFields walks decoded JSON and reports every identifier-shaped
// key holding a number, at any depth. It mirrors the rule the append
// path enforces, applied to what the API actually returned.
func numericIDFields(path string, v any) []numericIDField {
	var out []numericIDField
	switch t := v.(type) {
	case map[string]any:
		for key, child := range t {
			childPath := key
			if path != "" {
				childPath = path + "." + key
			}
			if isIDFieldName(key) {
				if n, ok := child.(float64); ok {
					out = append(out, numericIDField{path: childPath, value: fmt.Sprintf("%v", n)})
					continue
				}
			}
			out = append(out, numericIDFields(childPath, child)...)
		}
	case []any:
		for _, child := range t {
			out = append(out, numericIDFields(path, child)...)
		}
	}
	return out
}

// isIDFieldName reports whether a payload key names an identifier. The
// wire uses camelCase, so only those endings are considered.
func isIDFieldName(key string) bool {
	if key == "id" || key == "ids" {
		return true
	}
	for _, suffix := range []string{"Id", "ID", "Ids", "IDs"} {
		if len(key) > len(suffix) && strings.HasSuffix(key, suffix) {
			return true
		}
	}
	return false
}

// exerciseWorkspaceForTimeline drives the flows whose event payloads
// can carry internal keys, so the sweep above has something to
// inspect beyond the rows tenant creation happens to leave behind.
func exerciseWorkspaceForTimeline(t *testing.T, tt *helpers.TestTenant) {
	t.Helper()

	// A task transition: the most common payload shape on the timeline.
	var created struct {
		ID string `json:"id"`
	}
	doJSON(t, http.MethodPost, testServerURL+"/tasks", tt.AccessToken, map[string]any{
		"projectId": tt.ProjectPublicID,
		"title":     "timeline-id-sweep",
	}, &created)
	require.NotEmpty(t, created.ID, "task create returned no id")

	doJSON(t, http.MethodPost,
		fmt.Sprintf("%s/tasks/%s/transitions", testServerURL, created.ID),
		tt.AccessToken, map[string]any{"transition": "start"}, nil)

	// A comment, which appends through the best-effort path.
	status, body := doJSONStatus(t, http.MethodPost,
		fmt.Sprintf("%s/tasks/%s/comments", testServerURL, created.ID),
		tt.AccessToken, map[string]any{"body": "sweep comment"})
	assert.Lessf(t, status, 300, "comment create -> %d body=%s", status, string(body))
}
