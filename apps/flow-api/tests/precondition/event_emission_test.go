package precondition

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ReservedKind is one declared event kind that no code appends, together
// with why.
type ReservedKind struct {
	// Name is the constant in packages/go-shared/eventbus.
	Name string
	// Reason is what stands in place of a producer. It is carried in the
	// entry rather than in a comment above the list so it cannot end up
	// describing the neighbouring line.
	Reason string
}

// reservedKinds are the declared kinds nothing appends today.
//
// A reason here is one of two shapes, and the difference is the whole
// value of writing them down:
//
//   - nothing performs the operation the kind describes, so there is
//     nowhere an append could go;
//   - something performs it and appends a different kind, so the entry
//     records an open gap rather than approving one.
//
// The list is not documentation, because it is held to the tree from
// both sides. A kind that stops being reserved fails
// [TestEveryDeclaredEventKindIsAppendedSomewhere] as a stale entry, and a
// name that stops being declared fails the same way; either has to be
// deleted before the suite goes green again. That is what stops the list
// from being the place unemitted kinds go to be forgotten, which is the
// state the fan-out's title table was already in for them: every kind
// here has a delivery policy, and four have notification copy a user
// could receive if anything ever wrote the row.
var reservedKinds = []ReservedKind{
	{
		Name:   "AgentTaskAttached",
		Reason: "attaching an agent inserts a task_actors row with kind='agent' and appends task.actor.added; nothing appends this",
	},
	{
		Name:   "AgentTaskDetached",
		Reason: "removing an agent actor goes through the same path as a user actor and appends task.actor.removed",
	},
	{
		Name:   "AgentTaskThought",
		Reason: "an agent's reasoning is persisted to tasks.agent_memo, which is a column write with no event beside it",
	},
	{
		Name:   "AiSuggestionEdited",
		Reason: "editing a triage suggestion is a local change in the dialog that is never sent to the API",
	},
	{
		Name:   "CalMemoCompleted",
		Reason: "the memo update statement sets calendar_memos.done and appends calendar.memo.updated for every field it touches",
	},
	{
		Name:   "CommentAddedLegacy",
		Reason: "kept so historical rows still resolve to a family; a comment is appended as task.comment.added",
	},
	{
		Name:   "ItemActorAdded",
		Reason: "itemkit does not propagate task_actors rows to calendar_event_attendees",
	},
	{
		Name:   "ItemActorRemoved",
		Reason: "itemkit does not propagate a task_actors removal to the attendee rows",
	},
	{
		Name:   "ItemVisibilityChanged",
		Reason: "itemkit does not propagate a task's visibility to its linked events",
	},
	{
		Name:   "PageArchived",
		Reason: "pages.archived_at is written by no operation; archiving a page is not a request the API answers",
	},
	{
		Name:   "PageUnarchived",
		Reason: "the reverse of an operation that does not exist",
	},
}

// TestEveryDeclaredEventKindIsAppendedSomewhere holds each declared event
// kind to having a producer.
//
// The failure it is written against passes every other check there is. A
// kind is declared, resolves to a family, and is given a notification
// title and a severity — three files that all agree about what to do with
// the row when it arrives, and nothing anywhere that writes one. The
// copy is real and reaches nobody; the doc comment says the kind is
// appended when something happens and no code makes that true. Nothing
// fails, because every existing check reads the consuming side, and the
// consuming side is complete.
//
// So this reads the other side, and it reads it from the whole tree
// rather than from flow-api: a producer may be a Go call site naming the
// constant, a sqlc statement writing the wire string into a VALUES list
// with no Go constant on the path, or a run-time minter that builds the
// name from a request field. [emissionScope.Emitters] states which
// references count and what the rule does not look at — the largest gap
// being that it asks whether a producer exists, not whether it appends
// the kind under the condition the doc comment claims.
func TestEveryDeclaredEventKindIsAppendedSomewhere(t *testing.T) {
	t.Parallel()

	scope := emissionSetup(t)
	reserved := map[string]string{}
	for _, entry := range reservedKinds {
		reserved[entry.Name] = entry.Reason
	}

	for _, kind := range scope.Unemitted() {
		if _, ok := reserved[kind.Name]; ok {
			continue
		}
		mentions := scope.NonProducing(kind.Name)
		evidence := "nothing anywhere else in the tree names it"
		if len(mentions) > 0 {
			evidence = "it is named only where it cannot be written: " + describeSites(mentions)
		}
		t.Errorf("%s declares %s (%q) and no production Go or SQL appends it.\n"+
			"  %s.\n"+
			"  A kind with no producer still resolves to a family and still has a delivery policy, so every "+
			"check that reads the consuming side passes while the event never occurs.\n"+
			"  Append it where the operation its doc comment describes happens, or add it to reservedKinds "+
			"with the reason nothing does.",
			kind.Location(), kind.Name, kind.Wire, evidence)
	}

	for _, entry := range reservedKinds {
		kind, declared := scope.Kind(entry.Name)
		if !declared {
			t.Errorf("reservedKinds lists %s, which packages/go-shared/eventbus no longer declares; drop the stale entry",
				entry.Name)
			continue
		}
		if sites := scope.Emitters(entry.Name); len(sites) > 0 {
			t.Errorf("reservedKinds lists %s (%q) as having no producer, reason %q, but it is appended at %s.\n"+
				"  Drop the entry: a list that keeps an entry after the kind gains a producer records what was "+
				"once true and checks nothing.",
				entry.Name, kind.Wire, entry.Reason, describeSites(sites))
		}
		if strings.TrimSpace(entry.Reason) == "" {
			t.Errorf("reservedKinds lists %s with no reason; an entry without one is indistinguishable from a kind nobody looked at",
				entry.Name)
		}
	}
}

