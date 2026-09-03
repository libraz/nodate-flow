package recurrence

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// A recurring event is stored once and drawn in several places: the
// calendar and the public share page expand it in the browser, the agent
// surface and the notification scheduler expand it here. Each of those
// answers the same question — which occurrences does this rule have —
// and they only agree because testdata/recurrence_golden.json says what
// the answer is.
//
// An expander that no fixture reaches is the failure this guards. It
// compiles, it returns plausible instants, and nothing reports that its
// instants are not the ones the calendar draws; the divergence surfaces
// as a meeting missing from an agent's schedule, which nobody attributes
// to an expander. So the expanders are derived from the source rather
// than listed, and each one has to be reachable from the fixture.
//
// The derivation looks for the freq dispatch, because that is the part an
// expander cannot be written without: stepping a series means naming
// every frequency the grammar defines and producing an instant from it.
// A function that names the frequencies without producing an instant is
// doing something else — validating a stored rule, say — and is not
// matched. A function that does both, in a directory whose tests never
// read the fixture, is reported.

// frequencies are the freq values the stored grammar defines. An expander
// has to name all of them; anything that names a subset is not stepping a
// series.
var frequencies = []string{"daily", "weekly", "monthly", "yearly"}

// expanderMarker exempts one freq dispatch from needing fixture coverage:
//
//	not-a-series-expander: <why this does not expand a series>
//
// The reason is mandatory and has to read as prose, so that a passing
// mention of the marker cannot act as one.
var expanderMarker = regexp.MustCompile(`not-a-series-expander:[ \t]*[A-Za-z][^\n]*[A-Za-z]`)

// goldenFixtureName is the fixture every expander has to be reachable
// from. Matching on the file name is what makes a test that loads it
// count, however it goes about locating it.
const goldenFixtureName = "recurrence_golden.json"

// expander is one derived expansion site.
type expander struct {
	// Dir is the directory whose tests have to reach the fixture.
	Dir string
	// Path is the file the dispatch was found in, relative to the root.
	Path string
	// Name identifies the dispatch inside that file.
	Name string
	// ReadsBySetPos records that the site acts on the bySetPos selector.
	ReadsBySetPos bool
}

// ---------------------------------------------------------------------
// Go detection
// ---------------------------------------------------------------------

// namesEveryFrequency reports whether a function body names all four
// frequencies, either as the lowercase tokens the column stores or as the
// Freq constants that stand for them.
func namesEveryFrequency(fn *ast.FuncDecl) bool {
	seen := map[string]bool{}
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		switch v := n.(type) {
		case *ast.BasicLit:
			if v.Kind == token.STRING {
				if text, err := strconv.Unquote(v.Value); err == nil {
					seen[strings.ToLower(text)] = true
				}
			}
		case *ast.Ident:
			if suffix, ok := strings.CutPrefix(v.Name, "Freq"); ok {
				seen[strings.ToLower(suffix)] = true
			}
		}
		return true
	})
	for _, freq := range frequencies {
		if !seen[freq] {
			return false
		}
	}
	return true
}

// producesInstant reports whether a function yields a point in time: it
// returns one, or it builds one. Both spellings are accepted because a
// dispatch that assembles the occurrence inline never mentions time.Time
// in its signature.
func producesInstant(fn *ast.FuncDecl) bool {
	if fn.Type.Results != nil {
		for _, result := range fn.Type.Results.List {
			if mentionsTimeTime(result.Type) {
				return true
			}
		}
	}
	built := false
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		sel, ok := n.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		if sel.Sel.Name == "AddDate" {
			built = true
			return false
		}
		if pkg, ok := sel.X.(*ast.Ident); ok && pkg.Name == "time" && sel.Sel.Name == "Date" {
			built = true
			return false
		}
		return true
	})
	return built
}

