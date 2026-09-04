package responseids

import (
	"strings"
	"testing"
)

// TestToolResponsesCarryNoInternalIDs refuses a tool response holding a
// row's internal identifier.
//
// The counter a row is stored under is dense and guessable, and it addresses
// the row across every workspace at once. The UUID the wire uses is what a
// caller may hold. A response that hands out the counter cannot be taken
// back: a client that learned it keeps it, and it can then name rows it was
// never shown.
//
// Nothing is listed here. The internal ids come out of the generated models
// and the responses out of the handlers, so a table added tomorrow and a
// tool written next week are both read without anyone registering them.
func TestToolResponsesCarryNoInternalIDs(t *testing.T) {
	t.Parallel()

	report := treeReport(t)
	for _, f := range report.Findings {
		switch f.Kind {
		case Value:
			t.Errorf("%s: %s returns %s under %q, which is the row's internal counter "+
				"rather than its public id. The counter is dense, addresses the row in "+
				"every workspace at once, and cannot be withdrawn once a client has read "+
				"it. Return the public id instead",
				f.Location(), f.Owner(), f.Expr, f.Key)
		case Whole:
			t.Errorf("%s: %s returns the row %s as itself, so every field the model "+
				"carries is marshalled — the internal counter and the foreign keys "+
				"beside it — without any key being written. Build the response out of "+
				"the fields the tool means to expose",
				f.Location(), f.Owner(), f.Expr)
		}
	}
}

