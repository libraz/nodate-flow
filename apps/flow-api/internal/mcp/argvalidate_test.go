package mcp

import (
	"encoding/json"
	stderrors "errors"
	"regexp"
	"strings"
	"testing"

	"github.com/libraz/nodate-flow/apps/flow-api/internal/db/types"
	apierrors "github.com/libraz/nodate-flow/apps/flow-api/internal/errors"
	"github.com/libraz/nodate-flow/apps/flow-api/internal/taskstate"
)

// toolSchema is the input schema of a registered tool, so the cases
// below test the contract the server actually publishes rather than a
// schema written for the test.
func toolSchema(t *testing.T, name string) map[string]any {
	t.Helper()
	h := NewHandler(Deps{})
	tl, ok := h.tools[name]
	if !ok {
		t.Fatalf("tool %q is not registered", name)
	}
	return tl.inputSchema
}

func validateTool(t *testing.T, name string, args map[string]any) error {
	t.Helper()
	raw, err := json.Marshal(args)
	if err != nil {
		t.Fatalf("marshal args: %v", err)
	}
	return validateArgsAgainstSchema(toolSchema(t, name), raw)
}

func requireArgumentsInvalid(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("want MCP.TOOL.ARGUMENTS_INVALID, got no error")
	}
	var ae *apierrors.APIError
	if !stderrors.As(err, &ae) || ae.Spec == nil {
		t.Fatalf("want *apierrors.APIError, got %T: %v", err, err)
	}
	if ae.Spec.Code != apierrors.McpToolArgumentsInvalid.Code {
		t.Fatalf("want %s, got %s", apierrors.McpToolArgumentsInvalid.Code, ae.Spec.Code)
	}
}

// TestCreateTaskPriorityIsBounded is the case the audit named: create_task
// advertises priority 0..4 and stored 999 verbatim, which the UI renders
// as "no priority" while ORDER BY priority DESC puts it above everything
// a person set deliberately.
//
// Both directions are asserted. A rejection on its own is satisfied by an
// implementation that refuses every priority, which would be a worse bug
// than the one being fixed.
func TestCreateTaskPriorityIsBounded(t *testing.T) {
	t.Parallel()

	// The canonical hyphenated form, because that is what the API hands a
	// caller and therefore what a caller sends back.
	const projectID = "0192f4c0-d1e2-7a3b-8c9d-0e1f2a3b4c5d"

	t.Run("out_of_range/rejected", func(t *testing.T) {
		t.Parallel()
		for _, priority := range []int{999, 5, -1} {
			err := validateTool(t, "create_task", map[string]any{
				"projectId": projectID,
				"title":     "Bounded",
				"priority":  priority,
			})
			requireArgumentsInvalid(t, err)
		}
	})

	t.Run("in_range/accepted", func(t *testing.T) {
		t.Parallel()
		for priority := 0; priority <= 4; priority++ {
			if err := validateTool(t, "create_task", map[string]any{
				"projectId": projectID,
				"title":     "Bounded",
				"priority":  priority,
			}); err != nil {
				t.Fatalf("priority %d is inside the advertised range but was refused: %v", priority, err)
			}
		}
	})

	t.Run("omitted/accepted", func(t *testing.T) {
		t.Parallel()
		if err := validateTool(t, "create_task", map[string]any{
			"projectId": projectID,
			"title":     "Bounded",
		}); err != nil {
			t.Fatalf("priority is optional but omitting it was refused: %v", err)
		}
	})
}

