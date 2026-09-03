// Command gen-errors generates Go and TypeScript error code modules,
// i18n locale stubs, and per-code Markdown docs from errors/*.yaml.
//
// errors/*.yaml is the single source of truth. Re-running this command
// must produce byte-identical output (deterministic ordering).
//
//go:build ignore

package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// ----- YAML schema -----

type errorEntry struct {
	Code        string `yaml:"code"`
	Status      int    `yaml:"status"`
	Message     string `yaml:"message"`
	MessageJA   string `yaml:"message_ja"`
	MessageZH   string `yaml:"message_zh"`
	Description string `yaml:"description"`
	UserAction  string `yaml:"userAction"`
	I18nKey     string `yaml:"i18nKey"`
}

type resource struct {
	Breadcrumb string                `yaml:"breadcrumb"`
	Errors     map[string]errorEntry `yaml:"errors"`
}

type domainFile struct {
	Domain     string              `yaml:"domain"`
	Breadcrumb string              `yaml:"breadcrumb"`
	Resources  map[string]resource `yaml:"resources"`
}

// ----- flat record used by generators -----

type record struct {
	Domain      string
	File        string // YAML basename without extension
	Resource    string // resource key
	Key         string // error key
	Code        string
	Status      int
	Message     string
	MessageJA   string
	MessageZH   string
	Description string
	UserAction  string
	I18nKey     string // optional i18next key; empty when YAML omits i18nKey
	Breadcrumb  string // resource breadcrumb (fallback to domain)
}

// Abstract reasons forbidden as the final segment.
var abstractReasons = map[string]bool{
	"INVALID": true,
	"ERROR":   true,
	"FAILED":  true,
	"BAD":     true,
	"PROBLEM": true,
}

var codeRe = regexp.MustCompile(`^[A-Z][A-Z0-9_]*(\.[A-Z][A-Z0-9_]*){1,2}$`)

func main() {
	// --check answers "is what is committed what this generator would
	// produce today?" without writing anything, so it is safe to run from
	// verify-codegen and from a pre-commit hook.
	//
	// It compares content rather than hashing the YAML, because a source
	// edited without a regeneration is only one of the ways the catalog
	// goes wrong. A change in the generator, a hand-edit of its output and
	// a regeneration that produced the wrong thing all leave the sources
	// untouched, and a source hash calls every one of them fresh.
	// Comparing the files themselves catches all four alike.
	//
	// Same contract as scripts/gen-signal-kinds.go --check: rebuild every
	// output in memory, compare against what is committed, write nothing,
	// exit 1 on any disagreement.
	if len(os.Args) > 1 && os.Args[1] == "--check" {
		if err := run(true); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Fprintln(os.Stdout, "gen-errors: generated files match errors/*.yaml")
		return
	}
	if err := run(false); err != nil {
		fmt.Fprintln(os.Stderr, "gen-errors:", err)
		os.Exit(1)
	}
	fmt.Fprintln(os.Stdout, "gen-errors: ok")
}