// TestDerivationStillMatches is the positive control, and it runs on every
// invocation because it reads sources stated here rather than the tree.
//
// It holds the derivation against both shapes it exists to refuse, and
// against the neighbours that look like them: a public id under the same
// key, a parsed argument whose field is named after the column and holds the
// UUID, and a counter the response is entitled to carry. A check that
// flagged those would be answered by exempting them, which is the outcome
// this is written to avoid.
func TestDerivationStillMatches(t *testing.T) {
	t.Parallel()

	const models = `package generated

type Widget struct {
	ID          uint32       ` + "`" + `json:"-"` + "`" + `
	PublicID    types.PublicID ` + "`" + `json:"publicId"` + "`" + `
	WorkspaceID uint32       ` + "`" + `json:"-"` + "`" + `
	TaskID      sql.NullInt32 ` + "`" + `json:"-"` + "`" + `
	Attempts    uint8        ` + "`" + `json:"attempts"` + "`" + `
	Name        string       ` + "`" + `json:"name"` + "`" + `
}
`

	vocab, err := ParseModels("models.go", models)
	if err != nil {
		t.Fatalf("parse the control models: %v", err)
	}
	for _, name := range []string{"ID", "WorkspaceID", "TaskID"} {
		if !vocab[name] {
			t.Errorf("%s was not read as an internal id; it is an unserialised "+
				"integer key, which is the whole shape this looks for", name)
		}
	}
	for _, name := range []string{"PublicID", "Attempts", "Name"} {
		if vocab[name] {
			t.Errorf("%s was read as an internal id; a public id and a counter the "+
				"response may carry are not ones, and collecting them would put a "+
				"finding on a correct response", name)
		}
	}

	// Every line the control asserts on is numbered against this source, so
	// a detector that stops matching one shape cannot pass by matching the
	// other.
	const handlers = `package mcp

func register() {
	h.register(tool{
		name: "get_widget",
		run:  runGetWidget,
	})
	h.register(tool{
		name: "list_widgets",
		run:  runListWidgets,
	})
}

func runGetWidget(ctx context.Context, deps Deps, s *session, raw json.RawMessage) (any, error) {
	var in struct {
		TaskID string ` + "`" + `json:"taskId"` + "`" + `
	}
	if err := parseArgs(raw, &in); err != nil {
		return nil, err
	}
	row, err := deps.Queries.FindWidget(ctx, in.TaskID)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"id":     row.PublicID.String(),
		"taskId": in.TaskID,
		"name":   deps.Naming.Render(row.WorkspaceID),
		"nested": map[string]any{
			"attempts": row.Attempts,
		},
		"tags": []any{row.Name},
		"ws":   strconv.FormatUint(uint64(row.WorkspaceID), 10),
	}, nil
}

func runListWidgets(ctx context.Context, deps Deps, s *session, raw json.RawMessage) (any, error) {
	rows, err := deps.Queries.ListWidgets(ctx, s.workspaceID)
	if err != nil {
		return nil, err
	}
	return rows, nil
}
`

	report, err := ParseHandlers(vocab, map[string]string{"mcp.go": handlers})
	if err != nil {
		t.Fatalf("parse the control handlers: %v", err)
	}
	if report.Handlers != 2 || report.Tools != 2 {
		t.Fatalf("walked %d handlers naming %d tools, want 2 and 2; the handler "+
			"signature or the registration literal stopped matching, and a walk that "+
			"reaches nothing reports nothing", report.Handlers, report.Tools)
	}
	if report.MapValues != 7 {
		t.Errorf("inspected %d response values, want 7; the nested map and the list "+
			"are each a place an id reaches the wire", report.MapValues)
	}

	if len(report.Findings) != 2 {
		for _, f := range report.Findings {
			t.Logf("flagged %s %s under %q at line %d", f.Kind, f.Expr, f.Key, f.Line)
		}
		t.Fatalf("flagged %d responses, want exactly the two violating shapes", len(report.Findings))
	}

	// The lines are the control source's own, counted from its `package`
	// clause: the counter laundered through strconv, and the rows handed
	// back whole.
	value := report.Findings[0]
	if value.Kind != Value || value.Tool != "get_widget" || value.Key != "ws" || value.Line != 33 {
		t.Errorf("the value finding is %+v, want the laundered counter under \"ws\" on "+
			"get_widget at line 33", value)
	}
	whole := report.Findings[1]
	if whole.Kind != Whole || whole.Tool != "list_widgets" || whole.Key != "rows" || whole.Line != 42 {
		t.Errorf("the whole-row finding is %+v, want the returned rows on list_widgets "+
			"at line 42", whole)
	}

	for _, neighbour := range []struct {
		why  string
		line int
	}{
		{"a public id under the key an internal one would use is the correct response", 26},
		{"a parsed argument's taskId holds the public string, not the model's counter", 27},
		{"a helper's result is the helper's, whatever was handed to it", 28},
		{"a counter the model serialises is a value the response may carry", 30},
	} {
		for _, f := range report.Findings {
			if f.Line == neighbour.line {
				t.Errorf("flagged %s at line %d: %s", f.Expr, f.Line, neighbour.why)
			}
		}
	}
}

// TestSessionIDsAreRead holds the other half of the reading: a field whose
// type the sources state, on a struct that is not a model.
//
// The session the transport hands every handler carries the caller's
// workspace and user as the same counters, under names no model spells, so
// the vocabulary cannot answer for them. The declaration can, and it is
// three lines from the handler.
func TestSessionIDsAreRead(t *testing.T) {
	t.Parallel()

	const src = `package mcp

type session struct {
	userID      uint32
	workspaceID uint32
	token       string
}

func runWhoAmI(ctx context.Context, deps Deps, s *session, raw json.RawMessage) (any, error) {
	return map[string]any{"ws": s.workspaceID, "token": s.token}, nil
}
`

	report, err := ParseHandlers(Vocabulary{}, map[string]string{"mcp.go": src})
	if err != nil {
		t.Fatalf("parse the control source: %v", err)
	}
	if len(report.Findings) != 1 {
		for _, f := range report.Findings {
			t.Logf("flagged %s under %q", f.Expr, f.Key)
		}
		t.Fatalf("flagged %d responses, want the session counter alone; the vocabulary "+
			"is empty here, so this is the declaration being read or nothing being read",
			len(report.Findings))
	}
	if got := report.Findings[0]; got.Expr != "s.workspaceID" || got.Line != 10 {
		t.Errorf("flagged %s at line %d, want s.workspaceID at line 10", got.Expr, got.Line)
	}
}