func TestSchemaConstraintsAreEnforced(t *testing.T) {
	t.Parallel()

	const (
		publicID   = "0192f4c0-d1e2-7a3b-8c9d-0e1f2a3b4c5d"
		compactID  = "0192f4c0d1e27a3b8c9d0e1f2a3b4c5d"
		calendarID = "0192f4c0-d1e2-7a3b-8c9d-0e1f2a3b4c5e"
	)

	cases := []struct {
		name    string
		tool    string
		args    map[string]any
		wantErr bool
	}{
		{
			name:    "label colour longer than the column",
			tool:    "create_label",
			args:    map[string]any{"name": "urgent", "color": "#ef4444ef4444ef4444"},
			wantErr: true,
		},
		{
			name: "label colour in hex",
			tool: "create_label",
			args: map[string]any{"name": "urgent", "color": "#ef4444"},
		},
		{
			name:    "negative offset",
			tool:    "list_tasks",
			args:    map[string]any{"offset": -5},
			wantErr: true,
		},
		{
			name: "zero offset",
			tool: "list_tasks",
			args: map[string]any{"offset": 0},
		},
		{
			name:    "limit above the advertised ceiling",
			tool:    "search_tasks",
			args:    map[string]any{"query": "release", "limit": 5000},
			wantErr: true,
		},
		{
			name: "limit at the advertised ceiling",
			tool: "search_tasks",
			args: map[string]any{"query": "release", "limit": 200},
		},
		{
			name:    "calendar kind outside the column enum",
			tool:    "create_calendar_event",
			args:    map[string]any{"calendarId": calendarID, "title": "Standup", "kind": "reminder"},
			wantErr: true,
		},
		{
			name: "calendar kind inside the column enum",
			tool: "create_calendar_event",
			args: map[string]any{"calendarId": calendarID, "title": "Standup", "kind": "block"},
		},
		{
			name:    "showAs outside the column enum",
			tool:    "create_calendar_event",
			args:    map[string]any{"calendarId": calendarID, "title": "Standup", "showAs": "maybe"},
			wantErr: true,
		},
		{
			name:    "public id that is not one",
			tool:    "get_task",
			args:    map[string]any{"taskId": "42"},
			wantErr: true,
		},
		{
			// The form every API response carries and every caller sends
			// back. The pattern used to describe only the compact form
			// below, so enforcing it refused every real id in the suite.
			name: "public id, canonical hyphenated form",
			tool: "get_task",
			args: map[string]any{"taskId": publicID},
		},
		{
			name: "public id, compact form types.Parse also accepts",
			tool: "get_task",
			args: map[string]any{"taskId": compactID},
		},
		{
			name:    "public id missing a group",
			tool:    "get_task",
			args:    map[string]any{"taskId": "0192f4c0-d1e2-7a3b-0e1f2a3b4c5d"},
			wantErr: true,
		},
		{
			name:    "required argument missing",
			tool:    "get_task",
			args:    map[string]any{},
			wantErr: true,
		},
		{
			name:    "empty title",
			tool:    "create_task",
			args:    map[string]any{"projectId": publicID, "title": ""},
			wantErr: true,
		},
		{
			name:    "wrong type",
			tool:    "create_task",
			args:    map[string]any{"projectId": publicID, "title": 7},
			wantErr: true,
		},
		{
			name:    "unknown transition",
			tool:    "transition_task",
			args:    map[string]any{"taskId": publicID, "transition": "teleport"},
			wantErr: true,
		},
		{
			name: "known transition",
			tool: "transition_task",
			args: map[string]any{"taskId": publicID, "transition": "complete"},
		},
		{
			name:    "priority inside a nested array item",
			tool:    "apply_steps",
			args:    map[string]any{"parentTaskId": publicID, "steps": []any{map[string]any{"title": "step", "priority": 99}}},
			wantErr: true,
		},
		{
			name: "nested array item within bounds",
			tool: "apply_steps",
			args: map[string]any{"parentTaskId": publicID, "steps": []any{map[string]any{"title": "step", "priority": 2}}},
		},
		{
			name:    "public id inside a string array",
			tool:    "generate_page",
			args:    map[string]any{"contextDescription": "release notes", "taskIds": []any{"not-an-id"}},
			wantErr: true,
		},
		{
			name: "public ids inside a string array",
			tool: "generate_page",
			args: map[string]any{"contextDescription": "release notes", "taskIds": []any{publicID}},
		},
		{
			name: "unknown argument is ignored, not refused",
			tool: "get_task",
			args: map[string]any{"taskId": publicID, "includeComments": true},
		},
		{
			name: "explicit null reads as absent",
			tool: "create_task",
			args: map[string]any{"projectId": publicID, "title": "Bounded", "dueOn": nil},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := validateTool(t, tc.tool, tc.args)
			if tc.wantErr {
				requireArgumentsInvalid(t, err)
				return
			}
			if err != nil {
				t.Fatalf("args satisfy the advertised schema but were refused: %v", err)
			}
		})
	}
}

// TestValidateArgsAcceptsEmptyArguments proves a tool that declares no
// arguments is not made unusable by the validator.
func TestValidateArgsAcceptsEmptyArguments(t *testing.T) {
	t.Parallel()

	for _, raw := range []json.RawMessage{nil, json.RawMessage("{}")} {
		if err := validateArgsAgainstSchema(toolSchema(t, "list_projects"), raw); err != nil {
			t.Fatalf("list_projects takes no arguments but %q was refused: %v", string(raw), err)
		}
	}
}