// TestEventKindsWithOneProducerAreNotReported is the arm that says the
// rule is looking at something.
//
// A matcher loose enough to count the declaration, the family table or
// the fan-out would report no kind at all, and a green run is what that
// looks like from the outside. A matcher tight enough to miss the
// spellings a producer actually uses would report most of them. Both are
// pinned here by kinds appended from exactly one place, in each of the
// two forms a single append can take.
func TestEventKindsWithOneProducerAreNotReported(t *testing.T) {
	t.Parallel()

	scope := emissionSetup(t)

	// A kind appended by one Go call site, and one appended by a single
	// sqlc statement with no Go constant anywhere on its path. The second
	// is why SQL is read at all: dropping that half would report a kind
	// whose producer is a VALUES list.
	for name, want := range map[string]Language{
		"CalendarReminder":       LangGo,
		"AgentTaskHandoffToUser": LangSQL,
	} {
		sites := scope.Emitters(name)
		if len(sites) != 1 {
			t.Errorf("%s is derived as having %d producers; want exactly 1, in %s.\n  found: %s",
				name, len(sites), want, describeSites(sites))
			continue
		}
		if sites[0].Lang != want {
			t.Errorf("%s is derived as produced from %s at %s; want %s",
				name, sites[0].Lang, sites[0].Location(), want)
		}
		for _, surface := range emissionSurfaces {
			if sites[0].File == surface.Path {
				t.Errorf("%s is counted as produced by %s, which is a declaration surface: the matcher is "+
					"reading a kind's own declaration as its emission, which passes the rule on every kind",
					name, surface.Path)
			}
		}
	}

	// The transition kinds have no call site of their own: they are built
	// from a request field. Counting the builder is what keeps them out of
	// the reserved list, where they would be a false statement.
	minted := scope.Emitters("TaskTransitionStart")
	if len(minted) == 0 {
		t.Error("TaskTransitionStart is derived as having no producer, but the transition kinds are appended " +
			"through the run-time builder; the minter derivation has stopped matching")
	}
	for _, site := range minted {
		if !strings.Contains(site.Form, "(...)") {
			t.Errorf("TaskTransitionStart is counted as produced by a direct reference at %s (%s); "+
				"the constants have no call site of their own and this is reading something else",
				site.Location(), site.Form)
		}
	}
}

// emissionSetup parses the repository once and asserts the scan reached
// what it is supposed to have reached.
//
// A derived check reports nothing when its derivation stops matching, and
// nothing is also what a clean tree looks like. Every floor here separates
// the two, and the surface counts are the sharpest of them: a surface that
// stops matching a file silently turns into an inclusion, and since each
// surface names kinds in bulk, one of them alone would make the whole
// rule pass.
func emissionSetup(t *testing.T) *emissionScope {
	t.Helper()

	root, err := RepoRoot()
	if err != nil {
		t.Fatalf("locate repository root: %v", err)
	}
	scope, err := parseEmissionScope(root)
	if err != nil {
		t.Fatalf("scan the repository for event kinds: %v", err)
	}

	if len(scope.kinds) < 100 {
		t.Fatalf("only %d event kinds were read from %s; the constant declarations have stopped matching",
			len(scope.kinds), kindDeclarationFile)
	}
	if scope.files < 500 {
		t.Fatalf("only %d source files were scanned; the walk is reading a fraction of the tree", scope.files)
	}
	if scope.statements < 100 {
		t.Fatalf("only %d named SQL statements were read; a kind appended from SQL alone would be reported",
			scope.statements)
	}
	if len(scope.minters) == 0 {
		t.Fatal("no run-time kind builder was derived from the declaration file; every kind it mints would be reported as unemitted")
	}
	for _, minter := range scope.minters {
		if !strings.Contains(minter.Prefix, ".") {
			t.Fatalf("minter %s was derived with prefix %q, which is not a namespace; a short prefix answers for kinds it does not build",
				minter.Name, minter.Prefix)
		}
	}
	for _, surface := range emissionSurfaces {
		if _, statErr := os.Stat(filepath.Join(root, filepath.FromSlash(surface.Path))); statErr != nil {
			t.Fatalf("%s is excluded as a declaration surface (%s) but does not exist: %v",
				surface.Path, surface.Reason, statErr)
		}
		if scope.surfaceHits[surface.Path] == 0 {
			t.Fatalf("%s is excluded as a declaration surface (%s) and names no kind; either it has stopped "+
				"being one, or its references are now being counted as emission",
				surface.Path, surface.Reason)
		}
	}
	return scope
}