// run builds every output from the YAML catalog. With check set it
// compares the results against what is committed and writes nothing.
func run(check bool) error {
	root, err := repoRoot()
	if err != nil {
		return err
	}
	errorsDir := filepath.Join(root, "errors")
	matches, err := filepath.Glob(filepath.Join(errorsDir, "*.yaml"))
	if err != nil {
		return err
	}
	if len(matches) == 0 {
		return fmt.Errorf("no YAML files found in %s", errorsDir)
	}
	sort.Strings(matches)

	byFile := map[string][]record{}
	var fileNames []string
	seen := map[string]string{}

	for _, path := range matches {
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		var df domainFile
		if err := yaml.Unmarshal(raw, &df); err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		base := strings.TrimSuffix(filepath.Base(path), ".yaml")
		if df.Domain == "" {
			return fmt.Errorf("%s: missing 'domain'", path)
		}
		var recs []record
		for resKey, res := range df.Resources {
			for errKey, e := range res.Errors {
				if err := validateEntry(path, e); err != nil {
					return err
				}
				if prev, ok := seen[e.Code]; ok {
					return fmt.Errorf("duplicate code %q in %s and %s", e.Code, prev, path)
				}
				seen[e.Code] = path
				bc := res.Breadcrumb
				if bc == "" {
					bc = df.Breadcrumb
				}
				if e.MessageJA == "" {
					fmt.Fprintf(os.Stderr, "gen-errors: warning: %s: %s missing 'message_ja'\n", path, e.Code)
				}
				recs = append(recs, record{
					Domain:      df.Domain,
					File:        base,
					Resource:    resKey,
					Key:         errKey,
					Code:        e.Code,
					Status:      e.Status,
					Message:     e.Message,
					MessageJA:   e.MessageJA,
					MessageZH:   e.MessageZH,
					Description: e.Description,
					UserAction:  e.UserAction,
					I18nKey:     e.I18nKey,
					Breadcrumb:  bc,
				})
			}
		}
		sort.Slice(recs, func(i, j int) bool { return recs[i].Code < recs[j].Code })
		byFile[base] = recs
		fileNames = append(fileNames, base)
	}
	sort.Strings(fileNames)

	// All records sorted by code, used for aggregated outputs.
	var all []record
	for _, name := range fileNames {
		all = append(all, byFile[name]...)
	}
	sort.Slice(all, func(i, j int) bool { return all[i].Code < all[j].Code })

	// The outputs are assembled first and emitted second so that --check
	// and a real run answer from the same values.
	outputs := map[string][]byte{}

	// 1) Go per-domain files for all app directories that have an
	// internal/errors/ directory. flow-api receives all domains; auth-api
	// receives only updates for domains it already tracks. The runtime
	// helper (Spec/APIError/New/Newf/Wrap) lives in
	// packages/go-shared/apierr and is not generated here.
	goTargets := []struct {
		dir     string
		allDoms bool // true = write all domains; false = update existing only
	}{
		{filepath.Join(root, "apps", "flow-api", "internal", "errors"), true},
		{filepath.Join(root, "apps", "auth-api", "internal", "errors"), false},
	}
	for _, tgt := range goTargets {
		if _, err := os.Stat(tgt.dir); os.IsNotExist(err) {
			continue
		}
		for _, name := range fileNames {
			outPath := filepath.Join(tgt.dir, fileBase(name)+".go")
			if !tgt.allDoms {
				// Only update files that already exist in this app.
				if _, err := os.Stat(outPath); os.IsNotExist(err) {
					continue
				}
			}
			outputs[outPath] = genGoFile(byFile[name])
		}
	}

	// 2) TS per-domain files + barrel.
	tsDir := filepath.Join(root, "packages", "sdk", "src", "errors")
	for _, name := range fileNames {
		outputs[filepath.Join(tsDir, name+".ts")] = genTsFile(name, byFile[name])
	}
	outputs[filepath.Join(tsDir, "index.ts")] = genTsBarrel(fileNames)

	// 3) Locale bundles — every web app that has a locales/ directory.
	localeApps := []string{"apps/flow-web", "apps/accounts-web"}
	for _, app := range localeApps {
		// Skip apps whose locales directory doesn't exist yet.
		if _, err := os.Stat(filepath.Join(root, app, "locales", "en")); os.IsNotExist(err) {
			continue
		}
		for _, lang := range []string{"en", "ja", "zh"} {
			outputs[filepath.Join(root, app, "locales", lang, "errors.json")] = genLocale(all, lang)
		}
	}

	// An i18nKey has to resolve, or it reaches a reader as itself.
	//
	// The field is an override: when set, the frontend uses it instead of
	// the generated `errors:<CODE>` entry, which the loop above writes for
	// every code in all three locales of both apps. That makes a dead
	// override strictly worse than no override — the reader gets the raw
	// key string in a toast while a translated message for the same code
	// sits in the same bundle. Six of seven were in that state.
	//
	// Checked here rather than in a separate script because this is the
	// step that writes the catalog the override displaces: a reference
	// that resolves to nothing should not be able to reach a build.
	// 4) Per-code Markdown docs. A code removed from the YAML used to
	// leave its page behind, and a stale page reads exactly like a live
	// one — same shape, same path, no hint that the code it documents no
	// longer exists anywhere in the product. So the pages that are no
	// longer produced are part of the answer too: deleted on a real run,
	// reported as drift by --check.
	docsRoot := filepath.Join(root, "docs", "errors")
	for _, name := range fileNames {
		for _, r := range byFile[name] {
			outputs[filepath.Join(docsRoot, name, r.Code+".md")] = genDoc(r)
		}
	}
	obsolete, err := obsoleteDocs(docsRoot, outputs)
	if err != nil {
		return err
	}

	if check {
		if err := compareOutputs(root, outputs, obsolete); err != nil {
			return err
		}
		return checkI18nKeys(root, all, localeApps)
	}

	paths := make([]string, 0, len(outputs))
	for path := range outputs {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	for _, path := range paths {
		if err := writeFile(path, outputs[path]); err != nil {
			return err
		}
	}
	for _, path := range obsolete {
		if err := os.Remove(path); err != nil {
			return err
		}
		fmt.Printf("gen-errors: removed stale doc %s\n", path)
	}

	// A domain whose YAML file is gone leaves a whole directory of pages
	// that nothing generates any more. The directory goes only when the
	// prune emptied it, so a hand-written file keeps its folder alive.
	live := make(map[string]bool, len(fileNames))
	for _, name := range fileNames {
		live[name] = true
	}
	domains, err := os.ReadDir(docsRoot)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	for _, d := range domains {
		if !d.IsDir() || live[d.Name()] {
			continue
		}
		dir := filepath.Join(docsRoot, d.Name())
		if entries, err := os.ReadDir(dir); err == nil && len(entries) == 0 {
			if err := os.Remove(dir); err != nil {
				return err
			}
		}
	}

	return checkI18nKeys(root, all, localeApps)
}

// obsoleteDocs lists the generated error pages under docsRoot that the
// current catalog no longer produces.
//
// Only files whose stem is a valid error code are considered, so anything
// hand-written alongside them (a README, an index) is left alone: this
// generator owns the `<CODE>.md` namespace and nothing else.
func obsoleteDocs(docsRoot string, outputs map[string][]byte) ([]string, error) {
	domains, err := os.ReadDir(docsRoot)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var stale []string
	for _, d := range domains {
		if !d.IsDir() {
			continue
		}
		dir := filepath.Join(docsRoot, d.Name())
		entries, err := os.ReadDir(dir)
		if err != nil {
			return nil, err
		}
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			stem, ok := strings.CutSuffix(e.Name(), ".md")
			if !ok || !codeRe.MatchString(stem) {
				continue
			}
			path := filepath.Join(dir, e.Name())
			if _, produced := outputs[path]; !produced {
				stale = append(stale, path)
			}
		}
	}
	sort.Strings(stale)
	return stale, nil
}