func mentionsTimeTime(expr ast.Expr) bool {
	found := false
	ast.Inspect(expr, func(n ast.Node) bool {
		sel, ok := n.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		if pkg, ok := sel.X.(*ast.Ident); ok && pkg.Name == "time" && sel.Sel.Name == "Time" {
			found = true
		}
		return true
	})
	return found
}

// readsBySetPos reports whether a function acts on the bySetPos selector,
// as opposed to a rule type merely declaring the field. Struct types are
// skipped for exactly that reason: declaring the field is how an
// implementation accepts a stored rule that carries it, and is not the
// same as applying it.
func readsBySetPos(fn *ast.FuncDecl) bool {
	found := false
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		if _, ok := n.(*ast.StructType); ok {
			return false
		}
		if ident, ok := n.(*ast.Ident); ok && strings.EqualFold(ident.Name, "bySetPos") {
			found = true
		}
		return true
	})
	return found
}

// goExpanders returns the dispatch sites in one parsed Go file, skipping
// the ones that carry a marker.
func goExpanders(fset *token.FileSet, file *ast.File, root, path string) []expander {
	var out []expander
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		if !namesEveryFrequency(fn) || !producesInstant(fn) {
			continue
		}
		if markedInGo(file, fn) {
			continue
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			rel = path
		}
		out = append(out, expander{
			Dir:           filepath.Dir(path),
			Path:          rel,
			Name:          fn.Name.Name + " (" + fset.Position(fn.Pos()).String() + ")",
			ReadsBySetPos: readsBySetPos(fn),
		})
	}
	return out
}

// markedInGo reports whether a marker sits anywhere from the function's
// doc comment down to its closing brace.
func markedInGo(file *ast.File, fn *ast.FuncDecl) bool {
	start := fn.Pos()
	if fn.Doc != nil {
		start = fn.Doc.Pos()
	}
	for _, group := range file.Comments {
		for _, comment := range group.List {
			if comment.Pos() >= start && comment.End() <= fn.End() &&
				expanderMarker.MatchString(comment.Text) {
				return true
			}
		}
	}
	return false
}

// ---------------------------------------------------------------------
// TypeScript detection
// ---------------------------------------------------------------------

// tsCommentLine matches a line that is entirely comment, which is how a
// mention of a field is told apart from a use of it.
var tsCommentLine = regexp.MustCompile(`^\s*(//|/\*|\*)`)

// stripTSComments drops whole-line comments so prose about the grammar
// does not read as code acting on it.
func stripTSComments(src string) string {
	var kept []string
	for _, line := range strings.Split(src, "\n") {
		if tsCommentLine.MatchString(line) {
			continue
		}
		kept = append(kept, line)
	}
	return strings.Join(kept, "\n")
}

// calendarUnits are the units a series advances by, one per frequency.
// An expander steps by all four, because it has to step by whichever the
// rule names.
var calendarUnits = []string{"days", "weeks", "months", "years"}

// stepsByEveryCalendarUnit reports whether the source advances a date by
// each of those units, as luxon spells it: plus({ <unit>: n }). Luxon is
// how this codebase does calendar arithmetic in the browser, so an
// expander written here has that shape.
//
// Merely constructing dates is not enough to match, and deliberately so:
// month grids, day keys and range nudges all build Date values without
// expanding anything, and a check they trip is a check people learn to
// silence.
func stepsByEveryCalendarUnit(code string) bool {
	stepped := map[string]bool{}
	for offset := 0; ; {
		at := strings.Index(code[offset:], ".plus(")
		if at < 0 {
			break
		}
		start := offset + at + len(".plus(")
		end := strings.Index(code[start:], ")")
		if end < 0 {
			break
		}
		for _, unit := range calendarUnits {
			if strings.Contains(code[start:start+end], unit) {
				stepped[unit] = true
			}
		}
		offset = start + end
	}
	for _, unit := range calendarUnits {
		if !stepped[unit] {
			return false
		}
	}
	return true
}

