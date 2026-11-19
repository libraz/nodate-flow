package tasks

import (
	"reflect"
	"strings"
	"testing"
)

func TestProposeStepsInstructionIsOptionalAndBounded(t *testing.T) {
	t.Parallel()

	body, ok := reflect.TypeOf(ProposeStepsInput{}).FieldByName("Body")
	if !ok {
		t.Fatal("ProposeStepsInput has no body")
	}
	instruction, ok := body.Type.FieldByName("Instruction")
	if !ok {
		t.Fatal("propose-steps request has no instruction field")
	}
	jsonTag := strings.Split(instruction.Tag.Get("json"), ",")
	if len(jsonTag) != 2 || jsonTag[0] != "instruction" || jsonTag[1] != "omitempty" {
		t.Errorf("instruction JSON tag = %q, want json:\"instruction,omitempty\"", instruction.Tag.Get("json"))
	}
	if got := instruction.Tag.Get("maxLength"); got != "1000" {
		t.Errorf("instruction maxLength = %q, want 1000", got)
	}
}
