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
//	sink        a statement in sql/queries that projects a content
//	            column from a source, through some alias.
//	held to     the statement contains the canonical unit, anchored on
//	            that same alias, for every alias it takes content from.
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
}

// Location renders the statement's position for a failure message.
func (s Statement) Location() string { return fmt.Sprintf("%s:%d", s.Path, s.Line) }

// Marked reports whether the header carries a marker with a reason.
func (s Statement) Marked() bool { return markerPattern.MatchString(s.Header) }

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

// Exposures returns the aliases a statement takes task content from.
// A statement with none is out of scope: it may join tasks, filter on
// them, count them — none of that puts their content on the wire.
func Exposures(s Statement, sources map[string]*Source) []Exposure {
	body := commentTail.ReplaceAllString(s.Body, " ")
	if !strings.HasPrefix(strings.ToLower(strings.TrimSpace(body)), "select") {
		return nil
	}
	binds := relationBindings(body)
	list := topLevelSelectList(body)
	if list == "" {
		return nil
	}

	byAlias := map[string]map[string]bool{}
	for _, item := range selectItems(list) {
		alias, column, _, ok := selectItemColumn(item)
		if !ok {
			continue
		}
		rel, bound := binds[alias]
		if !bound {
			continue
		}
		src, isSource := sources[rel]
		if !isSource {
			continue
		}
		if column == "*" {
			for name := range src.Content {
				addColumn(byAlias, alias, name)
			}
			continue
		}
		if _, isContent := src.Content[column]; isContent {
			addColumn(byAlias, alias, column)
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
		out = append(out, Exposure{Alias: alias, Source: sources[binds[alias]], Columns: cols})
	}
	return out
}

func addColumn(m map[string]map[string]bool, alias, column string) {
	if m[alias] == nil {
		m[alias] = map[string]bool{}
	}
	m[alias][column] = true
}

// Guarded reports whether the statement carries the canonical unit for
// this exposure.
func Guarded(s Statement, e Exposure) bool {
	if !e.Source.Carries {
		return false
	}
	return strings.Contains(s.Normalized, Canonical(e.Alias, e.Source.Anchors))
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
	// StaleMarker is a marker on a statement that projects no task
	// content, or that carries the predicate anyway.
	StaleMarker
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
		exposures := Exposures(s, sources)
		if len(exposures) == 0 {
			if s.Marked() {
				findings = append(findings, Finding{Statement: s, Kind: StaleMarker})
			}
			continue
		}
		inScope = append(inScope, s)

		allGuarded := true
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
			default:
				allGuarded = false
				if !s.Marked() {
					findings = append(findings, Finding{Statement: s, Exposure: e, Kind: Unguarded})
				}
			}
		}
		if allGuarded && s.Marked() {
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
