// Command gen-signal-kinds generates Go and TypeScript signal-kind
// modules, i18n locale stubs, and per-source Markdown docs from
// signal_kinds/*.yaml.
//
// signal_kinds/*.yaml is the single source of truth. Re-running this
// command must produce byte-identical output (deterministic ordering).
//
// The output layout mirrors scripts/gen-errors.go:
//   - apps/flow-api/internal/signalkinds/signalkinds.go (typed Go consts + table)
//   - packages/sdk/src/signal-kinds/index.ts             (TS consts + lookup helper)
//   - apps/flow-web/locales/{en,ja,zh}/signal-kinds.json (i18n stubs)
//   - docs/signal-kinds/<source>.md                      (per-source catalogue)
//
//go:build ignore

package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// ----- YAML schema -----

type kindEntry struct {
	Kind               string `yaml:"kind"`
	Source             string `yaml:"source"`
	Retention          string `yaml:"retention"`
	AutonomyDefault    string `yaml:"autonomy_default"`
	SubjectTypeDefault string `yaml:"subject_type_default"`
	Description        string `yaml:"description"`
	DescriptionJA      string `yaml:"description_ja"`
	DescriptionZH      string `yaml:"description_zh"`
	// Label is the display name shown in the autonomy matrix. Optional:
	// when empty it is derived from the kind, which is what every entry
	// relied on before the field existed.
	Label   string `yaml:"label"`
	LabelJA string `yaml:"label_ja"`
	LabelZH string `yaml:"label_zh"`
	I18nKey string `yaml:"i18n_key"`
}

type kindFile struct {
	Kinds []kindEntry `yaml:"kinds"`
}

// ----- flat record used by generators -----

type record struct {
	File               string // YAML basename without extension
	Kind               string
	Source             string
	Retention          string
	AutonomyDefault    string
	SubjectTypeDefault string
	Description        string
	DescriptionJA      string
	DescriptionZH      string
	Label              string
	LabelJA            string
	LabelZH            string
	I18nKey            string
}

// Allowed enum values. Mirrors ADR 0008 D1/D2.
var (
	validRetention = map[string]bool{
		"stateful": true,
		"discrete": true,
	}
	validAutonomy = map[string]bool{
		"suggest": true,
		"draft":   true,
		"auto":    true,
	}
	validSubjectType = map[string]bool{
		"user":           true,
		"task":           true,
		"workspace":      true,
		"calendar_event": true,
	}
	// validSource is the canonical signals.source wire enum, mirrored
	// from packages/go-shared/signalwire (the single source of truth).
	// Every registry `source` must be a member or gen-signal-kinds
	// fails — drift between the registry and the wire/DB enum is caught
	// at codegen time, not at runtime as a 422. Keep in sync with
	// signalwire.Sources() and sql/flow/tables/signals.sql.
	validSource = map[string]bool{
		"manual":   true,
		"github":   true,
		"slack":    true,
		"email":    true,
		"google":   true,
		"webhook":  true,
		"calendar": true,
		"discord":  true,
	}
)

// Kind identifier rule: dotted lowercase segments, each starting with a
// letter, segments separated by '.'. Examples: "manual", "discord.presence",
// "calendar.event_day_arrived".
var kindRe = regexp.MustCompile(`^[a-z][a-z0-9_]*(\.[a-z][a-z0-9_]*)*$`)

func main() {
	// --check answers "is what is committed what this generator would
	// produce today?" without writing anything, so it is safe to run
	// from verify-codegen and from a pre-commit hook.
	//
	// It compares content rather than hashing the YAML, because the
	// failure this guards against is not only an un-regenerated source
	// edit. The generator once ignored the language it was writing for
	// and published the English copy into ja and zh; a source stamp
	// would have called that fresh. Comparing the files themselves
	// catches a change in the generator, a hand-edit of its output, and
	// a stale regeneration alike.
	if len(os.Args) > 1 && os.Args[1] == "--check" {
		if err := run(true); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Fprintln(os.Stdout, "gen-signal-kinds: generated files match signal_kinds/*.yaml")
		return
	}
	if err := run(false); err != nil {
		fmt.Fprintln(os.Stderr, "gen-signal-kinds:", err)
		os.Exit(1)
	}
	fmt.Fprintln(os.Stdout, "gen-signal-kinds: ok")
}

