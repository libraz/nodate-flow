// Package taskvisibility derives, from the committed SQL, which
// statements put a task's own content on the wire — and holds every one
// of them to a single spelling of the Layer 4 visibility rule.
//
// The rule itself was never the problem. It is short, it is written
// down, and `acl.TaskVisibilityFilter` has existed for as long as the
// visibility column has. What kept happening is that the set of
// statements projecting a task's title grew faster than the set applying
// the rule, and that each copy of the predicate was typed out again, so
// fixing one copy left the others as they were. A private task's title
// has reached readers who may not see it more than once that way, most
// recently through a calendar event's linked-task list.
//
// Sharing the fragment is not on its own available here. sqlc parses the
// statement it generates from, so a predicate spliced in at runtime is
// invisible to it; a view takes no parameters, so it cannot carry the
// actor. What is available is one canonical spelling, and something that
// reads every statement and refuses any other. So:
//
//	source      a relation that puts a task content column on the wire.
//	            `tasks` itself, and — derived, not listed — every view in
//	            sql/schema.sql that projects one of those columns from
//	            it, transitively. A view added tomorrow over tasks.title
//	            is a source tomorrow.
//	sink        a statement that projects a content column from a source,
//	            through some alias: one declared in sql/queries, and one
//	            built and executed in Go. Both are read because reading
//	            only the first covers exactly the projections that follow
//	            the convention SQL lives in sql/queries, and a projection
//	            moved into Go would leave the scope silently — see
//	            gosources.go.
//	held to     the statement contains the canonical unit, anchored on
//	            that same alias, for every alias it takes content from.
//
// A statement that reads through a SELECT of its own — a derived table, a
// CTE, a subquery in the select list — is followed into it, and the
// content it takes is attributed to the alias inside that reads the
// source, since that is the only place the rule could be written. What
// cannot be followed is named rather than dropped: a projection this could
// not read is the one shape that must not pass quietly, because a
// statement reporting no exposure and a statement whose exposure was
// dropped look identical from the outside.
//
// The unit is generated per (alias, source) from [Canonical] rather than
// matched loosely, so a predicate that has been edited in one place
// fails as surely as one that is missing: containment of an exact token
// sequence is the check, and "nearly the rule" is not the rule.
//
// The same unit is required of `acl.TaskVisibilityFilter`, the
// hand-splice form the dynamic list queries use. Two spellings that
// cannot share a string can still be held to being the same string.
//
// A statement that legitimately projects task content without the
// predicate says so at the statement, in a marker a machine reads — see
// [MarkerForm]. The reason is mandatory: a marker with nothing after it
// is not a marker.
package taskvisibility

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// ContentColumns are the `tasks` columns whose value tells the reader
// what the task is about. Projecting one of them is what puts the
// statement in scope.
//
// The list is short and it is a judgement, so it is stated rather than
// derived: nothing in the table definition distinguishes a title from a
// priority. Everything downstream of it — which relations expose these
// columns, which statements project them, under which names — is read
// out of the SQL.
//
// derived_state, due_on and priority are deliberately absent. They are
// metadata about a row whose existence the reader can already infer from
// a count, and holding every statement that returns a due date to an ACL
// predicate would put the engine and the reconciler in scope of a rule
// about disclosure.
var ContentColumns = []string{"title", "description", "notes", "agent_memo"}

// AnchorColumns are the `tasks` columns the predicate itself reads. A
// relation can only carry the rule if it exposes all four, and the names
// it exposes them under are what the unit is generated with — v_inbox
// calls them task_visibility, task_project_id, task_created_by_user_id
// and task_internal_id.
type AnchorColumns struct {
	Visibility string
	ProjectID  string
	CreatedBy  string
	InternalID string
}

// anchorSources are the `tasks` column names behind each anchor.
var anchorSources = []string{"visibility", "project_id", "created_by_user_id", "id"}

// MarkerForm is the machine-readable exemption, written in the comment
// block under a statement's `-- name:` header.
const MarkerForm = "task-visibility: not-applicable — <why this projection cannot disclose a task the reader may not see>"

// markerPattern matches [MarkerForm]. Requiring the reason to begin and
// end with a letter is what stops a sentence *about* the marker from
// acting as one, the same rule the affected-rows and precondition gates
// use for theirs.
var markerPattern = regexp.MustCompile(
	`task-visibility:[ \t]*not-applicable[ \t]*—[ \t]*[A-Za-z][^\n]*[A-Za-z]`)

// Canonical returns the one spelling of the visibility unit, for a task
// reached through alias under the column names the relation exposes.
//
// It is normalised text, not SQL to paste: the check asks whether a
// statement contains this token sequence once both have been through
// [Normalize].
func Canonical(alias string, cols AnchorColumns) string {
	raw := fmt.Sprintf(`%[1]s.%[2]s = 'public'
    OR (%[1]s.%[2]s = 'project' AND EXISTS (
      SELECT 1 FROM project_members pm
      WHERE pm.project_id = %[1]s.%[3]s
        AND pm.user_id = @actor
        AND pm.enabled = TRUE
    ))
    OR (%[1]s.%[2]s = 'private' AND (
      %[1]s.%[4]s = @actor
      OR EXISTS (
        SELECT 1 FROM task_actors ta
        WHERE ta.task_id = %[1]s.%[5]s
          AND ta.kind = 'user'
          AND ta.user_id = @actor
          AND ta.enabled = TRUE
      )
    ))`, alias, cols.Visibility, cols.ProjectID, cols.CreatedBy, cols.InternalID)
	return Normalize(raw)
}

// ----------------------------------------------------------------------
// Normalisation
// ----------------------------------------------------------------------

var (
	commentTail  = regexp.MustCompile(`--[^\n]*`)
	sqlcActorArg = regexp.MustCompile(`cast\s*\(\s*sqlc\.arg\s*\(\s*'?actor_user_id'?\s*\)\s*as\s+(unsigned|signed)\s*\)`)
	sqlcActorRaw = regexp.MustCompile(`sqlc\.arg\s*\(\s*'?actor_user_id'?\s*\)`)
	fromAliasPat = regexp.MustCompile(`\bfrom\s+(project_members|task_actors)\s+([a-z_][a-z0-9_]*)`)
	punctuation  = regexp.MustCompile(`([(),=])`)
)