// compareOutputs reports the committed files that differ from what the
// generator produces now, and the generated files it no longer produces.
// Nothing is written.
//
// Kept identical to the function of the same name in
// scripts/gen-signal-kinds.go: the two generators answer the drift
// question the same way, in the same message shape, with the same exit
// status, so neither can quietly become the weaker one.
func compareOutputs(root string, outputs map[string][]byte, obsolete []string) error {
	paths := make([]string, 0, len(outputs))
	for path := range outputs {
		paths = append(paths, path)
	}
	sort.Strings(paths)

	var stale []string
	for _, path := range paths {
		committed, err := os.ReadFile(path)
		switch {
		case os.IsNotExist(err):
			stale = append(stale, relPath(root, path)+"\n    the generator produces this file, but it is not committed")
		case err != nil:
			return fmt.Errorf("gen-errors: cannot read %s: %w", relPath(root, path), err)
		case !bytes.Equal(committed, outputs[path]):
			stale = append(stale, relPath(root, path)+"\n"+firstDifference(committed, outputs[path]))
		}
	}
	for _, path := range obsolete {
		stale = append(stale, relPath(root, path)+"\n    no longer produced by the generator; it is stale")
	}
	if len(stale) > 0 {
		return fmt.Errorf("gen-errors: these files are not what errors/*.yaml generates:\n\n  %s\n\n"+
			"Run 'make gen-errors' and commit the result. Editing a generated file by hand does not "+
			"survive the next run — put the change in errors/*.yaml instead",
			strings.Join(stale, "\n\n  "))
	}
	return nil
}

