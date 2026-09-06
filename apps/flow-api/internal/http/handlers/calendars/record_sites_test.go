package calendars

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strconv"
	"testing"
)

// calendarRecordGates are the two package adapters a handler records
// through, keyed by the name a call site writes.
var calendarRecordGates = map[string]bool{
	"recordCalendarChange": true,
	"recordCalendarAudit":  true,
}

// recordSite is one mutation literal handed to one of those adapters.
//
// It is read out of the source because both halves of a record land in
// the database, and a package test has no database: what it can hold is
// the shape of what the handler asks for, which is where the failure
// this guards against lives — an operation asking for nothing, or asking
// for an event kind on the adapter that appends none.
type recordSite struct {
	gate    string
	line    int
	fields  map[string]bool
	strings map[string]string
	payload map[string]bool
}

// recordSitesIn returns every record the named function asks for, in
// source order.
func recordSitesIn(t *testing.T, file, fn string) []recordSite {
	t.Helper()

	fset := token.NewFileSet()
	parsed, err := parser.ParseFile(fset, file, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", file, err)
	}
	var decl *ast.FuncDecl
	for _, d := range parsed.Decls {
		f, ok := d.(*ast.FuncDecl)
		if ok && f.Name.Name == fn && f.Body != nil {
			decl = f
			break
		}
	}
	if decl == nil {
		t.Fatalf("%s declares no %s", file, fn)
	}

	var out []recordSite
	ast.Inspect(decl.Body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		id, ok := call.Fun.(*ast.Ident)
		if !ok || !calendarRecordGates[id.Name] {
			return true
		}
		line := fset.Position(call.Pos()).Line
		lit := mutationLiteralArg(call)
		if lit == nil {
			t.Fatalf("%s:%d: %s is handed a mutation assembled elsewhere; written at the call site it is also what the "+
				"package guard reads, and away from it nothing checks the shape", file, line, id.Name)
		}
		site := recordSite{
			gate:    id.Name,
			line:    line,
			fields:  map[string]bool{},
			strings: map[string]string{},
			payload: map[string]bool{},
		}
		for _, elt := range lit.Elts {
			kv, ok := elt.(*ast.KeyValueExpr)
			if !ok {
				continue
			}
			key, ok := kv.Key.(*ast.Ident)
			if !ok {
				continue
			}
			site.fields[key.Name] = true
			if s, ok := literalString(kv.Value); ok {
				site.strings[key.Name] = s
			}
			if key.Name != "Payload" {
				continue
			}
			payload, ok := kv.Value.(*ast.CompositeLit)
			if !ok {
				continue
			}
			for _, entry := range payload.Elts {
				pair, ok := entry.(*ast.KeyValueExpr)
				if !ok {
					continue
				}
				if s, ok := literalString(pair.Key); ok {
					site.payload[s] = true
				}
			}
		}
		out = append(out, site)
		return true
	})
	return out
}

// mutationLiteralArg returns the mutationlog.Mutation written inline at
// a call, or nil when none was.
func mutationLiteralArg(call *ast.CallExpr) *ast.CompositeLit {
	for _, arg := range call.Args {
		lit, ok := arg.(*ast.CompositeLit)
		if !ok {
			continue
		}
		sel, ok := lit.Type.(*ast.SelectorExpr)
		if !ok {
			continue
		}
		pkg, ok := sel.X.(*ast.Ident)
		if ok && pkg.Name == "mutationlog" && sel.Sel.Name == "Mutation" {
			return lit
		}
	}
	return nil
}

// literalString returns the value of a string literal expression.
func literalString(e ast.Expr) (string, bool) {
	lit, ok := e.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "", false
	}
	s, err := strconv.Unquote(lit.Value)
	if err != nil {
		return "", false
	}
	return s, true
}

// siteFor returns the single record carrying an audit action, so a test
// names the record it is about rather than an index that moves.
func siteFor(t *testing.T, sites []recordSite, action string) recordSite {
	t.Helper()
	var found []recordSite
	for _, s := range sites {
		if s.strings["AuditAction"] == action {
			found = append(found, s)
		}
	}
	if len(found) != 1 {
		t.Fatalf("want exactly one record for %q, found %d", action, len(found))
	}
	return found[0]
}

// requireAuditOnly holds a record to the adapter that appends nothing.
//
// The event kind is the part that has to be absent rather than merely
// unused: a literal naming a kind the adapter will never append reads,
// to anyone reviewing it, as a change that reaches the timeline, and the
// timeline never carries it.
func requireAuditOnly(t *testing.T, site recordSite, resourceType string) {
	t.Helper()
	if site.gate != "recordCalendarAudit" {
		t.Errorf("line %d: recorded through %s; a record that appends no event goes through recordCalendarAudit",
			site.line, site.gate)
	}
	if site.fields["EventType"] {
		t.Errorf("line %d: names an event kind it will not append; either drop it or record through the adapter that appends one",
			site.line)
	}
	if got := site.strings["ResourceType"]; got != resourceType {
		t.Errorf("line %d: resource type %q, want %q", site.line, got, resourceType)
	}
	for _, field := range []string{"AuditAction", "ResourceID", "CallSite"} {
		if !site.fields[field] {
			t.Errorf("line %d: names no %s, so the audit row cannot be found by the query that looks for it", site.line, field)
		}
	}
}

