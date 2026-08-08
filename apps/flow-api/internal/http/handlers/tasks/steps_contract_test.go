package tasks

import (
	"reflect"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// jsonFields projects a DTO onto the wire: field name to Go type, plus
// whether the field is omitted when empty.
func jsonFields(t *testing.T, v any) map[string]struct {
	typ      reflect.Type
	optional bool
} {
	t.Helper()
	out := map[string]struct {
		typ      reflect.Type
		optional bool
	}{}
	rt := reflect.TypeOf(v)
	for i := 0; i < rt.NumField(); i++ {
		f := rt.Field(i)
		tag := f.Tag.Get("json")
		if tag == "" || tag == "-" {
			continue
		}
		parts := strings.Split(tag, ",")
		name := parts[0]
		optional := false
		for _, p := range parts[1:] {
			if p == "omitempty" {
				optional = true
			}
		}
		out[name] = struct {
			typ      reflect.Type
			optional bool
		}{typ: f.Type, optional: optional}
	}
	return out
}

// TestProposedStepIsApplyStepShaped is the contract behind the two
// endpoint descriptions: apply-steps "persists a proposal returned by
// /propose-steps". A caller must be able to hand back what it was
// given, so every field the proposal carries has to be a field the
// apply request accepts, under the same name and the same type.
//
// The pair drifted once already — priority was a label in the response
// and an integer in the request — and the only way to notice was to
// try it.
func TestProposedStepIsApplyStepShaped(t *testing.T) {
	t.Parallel()

	proposed := jsonFields(t, ProposedStep{})
	apply := jsonFields(t, ApplyStep{})

	require.NotEmpty(t, proposed)
	for name, p := range proposed {
		a, ok := apply[name]
		require.True(t, ok,
			"propose-steps returns %q, which apply-steps does not accept: the proposal cannot be applied as returned", name)
		assert.Equal(t, a.typ, p.typ,
			"%q is %s in the proposal and %s in the apply request; a client must convert to round trip", name, p.typ, a.typ)
	}
}

// Priority carries a column default and 0 is a real value ("none"), so
// requiring it rejects the most ordinary request there is — a step with
// only a title — while preventing no invalid state. CreateTaskBody and
// the MCP apply_steps tool both treat it as optional; this endpoint was
// the outlier.
func TestApplyStepPriorityIsOptional(t *testing.T) {
	t.Parallel()

	apply := jsonFields(t, ApplyStep{})

	require.Contains(t, apply, "priority")
	assert.True(t, apply["priority"].optional,
		"apply-steps must accept a step without a priority")
	assert.False(t, apply["title"].optional,
		"title stays required: a step with no title is not a step")
}