// Normalize reduces a statement to a token sequence that does not depend
// on how it was wrapped, which subquery alias it chose, or which of the
// two argument spellings it uses.
//
// Only three things are rewritten, and each is a difference the rule does
// not have: line comments go, the actor argument collapses onto one
// token whether it arrived as sqlc.arg or as a bind placeholder, and the
// aliases bound to project_members and task_actors are renamed to their
// canonical short forms. Everything else — column names, the shape of
// the boolean tree, the order of the branches — survives, because a
// change to any of those is a change to the rule.
func Normalize(sql string) string {
	text := commentTail.ReplaceAllString(sql, " ")
	text = strings.ToLower(text)
	text = sqlcActorArg.ReplaceAllString(text, "@actor")
	text = sqlcActorRaw.ReplaceAllString(text, "@actor")
	text = strings.ReplaceAll(text, "@actor", " @actor ")
	text = punctuation.ReplaceAllString(text, " $1 ")
	text = strings.Join(strings.Fields(text), " ")

	// Rename the subquery aliases after tokenising, so the boundaries
	// are unambiguous: `from project_members pm_vis` binds pm_vis, and
	// every `pm_vis .` in the statement becomes `pm .`.
	for _, m := range fromAliasPat.FindAllStringSubmatch(text, -1) {
		short := "pm"
		if m[1] == "task_actors" {
			short = "ta"
		}
		text = strings.ReplaceAll(text, " "+m[2]+".", " "+short+".")
		text = strings.ReplaceAll(text, "from "+m[1]+" "+m[2], "from "+m[1]+" "+short)
	}
	return text
}

// NormalizeFragment is [Normalize] for the hand-spliced Go fragment,
// whose actor argument is a bare bind placeholder. It is a separate entry
// point because turning every `?` into the actor is only sound for a
// fragment that binds nothing else, which the visibility filter does not.
func NormalizeFragment(sql string) string {
	return Normalize(strings.ReplaceAll(sql, "?", "@actor"))
}

// ----------------------------------------------------------------------
// Repository access
// ----------------------------------------------------------------------

// RepoRoot returns the repository root, found by walking up from the
// caller's working directory to the go.work that defines the workspace.
func RepoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, statErr := os.Stat(filepath.Join(dir, "go.work")); statErr == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", errors.New("taskvisibility: go.work not found above the working directory")
		}
		dir = parent
	}
}