// tsExpander reports whether a TypeScript source steps a series: it names
// every frequency and advances a date by every frequency's unit. A module
// that only declares the frequencies — the shared rule type, a preset
// picker, a narrowing guard — does the first and not the second.
func tsExpander(src string) bool {
	code := stripTSComments(src)
	for _, freq := range frequencies {
		if !strings.Contains(code, "'"+freq+"'") && !strings.Contains(code, `"`+freq+`"`) {
			return false
		}
	}
	return stepsByEveryCalendarUnit(code)
}

func tsReadsBySetPos(src string) bool {
	return strings.Contains(strings.ToLower(stripTSComments(src)), "bysetpos")
}

// ---------------------------------------------------------------------
// Tree walk
// ---------------------------------------------------------------------

// skippedDirs are the trees that hold no source of ours: dependencies,
// build output, and the fixture directory itself.
var skippedDirs = map[string]bool{
	"node_modules": true,
	"dist":         true,
	"build":        true,
	"coverage":     true,
	".git":         true,
	"testdata":     true,
}

// searchedRoots are the trees that hold product code. Everything an
// expander could live in is under one of them.
var searchedRoots = []string{"apps", "packages"}

func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate the test source file")
	}
	dir := filepath.Dir(file)
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.work")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.work not found above the package directory")
		}
		dir = parent
	}
}