// relPath renders a path relative to the repository root, falling back to
// the absolute path when it is outside.
func relPath(root, path string) string {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return path
	}
	return rel
}

// firstDifference names the line where the committed file and the freshly
// generated one diverge, so a failure points at a place rather than only
// at a file.
func firstDifference(committed, generated []byte) string {
	oldLines := strings.Split(string(committed), "\n")
	newLines := strings.Split(string(generated), "\n")
	n := len(oldLines)
	if len(newLines) > n {
		n = len(newLines)
	}
	for i := range n {
		var oldLine, newLine string
		if i < len(oldLines) {
			oldLine = oldLines[i]
		}
		if i < len(newLines) {
			newLine = newLines[i]
		}
		if oldLine == newLine {
			continue
		}
		return fmt.Sprintf("    first difference at line %d\n      committed:   %s\n      regenerated: %s",
			i+1, elide(oldLine), elide(newLine))
	}
	return "    content differs"
}

// elide shortens a line so one long generated declaration cannot bury the
// rest of the report.
func elide(line string) string {
	const max = 100
	runes := []rune(line)
	if len(runes) <= max {
		return line
	}
	return string(runes[:max]) + "…"
}

func validateEntry(path string, e errorEntry) error {
	if e.Code == "" {
		return fmt.Errorf("%s: entry missing 'code'", path)
	}
	if e.Message == "" {
		return fmt.Errorf("%s: %s missing 'message'", path, e.Code)
	}
	if e.Description == "" {
		return fmt.Errorf("%s: %s missing 'description'", path, e.Code)
	}
	if e.Status < 400 || e.Status > 599 {
		return fmt.Errorf("%s: %s has invalid status %d", path, e.Code, e.Status)
	}
	if !codeRe.MatchString(e.Code) {
		return fmt.Errorf("%s: %q is not a valid code (DOMAIN.RESOURCE.REASON)", path, e.Code)
	}
	parts := strings.Split(e.Code, ".")
	last := parts[len(parts)-1]
	if abstractReasons[last] {
		return fmt.Errorf("%s: %q ends in abstract reason %q", path, e.Code, last)
	}
	return nil
}

