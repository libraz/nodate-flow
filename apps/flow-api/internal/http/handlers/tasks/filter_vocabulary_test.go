// Parity checks between the vocabularies the task list filter enforces
// and the places those vocabularies are actually defined: the
// derived_state column for the states, and the priority scale the wire
// contract states for the numbers.
//
// The filter reaches both through handlerutil rather than holding lists
// of its own, so these checks are what keeps that reach honest. A filter
// that recognises a state the column cannot store drops every row a
// caller asked for while answering 200, and a filter whose ceiling sits
// below the one the create body advertises refuses to find tasks this
// same API accepted.
//
// Each comparison is a function rather than a body of assertions, and
// each has a control feeding it a declaration that is wrong on purpose.
// A parity check whose two sides are read from the same place passes
// whatever either side says, and this file is the only thing standing
// between the two vocabularies.
package tasks

import (
	"fmt"
	"reflect"
	"sort"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/http/handlers/handlerutil"
	"github.com/libraz/nodate-flow/apps/flow-api/tests/columnbounds"
)

// readSchemaEnums returns every ENUM column the schema declares, so a
// check reads the column definition rather than a copy of it.
func readSchemaEnums(t *testing.T) columnbounds.Schema {
	t.Helper()

	root, err := columnbounds.RepoRoot()
	require.NoError(t, err, "the repository root has to be locatable to read the schema")
	dump, err := columnbounds.ReadSchema(root)
	require.NoError(t, err, "sql/schema.sql is generated from sql/core and sql/flow and is tracked")

	schema := columnbounds.ParseSchema(dump)
	require.NotZero(t, schema.EnumCount(),
		"no ENUM column was parsed out of the schema, so nothing below is comparing anything")
	return schema
}

// unstorableStates returns the values the filter has to refuse: every
// member of every other ENUM in the schema that tasks.derived_state does
// not itself store, deduplicated and ordered so a failure names the same
// value on every run.
//
// They come from the rest of the schema rather than a hand-written list
// of near misses. Every other ENUM in this database is a plausible thing
// for a state vocabulary to drift into — they are the words this domain
// already uses — and none of them names a task state.
func unstorableStates(schema columnbounds.Schema, stored map[string]bool) []string {
	seen := map[string]bool{}
	var out []string
	for table, byName := range schema.Enums {
		for name, other := range byName {
			if table == "tasks" && name == "derived_state" {
				continue
			}
			for _, member := range other.Members {
				if stored[member] || seen[member] {
					continue
				}
				seen[member] = true
				out = append(out, member)
			}
		}
	}
	sort.Strings(out)
	return out
}

// storedStates returns the values tasks.derived_state accepts, read from
// the column definition.
func storedStates(t *testing.T, schema columnbounds.Schema) map[string]bool {
	t.Helper()

	column, ok := schema.EnumColumn("tasks", "derived_state")
	require.True(t, ok,
		"tasks.derived_state has to resolve as an ENUM for this comparison to mean anything")
	require.NotEmpty(t, column.Members)

	stored := map[string]bool{}
	for _, member := range column.Members {
		stored[member] = true
	}
	return stored
}

// stateVocabularyMismatches reports every way accepts disagrees with the
// values tasks.derived_state stores.
func stateVocabularyMismatches(stored map[string]bool, unstorable []string, accepts func(string) bool) []string {
	var mismatches []string
	for member := range stored {
		if !accepts(member) {
			mismatches = append(mismatches, fmt.Sprintf(
				"tasks.derived_state stores %q and the filter does not recognise it, so the state is "+
					"dropped from the IN list: asking for it answers with the rows of every other "+
					"state instead", member))
		}
	}
	for _, member := range unstorable {
		if accepts(member) {
			mismatches = append(mismatches, fmt.Sprintf(
				"the filter accepts %q, which tasks.derived_state does not store, so a caller "+
					"filtering by it binds a value no task row can match and is answered with an "+
					"empty page", member))
		}
	}
	sort.Strings(mismatches)
	return mismatches
}

// TestListStateFilterMatchesTheStoredStates pins the state filter's
// vocabulary to the derived_state column's own.
//
// Both directions matter and they fail differently. A stored state the
// filter does not recognise is dropped from the IN list, so asking for it
// silently returns the rows of the other states requested — or, when it
// was the only one asked for, the whole unfiltered page. A state the
// filter accepts and the column cannot hold binds a value no row can
// match, which reads as an empty project rather than a bad request.
func TestListStateFilterMatchesTheStoredStates(t *testing.T) {
	t.Parallel()

	schema := readSchemaEnums(t)
	stored := storedStates(t, schema)
	unstorable := unstorableStates(schema, stored)
	require.NotEmpty(t, unstorable,
		"no value outside tasks.derived_state was tried, so the filter was never asked to refuse anything")

	assert.Empty(t, stateVocabularyMismatches(stored, unstorable, handlerutil.IsTaskDerivedState))
}