// findExpanders walks the product trees and returns every dispatch site,
// ordered by path so a failure lists them the same way twice.
func findExpanders(t *testing.T, root string) []expander {
	t.Helper()
	fset := token.NewFileSet()
	var out []expander

	for _, top := range searchedRoots {
		err := filepath.WalkDir(filepath.Join(root, top), func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() {
				if skippedDirs[entry.Name()] {
					return fs.SkipDir
				}
				return nil
			}
			name := entry.Name()
			switch {
			case strings.HasSuffix(name, ".go"):
				if strings.HasSuffix(name, "_test.go") {
					return nil
				}
				file, parseErr := parser.ParseFile(fset, path, nil, parser.ParseComments)
				if parseErr != nil {
					return parseErr
				}
				out = append(out, goExpanders(fset, file, root, path)...)
			case strings.HasSuffix(name, ".ts"), strings.HasSuffix(name, ".tsx"):
				if strings.Contains(name, ".test.") || strings.Contains(name, ".d.ts") {
					return nil
				}
				raw, readErr := os.ReadFile(path) //#nosec G304 -- repository path
				if readErr != nil {
					return readErr
				}
				src := string(raw)
				if !tsExpander(src) || expanderMarker.MatchString(src) {
					return nil
				}
				rel, relErr := filepath.Rel(root, path)
				if relErr != nil {
					rel = path
				}
				out = append(out, expander{
					Dir:           filepath.Dir(path),
					Path:          rel,
					Name:          rel,
					ReadsBySetPos: tsReadsBySetPos(src),
				})
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", top, err)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out
}

// loadsGoldenFixture reports whether one test source names the fixture in
// code rather than in prose.
//
// The distinction is the whole value of the check: a test that mentions
// the fixture in a comment — to say where the arithmetic is pinned, say —
// reads as coverage to a plain text search while asserting nothing. The
// Go half asks the parser for string literals; the TypeScript half drops
// whole-line comments and looks for the quoted name.
func loadsGoldenFixture(path string, raw []byte) (bool, error) {
	if strings.HasSuffix(path, "_test.go") {
		file, err := parser.ParseFile(token.NewFileSet(), path, raw, 0)
		if err != nil {
			return false, err
		}
		found := false
		ast.Inspect(file, func(n ast.Node) bool {
			lit, ok := n.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			if text, err := strconv.Unquote(lit.Value); err == nil &&
				strings.Contains(text, goldenFixtureName) {
				found = true
			}
			return true
		})
		return found, nil
	}
	code := stripTSComments(string(raw))
	return strings.Contains(code, "'"+goldenFixtureName+"'") ||
		strings.Contains(code, `"`+goldenFixtureName+`"`), nil
}

// reachesGoldenFixture reports whether a test beside the expander loads
// the shared fixture. Both the directory itself and a __tests__ beside it
// count, because that is where each language puts its tests.
func reachesGoldenFixture(dir string) (bool, error) {
	for _, candidate := range []string{dir, filepath.Join(dir, "__tests__")} {
		entries, err := os.ReadDir(candidate)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			name := entry.Name()
			if !strings.HasSuffix(name, "_test.go") && !strings.Contains(name, ".test.") {
				continue
			}
			path := filepath.Join(candidate, name)
			raw, err := os.ReadFile(path) //#nosec G304 -- repository path
			if err != nil {
				return false, err
			}
			loads, err := loadsGoldenFixture(path, raw)
			if err != nil {
				return false, err
			}
			if loads {
				return true, nil
			}
		}
	}
	return false, nil
}

// ---------------------------------------------------------------------
// The checks
// ---------------------------------------------------------------------

// TestEveryExpanderReadsTheGoldenFixture fails on an expansion site whose
// tests never load testdata/recurrence_golden.json.
func TestEveryExpanderReadsTheGoldenFixture(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	found := findExpanders(t, root)

	// A derivation that quietly stops matching passes for the wrong
	// reason. Both languages expand series today, so finding none in
	// either means the detector has stopped working rather than the
	// expanders having gone away.
	var goSites, tsSites int
	for _, site := range found {
		if strings.HasSuffix(site.Path, ".go") {
			goSites++
			continue
		}
		tsSites++
	}
	if goSites == 0 {
		t.Fatal("no Go expansion site was derived from the tree; the detector has stopped matching")
	}
	if tsSites == 0 {
		t.Fatal("no TypeScript expansion site was derived from the tree; the detector has stopped matching")
	}

	for _, site := range found {
		reaches, err := reachesGoldenFixture(site.Dir)
		if err != nil {
			t.Fatalf("read tests beside %s: %v", site.Path, err)
		}
		if reaches {
			continue
		}
		t.Errorf("%s: this steps a recurrence rule into occurrences, and no test beside it loads "+
			"testdata/%s. An expander no fixture reaches still compiles and still returns "+
			"plausible instants, so a divergence from the calendar surfaces as a missing meeting "+
			"rather than as a failure. Point a test at the shared fixture, or say why this is not "+
			"a series expander with the marker not-a-series-expander followed by the reason",
			site.Name, goldenFixtureName)
	}
}

// TestNoExpanderAppliesBySetPosAlone keeps the selector's unimplemented
// state shared.
//
// bySetPos is the "nth matching day of the period" selector — what
// expresses "second Monday of the month". The stored grammar lists it and
// no expander applies it, which is a limit rather than a defect as long
// as every expander has the same limit. Implemented in one of them, the
// same stored rule would mean two different series depending on which
// surface answered, and the surface nobody watches would be the one that
// disagreed.
func TestNoExpanderAppliesBySetPosAlone(t *testing.T) {
	t.Parallel()

	root := repoRoot(t)
	var applying []string
	for _, site := range findExpanders(t, root) {
		if site.ReadsBySetPos {
			applying = append(applying, site.Name)
		}
	}
	if len(applying) == 0 {
		return
	}
	t.Errorf("these expansion sites act on bySetPos: %s. The selector is either applied by every "+
		"expander or by none — one stored rule cannot mean two series depending on which surface "+
		"was asked. Teach the others first, then extend testdata/%s so the shared meaning is "+
		"written down", strings.Join(applying, ", "), goldenFixtureName)
}

// TestExpanderDetectionSeesAFourthExpander is the positive control: it
// proves the derivation reports what it is meant to report, rather than
// passing because it matched nothing.
//
// It also pins the three rules that make the derivation worth anything —
// naming the frequencies is not enough without producing an instant,
// producing an instant is not enough without naming them, and a marker
// with no reason is not a marker.
func TestExpanderDetectionSeesAFourthExpander(t *testing.T) {
	t.Parallel()

	const src = `package p

// stepsASeries is an expander: it names every frequency and returns an
// instant.
func stepsASeries(anchor time.Time, freq string, n int) time.Time {
	switch freq {
	case "daily":
		return anchor.AddDate(0, 0, n)
	case "weekly":
		return anchor.AddDate(0, 0, 7*n)
	case "monthly":
		return anchor.AddDate(0, n, 0)
	case "yearly":
		return anchor.AddDate(n, 0, 0)
	}
	return anchor
}

// buildsInline names the frequencies and assembles the instant itself,
// never mentioning time.Time in its signature.
func buildsInline(y int, m time.Month, d int, freq string) any {
	if freq == "daily" || freq == "weekly" || freq == "monthly" || freq == "yearly" {
		return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
	}
	return nil
}

// validates names every frequency and produces no instant, so it is not
// an expander and needs no marker.
func validates(freq string) bool {
	switch freq {
	case "daily", "weekly", "monthly", "yearly":
		return true
	}
	return false
}

// steps produces an instant without naming the frequencies.
func steps(anchor time.Time, n int) time.Time {
	return anchor.AddDate(0, 0, n)
}

// exempt is an expander that says why it is not one.
//
// not-a-series-expander: this one is accounted for.
func exempt(anchor time.Time, freq string, n int) time.Time {
	switch freq {
	case "daily", "weekly", "monthly", "yearly":
		return anchor.AddDate(0, 0, n)
	}
	return anchor
}

// reasonless carries a marker with nothing after it.
//
// not-a-series-expander:
func reasonless(anchor time.Time, freq string, n int) time.Time {
	switch freq {
	case "daily", "weekly", "monthly", "yearly":
		return anchor.AddDate(0, 0, n)
	}
	return anchor
}
`

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "sample.go", src, parser.ParseComments)
	if err != nil {
		t.Fatalf("parse sample: %v", err)
	}

	var got []string
	for _, site := range goExpanders(fset, file, "", "sample.go") {
		got = append(got, strings.Split(site.Name, " ")[0])
	}
	want := []string{"stepsASeries", "buildsInline", "reasonless"}
	if len(got) != len(want) {
		t.Fatalf("derivation reported %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("site %d is %s, want %s", i, got[i], want[i])
		}
	}
}

// TestBySetPosDetectionSeesAnApplication is the positive control for the
// selector check: declaring the field is not applying it, and acting on
// it is.
func TestBySetPosDetectionSeesAnApplication(t *testing.T) {
	t.Parallel()

	const src = `package p

func declares(freq string) time.Time {
	type rule struct {
		BySetPos []int
	}
	switch freq {
	case "daily", "weekly", "monthly", "yearly":
		return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	}
	return time.Time{}
}

func applies(r *Rule, freq string) time.Time {
	if len(r.BySetPos) > 0 {
		return time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
	}
	switch freq {
	case "daily", "weekly", "monthly", "yearly":
		return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	}
	return time.Time{}
}
`

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "sample.go", src, parser.ParseComments)
	if err != nil {
		t.Fatalf("parse sample: %v", err)
	}

	sites := goExpanders(fset, file, "", "sample.go")
	if len(sites) != 2 {
		t.Fatalf("derivation reported %d sites, want 2", len(sites))
	}
	if sites[0].ReadsBySetPos {
		t.Error("a struct field declaring bySetPos was read as applying it")
	}
	if !sites[1].ReadsBySetPos {
		t.Error("a function acting on bySetPos was not reported as applying it")
	}
}