func repoRoot() (string, error) {
	wd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	dir := wd
	for {
		if _, err := os.Stat(filepath.Join(dir, "errors")); err == nil {
			if _, err := os.Stat(filepath.Join(dir, "scripts", "gen-errors.go")); err == nil {
				return dir, nil
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("could not locate repository root from %s", wd)
		}
		dir = parent
	}
}

// writeFile writes content only if it differs from existing, but always creates
// it on first run. The diff check is just a friendly no-op when unchanged.
func writeFile(path string, content []byte) error {
	if existing, err := os.ReadFile(path); err == nil && bytes.Equal(existing, content) {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, content, 0o644)
}

// fileBase normalises a YAML basename to a Go-friendly file name
// (e.g. "integration-gh" -> "integration_gh").
func fileBase(name string) string {
	return strings.ReplaceAll(name, "-", "_")
}

// goConst converts a code like "AUTH.LOGIN.INVALID_CREDENTIALS" to a Go
// identifier "AuthLoginInvalidCredentials".
func goConst(code string) string {
	parts := strings.Split(code, ".")
	var b strings.Builder
	for _, p := range parts {
		for _, sub := range strings.Split(p, "_") {
			if sub == "" {
				continue
			}
			b.WriteString(strings.ToUpper(sub[:1]))
			b.WriteString(strings.ToLower(sub[1:]))
		}
	}
	return b.String()
}

// tsConst converts a code to a JS identifier safe key for the const object.
// We keep the original dotted code as the map key (quoted), but also produce
// SCREAMING_SNAKE constant names.
func tsConst(code string) string {
	return strings.ReplaceAll(code, ".", "_")
}

// ----- Go per-domain file -----

func genGoFile(recs []record) []byte {
	var b bytes.Buffer
	b.WriteString("// Code generated by gen-errors. DO NOT EDIT.\n\n")
	b.WriteString("package errors\n\n")
	b.WriteString("// Error codes and specs.\n")
	b.WriteString("var (\n")
	for _, r := range recs {
		fmt.Fprintf(&b, "\t// %s — %s\n", r.Code, r.Message)
		// I18nKey is an optional sibling field on Spec. Emit it only when
		// the YAML entry sets `i18nKey`, so the rest of the catalog keeps
		// its existing wire layout byte-for-byte.
		if r.I18nKey != "" {
			fmt.Fprintf(&b, "\t%s = &Spec{Code: %q, Status: %d, Message: %q, Description: %q, UserAction: %q, I18nKey: %q}\n",
				goConst(r.Code), r.Code, r.Status, r.Message, r.Description, r.UserAction, r.I18nKey)
		} else {
			fmt.Fprintf(&b, "\t%s = &Spec{Code: %q, Status: %d, Message: %q, Description: %q, UserAction: %q}\n",
				goConst(r.Code), r.Code, r.Status, r.Message, r.Description, r.UserAction)
		}
	}
	b.WriteString(")\n")
	return b.Bytes()
}

// ----- TS per-domain file -----

func genTsFile(domainFile string, recs []record) []byte {
	var b bytes.Buffer
	b.WriteString("// Code generated by gen-errors. DO NOT EDIT.\n\n")
	fmt.Fprintf(&b, "export const %sErrors = {\n", tsExportName(domainFile))
	for _, r := range recs {
		fmt.Fprintf(&b, "  %s: {\n", tsConst(r.Code))
		fmt.Fprintf(&b, "    code: %q,\n", r.Code)
		fmt.Fprintf(&b, "    status: %d,\n", r.Status)
		fmt.Fprintf(&b, "    message: %q,\n", r.Message)
		// i18nKey is optional in the YAML schema. Emit it only when the
		// entry sets one, so unrelated TS modules don't gain a noisy
		// undefined / empty member.
		if r.I18nKey != "" {
			fmt.Fprintf(&b, "    i18nKey: %q,\n", r.I18nKey)
		}
		b.WriteString("  },\n")
	}
	b.WriteString("} as const;\n\n")
	fmt.Fprintf(&b, "export type %sErrorCode = (typeof %sErrors)[keyof typeof %sErrors][\"code\"];\n",
		tsExportName(domainFile), tsExportName(domainFile), tsExportName(domainFile))
	return b.Bytes()
}

func tsExportName(domainFile string) string {
	parts := strings.Split(domainFile, "-")
	var b strings.Builder
	for _, p := range parts {
		if p == "" {
			continue
		}
		b.WriteString(strings.ToUpper(p[:1]))
		b.WriteString(p[1:])
	}
	return b.String()
}

func genTsBarrel(names []string) []byte {
	var b bytes.Buffer
	b.WriteString("// Code generated by gen-errors. DO NOT EDIT.\n\n")
	for _, n := range names {
		fmt.Fprintf(&b, "export * from \"./%s.js\";\n", n)
	}
	return b.Bytes()
}

// ----- locales -----

// genLocale emits the errors.json file for a specific language.
//
// For "en" we always use record.Message. For "ja" we use record.MessageJA
// when set; otherwise we emit an empty string so the i18n lint in
// scripts/i18n-translate.mjs (--check) fails loudly rather than silently
// falling back to English copy inside the JA locale.
//
// For "zh" we use record.MessageZH when set; otherwise we fall back to
// the English message. The zh catalog is being filled in incrementally,
// and empty strings would trip the i18n empty-value lint, so until every
// entry has a Simplified Chinese translation we ship the English text as
// a deterministic placeholder instead of "" or "[TODO]".
func genLocale(all []record, lang string) []byte {
	var b bytes.Buffer
	b.WriteString("{\n")
	for i, r := range all {
		var val string
		switch lang {
		case "ja":
			val = r.MessageJA
		case "zh":
			if r.MessageZH != "" {
				val = r.MessageZH
			} else {
				val = r.Message
			}
		default:
			val = r.Message
		}
		fmt.Fprintf(&b, "  %q: %q", r.Code, val)
		if i < len(all)-1 {
			b.WriteString(",")
		}
		b.WriteString("\n")
	}
	b.WriteString("}\n")
	return b.Bytes()
}

// ----- docs -----

func genDoc(r record) []byte {
	var b bytes.Buffer
	fmt.Fprintf(&b, "<!-- Code generated by gen-errors. DO NOT EDIT. -->\n\n")
	fmt.Fprintf(&b, "# %s\n\n", r.Code)
	fmt.Fprintf(&b, "- **Status**: %d\n", r.Status)
	fmt.Fprintf(&b, "- **Breadcrumb**: %s\n", r.Breadcrumb)
	// i18n key is rendered as an additional bullet (in the same metadata
	// block as Status / Breadcrumb) only when the YAML provides one, so
	// existing docs are untouched until they opt in.
	if r.I18nKey != "" {
		fmt.Fprintf(&b, "- **i18n key**: `%s`\n", r.I18nKey)
	}
	b.WriteString("\n")
	fmt.Fprintf(&b, "## Message\n\n%s\n\n", r.Message)
	fmt.Fprintf(&b, "## Description\n\n%s\n\n", r.Description)
	if r.UserAction != "" {
		fmt.Fprintf(&b, "## User action\n\n%s\n", r.UserAction)
	}
	return b.Bytes()
}

// checkI18nKeys fails when a record's optional i18nKey does not resolve
// to a string in every locale of at least one app.
//
// "At least one app" rather than "every app" because the namespaces are
// not shared: accounts-web ships auth.json and flow-web does not, so a
// key under `auth.` can only ever resolve on one side. Requiring all
// three locales of the app that does have it is what stops a key that
// exists in English and nowhere else.
func checkI18nKeys(root string, all []record, localeApps []string) error {
	var bad []string
	for _, r := range all {
		if r.I18nKey == "" {
			continue
		}
		ns, path, ok := strings.Cut(r.I18nKey, ".")
		if !ok {
			bad = append(bad, fmt.Sprintf("%s: i18nKey %q has no namespace", r.Code, r.I18nKey))
			continue
		}
		resolved := false
		for _, app := range localeApps {
			inAllLangs := true
			for _, lang := range []string{"en", "ja", "zh"} {
				if !localeKeyExists(filepath.Join(root, app, "locales", lang, ns+".json"), path) {
					inAllLangs = false
					break
				}
			}
			if inAllLangs {
				resolved = true
				break
			}
		}
		if !resolved {
			bad = append(bad, fmt.Sprintf("%s: i18nKey %q resolves in no app's en+ja+zh", r.Code, r.I18nKey))
		}
	}
	if len(bad) == 0 {
		return nil
	}
	sort.Strings(bad)
	return fmt.Errorf("gen-errors: %d unresolvable i18nKey reference(s):\n  %s\n\nEither add the key to that namespace in all three locales, or drop i18nKey and let the generated errors:<CODE> entry carry the message",
		len(bad), strings.Join(bad, "\n  "))
}

// localeKeyExists reports whether a dotted path resolves to a string in
// the given locale JSON file.
func localeKeyExists(file, dotted string) bool {
	raw, err := os.ReadFile(file)
	if err != nil {
		return false
	}
	var doc any
	if err := json.Unmarshal(raw, &doc); err != nil {
		return false
	}
	cur := doc
	for _, seg := range strings.Split(dotted, ".") {
		m, ok := cur.(map[string]any)
		if !ok {
			return false
		}
		cur, ok = m[seg]
		if !ok {
			return false
		}
	}
	str, ok := cur.(string)
	return ok && strings.TrimSpace(str) != ""
}
