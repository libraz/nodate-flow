package generated

import (
	"strings"
	"testing"
)

func TestFindDescriptionVersionIsTaskScoped(t *testing.T) {
	t.Parallel()

	if !strings.Contains(findDescriptionVersion, "tdv.task_id = ?") {
		t.Fatalf("FindDescriptionVersion must be scoped to the selected task:\n%s", findDescriptionVersion)
	}
}