// TestTypeScriptDetectionSeparatesExpandersFromDeclarations is the
// positive control for the TypeScript half, which reads text rather than
// a syntax tree and so has to be shown to tell the two apart.
func TestTypeScriptDetectionSeparatesExpandersFromDeclarations(t *testing.T) {
	t.Parallel()

	const declaration = `export interface RecurrenceRule {
  freq: 'daily' | 'weekly' | 'monthly' | 'yearly';
  interval?: number;
}
`
	// A preset picker names every frequency and builds dates for its own
	// grid, then hands the rule to the shared expander. Matching it would
	// report a component that expands nothing.
	const picker = `function presetToRule(preset: Preset): RecurrenceRule {
  switch (preset) {
    case 'daily':
      return { freq: 'daily' };
    case 'weekly':
      return { freq: 'weekly' };
    case 'monthly':
      return { freq: 'monthly' };
    case 'yearly':
      return { freq: 'yearly' };
  }
}

function gridEnd(rangeEnd: Date): DateTime {
  return DateTime.fromJSDate(rangeEnd).plus({ milliseconds: 1 });
}

function dayKey(sec: number): string {
  return new Date(sec * 1000).toISOString();
}
`
	const expansion = `function occurrenceFromAnchor(anchor: DateTime, freq: Freq, offset: number): DateTime {
  switch (freq) {
    case 'daily':
      return anchor.plus({ days: offset });
    case 'weekly':
      return anchor.plus({ weeks: offset });
    case 'monthly':
      return anchor.plus({ months: offset });
    case 'yearly':
      return anchor.plus({ years: offset });
  }
}
`
	const mentionsInProse = `// bySetPos is decoded and never applied.
function occurrenceFromAnchor(anchor: DateTime, freq: Freq, offset: number): DateTime {
  switch (freq) {
    case 'daily':
    case 'weekly':
    case 'monthly':
    case 'yearly':
      return anchor.plus({ days: offset });
  }
}
`

	if tsExpander(declaration) {
		t.Error("a module that only declares the frequencies was read as an expander")
	}
	if tsExpander(picker) {
		t.Error("a preset picker that builds dates for its own grid was read as an expander")
	}
	if !tsExpander(expansion) {
		t.Error("a module that steps a series was not read as an expander")
	}
	if tsReadsBySetPos(mentionsInProse) {
		t.Error("a comment about bySetPos was read as an application of it")
	}
	if !tsReadsBySetPos("const nth = rule.bySetPos[0];") {
		t.Error("code reading bySetPos was not reported")
	}
}

