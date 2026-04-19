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
	Description string `yaml:"description"`
	UserAction  string `yaml:"userAction"`
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
	Description string
	UserAction  string
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
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "gen-errors:", err)
		os.Exit(1)
	}
	fmt.Fprintln(os.Stdout, "gen-errors: ok")
}

func run() error {
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
				recs = append(recs, record{
					Domain:      df.Domain,
					File:        base,
					Resource:    resKey,
					Key:         errKey,
					Code:        e.Code,
					Status:      e.Status,
					Message:     e.Message,
					Description: e.Description,
					UserAction:  e.UserAction,
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

	// Generate Go per-domain files for all app directories that have
	// an internal/errors/ directory. flow-api receives all domains;
	// auth-api and time-api receive only updates for domains they already
	// track. The runtime helper (Spec/APIError/New/Newf/Wrap) lives in
	// packages/go-shared/apierr and is not generated here.
	goTargets := []struct {
		dir     string
		allDoms bool // true = write all domains; false = update existing only
		runtime bool // true = write errors.go runtime helper
	}{
		{filepath.Join(root, "apps", "flow-api", "internal", "errors"), true, false},
		{filepath.Join(root, "apps", "auth-api", "internal", "errors"), false, false},
		{filepath.Join(root, "apps", "time-api", "internal", "errors"), false, false},
	}
	for _, tgt := range goTargets {
		if _, err := os.Stat(tgt.dir); os.IsNotExist(err) {
			continue
		}
		if err := os.MkdirAll(tgt.dir, 0o755); err != nil {
			return err
		}
		for _, name := range fileNames {
			outPath := filepath.Join(tgt.dir, fileBase(name)+".go")
			if !tgt.allDoms {
				// Only update files that already exist in this app.
				if _, err := os.Stat(outPath); os.IsNotExist(err) {
					continue
				}
			}
			if err := writeFile(outPath, genGoFile(byFile[name])); err != nil {
				return err
			}
		}
		if tgt.runtime {
			if err := writeFile(filepath.Join(tgt.dir, "errors.go"), genGoRuntime()); err != nil {
				return err
			}
		}
	}

	// Generate TS per-domain files + barrel.
	tsDir := filepath.Join(root, "packages", "sdk", "src", "errors")
	if err := os.MkdirAll(tsDir, 0o755); err != nil {
		return err
	}
	for _, name := range fileNames {
		if err := writeFile(filepath.Join(tsDir, name+".ts"), genTsFile(name, byFile[name])); err != nil {
			return err
		}
	}
	if err := writeFile(filepath.Join(tsDir, "index.ts"), genTsBarrel(fileNames)); err != nil {
		return err
	}

	// Locale files — write to all web app directories that have locales/.
	localeApps := []string{"apps/flow-web", "apps/accounts-web"}
	for _, app := range localeApps {
		enDir := filepath.Join(root, app, "locales", "en")
		jaDir := filepath.Join(root, app, "locales", "ja")
		// Skip apps whose locales directory doesn't exist yet.
		if _, err := os.Stat(enDir); os.IsNotExist(err) {
			continue
		}
		if err := os.MkdirAll(enDir, 0o755); err != nil {
			return err
		}
		if err := os.MkdirAll(jaDir, 0o755); err != nil {
			return err
		}
		if err := writeFile(filepath.Join(enDir, "errors.json"), genLocale(all, false)); err != nil {
			return err
		}
		if err := writeFile(filepath.Join(jaDir, "errors.json"), genLocale(all, true)); err != nil {
			return err
		}
	}

	// Docs.
	docsRoot := filepath.Join(root, "docs", "errors")
	for _, name := range fileNames {
		dir := filepath.Join(docsRoot, name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
		for _, r := range byFile[name] {
			if err := writeFile(filepath.Join(dir, r.Code+".md"), genDoc(r)); err != nil {
				return err
			}
		}
	}

	return nil
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
		fmt.Fprintf(&b, "\t%s = &Spec{Code: %q, Status: %d, Message: %q}\n",
			goConst(r.Code), r.Code, r.Status, r.Message)
	}
	b.WriteString(")\n")
	return b.Bytes()
}

// ----- Go runtime helper -----

func genGoRuntime() []byte {
	return []byte(`// Code generated by gen-errors. DO NOT EDIT.

package errors

import (
	"errors"
	"fmt"
)

// Spec describes an error code defined in errors/*.yaml.
type Spec struct {
	Code    string
	Status  int
	Message string
}

// APIError is the runtime error value carried through the API. It wraps a
// Spec and optional details for structured rendering.
type APIError struct {
	Spec    *Spec
	Details map[string]any
	Cause   error
}

// Error implements the error interface.
func (e *APIError) Error() string {
	if e == nil || e.Spec == nil {
		return "<nil api error>"
	}
	if e.Cause != nil {
		return fmt.Sprintf("%s: %s: %v", e.Spec.Code, e.Spec.Message, e.Cause)
	}
	return fmt.Sprintf("%s: %s", e.Spec.Code, e.Spec.Message)
}

// Unwrap exposes the underlying cause for errors.Is / errors.As chains.
func (e *APIError) Unwrap() error { return e.Cause }

// Is reports whether target is an APIError carrying the same Spec pointer.
func (e *APIError) Is(target error) bool {
	var other *APIError
	if !errors.As(target, &other) {
		return false
	}
	return other != nil && other.Spec == e.Spec
}

// New constructs an APIError from a Spec.
func New(spec *Spec) *APIError {
	return &APIError{Spec: spec}
}

// Newf constructs an APIError from a Spec and wraps a formatted cause.
func Newf(spec *Spec, format string, args ...any) *APIError {
	return &APIError{Spec: spec, Cause: fmt.Errorf(format, args...)}
}

// Wrap constructs an APIError from a Spec and an underlying cause.
func Wrap(spec *Spec, cause error) *APIError {
	return &APIError{Spec: spec, Cause: cause}
}

// WithDetail returns a copy of the error with an added detail key.
func (e *APIError) WithDetail(key string, value any) *APIError {
	d := make(map[string]any, len(e.Details)+1)
	for k, v := range e.Details {
		d[k] = v
	}
	d[key] = value
	return &APIError{Spec: e.Spec, Details: d, Cause: e.Cause}
}
`)
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

func genLocale(all []record, blank bool) []byte {
	var b bytes.Buffer
	b.WriteString("{\n")
	for i, r := range all {
		val := r.Message
		if blank {
			val = ""
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
	fmt.Fprintf(&b, "- **Breadcrumb**: %s\n\n", r.Breadcrumb)
	fmt.Fprintf(&b, "## Message\n\n%s\n\n", r.Message)
	fmt.Fprintf(&b, "## Description\n\n%s\n\n", r.Description)
	if r.UserAction != "" {
		fmt.Fprintf(&b, "## User action\n\n%s\n", r.UserAction)
	}
	return b.Bytes()
}
