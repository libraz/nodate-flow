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
	// Anchors are kept per alias: a self-join (a task and its parent)
	// exposes two anchor sets, and mixing them builds a rule that checks
	// one row's visibility against another row's members.
	perAlias := map[string]map[string]string{}
	expose := func(alias, need, exposed string) {
		if perAlias[alias] == nil {
			perAlias[alias] = map[string]string{}
		}
		perAlias[alias][need] = exposed
	}
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
				expose(alias, "visibility", src.Anchors.Visibility)
				expose(alias, "project_id", src.Anchors.ProjectID)
				expose(alias, "created_by_user_id", src.Anchors.CreatedBy)
				expose(alias, "id", src.Anchors.InternalID)
			}
			continue
		}
		if origin, isContent := src.Content[column]; isContent {
			out.Content[exposed] = origin
		}
		if src.Carries {
			switch column {
			case src.Anchors.Visibility:
				expose(alias, "visibility", exposed)
			case src.Anchors.ProjectID:
				expose(alias, "project_id", exposed)
			case src.Anchors.CreatedBy:
				expose(alias, "created_by_user_id", exposed)
			case src.Anchors.InternalID:
				expose(alias, "id", exposed)
			}
		}
	}
	if len(out.Content) == 0 {
		return nil
	}
	var complete, ownNamed []string
	for alias, anchors := range perAlias {
		whole := true
		for _, need := range anchorSources {
			if anchors[need] == "" {
				whole = false
			}
		}
		if !whole {
			continue
		}
		complete = append(complete, alias)
		if anchors["visibility"] == inputs[alias].Anchors.Visibility {
			ownNamed = append(ownNamed, alias)
		}
	}
	// One complete set wins outright; among several, the one exposing
	// visibility under its source's own name is the row's. Anything else
	// is ambiguous and carries nothing, so the gate fails instead of
	// guessing.
	winner := ""
	switch {
	case len(complete) == 1:
		winner = complete[0]
	case len(ownNamed) == 1:
		winner = ownNamed[0]
	}
	out.Carries = winner != ""
	if out.Carries {
		a := perAlias[winner]
		out.Anchors = AnchorColumns{
			Visibility: a["visibility"], ProjectID: a["project_id"],
			CreatedBy: a["created_by_user_id"], InternalID: a["id"],
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