// TestPublicIDPatternMatchesEmittedIDs ties the advertised public-id
// pattern to an id the system actually produces.
//
// This is the check whose absence let the pattern say `^[0-9a-f]{32}$`
// while types.PublicID.String() emitted a hyphenated UUID. A pattern
// nothing enforces and nothing tests is a sentence in a schema, and it
// was wrong for as long as it was only that. Generating the id rather
// than writing one down is the point: a literal in this test would drift
// the same way the pattern did.
func TestPublicIDPatternMatchesEmittedIDs(t *testing.T) {
	t.Parallel()

	re := compilePattern(publicIDPattern)
	if re == nil {
		t.Fatal("publicIDPattern does not compile")
	}
	for i := 0; i < 32; i++ {
		emitted := types.New().String()
		if !re.MatchString(emitted) {
			t.Fatalf("publicIDPattern %q rejects %q, which is what types.PublicID.String() emits",
				publicIDPattern, emitted)
		}
		// Every id the pattern admits must be one the resolvers can turn
		// back into a PublicID; a pattern that accepts what types.Parse
		// refuses moves the rejection from a named argument error to an
		// opaque one deeper in the tool.
		if _, err := types.Parse(emitted); err != nil {
			t.Fatalf("types.Parse refuses its own output %q: %v", emitted, err)
		}
	}

	// The unhyphenated form the resolvers also accept.
	compact := strings.ReplaceAll(types.New().String(), "-", "")
	if !re.MatchString(compact) {
		t.Errorf("publicIDPattern rejects %q, which types.Parse accepts", compact)
	}
	if _, err := types.Parse(compact); err != nil {
		t.Errorf("types.Parse refuses the compact form %q the pattern admits: %v", compact, err)
	}

	for _, notAnID := range []string{"", "42", "not-a-uuid", "0192f4c0d1e27a3b8c9d0e1f2a3b4c5", "../../etc/passwd"} {
		if re.MatchString(notAnID) {
			t.Errorf("publicIDPattern accepts %q, which is not a public id", notAnID)
		}
	}
}

// TestAdvertisedVocabulariesMatchTheirSource does for the closed
// vocabularies what [TestPublicIDPatternMatchesEmittedIDs] does for
// ids: checks the rule a schema publishes against the code that
// actually decides, rather than against a second copy written by hand.
//
// A schema listing a verb the state machine does not know refuses a
// legitimate call; one omitting a verb it does know refuses a
// legitimate call too. Neither is visible from reading the schema.
func TestAdvertisedVocabulariesMatchTheirSource(t *testing.T) {
	t.Parallel()

	h := NewHandler(Deps{})

	t.Run("transition_task", func(t *testing.T) {
		t.Parallel()
		prop := stringProperty(t, h, "transition_task", "transition")
		for _, verb := range []string{
			taskstate.TransitionStart, taskstate.TransitionBlock, taskstate.TransitionUnblock,
			taskstate.TransitionSubmit, taskstate.TransitionComplete, taskstate.TransitionReopen,
			taskstate.TransitionCancel,
		} {
			if !taskstate.IsKnownTransition(verb) {
				t.Fatalf("the test's own list is stale: taskstate does not know %q", verb)
			}
			if err := validateValue(prop, verb, "transition"); err != nil {
				t.Errorf("transition_task refuses %q, which the state machine accepts: %v", verb, err)
			}
		}
		if err := validateValue(prop, "teleport", "transition"); err == nil {
			t.Error("transition_task accepts a verb the state machine does not know")
		}
	})

	t.Run("add_favorite", func(t *testing.T) {
		t.Parallel()
		prop := stringProperty(t, h, "add_favorite", "targetType")
		if len(validFavoriteTargetTypes) == 0 {
			t.Fatal("no favorite target types registered")
		}
		for name := range validFavoriteTargetTypes {
			if err := validateValue(prop, name, "targetType"); err != nil {
				t.Errorf("add_favorite refuses target type %q, which the tool accepts: %v", name, err)
			}
		}
		if err := validateValue(prop, "workspace", "targetType"); err == nil {
			t.Error("add_favorite accepts a target type the tool cannot resolve")
		}
	})
}

// stringProperty returns one top-level string property's schema node.
func stringProperty(t *testing.T, h *Handler, toolName, propName string) map[string]any {
	t.Helper()
	tl, ok := h.tools[toolName]
	if !ok {
		t.Fatalf("tool %q is not registered", toolName)
	}
	props, ok := tl.inputSchema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("tool %q declares no properties", toolName)
	}
	prop, ok := props[propName].(map[string]any)
	if !ok {
		t.Fatalf("tool %q has no %q property", toolName, propName)
	}
	return prop
}

