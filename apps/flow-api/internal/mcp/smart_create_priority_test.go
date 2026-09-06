package mcp

import (
	"strings"
	"testing"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/handlers/handlerutil"
)

// TestSmartCreatePriorityMappingIsTheSharedOne pins the numbers the
// smart-create path writes into tasks.priority.
//
// The tool used to carry its own copy of the word-to-number map. A copy
// that agrees today is a copy that can stop agreeing, and the divergence
// would be silent: a subtask created through an agent would simply rank
// differently from the same proposal applied through the API, with
// nothing anywhere saying so. The values below are the ones the copy
// produced, held against the shared mapping that replaced it.
func TestSmartCreatePriorityMappingIsTheSharedOne(t *testing.T) {
	t.Parallel()

	// The vocabulary the prompt asks for.
	for label, want := range map[string]int32{
		"low":    1,
		"medium": 2,
		"high":   3,
	} {
		got, known := handlerutil.PriorityFromLabel(label)
		if got != want {
			t.Errorf("priority %q maps to %d, want %d", label, got, want)
		}
		if !known {
			t.Errorf("priority %q is a word the prompt asks for and has to be reported as one", label)
		}
	}

	// A word the prompt never offered resolves to no priority and says so.
	// The second return value is the reason the shared mapping is worth
	// reaching for at all: the copy it replaced answered "urgent" with the
	// same number it answered an omitted priority with, and left nothing
	// to tell the two apart.
	got, known := handlerutil.PriorityFromLabel("urgent")
	if got != handlerutil.PriorityNone {
		t.Errorf("a word outside the vocabulary maps to %d, want %d", got, handlerutil.PriorityNone)
	}
	if known {
		t.Error("a word outside the vocabulary has to be reported, not absorbed")
	}

	// An omitted priority is ordinary output: the model is not obliged to
	// rank every subtask it proposes, so it must not be reported as drift.
	got, known = handlerutil.PriorityFromLabel("")
	if got != handlerutil.PriorityNone || !known {
		t.Errorf("an omitted priority resolved to (%d, %t), want (%d, true)",
			got, known, handlerutil.PriorityNone)
	}
}

// TestSmartCreateKeepsNoPriorityMappingOfItsOwn proves the copy is gone
// and holds it gone.
//
// The behavioural check above passes either way — it exercises the shared
// function directly — so on its own it would not notice a local map coming
// back, which is the state the two mappings drifted from.
func TestSmartCreateKeepsNoPriorityMappingOfItsOwn(t *testing.T) {
	t.Parallel()

	src := readMCPSource(t, "tools.go")
	if !strings.Contains(src, "handlerutil.PriorityFromLabel") {
		t.Error("the smart-create path has to resolve a proposed priority through handlerutil.PriorityFromLabel, so a model's word means the same number on both transports")
	}
	if !strings.Contains(src, "priority outside the vocabulary") {
		t.Error("a model answering outside the vocabulary it was handed has to be reported; absorbing it makes a malformed answer indistinguishable from a deliberate one")
	}
	for _, gone := range []string{`case "low":`, `case "medium":`, `case "high":`} {
		if strings.Contains(src, gone) {
			t.Errorf("a priority mapping local to this package is back in tools.go (%s); one copy per transport is how the two came to disagree", gone)
		}
	}
}
