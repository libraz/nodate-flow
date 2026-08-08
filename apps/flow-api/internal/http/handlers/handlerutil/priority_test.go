package handlerutil

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestPriorityFromLabelMapsThePromptVocabulary(t *testing.T) {
	t.Parallel()

	cases := map[string]int32{
		"low":     1,
		"medium":  2,
		"high":    3,
		"HIGH":    3,
		"  Low  ": 1,
	}
	for label, want := range cases {
		got, known := PriorityFromLabel(label)
		assert.Equal(t, want, got, "label %q", label)
		assert.True(t, known, "label %q is in the vocabulary the prompt asks for", label)
	}
}

// A word outside the vocabulary means the model answered something the
// prompt did not ask for. Reading it as "no priority" keeps the value
// honest, and reporting it keeps the substitution visible — the client
// map this replaced turned an unrecognised word into a mid-range
// priority with nothing said about it.
func TestPriorityFromLabelReportsAWordOutsideTheVocabulary(t *testing.T) {
	t.Parallel()

	for _, label := range []string{"urgent", "P1", "中", "medium-high", "1"} {
		got, known := PriorityFromLabel(label)
		assert.Equal(t, PriorityNone, got, "label %q", label)
		assert.False(t, known,
			"%q is not a word the prompt asks for; resolving it silently hides the model's answer", label)
	}
}

// An absent priority is not a malformed one: the model is not obliged to
// rank every step, and reporting the empty string as a failure would put
// a warning in the log for ordinary output.
func TestPriorityFromLabelAcceptsAnAbsentPriority(t *testing.T) {
	t.Parallel()

	got, known := PriorityFromLabel("")
	assert.Equal(t, PriorityNone, got)
	assert.True(t, known)
}

// The scale is the tasks.priority column's, so nothing this returns may
// fall outside the bounds the DTOs declare.
func TestPriorityFromLabelStaysInsideTheDeclaredBounds(t *testing.T) {
	t.Parallel()

	for _, label := range []string{"low", "medium", "high", "nonsense", ""} {
		got, _ := PriorityFromLabel(label)
		assert.GreaterOrEqual(t, got, PriorityNone, "label %q", label)
		assert.LessOrEqual(t, got, PriorityMax, "label %q", label)
	}
}