// TestToolSchemaPatternsCompile fails on a pattern the validator would
// have to skip. compilePattern treats an uncompilable pattern as no
// constraint, which is the right runtime behaviour and the wrong thing
// to discover in production.
func TestToolSchemaPatternsCompile(t *testing.T) {
	t.Parallel()

	h := NewHandler(Deps{})
	walkProperties(t, h, func(t *testing.T, toolName, path string, prop map[string]any) {
		pat, ok := prop["pattern"].(string)
		if !ok || pat == "" {
			return
		}
		if _, err := regexp.Compile(pat); err != nil {
			t.Errorf("tool %q property %q has a pattern that does not compile: %v", toolName, path, err)
		}
	})
}

// TestToolSchemaKeywordsAreEnforced proves no schema advertises a
// constraint the validator does not implement.
//
// This is the half that keeps "advertised equals enforced" true going
// forwards. Adding a keyword to a schema is how a future tool would
// declare a bound; if the validator has never heard of it, the tool
// publishes a promise the server does not keep, which is the failure
// this whole mechanism exists to end.
func TestToolSchemaKeywordsAreEnforced(t *testing.T) {
	t.Parallel()

	h := NewHandler(Deps{})
	var check func(t *testing.T, toolName, path string, schema map[string]any)
	check = func(t *testing.T, toolName, path string, schema map[string]any) {
		for key, v := range schema {
			if !validationKeywords[key] {
				t.Errorf("tool %q schema %q uses JSON Schema keyword %q, which validateValue does not enforce; "+
					"implement it in argvalidate.go or drop it, because an unenforced keyword is a promise to the client the server does not keep",
					toolName, path, key)
			}
			if key != "properties" {
				continue
			}
			props, ok := v.(map[string]any)
			if !ok {
				continue
			}
			for name, raw := range props {
				prop, ok := raw.(map[string]any)
				if !ok {
					continue
				}
				child := name
				if path != "" {
					child = path + "." + name
				}
				check(t, toolName, child, prop)
				if items, ok := prop["items"].(map[string]any); ok {
					check(t, toolName, child+"[]", items)
				}
			}
		}
	}
	for name, tl := range h.tools {
		check(t, name, "", tl.inputSchema)
	}
}

// TestSchemaRejectionNamesTheArgument proves the refusal says which
// argument was wrong. An agent that is told only "arguments are invalid"
// has nothing to correct and retries the same call.
func TestSchemaRejectionNamesTheArgument(t *testing.T) {
	t.Parallel()

	err := validateTool(t, "create_task", map[string]any{
		"projectId": "0192f4c0d1e27a3b8c9d0e1f2a3b4c5d",
		"title":     "Bounded",
		"priority":  999,
	})
	requireArgumentsInvalid(t, err)
	if !strings.Contains(err.Error(), "priority") {
		t.Fatalf("rejection does not name the offending argument: %v", err)
	}
}

// TestEveryToolAcceptsAnEmittedPublicID drives every registered tool
// with a freshly generated id in each of its public-id arguments, and
// requires none of them to be refused.
//
// The case-by-case tests above check the tools they name. This one
// checks the whole registry, because the defect it guards against was
// not in any single tool: one wrong constant, shared by every schema,
// rejected every id the API emits — across twelve end-to-end tests at
// once. A per-tool test would have found it only in the tools somebody
// thought to write a case for.
func TestEveryToolAcceptsAnEmittedPublicID(t *testing.T) {
	t.Parallel()

	h := NewHandler(Deps{})
	checked := 0
	walkProperties(t, h, func(t *testing.T, toolName, path string, prop map[string]any) {
		if typ, _ := prop["type"].(string); typ != "string" {
			return
		}
		if pat, _ := prop["pattern"].(string); pat != publicIDPattern {
			return
		}
		checked++
		// A fresh id per property, so the test cannot pass on one lucky
		// literal, and each property is validated against its own schema
		// node rather than through the whole tool — the subject is the
		// pattern, not whichever other arguments the tool requires.
		id := types.New().String()
		if err := validateValue(prop, id, path); err != nil {
			t.Errorf("tool %q argument %q refuses %q, which is what the API emits: %v",
				toolName, path, id, err)
		}
	})
	if checked == 0 {
		t.Fatal("no public-id arguments found; the guard is passing because it is looking at nothing")
	}
}