// run builds every output from the YAML registry. With check set it
// compares the results against what is committed and writes nothing.
func run(check bool) error {
	root, err := repoRoot()
	if err != nil {
		return err
	}
	srcDir := filepath.Join(root, "signal_kinds")
	matches, err := filepath.Glob(filepath.Join(srcDir, "*.yaml"))
	if err != nil {
		return err
	}
	if len(matches) == 0 {
		return fmt.Errorf("no YAML files found in %s", srcDir)
	}
	sort.Strings(matches)

	byFile := map[string][]record{}
	bySource := map[string][]record{}
	var fileNames []string
	var sourceNames []string
	seen := map[string]string{} // kind -> path

	for _, path := range matches {
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		var kf kindFile
		if err := yaml.Unmarshal(raw, &kf); err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
		base := strings.TrimSuffix(filepath.Base(path), ".yaml")
		if len(kf.Kinds) == 0 {
			return fmt.Errorf("%s: no 'kinds' entries", path)
		}
		var recs []record
		for _, e := range kf.Kinds {
			if err := validateEntry(path, e); err != nil {
				return err
			}
			if prev, ok := seen[e.Kind]; ok {
				return fmt.Errorf("duplicate kind %q in %s and %s", e.Kind, prev, path)
			}
			seen[e.Kind] = path
			label := e.Label
			if label == "" {
				label = defaultLabel(e.Kind)
			}
			r := record{
				File:               base,
				Kind:               e.Kind,
				Source:             e.Source,
				Retention:          e.Retention,
				AutonomyDefault:    e.AutonomyDefault,
				SubjectTypeDefault: e.SubjectTypeDefault,
				Description:        e.Description,
				DescriptionJA:      e.DescriptionJA,
				DescriptionZH:      e.DescriptionZH,
				Label:              label,
				LabelJA:            e.LabelJA,
				LabelZH:            e.LabelZH,
				I18nKey:            e.I18nKey,
			}
			recs = append(recs, r)
			if _, ok := bySource[e.Source]; !ok {
				sourceNames = append(sourceNames, e.Source)
			}
			bySource[e.Source] = append(bySource[e.Source], r)
		}
		sort.Slice(recs, func(i, j int) bool { return recs[i].Kind < recs[j].Kind })
		byFile[base] = recs
		fileNames = append(fileNames, base)
	}
	sort.Strings(fileNames)
	sort.Strings(sourceNames)
	for s := range bySource {
		recs := bySource[s]
		sort.Slice(recs, func(i, j int) bool { return recs[i].Kind < recs[j].Kind })
		bySource[s] = recs
	}

	// Aggregated, sorted by kind, for outputs that need a single list.
	var all []record
	for _, name := range fileNames {
		all = append(all, byFile[name]...)
	}
	sort.Slice(all, func(i, j int) bool { return all[i].Kind < all[j].Kind })

	// The outputs are assembled first and emitted second so that --check
	// and a real run answer from the same values.
	outputs := map[string][]byte{}

	// 1) Go module — typed consts and lookup table.
	outputs[filepath.Join(root, "apps", "flow-api", "internal", "signalkinds", "signalkinds.go")] = genGoFile(all)

	// 2) TS module — SDK consts + lookup helper.
	outputs[filepath.Join(root, "packages", "sdk", "src", "signal-kinds", "index.ts")] = genTsFile(all)

	// 3) i18n bundles — flow-web only (accounts-web is identity-side and
	// has no signal surface).
	localeApp := filepath.Join(root, "apps", "flow-web", "locales")
	for _, lang := range []string{"en", "ja", "zh"} {
		dir := filepath.Join(localeApp, lang)
		if _, err := os.Stat(dir); os.IsNotExist(err) {
			continue
		}
		outputs[filepath.Join(dir, "signal-kinds.json")] = genLocale(all, lang)
	}

	// 4) Per-source Markdown docs.
	docsDir := filepath.Join(root, "docs", "signal-kinds")
	for _, src := range sourceNames {
		outputs[filepath.Join(docsDir, src+".md")] = genDoc(src, bySource[src])
	}

	if check {
		return compareOutputs(root, outputs)
	}
	if err := os.MkdirAll(docsDir, 0o755); err != nil {
		return err
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
	return nil
}

// compareOutputs reports the committed files that differ from what the
// generator produces now.
func compareOutputs(root string, outputs map[string][]byte) error {
	paths := make([]string, 0, len(outputs))
	for path := range outputs {
		paths = append(paths, path)
	}
	sort.Strings(paths)

	var stale []string
	for _, path := range paths {
		committed, err := os.ReadFile(path)
		rel, relErr := filepath.Rel(root, path)
		if relErr != nil {
			rel = path
		}
		switch {
		case os.IsNotExist(err):
			stale = append(stale, rel+" (not committed)")
		case err != nil:
			return fmt.Errorf("gen-signal-kinds: cannot read %s: %w", rel, err)
		case !bytes.Equal(committed, outputs[path]):
			stale = append(stale, rel)
		}
	}
	if len(stale) > 0 {
		return fmt.Errorf("gen-signal-kinds: these files are not what signal_kinds/*.yaml generates:\n  %s\n\n"+
			"Run 'make gen-signal-kinds' and commit the result. Editing a generated locale by hand does not "+
			"survive the next run — put the translation in signal_kinds/*.yaml instead",
			strings.Join(stale, "\n  "))
	}
	return nil
}

func validateEntry(path string, e kindEntry) error {
	if e.Kind == "" {
		return fmt.Errorf("%s: entry missing 'kind'", path)
	}
	if !kindRe.MatchString(e.Kind) {
		return fmt.Errorf("%s: %q is not a valid kind (dotted lowercase, e.g. \"discord.presence\")", path, e.Kind)
	}
	if e.Source == "" {
		return fmt.Errorf("%s: %s missing 'source'", path, e.Kind)
	}
	if !validSource[e.Source] {
		return fmt.Errorf("%s: %s has source %q not in the canonical wire enum "+
			"(packages/go-shared/signalwire); add it there + sql/flow/tables/signals.sql or fix the YAML", path, e.Kind, e.Source)
	}
	if !validRetention[e.Retention] {
		return fmt.Errorf("%s: %s has invalid retention %q (want stateful|discrete)", path, e.Kind, e.Retention)
	}
	if !validAutonomy[e.AutonomyDefault] {
		return fmt.Errorf("%s: %s has invalid autonomy_default %q (want suggest|draft|auto)", path, e.Kind, e.AutonomyDefault)
	}
	if !validSubjectType[e.SubjectTypeDefault] {
		return fmt.Errorf("%s: %s has invalid subject_type_default %q (want user|task|workspace|calendar_event)", path, e.Kind, e.SubjectTypeDefault)
	}
	if e.Description == "" {
		return fmt.Errorf("%s: %s missing 'description'", path, e.Kind)
	}
	if e.I18nKey == "" {
		return fmt.Errorf("%s: %s missing 'i18n_key'", path, e.Kind)
	}
	// Translations are required, not optional, because this generator
	// overwrites the locale files whole. A kind added without them used
	// to be emitted as English into every locale, and any translation
	// written by hand afterwards was destroyed by the next `make gen` —
	// the failure looked like a translator's mistake and had no trace
	// pointing here. Failing at the source is the only place the
	// omission is visible.
	//
	// The registry is a small closed enumeration (one entry per signal
	// the product can act on), so requiring both languages up front
	// costs a sentence per kind. This is deliberately stricter than the
	// error catalogue, which has hundreds of entries and lets zh land
	// incrementally.
	for _, missing := range []struct{ field, value string }{
		{"label_ja", e.LabelJA},
		{"label_zh", e.LabelZH},
		{"description_ja", e.DescriptionJA},
		{"description_zh", e.DescriptionZH},
	} {
		if missing.value == "" {
			return fmt.Errorf("%s: %s missing %q; every signal kind must ship its ja and zh copy "+
				"or the generator will publish English into those locales", path, e.Kind, missing.field)
		}
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
		if _, err := os.Stat(filepath.Join(dir, "signal_kinds")); err == nil {
			if _, err := os.Stat(filepath.Join(dir, "scripts", "gen-signal-kinds.go")); err == nil {
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

// writeFile writes content only if it differs from existing, but always
// creates it on first run.
func writeFile(path string, content []byte) error {
	if existing, err := os.ReadFile(path); err == nil && bytes.Equal(existing, content) {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, content, 0o644)
}

// goIdent converts a dotted kind like "discord.presence" or
// "calendar.event_day_arrived" into a Go identifier "DiscordPresence" /
// "CalendarEventDayArrived".
func goIdent(kind string) string {
	parts := strings.Split(kind, ".")
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

// ----- Go module -----

func genGoFile(all []record) []byte {
	var b bytes.Buffer
	b.WriteString("// Code generated by gen-signal-kinds. DO NOT EDIT.\n")
	b.WriteString("//\n")
	b.WriteString("// Source: signal_kinds/*.yaml\n")
	b.WriteString("// Regenerate with: go run scripts/gen-signal-kinds.go\n\n")
	b.WriteString("// Package signalkinds enumerates the closed set of signal kinds the\n")
	b.WriteString("// system observes and routes to the judge. Adding a new kind is a\n")
	b.WriteString("// YAML edit followed by a regenerate; it is not a code edit.\n")
	b.WriteString("package signalkinds\n\n")

	b.WriteString("import \"github.com/libraz/nodate-flow/packages/go-shared/signalwire\"\n\n")

	b.WriteString("// Kind is the dotted string identifier of a signal kind.\n")
	b.WriteString("type Kind string\n\n")

	b.WriteString("// String returns the underlying dotted identifier.\n")
	b.WriteString("func (k Kind) String() string { return string(k) }\n\n")

	b.WriteString("// Retention classifies how the retention sweep treats a kind:\n")
	b.WriteString("//   - stateful: latest-per-subject is meaningful, older rows can be\n")
	b.WriteString("//     pruned once superseded.\n")
	b.WriteString("//   - discrete: every row is an independent event, pruning is by\n")
	b.WriteString("//     expires_at only.\n")
	b.WriteString("type Retention string\n\n")
	b.WriteString("// Retention values.\n")
	b.WriteString("const (\n")
	b.WriteString("\tRetentionStateful Retention = \"stateful\"\n")
	b.WriteString("\tRetentionDiscrete Retention = \"discrete\"\n")
	b.WriteString(")\n\n")

	b.WriteString("// Autonomy is the default autonomy level applied when no\n")
	b.WriteString("// auto_action_rules row matches a (workspace, kind) pair.\n")
	b.WriteString("type Autonomy string\n\n")
	b.WriteString("// Autonomy values.\n")
	b.WriteString("const (\n")
	b.WriteString("\tAutonomySuggest Autonomy = \"suggest\"\n")
	b.WriteString("\tAutonomyDraft   Autonomy = \"draft\"\n")
	b.WriteString("\tAutonomyAuto    Autonomy = \"auto\"\n")
	b.WriteString(")\n\n")

	b.WriteString("// SubjectType names the table the signal's subject_id points at.\n")
	b.WriteString("type SubjectType string\n\n")
	b.WriteString("// SubjectType values.\n")
	b.WriteString("const (\n")
	b.WriteString("\tSubjectUser          SubjectType = \"user\"\n")
	b.WriteString("\tSubjectTask          SubjectType = \"task\"\n")
	b.WriteString("\tSubjectWorkspace     SubjectType = \"workspace\"\n")
	b.WriteString("\tSubjectCalendarEvent SubjectType = \"calendar_event\"\n")
	b.WriteString(")\n\n")

	b.WriteString("// Typed constants for every signal kind in the registry.\n")
	b.WriteString("const (\n")
	for _, r := range all {
		fmt.Fprintf(&b, "\t// %s — %s\n", goIdent(r.Kind), r.Description)
		fmt.Fprintf(&b, "\t%s Kind = %q\n", goIdent(r.Kind), r.Kind)
	}
	b.WriteString(")\n\n")

	b.WriteString("// Definition describes one signal kind sourced from\n")
	b.WriteString("// signal_kinds/*.yaml.\n")
	b.WriteString("type Definition struct {\n")
	b.WriteString("\tKind               Kind\n")
	b.WriteString("\tSource             string\n")
	b.WriteString("\tRetention          Retention\n")
	b.WriteString("\tAutonomyDefault    Autonomy\n")
	b.WriteString("\tSubjectTypeDefault SubjectType\n")
	b.WriteString("\tDescription        string\n")
	b.WriteString("\tI18nKey            string\n")
	b.WriteString("}\n\n")

	b.WriteString("var defs = []Definition{\n")
	for _, r := range all {
		b.WriteString("\t{\n")
		fmt.Fprintf(&b, "\t\tKind:               %s,\n", goIdent(r.Kind))
		fmt.Fprintf(&b, "\t\tSource:             %q,\n", r.Source)
		fmt.Fprintf(&b, "\t\tRetention:          Retention(%q),\n", r.Retention)
		fmt.Fprintf(&b, "\t\tAutonomyDefault:    Autonomy(%q),\n", r.AutonomyDefault)
		fmt.Fprintf(&b, "\t\tSubjectTypeDefault: SubjectType(%q),\n", r.SubjectTypeDefault)
		fmt.Fprintf(&b, "\t\tDescription:        %q,\n", r.Description)
		fmt.Fprintf(&b, "\t\tI18nKey:            %q,\n", r.I18nKey)
		b.WriteString("\t},\n")
	}
	b.WriteString("}\n\n")

	b.WriteString("var byKind = func() map[Kind]Definition {\n")
	b.WriteString("\tm := make(map[Kind]Definition, len(defs))\n")
	b.WriteString("\tfor _, d := range defs {\n")
	b.WriteString("\t\tm[d.Kind] = d\n")
	b.WriteString("\t}\n")
	b.WriteString("\treturn m\n")
	b.WriteString("}()\n\n")

	b.WriteString("// All returns every defined signal kind, sorted by kind.\n")
	b.WriteString("func All() []Definition {\n")
	b.WriteString("\tout := make([]Definition, len(defs))\n")
	b.WriteString("\tcopy(out, defs)\n")
	b.WriteString("\treturn out\n")
	b.WriteString("}\n\n")

	b.WriteString("// Lookup returns the definition for k, if present.\n")
	b.WriteString("func Lookup(k Kind) (Definition, bool) {\n")
	b.WriteString("\td, ok := byKind[k]\n")
	b.WriteString("\treturn d, ok\n")
	b.WriteString("}\n\n")

	b.WriteString("// init asserts that every registry Source is a member of the\n")
	b.WriteString("// canonical signals.source wire enum (packages/go-shared/signalwire).\n")
	b.WriteString("// A registry source the wire/DB enum rejects would make the signal\n")
	b.WriteString("// it advertises fail with a Huma 422 before the handler runs; this\n")
	b.WriteString("// panic turns that drift into a startup failure (and a test failure\n")
	b.WriteString("// via the package's import) instead.\n")
	b.WriteString("func init() {\n")
	b.WriteString("\tsrcs := make([]string, 0, len(defs))\n")
	b.WriteString("\tfor _, d := range defs {\n")
	b.WriteString("\t\tsrcs = append(srcs, d.Source)\n")
	b.WriteString("\t}\n")
	b.WriteString("\tif err := signalwire.AssertSourcesCovered(srcs); err != nil {\n")
	b.WriteString("\t\tpanic(err)\n")
	b.WriteString("\t}\n")
	b.WriteString("}\n")
	return b.Bytes()
}

// ----- TS module -----

func genTsFile(all []record) []byte {
	var b bytes.Buffer
	b.WriteString("// Code generated by gen-signal-kinds. DO NOT EDIT.\n")
	b.WriteString("//\n")
	b.WriteString("// Source: signal_kinds/*.yaml\n")
	b.WriteString("// Regenerate with: go run scripts/gen-signal-kinds.go\n\n")

	b.WriteString("export type SignalRetention = \"stateful\" | \"discrete\";\n")
	b.WriteString("export type SignalAutonomy = \"suggest\" | \"draft\" | \"auto\";\n")
	b.WriteString("export type SignalSubjectType =\n")
	b.WriteString("  | \"user\"\n")
	b.WriteString("  | \"task\"\n")
	b.WriteString("  | \"workspace\"\n")
	b.WriteString("  | \"calendar_event\";\n\n")

	b.WriteString("export interface SignalKindDefinition {\n")
	b.WriteString("  readonly kind: string;\n")
	b.WriteString("  readonly source: string;\n")
	b.WriteString("  readonly retention: SignalRetention;\n")
	b.WriteString("  readonly autonomyDefault: SignalAutonomy;\n")
	b.WriteString("  readonly subjectTypeDefault: SignalSubjectType;\n")
	b.WriteString("  readonly description: string;\n")
	b.WriteString("  readonly i18nKey: string;\n")
	b.WriteString("}\n\n")

	b.WriteString("export const SIGNAL_KINDS = [\n")
	for _, r := range all {
		b.WriteString("  {\n")
		fmt.Fprintf(&b, "    kind: %q,\n", r.Kind)
		fmt.Fprintf(&b, "    source: %q,\n", r.Source)
		fmt.Fprintf(&b, "    retention: %q,\n", r.Retention)
		fmt.Fprintf(&b, "    autonomyDefault: %q,\n", r.AutonomyDefault)
		fmt.Fprintf(&b, "    subjectTypeDefault: %q,\n", r.SubjectTypeDefault)
		fmt.Fprintf(&b, "    description: %q,\n", r.Description)
		fmt.Fprintf(&b, "    i18nKey: %q,\n", r.I18nKey)
		b.WriteString("  },\n")
	}
	b.WriteString("] as const satisfies readonly SignalKindDefinition[];\n\n")

	b.WriteString("export type SignalKind = (typeof SIGNAL_KINDS)[number][\"kind\"];\n\n")

	b.WriteString("const BY_KIND: ReadonlyMap<string, SignalKindDefinition> = new Map(\n")
	b.WriteString("  SIGNAL_KINDS.map((d) => [d.kind, d]),\n")
	b.WriteString(");\n\n")

	b.WriteString("/**\n")
	b.WriteString(" * Look up a signal-kind definition by its dotted identifier.\n")
	b.WriteString(" * Returns undefined when the kind is not in the registry.\n")
	b.WriteString(" */\n")
	b.WriteString("export function lookup(kind: string): SignalKindDefinition | undefined {\n")
	b.WriteString("  return BY_KIND.get(kind);\n")
	b.WriteString("}\n")
	return b.Bytes()
}

// ----- locales -----

// genLocale emits the signal-kinds.json file for one language. The JSON
// shape is flat dotted keys, matching the existing errors.json shape so
// the i18n loaders can treat both bundles identically. Each kind
// contributes two entries: "<i18n_key>.label" (display label) and
// "<i18n_key>.description" (longer help text).
//
// Every language is read from the YAML entry. This function used to
// ignore its own `lang` argument and write the English text into all
// three files, which made the three locale bundles byte-identical and
// the autonomy matrix English for ja and zh readers. Editing the JSON
// by hand appeared to fix it until the next `make gen` overwrote the
// file — validateEntry now refuses an entry that has nothing to write
// here.
func genLocale(all []record, lang string) []byte {
	type pair struct{ key, value string }
	var pairs []pair
	for _, r := range all {
		label, desc := r.Label, r.Description
		switch lang {
		case "ja":
			label, desc = r.LabelJA, r.DescriptionJA
		case "zh":
			label, desc = r.LabelZH, r.DescriptionZH
		}
		pairs = append(pairs,
			pair{key: r.I18nKey + ".label", value: label},
			pair{key: r.I18nKey + ".description", value: desc},
		)
	}
	sort.Slice(pairs, func(i, j int) bool { return pairs[i].key < pairs[j].key })

	var b bytes.Buffer
	b.WriteString("{\n")
	for i, p := range pairs {
		fmt.Fprintf(&b, "  %q: %q", p.key, p.value)
		if i < len(pairs)-1 {
			b.WriteString(",")
		}
		b.WriteString("\n")
	}
	b.WriteString("}\n")
	return b.Bytes()
}

// defaultLabel turns a dotted kind into a human-ish title-case label
// usable as the default i18n value for the "label" key. For instance
// "discord.presence" -> "Discord presence",
// "calendar.event_day_arrived" -> "Calendar event day arrived".
func defaultLabel(kind string) string {
	parts := strings.Split(kind, ".")
	var words []string
	for _, p := range parts {
		for _, sub := range strings.Split(p, "_") {
			if sub == "" {
				continue
			}
			words = append(words, sub)
		}
	}
	if len(words) == 0 {
		return kind
	}
	for i, w := range words {
		if i == 0 {
			words[i] = strings.ToUpper(w[:1]) + strings.ToLower(w[1:])
		} else {
			words[i] = strings.ToLower(w)
		}
	}
	return strings.Join(words, " ")
}

// ----- docs -----

func genDoc(source string, recs []record) []byte {
	var b bytes.Buffer
	b.WriteString("<!-- Code generated by gen-signal-kinds. DO NOT EDIT. -->\n")
	b.WriteString("<!-- Source: signal_kinds/*.yaml -->\n")
	b.WriteString("<!-- Regenerate with: go run scripts/gen-signal-kinds.go -->\n\n")
	fmt.Fprintf(&b, "# Signal kinds: %s\n\n", source)
	fmt.Fprintf(&b, "Catalogue of signal kinds whose `source = %q`.\n\n", source)
	for _, r := range recs {
		fmt.Fprintf(&b, "## `%s`\n\n", r.Kind)
		fmt.Fprintf(&b, "- **Source**: `%s`\n", r.Source)
		fmt.Fprintf(&b, "- **Retention**: `%s`\n", r.Retention)
		fmt.Fprintf(&b, "- **Autonomy default**: `%s`\n", r.AutonomyDefault)
		fmt.Fprintf(&b, "- **Subject type default**: `%s`\n", r.SubjectTypeDefault)
		fmt.Fprintf(&b, "- **i18n key**: `%s`\n", r.I18nKey)
		b.WriteString("\n")
		fmt.Fprintf(&b, "%s\n\n", r.Description)
	}
	return b.Bytes()
}