// A presigned GET URL carries the stored object to whoever holds it, so
// the download is the moment a file can leave the workspace. Nothing
// else records that: the attachment row is untouched and the object
// store keeps no per-workspace trail, so an administrator asked which
// files went out has this row or nothing.
func TestAttachmentDownloadLeavesAnAuditRowAndNoTimelineRow(t *testing.T) {
	t.Parallel()

	sites := recordSitesIn(t, "attachments.go", "DownloadAttachment")
	if len(sites) != 1 {
		t.Fatalf("want one record for a download, found %d", len(sites))
	}
	site := siteFor(t, sites, "calendar.attachment.download")
	requireAuditOnly(t, site, "calendar.attachment")

	// The three ids are what makes the row answer "which file, on which
	// event, in which calendar" without a second query.
	for _, key := range []string{"eventId", "calendarId", "attachmentId"} {
		if !site.payload[key] {
			t.Errorf("the download record carries no %s; the row then names the act without naming what was handed out", key)
		}
	}
}

// Confirm turns a size reservation into a measured object the sweeper
// leaves alone, which is a state change worth finding later. It is not a
// second appearance on the timeline: the attachment reached it when the
// presign committed the row.
func TestAttachmentConfirmRecordsTheMeasuredSizeWithoutASecondTimelineRow(t *testing.T) {
	t.Parallel()

	sites := recordSitesIn(t, "confirm_attachment.go", "ConfirmAttachment")
	confirmed := siteFor(t, sites, "calendar.attachment.confirm")
	requireAuditOnly(t, confirmed, "calendar.attachment")

	if !confirmed.payload["byteSize"] {
		t.Error("the confirm record carries no byteSize; measuring the object is the whole of what this endpoint establishes")
	}

	// The rejection is a different act with a different outcome — the
	// attachment is gone — so it stays on the timeline and must not be
	// folded into the record above.
	rejected := siteFor(t, sites, "calendar.attachment.delete")
	if rejected.gate != "recordCalendarChange" {
		t.Errorf("line %d: the oversize rejection deletes the attachment, so it belongs on the timeline", rejected.line)
	}
	if !rejected.fields["EventType"] {
		t.Errorf("line %d: the oversize rejection names no event kind", rejected.line)
	}
}

// PresignAttachment is the writer that owns the attachment's event.
// Confirm records the audit half only on the strength of that; if the
// presign stopped appending, the attachment would reach the timeline
// nowhere and confirm would be the place it has to.
func TestTheAttachmentEventIsAppendedWhenTheRowIsCreated(t *testing.T) {
	t.Parallel()

	sites := recordSitesIn(t, "attachments.go", "PresignAttachment")
	created := siteFor(t, sites, "calendar.attachment.create")
	if created.gate != "recordCalendarChange" {
		t.Fatalf("line %d: the attachment reaches the timeline through %s", created.line, created.gate)
	}
	if got := created.strings["EventType"]; got != "" {
		t.Fatalf("line %d: event kind written as a string %q; it is a named constant so both transports spell it once", created.line, got)
	}
	if !created.fields["EventType"] {
		t.Fatal("the attachment is put on no timeline when it is created, so confirm cannot record the audit half alone")
	}
}

// A publish batch leaves one record, and which one it is answers the
// question the endpoint exists to raise: did anything reach a URL the
// public can open. Recording only the batch that published something
// left the wholly refused attempt — the one an administrator chasing an
// exposure is looking for — as silence indistinguishable from a request
// nobody made.
func TestAPublishedBatchAndARefusedOneAreToldApart(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name        string
		attached    int
		skipped     int
		wantRefusal bool
	}{
		{
			name:        "a batch that published something is the other record's",
			attached:    3,
			skipped:     0,
			wantRefusal: false,
		},
		{
			name:        "a partly refused batch published something, so it is still the other record's",
			attached:    2,
			skipped:     5,
			wantRefusal: false,
		},
		{
			name:        "every candidate refused is the case with no other record",
			attached:    0,
			skipped:     4,
			wantRefusal: true,
		},
		{
			name:        "one refusal is enough; a single event is what a share usually publishes",
			attached:    0,
			skipped:     1,
			wantRefusal: true,
		},
		{
			name:        "a request naming no events asked for nothing",
			attached:    0,
			skipped:     0,
			wantRefusal: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := attachRefusedEveryCandidate(tc.attached, tc.skipped); got != tc.wantRefusal {
				t.Errorf("attached=%d skipped=%d: refusal record %v, want %v", tc.attached, tc.skipped, got, tc.wantRefusal)
			}
		})
	}
}

// The two outcomes above have to reach the reader as two different rows.
// One action name for both would put a refused attempt and a successful
// publish under the same query, which is the distinction the record is
// for.
func TestThePublishedAndRefusedRecordsAreDistinct(t *testing.T) {
	t.Parallel()

	sites := recordSitesIn(t, "public_shares.go", "AttachEventsToShare")
	published := siteFor(t, sites, "calendar.share.events_attach")
	refused := siteFor(t, sites, "calendar.share.events_attach_refused")

	if published.gate != "recordCalendarChange" || !published.fields["EventType"] {
		t.Errorf("line %d: a batch that published events belongs on the timeline", published.line)
	}
	requireAuditOnly(t, refused, "calendar.share")

	if published.strings["ResourceType"] != refused.strings["ResourceType"] {
		t.Errorf("the two records name different resource types (%q and %q); both are about the same share",
			published.strings["ResourceType"], refused.strings["ResourceType"])
	}

	// A refusal that does not say how much was refused, out of how much
	// was asked for, cannot be told from a batch of one.
	for _, key := range []string{"shareId", "skipped", "requested"} {
		if !refused.payload[key] {
			t.Errorf("the refusal record carries no %s; the row then reports that nothing was published without reporting the scale of the attempt", key)
		}
	}
}