// TestListRowStructsAreRead holds the arm that reads a response assembled
// through a declared type rather than a map.
//
// A list-shaped tool names a row type beside itself, fills one per row and
// hands the slice back inside a map that states one key. Everything the
// caller receives is written in that type's literals, so a check reading
// only the map would inspect the key and nothing under it.
func TestListRowStructsAreRead(t *testing.T) {
	t.Parallel()

	const models = `package generated

type Widget struct {
	ID       uint32 ` + "`" + `json:"-"` + "`" + `
	UserID   uint32 ` + "`" + `json:"-"` + "`" + `
	PublicID types.PublicID ` + "`" + `json:"publicId"` + "`" + `
}
`
	vocab, err := ParseModels("models.go", models)
	if err != nil {
		t.Fatalf("parse the control models: %v", err)
	}

	const src = `package mcp

type widgetOut struct {
	ID    string ` + "`" + `json:"id"` + "`" + `
	Owner uint32 ` + "`" + `json:"owner"` + "`" + `
	seq   uint32
}

func runListWidgets(ctx context.Context, deps Deps, s *session, raw json.RawMessage) (any, error) {
	rows, err := deps.Queries.ListWidgets(ctx, s.workspaceID)
	if err != nil {
		return nil, err
	}
	out := make([]widgetOut, 0, len(rows))
	for _, r := range rows {
		out = append(out, widgetOut{ID: r.PublicID.String(), Owner: r.UserID, seq: r.ID})
	}
	return map[string]any{"widgets": out}, nil
}
`

	report, err := ParseHandlers(vocab, map[string]string{"mcp.go": src})
	if err != nil {
		t.Fatalf("parse the control source: %v", err)
	}
	if report.MapValues != 3 {
		t.Errorf("inspected %d response values, want 3: the one key of the map and the "+
			"two fields of the row type that reach the wire", report.MapValues)
	}
	if len(report.Findings) != 1 {
		for _, f := range report.Findings {
			t.Logf("flagged %s under %q", f.Expr, f.Key)
		}
		t.Fatalf("flagged %d responses, want the owner counter alone", len(report.Findings))
	}
	got := report.Findings[0]
	if got.Expr != "r.UserID" || got.Key != "owner" || got.Line != 16 {
		t.Errorf("flagged %s under %q at line %d, want r.UserID under \"owner\" at line 16; "+
			"the public id beside it is the correct field, and the unexported one the "+
			"marshaller never sees", got.Expr, got.Key, got.Line)
	}
}

// treeReport walks the tree and proves the walk reached something.
//
// This check reads files by path and matches on a signature. A tree that
// moved, a handler shape that changed, a model file that stopped carrying
// the tags — each of them empties the walk, and an empty walk reports no
// findings for the same reason a clean one does.
func treeReport(t *testing.T) Report {
	t.Helper()
	root, err := RepoRoot()
	if err != nil {
		t.Fatalf("locate repository root: %v", err)
	}

	vocab, err := InternalIDs(root)
	if err != nil {
		t.Fatalf("read the generated models: %v", err)
	}
	if len(vocab) == 0 {
		t.Fatal("no internal id was read out of the generated models; the field types " +
			"or the tags marking them unserialised stopped matching, rather than the " +
			"surrogate keys having gone away")
	}

	report, err := Walk(root, vocab)
	if err != nil {
		t.Fatalf("read the tool handlers: %v", err)
	}
	if report.Handlers == 0 {
		t.Fatal("no tool handler was walked; the signature every one of them carries " +
			"stopped matching, and nothing was read")
	}
	if report.MapValues == 0 {
		t.Fatal("no response value was inspected; the handlers were walked and none of " +
			"them was seen to build a response, which is not how any of them is written")
	}

	t.Logf("%d internal id fields, %d tool handlers (%d named by a tool), %d response values inspected",
		len(vocab), report.Handlers, report.Tools, report.MapValues)
	t.Logf("internal ids: %s", strings.Join(vocab.Names(), " "))
	return report
}
