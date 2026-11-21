package ai

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/ai/providers"
)

// TestSmartCreateOutputBoundsSubtaskTitles pins the subtask title width the
// smart-create contract accepts to tasks.title, counted in characters: a
// title at the width passes even when it ends in multi-byte characters, and
// one character more is a schema mismatch rather than a value to cut.
func TestSmartCreateOutputBoundsSubtaskTitles(t *testing.T) {
	t.Parallel()

	proposal := func(title string) *providers.Response {
		text := fmt.Sprintf(`{"suggestedAssignees":[],"subtasks":[`+
			`{"title":%q,"description":"d","priority":"low"},`+
			`{"title":"second","description":"d","priority":"low"}]}`, title)
		return &providers.Response{Text: text, Termination: providers.TerminationComplete}
	}
	atWidth := strings.Repeat("A", 480) + strings.Repeat("設", 20)

	if _, err := outputPayload(proposal(atWidth), smartCreateOutput); err != nil {
		t.Fatalf("a %d-character title was refused: %v", 500, err)
	}
	_, err := outputPayload(proposal(atWidth+"設"), smartCreateOutput)
	if !errors.Is(err, providers.ErrResponseSchemaMismatch) {
		t.Fatalf("a 501-character title: got %v, want ErrResponseSchemaMismatch", err)
	}
}