// TestFixtureCoverageNeedsALoadNotAMention is the positive control for
// the coverage side of the derivation. A test that only names the fixture
// in prose asserts nothing about expansion, and counting it as coverage
// is how an unchecked expander passes for a checked one.
func TestFixtureCoverageNeedsALoadNotAMention(t *testing.T) {
	t.Parallel()

	mention := []byte(`package p

// The arithmetic is pinned in testdata/recurrence_golden.json, which this
// package does not read.
func TestSomethingElse(t *testing.T) {}
`)
	loads, err := loadsGoldenFixture("mention_test.go", mention)
	if err != nil {
		t.Fatalf("parse mention: %v", err)
	}
	if loads {
		t.Error("a comment naming the fixture was counted as loading it")
	}

	load := []byte(`package p

func TestGolden(t *testing.T) {
	raw, _ := os.ReadFile(filepath.Join(dir, "testdata", "recurrence_golden.json"))
	_ = raw
}
`)
	loads, err = loadsGoldenFixture("load_test.go", load)
	if err != nil {
		t.Fatalf("parse load: %v", err)
	}
	if !loads {
		t.Error("a test reading the fixture was not counted as loading it")
	}

	if ok, _ := loadsGoldenFixture("mention.test.ts", []byte(
		"// see testdata/recurrence_golden.json\nconst x = 1;\n")); ok {
		t.Error("a TypeScript comment naming the fixture was counted as loading it")
	}
	if ok, _ := loadsGoldenFixture("load.test.ts", []byte(
		"const p = path.join(dir, 'testdata', 'recurrence_golden.json');\n")); !ok {
		t.Error("a TypeScript test reading the fixture was not counted as loading it")
	}
}