// ReadSchema returns the contents of sql/schema.sql under root.
func ReadSchema(root string) (string, error) {
	raw, err := os.ReadFile(filepath.Join(root, "sql", "schema.sql")) //#nosec G304,G122 -- repository path read at test time
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

// ReadQueries returns every .sql file under sql/queries, keyed by its
// repository-relative path.
func ReadQueries(root string) (map[string]string, error) {
	out := map[string]string{}
	base := filepath.Join(root, "sql", "queries")
	err := filepath.WalkDir(base, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			return nil
		}
		raw, readErr := os.ReadFile(path) //#nosec G304,G122 -- repository path walked at test time
		if readErr != nil {
			return readErr
		}
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			rel = path
		}
		out[filepath.ToSlash(rel)] = string(raw)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// ReadVisibilityFilterFragment returns the SQL fragment
// acl.TaskVisibilityFilter splices into the dynamic list queries, read
// out of the Go source rather than by calling it: the check has to run
// without a database and without importing the handler tree.
func ReadVisibilityFilterFragment(root string) (string, error) {
	path := filepath.Join(root, "apps", "flow-api", "internal", "acl", "check.go")
	raw, err := os.ReadFile(path) //#nosec G304,G122 -- repository path read at test time
	if err != nil {
		return "", err
	}
	text := string(raw)
	const marker = "const frag = `"
	at := strings.Index(text, marker)
	if at < 0 {
		return "", fmt.Errorf("taskvisibility: no `const frag` literal in %s; "+
			"the hand-spliced form of the rule has moved and this check has stopped reading it", path)
	}
	rest := text[at+len(marker):]
	end := strings.Index(rest, "`")
	if end < 0 {
		return "", fmt.Errorf("taskvisibility: unterminated `const frag` literal in %s", path)
	}
	return rest[:end], nil
}

// ----------------------------------------------------------------------
// Sources
// ----------------------------------------------------------------------

// Source is a relation that puts task content on the wire.
type Source struct {
	// Name is the table or view name.
	Name string
	// Content maps the name the relation exposes a content column under
	// onto the `tasks` column behind it.
	Content map[string]string
	// Anchors are the names the relation exposes the predicate's own
	// inputs under, and Carries reports whether it exposes all of them.
	Anchors AnchorColumns
	Carries bool
}

// Sources derives every relation exposing task content from the schema.
//
// `tasks` seeds the set; each view is then resolved against what is
// already known, repeatedly, until nothing new appears — which is what
// lets v_task_list inherit through v_task_list_all without either being
// named here.
func Sources(schema string) map[string]*Source {
	base := &Source{Name: "tasks", Content: map[string]string{}, Carries: true}
	for _, c := range ContentColumns {
		base.Content[c] = c
	}
	base.Anchors = AnchorColumns{
		Visibility: "visibility", ProjectID: "project_id",
		CreatedBy: "created_by_user_id", InternalID: "id",
	}
	out := map[string]*Source{"tasks": base}

	views := parseViews(schema)
	for changed := true; changed; {
		changed = false
		for _, v := range views {
			resolved := resolveView(v, out)
			if resolved == nil {
				continue
			}
			prev, seen := out[v.name]
			if seen && sameSource(prev, resolved) {
				continue
			}
			out[v.name] = resolved
			changed = true
		}
	}
	return out
}

func sameSource(a, b *Source) bool {
	if a.Carries != b.Carries || a.Anchors != b.Anchors || len(a.Content) != len(b.Content) {
		return false
	}
	for k, v := range a.Content {
		if b.Content[k] != v {
			return false
		}
	}
	return true
}

// view is one CREATE VIEW body, cut into its select list and its
// relation bindings.
type view struct {
	name string
	// body is the statement the view is defined as, kept so a check can
	// ask what shape it is in.
	body string
	// items are the top-level select-list entries, normalised.
	items []string
	// binds maps an alias onto the relation it refers to.
	binds map[string]string
}

var createViewPat = regexp.MustCompile(`(?i)create\s+(or\s+replace\s+)?(algorithm\s*=\s*\w+\s+)?view\s+` + "`?" + `(\w+)` + "`?" + `\s+as\s`)

// parseViews cuts every CREATE VIEW in the schema into its select list
// and the relations it reads from.
func parseViews(schema string) []view {
	var out []view
	locs := createViewPat.FindAllStringSubmatchIndex(schema, -1)
	for i, loc := range locs {
		name := schema[loc[6]:loc[7]]
		end := len(schema)
		if i+1 < len(locs) {
			end = locs[i+1][0]
		}
		// Comments go before the statement is cut at its terminator, not
		// after. A view's column comments are prose and prose contains
		// semicolons, and cutting first truncated the view at the first
		// one — leaving a source that exposed three columns and bound no
		// relations, which is a derivation that has stopped matching
		// while still returning something.
		body := commentTail.ReplaceAllString(schema[loc[1]:end], " ")
		if cut := strings.Index(body, ";"); cut >= 0 {
			body = body[:cut]
		}
		out = append(out, view{
			name:  strings.ToLower(name),
			body:  body,
			items: selectItems(topLevelSelectList(body)),
			binds: relationBindings(body),
		})
	}
	return out
}

// resolveView maps a view's select list onto what its inputs expose,
// returning nil while any input is still unknown.
func resolveView(v view, known map[string]*Source) *Source {
	// A view whose inputs include no known source exposes no task
	// content: there is nothing for it to inherit.
	inputs := map[string]*Source{}
	for alias, rel := range v.binds {
		if src, ok := known[rel]; ok {
			inputs[alias] = src
		}
	}
	if len(inputs) == 0 {
		return nil
	}

	out := &Source{Name: v.name, Content: map[string]string{}}
	anchors := map[string]string{}
	for _, item := range v.items {
		alias, column, exposed, ok := selectItemColumn(item)
		if !ok {
			continue
		}
		src, isSource := inputs[alias]
		if !isSource {
			continue
		}
		if column == "*" {
			for name, origin := range src.Content {
				out.Content[name] = origin
			}
			if src.Carries {
				anchors["visibility"] = src.Anchors.Visibility
				anchors["project_id"] = src.Anchors.ProjectID
				anchors["created_by_user_id"] = src.Anchors.CreatedBy
				anchors["id"] = src.Anchors.InternalID
			}
			continue
		}
		if origin, isContent := src.Content[column]; isContent {
			out.Content[exposed] = origin
		}
		if src.Carries {
			switch column {
			case src.Anchors.Visibility:
				anchors["visibility"] = exposed
			case src.Anchors.ProjectID:
				anchors["project_id"] = exposed
			case src.Anchors.CreatedBy:
				anchors["created_by_user_id"] = exposed
			case src.Anchors.InternalID:
				anchors["id"] = exposed
			}
		}
	}
	if len(out.Content) == 0 {
		return nil
	}
	complete := true
	for _, need := range anchorSources {
		if anchors[need] == "" {
			complete = false
		}
	}
	out.Carries = complete
	if complete {
		out.Anchors = AnchorColumns{
			Visibility: anchors["visibility"], ProjectID: anchors["project_id"],
			CreatedBy: anchors["created_by_user_id"], InternalID: anchors["id"],
		}
	}
	return out
}

// ----------------------------------------------------------------------
// Statements
// ----------------------------------------------------------------------

// Statement is one named statement in sql/queries.
type Statement struct {
	Name string
	Path string
	Line int
	// Header is the comment block between the `-- name:` line and the
	// first line of SQL, which is where a marker lives.
	Header string
	// Body is the statement as written.
	Body string
	// Normalized is [Normalize] applied to Body.
	Normalized string
	// Spliced are the normalised shared predicate fragments the statement
	// reaches through a function call rather than through its own text.
	//
	// A dynamic list query cannot contain the rule: its WHERE clause is
	// assembled at run time, so the predicate arrives from
	// acl.TaskVisibilityFilter. The fragment is still text, and it is
	// still held to being the canonical unit — so a statement carrying it
	// carries the rule, and calling that unreadable would report a gap
	// where the rule is the thing that is there.
	Spliced []string
}

// Location renders the statement's position for a failure message.
func (s Statement) Location() string { return fmt.Sprintf("%s:%d", s.Path, s.Line) }

// Marked reports whether the header carries a marker with a reason.
func (s Statement) Marked() bool { return markerPattern.MatchString(s.Header) }

// Unreadable reports whether part of the statement is not in the source.
//
// It matters where the missing part is the predicate: the projection is
// visible, so the statement is in scope, but whether the rule is applied
// cannot be decided from the text. Saying "no rule here" of a predicate
// nobody read would be a guess presented as a finding.
func (s Statement) Unreadable() bool {
	return strings.Contains(s.Normalized, UnreadableToken)
}

var headerPattern = regexp.MustCompile(`^--\s*name:\s*(\S+)\s+:(\S+)`)

// Statements cuts every query file at its sqlc `-- name:` headers.
func Statements(files map[string]string) []Statement {
	paths := make([]string, 0, len(files))
	for p := range files {
		paths = append(paths, p)
	}
	sort.Strings(paths)

	var out []Statement
	for _, path := range paths {
		out = append(out, parseQueryFile(path, files[path])...)
	}
	return out
}

func parseQueryFile(path, text string) []Statement {
	var out []Statement
	var current *Statement
	var header, body []string
	inHeader := false

	flush := func() {
		if current == nil {
			return
		}
		current.Header = strings.Join(header, "\n")
		current.Body = strings.Join(body, "\n")
		current.Normalized = Normalize(current.Body)
		out = append(out, *current)
		current, header, body, inHeader = nil, nil, nil, false
	}

	for i, line := range strings.Split(text, "\n") {
		if match := headerPattern.FindStringSubmatch(strings.TrimSpace(line)); match != nil {
			flush()
			current = &Statement{Name: match[1], Path: path, Line: i + 1}
			inHeader = true
			continue
		}
		if current == nil {
			continue
		}
		trimmed := strings.TrimSpace(line)
		if inHeader && (trimmed == "" || strings.HasPrefix(trimmed, "--")) {
			header = append(header, line)
			continue
		}
		inHeader = false
		body = append(body, line)
	}
	flush()
	return out
}

// ----------------------------------------------------------------------
// Scope and verdict
// ----------------------------------------------------------------------

// Exposure is one alias in a statement through which task content
// reaches the wire.
type Exposure struct {
	// Alias is what the statement calls the relation.
	Alias string
	// Source is the relation behind it.
	Source *Source
	// Columns are the exposed content column names the statement
	// projects, sorted, for the failure message.
	Columns []string
}

// Exposures returns the aliases a statement takes task content from,
// together with the projections it could not follow to a relation.
//
// A statement with neither is out of scope: it may join tasks, filter on
// them, count them — none of that puts their content on the wire.
//
// Content reached through a SELECT written into the statement — a derived
// table, a CTE — is attributed to the relation inside it under the alias
// that reads it, not to the wrapper. The wrapper exposes none of the
// rule's inputs, so an exposure reported against it would name a place the
// rule cannot be written; the alias inside is where it goes. The second
// return value is what makes the difference between "nothing here" and
// "this could not be read" visible: a projection this cannot follow is
// named rather than dropped, because dropping one is how a whole UNION
// over a derived table came to report no exposure at all.
func Exposures(s Statement, sources map[string]*Source) ([]Exposure, []string) {
	body := commentTail.ReplaceAllString(s.Body, " ")
	if !startsStatement(body) {
		return nil, nil
	}
	sc := newScope(sources)
	main := sc.takeCTEs(body, 0)
	binds := sc.bindingsIn(main, 0)
	list := topLevelSelectList(main)
	if list == "" {
		return nil, nil
	}

	sole := soleBinding(binds)
	byAlias := map[string]map[string]bool{}
	srcByAlias := map[string]*Source{}
	var opaque []string
	for _, item := range selectItems(list) {
		refs, followed := sc.itemRefs(item, binds, sole, 0)
		if !followed {
			opaque = append(opaque, strings.Join(strings.Fields(item), " "))
			continue
		}
		for _, ref := range refs {
			addColumn(byAlias, ref.alias, ref.column)
			srcByAlias[ref.alias] = ref.source
		}
	}

	aliases := make([]string, 0, len(byAlias))
	for a := range byAlias {
		aliases = append(aliases, a)
	}
	sort.Strings(aliases)

	out := make([]Exposure, 0, len(aliases))
	for _, alias := range aliases {
		cols := make([]string, 0, len(byAlias[alias]))
		for c := range byAlias[alias] {
			cols = append(cols, c)
		}
		sort.Strings(cols)
		out = append(out, Exposure{Alias: alias, Source: srcByAlias[alias], Columns: cols})
	}
	return out, opaque
}

// startsStatement reports whether the text begins a statement this reads:
// a SELECT, or the WITH clause in front of one.
func startsStatement(body string) bool {
	head := strings.ToLower(strings.TrimSpace(body))
	return strings.HasPrefix(head, "select") || strings.HasPrefix(head, "with")
}

func addColumn(m map[string]map[string]bool, alias, column string) {
	if m[alias] == nil {
		m[alias] = map[string]bool{}
	}
	m[alias][column] = true
}

// Guarded reports whether the statement carries the canonical unit for
// this exposure, in its own text or in a fragment spliced into it.
//
// The two are the same test on two pieces of text, deliberately: what
// reaches the database is the concatenation, and a reader on the other end
// of the response cannot tell which piece a predicate arrived in.
func Guarded(s Statement, e Exposure) bool {
	if !e.Source.Carries {
		return false
	}
	unit := Canonical(e.Alias, e.Source.Anchors)
	if strings.Contains(s.Normalized, unit) {
		return true
	}
	for _, fragment := range s.Spliced {
		if strings.Contains(fragment, unit) {
			return true
		}
	}
	return false
}

// AttemptsRule reports whether the statement writes this rule's private
// branch against the alias.
//
// It is the difference between a statement nobody applied the rule to and
// one where somebody applied a version of it. The second is the worse
// case — it looks handled — so the failure has to say which it is, and
// no marker excuses it.
//
// The private branch is the tell, not the public one. Restricting a
// projection to `visibility = 'public'` on its own is a different and
// stronger rule — the unauthenticated share pages have no actor to
// compare against — whereas anything naming the private branch is
// reaching for this rule and has to reach the whole of it.
func AttemptsRule(s Statement, e Exposure) bool {
	if !e.Source.Carries {
		return false
	}
	return strings.Contains(s.Normalized,
		Normalize(e.Alias+"."+e.Source.Anchors.Visibility+" = 'private'"))
}

// Finding is one thing the check has to say about a statement.
type Finding struct {
	Statement Statement
	Exposure  Exposure
	Kind      FindingKind
	// Detail carries what the finding is about when there is no exposure
	// to point at: the text of a projection this could not follow. Empty
	// for every other kind.
	Detail string
}

// FindingKind distinguishes the ways a statement can fail the rule.
type FindingKind int

const (
	// Unguarded is content on the wire with no visibility branch at all
	// and no marker.
	Unguarded FindingKind = iota
	// Divergent is content on the wire behind a visibility branch that
	// is not the canonical unit — the failure that lets one copy be
	// fixed while the others stay wrong.
	Divergent
	// NoAnchor is content on the wire from a relation that does not
	// expose the predicate's own inputs, so the rule cannot be written
	// against it at all.
	NoAnchor
	// Unreadable is content on the wire from a statement assembled at run
	// time, where the part of the text that would carry the rule is not
	// in the source. It is reported separately from Unguarded because it
	// is a different claim: not "this projection applies no rule" but
	// "nothing here can tell whether it does", and a check that quietly
	// called the second the first would be presenting a guess as a
	// finding.
	Unreadable
	// StaleMarker is a marker on a statement that projects no task
	// content, or that carries the predicate anyway.
	StaleMarker
	// Opaque is a projection this could not follow to the relation behind
	// it: a select-list entry wrapping a SELECT, or an unqualified content
	// column in a statement that reads more than one relation.
	//
	// It exists because the alternative is the failure this package was
	// blind to for as long as it had no name for it. A projection that
	// cannot be followed used to be dropped, and a dropped projection is
	// indistinguishable from a statement that projects nothing: the whole
	// statement then reports no exposure and passes. Naming it costs a
	// statement author a marker; not naming it costs a reader a title.
	Opaque
)

// Check holds every statement to the rule and returns what it found,
// together with the scope it covered.
//
// The scope is returned because the way a derived check fails is by
// deriving nothing — a renamed column, a view parser that stopped
// matching — and then passing because it looked at nothing. The caller
// asserts on both ends for that reason.
func Check(statements []Statement, sources map[string]*Source) (findings []Finding, inScope []Statement, guarded int) {
	for _, s := range statements {
		exposures, opaque := Exposures(s, sources)
		if len(exposures) == 0 && len(opaque) == 0 {
			if s.Marked() {
				findings = append(findings, Finding{Statement: s, Kind: StaleMarker})
			}
			continue
		}
		inScope = append(inScope, s)

		allGuarded := true
		for _, item := range opaque {
			// A projection nobody could follow is not guarded and is not
			// unguarded either. It is reported as itself, and a marker
			// answers it the way a marker answers any statement whose
			// disclosure is settled somewhere this cannot read.
			allGuarded = false
			if !s.Marked() {
				findings = append(findings, Finding{Statement: s, Kind: Opaque, Detail: item})
			}
		}
		for _, e := range exposures {
			switch {
			case Guarded(s, e):
				guarded++
			case !e.Source.Carries:
				allGuarded = false
				if !s.Marked() {
					findings = append(findings, Finding{Statement: s, Exposure: e, Kind: NoAnchor})
				}
			case AttemptsRule(s, e):
				// A divergent predicate is never excused by a marker:
				// the marker says the rule does not apply here, and
				// something in the statement is already applying it.
				allGuarded = false
				findings = append(findings, Finding{Statement: s, Exposure: e, Kind: Divergent})
			case s.Unreadable():
				allGuarded = false
				if !s.Marked() {
					findings = append(findings, Finding{Statement: s, Exposure: e, Kind: Unreadable})
				}
			default:
				allGuarded = false
				if !s.Marked() {
					findings = append(findings, Finding{Statement: s, Exposure: e, Kind: Unguarded})
				}
			}
		}
		if allGuarded && s.Marked() && len(exposures) > 0 {
			findings = append(findings, Finding{Statement: s, Exposure: exposures[0], Kind: StaleMarker})
		}
	}
	return findings, inScope, guarded
}

// Describe renders an exposure for a failure message.
func Describe(e Exposure) string {
	return fmt.Sprintf("%s.%s (from %s)", e.Alias, strings.Join(e.Columns, ", "+e.Alias+"."), e.Source.Name)
}

// ----------------------------------------------------------------------
// Subqueries
// ----------------------------------------------------------------------
//
// A derived table and a CTE are views written inside the statement, and a
// title projected through one reaches the reader exactly as a title
// projected from `tasks` does. They are resolved here the way
// [resolveView] resolves a view, with one difference that matters: the
// result records which alias inside the subquery the content came from,
// because the wrapper exposes none of the predicate's inputs and the rule
// would have to be written against the inner alias.
//
// Not everything can be followed. What cannot is said so — see [Opaque] —
// rather than left out, which is the whole difference between this and
// what was here before: `SELECT title, ... FROM ( ... UNION ... ) linked`
// found several relations, decided none of them was the one a bare column
// belonged to, dropped every column, and reported a statement projecting
// two task titles as projecting nothing at all.

// maxSubqueryDepth bounds how far this follows a statement into itself. A
// projection nested deeper is reported as [Opaque], never dropped.
const maxSubqueryDepth = 8

// columnRef is one content column of a relation, named by the alias that
// reads it in the statement it is read in.
type columnRef struct {
	alias  string
	source *Source
	column string
}

// binding is what an alias refers to: a relation from the schema, a
// subquery written into the statement, or something that is neither.
type binding struct {
	source *Source
	sub    *subquery
}

// subquery is a resolved SELECT written inside a statement.
type subquery struct {
	// named maps a column name the subquery exposes onto the content
	// behind it.
	named map[string][]columnRef
	// unnamed is content it exposes under no name a caller can write, so
	// only a `*` over the subquery reaches it.
	unnamed []columnRef
	// opaqueNames are the exposed names whose text could not be followed;
	// opaqueStar says the same of an entry that has no name of its own.
	opaqueNames map[string]bool
	opaqueStar  bool
}

// refs returns the content a column of the subquery carries, and whether
// that column could be followed at all.
func (q *subquery) refs(column string) ([]columnRef, bool) {
	if q == nil {
		return nil, true
	}
	if column == "*" {
		out := append([]columnRef{}, q.unnamed...)
		for _, refs := range q.named {
			out = append(out, refs...)
		}
		// A `*` reaches every entry, including the ones nobody could
		// follow, so it inherits their answer.
		return out, !q.opaqueStar && len(q.opaqueNames) == 0
	}
	if q.opaqueNames[column] {
		return nil, false
	}
	return q.named[column], true
}

// scope is what names mean inside one statement: the relations the schema
// defines, plus the CTEs the statement declares in front of itself.
type scope struct {
	sources map[string]*Source
	ctes    map[string]*subquery
}

func newScope(sources map[string]*Source) scope {
	return scope{sources: sources, ctes: map[string]*subquery{}}
}

var withPrefixPat = regexp.MustCompile(`(?is)^\s*with\s+(recursive\s+)?`)

// takeCTEs resolves the statement's WITH clause into the scope and
// returns the statement that follows it.
//
// The CTEs are resolved in the order they are written, so one that reads
// an earlier one is followed. A recursive CTE reading itself is not: at
// the point its own body is read it is not resolved yet, and the branch
// referring to it binds to nothing. The seed branch is where content
// enters such a CTE, and that branch is read.
func (sc scope) takeCTEs(sql string, depth int) string {
	loc := withPrefixPat.FindStringIndex(sql)
	if loc == nil {
		return sql
	}
	rest := sql[loc[1]:]
	for {
		name, after, ok := readIdentifier(rest)
		if !ok {
			return rest
		}
		after = strings.TrimLeft(after, " \t\r\n")
		// An optional column-name list stands between the name and AS.
		if strings.HasPrefix(after, "(") {
			end := matchParen(after, 0)
			if end < 0 {
				return rest
			}
			after = strings.TrimLeft(after[end+1:], " \t\r\n")
		}
		if !strings.HasPrefix(strings.ToLower(after), "as") {
			return rest
		}
		after = strings.TrimLeft(after[len("as"):], " \t\r\n")
		if !strings.HasPrefix(after, "(") {
			return rest
		}
		end := matchParen(after, 0)
		if end < 0 {
			return rest
		}
		sc.ctes[strings.ToLower(name)] = sc.resolve(after[1:end], depth+1)
		after = strings.TrimLeft(after[end+1:], " \t\r\n")
		if !strings.HasPrefix(after, ",") {
			return after
		}
		rest = after[1:]
	}
}

// resolve reads one subquery body — every branch of it — into the
// content its columns carry.
func (sc scope) resolve(body string, depth int) *subquery {
	out := &subquery{named: map[string][]columnRef{}, opaqueNames: map[string]bool{}}
	if depth > maxSubqueryDepth {
		out.opaqueStar = true
		return out
	}
	for _, branch := range unionBranches(body) {
		inner := scope{sources: sc.sources, ctes: sc.ctes}
		branch = inner.takeCTEs(branch, depth)
		binds := inner.bindingsIn(branch, depth)
		sole := soleBinding(binds)
		for _, item := range selectItems(topLevelSelectList(branch)) {
			refs, followed := inner.itemRefs(item, binds, sole, depth)
			name := itemOutputName(item)
			if !followed {
				if name == "" {
					out.opaqueStar = true
				} else {
					out.opaqueNames[name] = true
				}
				continue
			}
			if len(refs) == 0 {
				continue
			}
			if name == "" {
				out.unnamed = append(out.unnamed, refs...)
				continue
			}
			out.named[name] = append(out.named[name], refs...)
		}
	}
	return out
}

// bindingsIn maps every alias the statement binds at its own level onto
// what it refers to.
//
// Only that level is read. A relation named inside a subquery is that
// subquery's, and reading it here is what made a statement over a derived
// table look like a statement over three relations — with the effect that
// no unqualified column in it belonged to anything.
func (sc scope) bindingsIn(sql string, depth int) map[string]binding {
	out := map[string]binding{}
	text := sql
	for _, d := range derivedTables(sql) {
		out[d.alias] = binding{sub: sc.resolve(d.body, depth+1)}
		text = blankSpan(text, d.start, d.end)
	}
	for _, m := range relationPat.FindAllStringSubmatch(blankNested(text), -1) {
		rel := strings.ToLower(m[2])
		alias := strings.ToLower(m[5])
		if alias == "" || reservedAfterRelation[alias] {
			alias = rel
		}
		switch {
		case sc.ctes[rel] != nil:
			out[alias] = binding{sub: sc.ctes[rel]}
		case sc.sources[rel] != nil:
			out[alias] = binding{source: sc.sources[rel]}
		default:
			// Bound to something that exposes no task content. Recorded
			// anyway: it is what makes an unqualified column ambiguous.
			out[alias] = binding{}
		}
	}
	return out
}

// itemRefs returns the content one select-list entry puts on the wire,
// and whether the entry could be followed at all.
func (sc scope) itemRefs(item string, binds map[string]binding, sole string, depth int) ([]columnRef, bool) {
	if alias, column, _, ok := selectItemColumn(item); ok {
		return refsThrough(binds, alias, column)
	}
	if column, _, ok := bareSelectItemColumn(item); ok {
		// A bare column belongs to the one relation the statement reads.
		// Leaving it out entirely would put a projection outside the rule
		// for spelling `SELECT title FROM tasks` rather than
		// `SELECT t.title FROM tasks t`, which is a difference in typing
		// and not in what reaches the reader.
		if sole != "" {
			return refsThrough(binds, sole, column)
		}
		// More than one relation and no qualifier: which one it came from
		// is a guess, and a check about disclosure does not guess. It says
		// so instead, and only where the guess would matter — a name no
		// bound relation exposes as content discloses nothing whichever
		// one it came from.
		return nil, !namesContent(binds, column)
	}
	return sc.expressionRefs(item, binds, sole, depth)
}

// expressionRefs reads the content an expression puts on the wire.
//
// An expression is followed as far as the columns it names: every
// `alias.column` in it is resolved the way a plain projection would be,
// which is what lets `BIN_TO_UUID(t.public_id, 0)` be seen for what it is
// rather than assumed to carry a title.
//
// A statement written into the expression — a scalar subquery, an EXISTS —
// is resolved as a subquery, so `(SELECT t.title FROM tasks t WHERE ...)`
// is followed to the same place a plain projection of it would be. What
// is left over after both are read is an expression whose text names no
// relation this could resolve, and that is the case that says so.
func (sc scope) expressionRefs(item string, binds map[string]binding, sole string, depth int) ([]columnRef, bool) {
	var out []columnRef
	rest := item
	for _, nested := range nestedSelects(item) {
		refs, followed := sc.resolve(nested.body, depth+1).refs("*")
		if !followed {
			return nil, false
		}
		out = append(out, refs...)
		rest = blankSpan(rest, nested.start, nested.end)
	}
	if containsSelect(rest) {
		// A SELECT this could not cut out of the entry: the statement the
		// column comes from is not one this read.
		return nil, false
	}
	body := stripOutputAlias(stripLiterals(rest))
	for _, m := range qualifiedRefPat.FindAllStringSubmatch(body, -1) {
		refs, followed := refsThrough(binds, strings.ToLower(m[1]), strings.ToLower(m[2]))
		if !followed {
			return nil, false
		}
		out = append(out, refs...)
	}
	if len(out) > 0 || sole == "" {
		return out, true
	}
	// No qualifier anywhere and one relation to belong to: the bare names
	// in the expression are that relation's.
	src := binds[sole].source
	if src == nil {
		return nil, true
	}
	for _, word := range identifierPat.FindAllString(body, -1) {
		if _, isContent := src.Content[strings.ToLower(word)]; isContent {
			out = append(out, columnRef{alias: sole, source: src, column: strings.ToLower(word)})
		}
	}
	return out, true
}

// refsThrough resolves one `alias.column` against the statement's
// bindings.
func refsThrough(binds map[string]binding, alias, column string) ([]columnRef, bool) {
	bound, ok := binds[alias]
	if !ok {
		return nil, true
	}
	if bound.sub != nil {
		return bound.sub.refs(column)
	}
	if bound.source == nil {
		return nil, true
	}
	if column == "*" {
		out := make([]columnRef, 0, len(bound.source.Content))
		for name := range bound.source.Content {
			out = append(out, columnRef{alias: alias, source: bound.source, column: name})
		}
		return out, true
	}
	if _, isContent := bound.source.Content[column]; !isContent {
		return nil, true
	}
	return []columnRef{{alias: alias, source: bound.source, column: column}}, true
}

// namesContent reports whether any relation the statement reads exposes
// content under this column name.
func namesContent(binds map[string]binding, column string) bool {
	for _, bound := range binds {
		switch {
		case bound.source != nil:
			if _, ok := bound.source.Content[column]; ok {
				return true
			}
		case bound.sub != nil:
			if refs, _ := bound.sub.refs(column); len(refs) > 0 {
				return true
			}
		}
	}
	return false
}

// soleBinding returns the alias a statement reads when it reads exactly
// one relation, and the empty string otherwise.
//
// An unaliased relation binds under its own name, so a statement reading
// one table twice under two aliases has two bindings and no sole
// relation — which is the case where a bare column could belong to
// either.
func soleBinding(binds map[string]binding) string {
	if len(binds) != 1 {
		return ""
	}
	for alias := range binds {
		return alias
	}
	return ""
}

// derivedTable is one SELECT in a statement's FROM clause, bound to an
// alias. start is the FROM, JOIN or comma that introduces it and end is
// just past its alias, so the whole of it can be taken out of the text
// the plain relation scan then reads.
type derivedTable struct {
	alias      string
	body       string
	start, end int
}

// derivedTables finds the subqueries a statement's own FROM clause reads
// from, at that level only.
//
// The search starts at the top-level FROM so that a subquery in the
// select list or in a WHERE predicate is not mistaken for a relation the
// statement reads: those return values, not rows to join against.
func derivedTables(sql string) []derivedTable {
	lower := strings.ToLower(sql)
	from := topLevelFrom(lower)
	if from < 0 {
		return nil
	}
	var out []derivedTable
	depth := 0
	for i := from; i < len(sql); i++ {
		switch lower[i] {
		case '\'':
			i = skipString(lower, i)
		case '(':
			if keyword, opens := derivedTableStart(lower, i); depth == 0 && opens {
				end := matchParen(sql, i)
				if end < 0 {
					return out
				}
				alias, after := readTableAlias(sql, end+1)
				if alias != "" {
					out = append(out, derivedTable{
						alias: alias, body: sql[i+1 : end], start: keyword, end: after,
					})
				}
				i = end
				continue
			}
			depth++
		case ')':
			depth--
		}
	}
	return out
}

// derivedTableStart reports whether the parenthesis at `at` opens a
// relation the FROM clause reads rather than a grouped expression — what
// stands in front of it is FROM, JOIN, or the comma between two
// relations — and returns the offset of that word.
//
// The word is part of what the derived table occupies, because the plain
// relation scan runs over the same text afterwards: leaving a bare FROM
// behind made it read the next word as the relation, which for a
// statement ending `) linked ORDER BY ...` is the word ORDER. That second
// binding was enough to make every unqualified column in the statement
// ambiguous.
func derivedTableStart(lower string, at int) (int, bool) {
	head := strings.TrimRight(lower[:at], " \t\r\n")
	if strings.HasSuffix(head, ",") {
		return len(head) - 1, true
	}
	for _, word := range []string{"straight_join", "from", "join"} {
		if strings.HasSuffix(head, word) && isBoundary(head, len(head)-len(word), len(word)) {
			return len(head) - len(word), true
		}
	}
	return 0, false
}

// readTableAlias reads the name a derived table is bound to, with or
// without AS, and returns it with the offset just past it.
func readTableAlias(sql string, at int) (string, int) {
	rest := strings.TrimLeft(sql[at:], " \t\r\n")
	at = len(sql) - len(rest)
	if len(rest) >= 2 && strings.EqualFold(rest[:2], "as") && (len(rest) == 2 || !isIdentByte(rest[2])) {
		rest = strings.TrimLeft(rest[2:], " \t\r\n")
		at = len(sql) - len(rest)
	}
	name, after, ok := readIdentifier(rest)
	if !ok {
		return "", at
	}
	return strings.ToLower(name), at + (len(rest) - len(after))
}

// readIdentifier reads a leading identifier, backquoted or not, and
// returns it with the text that follows.
func readIdentifier(text string) (string, string, bool) {
	trimmed := strings.TrimLeft(text, " \t\r\n")
	if strings.HasPrefix(trimmed, "`") {
		if end := strings.Index(trimmed[1:], "`"); end >= 0 {
			return trimmed[1 : end+1], trimmed[end+2:], true
		}
		return "", text, false
	}
	i := 0
	for i < len(trimmed) && isIdentByte(trimmed[i]) {
		i++
	}
	if i == 0 {
		return "", text, false
	}
	return trimmed[:i], trimmed[i:], true
}

// unionBranches splits a subquery body on the UNIONs between its
// branches, ignoring any inside parentheses or string literals.
func unionBranches(body string) []string {
	lower := strings.ToLower(body)
	var out []string
	depth, start := 0, 0
	for i := 0; i < len(lower); i++ {
		switch {
		case lower[i] == '\'':
			i = skipString(lower, i)
		case lower[i] == '(':
			depth++
		case lower[i] == ')':
			depth--
		case depth == 0 && strings.HasPrefix(lower[i:], "union") && isBoundary(lower, i, len("union")):
			out = append(out, body[start:i])
			rest := strings.TrimLeft(lower[i+len("union"):], " \t\r\n")
			skip := len(lower[i+len("union"):]) - len(rest)
			if strings.HasPrefix(rest, "all") && isBoundary(rest, 0, len("all")) {
				skip += len("all")
			}
			i += len("union") + skip - 1
			start = i + 1
		}
	}
	return append(out, body[start:])
}

// nestedSelect is a parenthesised statement inside a select-list entry:
// a scalar subquery, the argument of an EXISTS, the right-hand side of an
// IN.
type nestedSelect struct {
	body       string
	start, end int
}

// nestedSelects finds the statements written into one select-list entry.
//
// A parenthesis is a statement when what follows it is SELECT; anything
// else — a function's arguments, a grouped expression — is descended into
// rather than taken whole, so a COALESCE around a scalar subquery yields
// the subquery and not the call. A subquery inside a subquery is left to
// the resolution of the one that contains it.
func nestedSelects(item string) []nestedSelect {
	lower := strings.ToLower(item)
	var out []nestedSelect
	for i := 0; i < len(item); i++ {
		switch lower[i] {
		case '\'':
			i = skipString(lower, i)
		case '(':
			end := matchParen(item, i)
			if end < 0 {
				return out
			}
			inner := item[i+1 : end]
			if strings.HasPrefix(strings.TrimSpace(strings.ToLower(inner)), "select") {
				out = append(out, nestedSelect{body: inner, start: i, end: end + 1})
				i = end
			}
		}
	}
	return out
}

// matchParen returns the offset of the parenthesis closing the one at
// open, or -1 when it is never closed.
func matchParen(sql string, open int) int {
	depth := 0
	for i := open; i < len(sql); i++ {
		switch sql[i] {
		case '\'':
			i = skipString(sql, i)
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

// topLevelFrom returns the offset of the FROM that ends a statement's
// select list, or -1.
func topLevelFrom(lower string) int {
	start := strings.Index(lower, "select")
	if start < 0 {
		return -1
	}
	depth := 0
	for i := start + len("select"); i < len(lower); i++ {
		switch {
		case lower[i] == '\'':
			i = skipString(lower, i)
		case lower[i] == '(':
			depth++
		case lower[i] == ')':
			depth--
		case depth == 0 && strings.HasPrefix(lower[i:], "from") && isBoundary(lower, i, len("from")):
			return i
		}
	}
	return -1
}

// blankSpan replaces a region with spaces, keeping every other offset
// where it was.
func blankSpan(sql string, start, end int) string {
	if start < 0 || end > len(sql) || start >= end {
		return sql
	}
	return sql[:start] + strings.Repeat(" ", end-start) + sql[end:]
}

// blankNested replaces everything inside parentheses with spaces, so a
// relation named in a subquery is not read as one this statement joins.
func blankNested(sql string) string {
	out := []byte(sql)
	depth := 0
	for i := 0; i < len(sql); i++ {
		switch sql[i] {
		case '\'':
			end := skipString(sql, i)
			if depth > 0 {
				for j := i; j <= end && j < len(out); j++ {
					out[j] = ' '
				}
			}
			i = end
			continue
		case '(':
			depth++
		case ')':
			depth--
		}
		if depth > 0 || (depth == 0 && sql[i] == ')') {
			out[i] = ' '
		}
	}
	return string(out)
}

var (
	qualifiedRefPat  = regexp.MustCompile("(?is)`?(\\w+)`?\\s*\\.\\s*`?(\\w+)`?")
	identifierPat    = regexp.MustCompile(`(?i)[a-z_][a-z0-9_]*`)
	selectWordPat    = regexp.MustCompile(`(?is)\bselect\b`)
	outputAliasPat   = regexp.MustCompile("(?is)\\s+as\\s+`?(\\w+)`?\\s*$")
	stringLiteralPat = regexp.MustCompile(`'(\\.|[^'\\])*'`)
)

// containsSelect reports whether a select-list entry wraps a statement of
// its own.
func containsSelect(item string) bool { return selectWordPat.MatchString(item) }

// stripLiterals blanks string literals so a word inside one is not read
// as a column name.
func stripLiterals(item string) string {
	return stringLiteralPat.ReplaceAllStringFunc(item, func(lit string) string {
		return strings.Repeat(" ", len(lit))
	})
}

// stripOutputAlias removes the `AS name` a select-list entry ends with,
// so the name it is exposed under is not read as a column it reads.
func stripOutputAlias(item string) string {
	return outputAliasPat.ReplaceAllString(item, "")
}

// itemOutputName returns the name a select-list entry is exposed under,
// or the empty string when it has none a caller could write.
func itemOutputName(item string) string {
	if _, _, exposed, ok := selectItemColumn(item); ok {
		return exposed
	}
	if _, exposed, ok := bareSelectItemColumn(item); ok {
		return exposed
	}
	if m := outputAliasPat.FindStringSubmatch(item); m != nil {
		return strings.ToLower(m[1])
	}
	return ""
}

// ----------------------------------------------------------------------
// SQL shredding
// ----------------------------------------------------------------------

var relationPat = regexp.MustCompile(`(?is)\b(from|join)\s+` + "`?" + `(\w+)` + "`?" + `((\s+as)?\s+` + "`?" + `(\w+)` + "`?" + `)?`)

// reservedAfterRelation are the words that follow a relation name
// without being an alias for it.
var reservedAfterRelation = map[string]bool{
	"on": true, "where": true, "inner": true, "left": true, "right": true,
	"cross": true, "join": true, "using": true, "group": true, "order": true,
	"limit": true, "set": true, "and": true, "or": true, "for": true,
	"union": true, "having": true, "straight_join": true, "as": true,
	"select": true, "natural": true, "lateral": true,
}

// relationBindings maps every alias in a statement onto the relation it
// names. An unaliased relation binds under its own name, which is how
// `FROM tasks WHERE tasks.title` resolves.
func relationBindings(sql string) map[string]string {
	out := map[string]string{}
	for _, m := range relationPat.FindAllStringSubmatch(sql, -1) {
		rel := strings.ToLower(m[2])
		alias := strings.ToLower(m[5])
		if alias == "" || reservedAfterRelation[alias] {
			alias = rel
		}
		out[alias] = rel
	}
	return out
}

// topLevelSelectList returns the text between a statement's leading
// SELECT and the FROM at the same nesting depth. A correlated subquery
// in the select list is inside parentheses, so its own FROM never ends
// the list.
func topLevelSelectList(sql string) string {
	lower := strings.ToLower(sql)
	start := strings.Index(lower, "select")
	if start < 0 {
		return ""
	}
	start += len("select")
	depth := 0
	for i := start; i < len(lower); i++ {
		switch {
		case lower[i] == '\'':
			i = skipString(lower, i)
		case lower[i] == '(':
			depth++
		case lower[i] == ')':
			depth--
		case depth == 0 && strings.HasPrefix(lower[i:], "from") && isBoundary(lower, i, len("from")):
			return sql[start:i]
		}
	}
	return sql[start:]
}

func skipString(text string, i int) int {
	for i++; i < len(text); i++ {
		if text[i] == '\\' {
			i++
			continue
		}
		if text[i] == '\'' {
			return i
		}
	}
	return i
}

func isBoundary(text string, at, length int) bool {
	before := at == 0 || !isIdentByte(text[at-1])
	after := at+length >= len(text) || !isIdentByte(text[at+length])
	return before && after
}

func isIdentByte(c byte) bool {
	return c == '_' || (c >= '0' && c <= '9') || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

// selectItems splits a select list on the commas between its entries,
// ignoring commas inside parentheses or string literals.
func selectItems(list string) []string {
	var out []string
	var current strings.Builder
	depth := 0
	for i := 0; i < len(list); i++ {
		switch {
		case list[i] == '\'':
			end := skipString(list, i)
			current.WriteString(list[i : end+1])
			i = end
		case list[i] == '(':
			depth++
			current.WriteByte(list[i])
		case list[i] == ')':
			depth--
			current.WriteByte(list[i])
		case list[i] == ',' && depth == 0:
			out = appendItem(out, current.String())
			current.Reset()
		default:
			current.WriteByte(list[i])
		}
	}
	return appendItem(out, current.String())
}

func appendItem(items []string, item string) []string {
	if trimmed := strings.TrimSpace(item); trimmed != "" {
		return append(items, trimmed)
	}
	return items
}

var plainColumnPat = regexp.MustCompile(`(?is)^` + "`?" + `(\w+)` + "`?" + `\s*\.\s*` + "`?" + `(\w+|\*)` + "`?" + `(\s+as\s+` + "`?" + `(\w+)` + "`?" + `)?\s*$`)

var bareColumnPat = regexp.MustCompile(`(?is)^` + "`?" + `(\w+|\*)` + "`?" + `(\s+as\s+` + "`?" + `(\w+)` + "`?" + `)?\s*$`)

// bareSelectItemColumn reads a select-list entry written without a
// qualifier — `title`, or `title AS name`.
func bareSelectItemColumn(item string) (column, exposed string, ok bool) {
	m := bareColumnPat.FindStringSubmatch(strings.TrimSpace(item))
	if m == nil {
		return "", "", false
	}
	column = strings.ToLower(m[1])
	exposed = column
	if m[3] != "" {
		exposed = strings.ToLower(m[3])
	}
	return column, exposed, true
}

// selectItemColumn reads a select-list entry of the form
// `alias.column [AS name]`, returning the alias, the source column and
// the name the row exposes it under.
//
// Anything else — an expression, a literal, a correlated subquery — is
// not a column projection and is reported as such. A view that wraps a
// title in a function is out of what this can follow, and would have to
// be spelled plainly to come into scope; a check that guessed at
// expressions would be guessing about the thing it is meant to prove.
func selectItemColumn(item string) (alias, column, exposed string, ok bool) {
	m := plainColumnPat.FindStringSubmatch(strings.TrimSpace(item))
	if m == nil {
		return "", "", "", false
	}
	alias = strings.ToLower(m[1])
	column = strings.ToLower(m[2])
	exposed = column
	if m[4] != "" {
		exposed = strings.ToLower(m[4])
	}
	return alias, column, exposed, true
}