// TestStateVocabularyCheckRefusesADriftedFilter is the control: it feeds
// the comparison predicates that are wrong in each direction and requires
// it to say so. Without it, a comparison that silently reached the same
// list twice would report parity it never established.
//
// Both wrong predicates are built from the schema rather than from words
// chosen here, so the control cannot pass by probing a value the check
// never asks about — which is exactly how it would stop being a control.
func TestStateVocabularyCheckRefusesADriftedFilter(t *testing.T) {
	t.Parallel()

	schema := readSchemaEnums(t)
	stored := storedStates(t, schema)
	unstorable := unstorableStates(schema, stored)
	require.NotEmpty(t, unstorable)

	// A state the column stores, gone from the filter.
	dropped := ""
	for member := range stored {
		if dropped == "" || member < dropped {
			dropped = member
		}
	}
	narrowed := stateVocabularyMismatches(stored, unstorable, func(s string) bool {
		return s != dropped && handlerutil.IsTaskDerivedState(s)
	})
	assert.NotEmpty(t, narrowed, "a filter that dropped the stored state %q has to be reported", dropped)

	// A state the filter takes and the column cannot hold.
	added := unstorable[0]
	widened := stateVocabularyMismatches(stored, unstorable, func(s string) bool {
		return s == added || handlerutil.IsTaskDerivedState(s)
	})
	assert.NotEmpty(t, widened, "a filter that accepts the unstorable state %q has to be reported", added)
}

// priorityBoundViolations reports how a field's declared bounds differ
// from the priority scale the filter enforces, empty when they agree.
func priorityBoundViolations(typ reflect.Type, name string) []string {
	field, ok := typ.FieldByName(name)
	if !ok {
		return []string{fmt.Sprintf("%s.%s does not exist", typ.Name(), name)}
	}

	var out []string
	check := func(tag string, want int32) {
		raw, stated := field.Tag.Lookup(tag)
		if !stated {
			out = append(out, fmt.Sprintf(
				"%s.%s states no %s, so the contract leaves that end of the range open while the "+
					"list filter enforces one", typ.Name(), name, tag))
			return
		}
		declared, err := strconv.ParseInt(raw, 10, 32)
		if err != nil {
			out = append(out, fmt.Sprintf("%s.%s states %s=%q, which is not a number",
				typ.Name(), name, tag, raw))
			return
		}
		if declared != int64(want) {
			out = append(out, fmt.Sprintf(
				"%s.%s advertises %s=%d while the priority scale sets %d, so a value this API "+
					"accepts on write is dropped by the list filter on read",
				typ.Name(), name, tag, declared, want))
		}
	}
	check("minimum", handlerutil.PriorityNone)
	check("maximum", handlerutil.PriorityMax)
	return out
}

// TestTaskPriorityBoundsMatchTheWireContract pins the bounds the list
// filter enforces to the ones every task endpoint advertises.
//
// The schema declares priority as a plain integer, so the column settles
// nothing here: the scale exists only in the shared constants and in the
// tags this API publishes through OpenAPI and the generated SDK. When
// they disagree, a client is told a value is in range, sends it, and the
// filter drops it — the list comes back without the tasks the caller
// created at exactly that priority, and no error is raised anywhere.
func TestTaskPriorityBoundsMatchTheWireContract(t *testing.T) {
	t.Parallel()

	// The declarations a caller can read: the create body, the patch body
	// and the list filter's own query parameter.
	surfaces := []reflect.Type{
		reflect.TypeOf(CreateTaskBody{}),
		reflect.TypeOf(PatchTaskBody{}),
		reflect.TypeOf(ListTasksInput{}),
	}
	for _, typ := range surfaces {
		t.Run(typ.Name(), func(t *testing.T) {
			t.Parallel()
			assert.Empty(t, priorityBoundViolations(typ, "Priority"))
		})
	}
}

// TestPriorityBoundCheckRefusesAMisdeclaredField is the control for the
// bound comparison, covering both a declaration that states the wrong
// ceiling and one that states none at all.
func TestPriorityBoundCheckRefusesAMisdeclaredField(t *testing.T) {
	t.Parallel()

	type narrowedCeiling struct {
		Priority int32 `json:"priority" minimum:"0" maximum:"3"`
	}
	assert.NotEmpty(t, priorityBoundViolations(reflect.TypeOf(narrowedCeiling{}), "Priority"),
		"a ceiling below the priority scale has to be reported")

	type unbounded struct {
		Priority int32 `json:"priority"`
	}
	assert.NotEmpty(t, priorityBoundViolations(reflect.TypeOf(unbounded{}), "Priority"),
		"a field stating no bounds at all has to be reported")
}
