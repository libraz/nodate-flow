// Command check-metrics-wiring pins the one thing nothing else in the
// pipeline can see: the address Prometheus scrapes has to be the address
// the service binds for /metrics.
//
// Every service serves /metrics on a separate internal-only listener,
// never on its public API port, and that separation is invisible from
// the scrape configuration. A target aimed at the API port is
// syntactically perfect, resolves, connects, and comes back with a 404 —
// so no series is ever stored, and every alert and every dashboard panel
// built on those series is silently unable to fire. Nothing turns red:
// the services are up, the rules are loaded, the panels render empty,
// and an empty panel is what a quiet system looks like.
//
// No single file holds enough to catch that. Three sources have to
// agree, and this joins them:
//
//	Go       which service serves /metrics, the config field its
//	         listener address is built from, and the env var and
//	         default that field declares.
//	compose  what the deployment sets that env var to, and the service
//	         name a target has to name to reach it on the network.
//	Prom     what is actually scraped.
//
// The join refuses a verdict rather than guessing. A compose value that
// is not a literal port — a substitution whose expansion lives outside
// this repository — is reported as unresolved and fails, because
// "probably 9090" is exactly the reasoning that let the original
// mismatch stand.
//
// The census printed on every run is a report and not a failure, and
// that split is a decision. A metric an alert queries but no service
// declares, and a label a query selects on that the metric does not
// carry, are each real: the rule cannot fire either way. But fixing one
// means implementing a metric or rewriting a query, not correcting a
// port, and a check carrying more exemptions than findings stops being
// read at all. So the wiring fails and the census speaks.
//
// One property of the declarations themselves is failed rather than
// reported, because it is not a difference of opinion between two files
// but a value the client library refuses: a fully-qualified metric name
// declared by more than one collector. The default registry admits one
// collector per name, so the second registration panics at init and
// takes the whole binary with it before anything it was linked into can
// run.
//
// Usage: go run scripts/check-metrics-wiring.go [repository-root]
//
// Exit codes:
//
//	0 — every service serving /metrics is scraped where it listens
//	1 — a mismatch, a missing target, or a value that could not be resolved
//
//go:build ignore

package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string) error {
	root, err := repoRoot(args)
	if err != nil {
		return err
	}

	listeners, listenerProblems, err := findListeners(root)
	if err != nil {
		return err
	}
	composePath := filepath.Join(root, "compose.yml")
	composeServices, err := readCompose(composePath)
	if err != nil {
		return err
	}
	promPath := filepath.Join(root, "infra", "prometheus", "prometheus.yml")
	targets, err := readTargets(promPath)
	if err != nil {
		return err
	}

	// Non-emptiness proof. Every stage of this join answers "nothing
	// found" the same way it answers "nothing wrong", so a walk that has
	// stopped matching — a service that moved, a compose key that was
	// renamed, a scrape file restructured — would report a clean wiring
	// over an empty set. These are checked before the join, and the
	// counts are printed on every run so a shrinking inventory is
	// visible before it reaches zero.
	appBacked := appBackedServices(composeServices)
	switch {
	case len(listeners) == 0:
		return fmt.Errorf("check-metrics-wiring: no service under apps/ in %s was found to serve /metrics, so nothing was joined against the scrape configuration.\n"+
			"Either the services moved, or the registration no longer has the shape this reads: Handle(\"/metrics\", …) on a mux, and an http.Server literal in the same function whose Handler is that mux.", root)
	case len(appBacked) == 0:
		return fmt.Errorf("check-metrics-wiring: no service in %s builds from apps/, so no scrape target could be attributed to a service.\n"+
			"Either the build stanzas changed shape, or the applications are deployed from somewhere this does not read.", relPath(root, composePath))
	case len(targets) == 0:
		return fmt.Errorf("check-metrics-wiring: no scrape target was found in %s, so every service would have looked correctly wired.\n"+
			"Either the targets moved out of static_configs, or the file is no longer the scrape configuration.", relPath(root, promPath))
	}

	failures := append([]string(nil), listenerProblems...)

	// Which compose service builds which application. A service that
	// exists twice for one directory is not resolvable to one port.
	byApp := map[string][]*composeService{}
	for _, svc := range appBacked {
		byApp[svc.app] = append(byApp[svc.app], svc)
	}

	scraped := map[string][]scrapeTarget{}
	for _, t := range targets {
		scraped[t.host] = append(scraped[t.host], t)
	}

	// Stage one and two: every service serving /metrics resolves to one
	// compose service and one literal port.
	type wired struct {
		listener *listener
		service  *composeService
		port     int
		source   string
	}
	var resolved []wired
	for _, l := range listeners {
		services := byApp[l.app]
		if len(services) == 0 {
			failures = append(failures, fmt.Sprintf(
				"%s serves /metrics (%s) and no service in %s builds it, so nothing knows what name to scrape it under",
				l.app, relPath(root, l.where), relPath(root, composePath)))
			continue
		}
		if len(services) > 1 {
			failures = append(failures, fmt.Sprintf(
				"%s serves /metrics (%s) and %d services in %s build it, so its scrape target is ambiguous",
				l.app, relPath(root, l.where), len(services), relPath(root, composePath)))
			continue
		}
		svc := services[0]
		port, source, err := l.effectivePort(svc)
		if err != nil {
			failures = append(failures, fmt.Sprintf("%s: %v", l.app, err))
			continue
		}
		resolved = append(resolved, wired{listener: l, service: svc, port: port, source: source})
	}

	// Stage three: the target exists, and it is the port the service binds.
	for _, w := range resolved {
		hits := scraped[w.service.name]
		if len(hits) == 0 {
			failures = append(failures, fmt.Sprintf(
				"%s binds :%d for /metrics (%s) and no scrape target in %s names %q, so it is scraped by nothing",
				w.listener.app, w.port, w.source, relPath(root, promPath), w.service.name))
			continue
		}
		for _, t := range hits {
			if t.port == w.port {
				continue
			}
			failures = append(failures, fmt.Sprintf(
				"%s binds :%d for /metrics (%s) and job %q scrapes %s:%d — the target is not the address the service listens on",
				w.listener.app, w.port, w.source, t.job, t.host, t.port))
		}
	}

	// A target naming a host that is no service at all can never
	// connect: on the compose network the hostname is the service name.
	// A target naming a service that builds from somewhere other than
	// apps/ — the collector — is somebody else's invariant, so it is
	// counted and left alone.
	skipped := 0
	for _, t := range targets {
		svc, known := composeServices[t.host]
		switch {
		case !known:
			failures = append(failures, fmt.Sprintf(
				"job %q scrapes %s:%d and %s defines no service named %q, so the target resolves to nothing",
				t.job, t.host, t.port, relPath(root, composePath), t.host))
		case svc.app == "":
			skipped++
		}
	}

	fmt.Printf("check-metrics-wiring: %d service(s) serving /metrics, %d compose service(s) built from apps/, %d scrape target(s) (%d outside apps/, not this check's business)\n",
		len(listeners), len(appBacked), len(targets), skipped)

	declared, err := declaredMetrics(root)
	if err != nil {
		return err
	}

	// The census runs whether or not the wiring holds: the counts are
	// the reason this target shows its output, and suppressing them on
	// the run that fails would hide them exactly when somebody is
	// reading.
	if err := census(root, declared); err != nil {
		return err
	}

	// Both verdicts are reported together. They are independent
	// problems — one is a name declared twice, the other an address
	// scraped at the wrong port — and stopping at the first would hide
	// the second behind a fix for something unrelated.
	var problems []error
	if err := duplicateDeclarations(declared); err != nil {
		problems = append(problems, err)
	}
	if len(failures) > 0 {
		sort.Strings(failures)
		problems = append(problems, fmt.Errorf("check-metrics-wiring: %d metrics wiring problem(s):\n\n  %s\n\n"+
			"A scrape target that is not the port the service binds collects nothing, and collects\n"+
			"it silently: the target is simply down, every series behind it is absent, and every\n"+
			"alert and panel built on those series stays quiet forever. Point the target at the\n"+
			"metrics listener, or change what the service binds — the two are one decision.",
			len(failures), strings.Join(failures, "\n  ")))
	}
	return errors.Join(problems...)
}

// ----- stage one: what the Go sources bind -----

// listener is one service's metrics listener: where it is written, and
// what its bind address is made of.
//
// The address is a prefix and a config field rather than a string,
// because both shapes in the tree are that: ":" + cfg.MetricsPort and
// cfg.MetricsAddr are the same expression with an empty prefix.
type listener struct {
	app        string // "apps/flow-api"
	where      string // file:line of the http.Server literal
	prefix     string // literal text the address is built in front of
	field      string // config field the address reads, empty for a literal
	envVar     string // env var that field declares
	envDefault string // default that field declares
	literal    string // address when it is written out, no config field
}

// effectivePort is the port this listener binds under the deployment,
// and a description of where that value came from.
//
// A value that is not a literal port is refused rather than resolved:
// a substitution expands from a file outside this repository, and a
// guess about its contents is the same reasoning that leaves a
// mismatch standing.
func (l *listener) effectivePort(svc *composeService) (int, string, error) {
	value := l.literal
	source := fmt.Sprintf("written in %s", l.where)
	if l.field != "" {
		value = l.envDefault
		source = fmt.Sprintf("%s default", l.envVar)
		if set, ok := svc.env[l.envVar]; ok {
			value = set
			source = fmt.Sprintf("%s in compose.yml", l.envVar)
		}
	}
	if value == "" {
		return 0, "", fmt.Errorf("the /metrics listener at %s reads %s, which declares no default, and no service sets %s — the port it binds is not decided anywhere this can read",
			l.where, l.field, l.envVar)
	}
	port, err := portOf(l.prefix + value)
	if err != nil {
		return 0, "", fmt.Errorf("the /metrics listener at %s binds %q (%s), %w — refusing a verdict rather than assuming what it expands to",
			l.where, l.prefix+value, source, err)
	}
	return port, source, nil
}

// findListeners walks each application for the function that registers
// /metrics on a mux and serves that mux, and resolves the address it
// binds back to the config field it is built from.
//
// Anything half-found — a registration with no server, a server whose
// address is an expression this cannot read, a field with no env tag —
// is returned as a problem rather than skipped. A service dropping out
// of the inventory is indistinguishable from a service that is wired
// correctly, which is the direction this check must never fail in.
func findListeners(root string) ([]*listener, []string, error) {
	appsDir := filepath.Join(root, "apps")
	entries, err := os.ReadDir(appsDir)
	if err != nil {
		return nil, nil, fmt.Errorf("check-metrics-wiring: %w", err)
	}
	var listeners []*listener
	var problems []string
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		app := path.Join("apps", entry.Name())
		appDir := filepath.Join(appsDir, entry.Name())
		found, appProblems, err := listenersIn(root, app, appDir)
		if err != nil {
			return nil, nil, err
		}
		listeners = append(listeners, found...)
		problems = append(problems, appProblems...)
	}
	sort.Slice(listeners, func(i, j int) bool { return listeners[i].app < listeners[j].app })
	return listeners, problems, nil
}

func listenersIn(root, app, appDir string) ([]*listener, []string, error) {
	files, err := goFiles(appDir)
	if err != nil {
		return nil, nil, err
	}
	if len(files) == 0 {
		return nil, nil, nil
	}
	fset := token.NewFileSet()
	var listeners []*listener
	var problems []string
	for _, file := range files {
		parsed, err := parser.ParseFile(fset, file, nil, 0)
		if err != nil {
			return nil, nil, fmt.Errorf("check-metrics-wiring: %s: %w", relPath(root, file), err)
		}
		for _, decl := range parsed.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			muxes := metricsMuxes(fn.Body)
			if len(muxes) == 0 {
				continue
			}
			for _, mux := range muxes {
				addr, at, ok := serverAddrFor(fn.Body, mux)
				where := fmt.Sprintf("%s:%d", relPath(root, file), fset.Position(at).Line)
				if !ok {
					problems = append(problems, fmt.Sprintf(
						"%s registers /metrics on %s and no http.Server literal in the same function serves that mux (%s), so the address it binds could not be read",
						app, mux, where))
					continue
				}
				addr, ok = addrValue(fn.Body, addr)
				if !ok {
					problems = append(problems, fmt.Sprintf(
						"%s serves /metrics on an address named in one place and assigned in none this can find (%s), so the port it binds is unknown",
						app, where))
					continue
				}
				l, err := listenerFrom(app, where, addr)
				if err != nil {
					problems = append(problems, fmt.Sprintf("%s: %v", app, err))
					continue
				}
				listeners = append(listeners, l)
			}
		}
	}
	if len(listeners) == 0 {
		return nil, problems, nil
	}
	// Resolve every config field against this application's own config
	// declarations: the field name alone is not an answer, the env var
	// and the default it declares are.
	fields, err := envFields(root, appDir)
	if err != nil {
		return nil, nil, err
	}
	var kept []*listener
	for _, l := range listeners {
		if l.field == "" {
			kept = append(kept, l)
			continue
		}
		declared, ok := fields[l.field]
		switch {
		case !ok:
			problems = append(problems, fmt.Sprintf(
				"%s binds its /metrics listener from %s (%s) and no struct field of that name in %s carries an env tag, so the value it takes is not readable from the sources",
				app, l.field, l.where, app))
		case len(declared) > 1:
			problems = append(problems, fmt.Sprintf(
				"%s binds its /metrics listener from %s (%s) and %d fields of that name in %s carry an env tag, so which env var decides the port is ambiguous",
				app, l.field, l.where, len(declared), app))
		default:
			l.envVar = declared[0].env
			l.envDefault = declared[0].def
			kept = append(kept, l)
		}
	}
	return kept, problems, nil
}

// metricsMuxes names the muxes a function registers /metrics on.
func metricsMuxes(body *ast.BlockStmt) []string {
	var found []string
	seen := map[string]bool{}
	ast.Inspect(body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok || len(call.Args) == 0 {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "Handle" {
			return true
		}
		recv, ok := sel.X.(*ast.Ident)
		if !ok {
			return true
		}
		if stringLit(call.Args[0]) != "/metrics" {
			return true
		}
		if !seen[recv.Name] {
			seen[recv.Name] = true
			found = append(found, recv.Name)
		}
		return true
	})
	return found
}

// serverAddrFor returns the Addr expression of the http.Server literal
// whose Handler is the named mux.
func serverAddrFor(body *ast.BlockStmt, mux string) (ast.Expr, token.Pos, bool) {
	var addr ast.Expr
	var at token.Pos
	ast.Inspect(body, func(n ast.Node) bool {
		lit, ok := n.(*ast.CompositeLit)
		if !ok || !isSelector(lit.Type, "http", "Server") {
			return true
		}
		var candidate ast.Expr
		serves := false
		for _, elt := range lit.Elts {
			kv, ok := elt.(*ast.KeyValueExpr)
			if !ok {
				continue
			}
			key, ok := kv.Key.(*ast.Ident)
			if !ok {
				continue
			}
			switch key.Name {
			case "Addr":
				candidate = kv.Value
			case "Handler":
				if ident, ok := kv.Value.(*ast.Ident); ok && ident.Name == mux {
					serves = true
				}
			}
		}
		if serves && candidate != nil {
			addr = candidate
			at = lit.Pos()
			return false
		}
		return true
	})
	return addr, at, addr != nil
}

// addrValue follows an Addr written as an identifier back to the
// expression that gives it its value.
//
// The address is named before it is used, because the same string is
// both listened on and logged, so the http.Server literal holds a name
// rather than the expression. Only a single assignment in the same
// function counts: an address rewritten further down is one this cannot
// reduce to a port, and reading the first assignment would answer with
// a value the process never binds.
func addrValue(body *ast.BlockStmt, expr ast.Expr) (ast.Expr, bool) {
	ident, ok := expr.(*ast.Ident)
	if !ok {
		return expr, true
	}
	var found []ast.Expr
	ast.Inspect(body, func(n ast.Node) bool {
		switch stmt := n.(type) {
		case *ast.AssignStmt:
			if len(stmt.Lhs) != len(stmt.Rhs) {
				return true
			}
			for i, lhs := range stmt.Lhs {
				if id, ok := lhs.(*ast.Ident); ok && id.Name == ident.Name {
					found = append(found, stmt.Rhs[i])
				}
			}
		case *ast.ValueSpec:
			if len(stmt.Names) != len(stmt.Values) {
				return true
			}
			for i, name := range stmt.Names {
				if name.Name == ident.Name {
					found = append(found, stmt.Values[i])
				}
			}
		}
		return true
	})
	if len(found) != 1 {
		return nil, false
	}
	return found[0], true
}

// listenerFrom reads a bind address expression.
//
// Two shapes exist and both reduce to a prefix and a config field:
// ":" + cfg.MetricsPort carries the colon in the source, cfg.MetricsAddr
// carries it in the value. Anything else — a call, a formatted string —
// is refused, because the port it produces cannot be compared with a
// scrape target without running it.
func listenerFrom(app, where string, expr ast.Expr) (*listener, error) {
	switch e := expr.(type) {
	case *ast.BasicLit:
		if e.Kind != token.STRING {
			break
		}
		return &listener{app: app, where: where, literal: stringLit(e)}, nil
	case *ast.SelectorExpr:
		return &listener{app: app, where: where, field: e.Sel.Name}, nil
	case *ast.BinaryExpr:
		if e.Op != token.ADD {
			break
		}
		prefix := stringLit(e.X)
		sel, ok := e.Y.(*ast.SelectorExpr)
		if prefix == "" || !ok {
			break
		}
		return &listener{app: app, where: where, prefix: prefix, field: sel.Sel.Name}, nil
	}
	return nil, fmt.Errorf("the http.Server at %s serves /metrics on an address this cannot read back to a config field, so the port it binds is unknown", where)
}

// envDecl is one config field's env binding.
type envDecl struct {
	env string
	def string
}

// envFields indexes an application's config fields by name, keeping only
// those that bind an env var. The struct tag is the whole declaration:
// the name of the variable and the value that applies when the
// deployment sets nothing.
func envFields(root, appDir string) (map[string][]envDecl, error) {
	files, err := goFiles(appDir)
	if err != nil {
		return nil, err
	}
	fields := map[string][]envDecl{}
	fset := token.NewFileSet()
	for _, file := range files {
		parsed, err := parser.ParseFile(fset, file, nil, 0)
		if err != nil {
			return nil, fmt.Errorf("check-metrics-wiring: %s: %w", relPath(root, file), err)
		}
		ast.Inspect(parsed, func(n ast.Node) bool {
			st, ok := n.(*ast.StructType)
			if !ok || st.Fields == nil {
				return true
			}
			for _, field := range st.Fields.List {
				if field.Tag == nil {
					continue
				}
				tag := reflect.StructTag(strings.Trim(stringLitRaw(field.Tag.Value), "`"))
				name, ok := tag.Lookup("env")
				if !ok {
					continue
				}
				// The tag carries options after the name
				// (",required"); the variable is the first part.
				name, _, _ = strings.Cut(name, ",")
				def, _ := tag.Lookup("envDefault")
				for _, ident := range field.Names {
					fields[ident.Name] = append(fields[ident.Name], envDecl{env: name, def: def})
				}
			}
			return true
		})
	}
	return fields, nil
}

// ----- stage two: what the deployment sets -----

type composeService struct {
	name string
	app  string // "apps/<name>" this service builds, empty when it builds none
	env  map[string]string
}

func readCompose(path string) (map[string]*composeService, error) {
	doc, err := readYAML(path)
	if err != nil {
		return nil, err
	}
	services := map[string]*composeService{}
	block := mapValue(doc, "services")
	if block == nil || block.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("check-metrics-wiring: %s declares no services block", path)
	}
	for i := 0; i+1 < len(block.Content); i += 2 {
		name := block.Content[i].Value
		body := block.Content[i+1]
		services[name] = &composeService{
			name: name,
			app:  appOf(mapValue(body, "build")),
			env:  environmentOf(mapValue(body, "environment")),
		}
	}
	return services, nil
}

// appOf reads which application directory a build stanza builds.
//
// Both halves are consulted because the context is the repository root
// for every service here and the application is named by the Dockerfile
// path; a service written the other way round is read just as well.
func appOf(build *yaml.Node) string {
	if build == nil {
		return ""
	}
	var candidates []string
	switch build.Kind {
	case yaml.ScalarNode:
		candidates = append(candidates, build.Value)
	case yaml.MappingNode:
		for _, key := range []string{"dockerfile", "context"} {
			if v := mapValue(build, key); v != nil {
				candidates = append(candidates, v.Value)
			}
		}
	}
	for _, candidate := range candidates {
		parts := strings.Split(path.Clean(candidate), "/")
		for i, part := range parts {
			if part == "apps" && i+1 < len(parts) {
				return path.Join("apps", parts[i+1])
			}
		}
	}
	return ""
}

// environmentOf reads a service's environment in either spelling: a
// mapping of names to values, or a sequence of NAME=value entries.
func environmentOf(env *yaml.Node) map[string]string {
	out := map[string]string{}
	if env == nil {
		return out
	}
	switch env.Kind {
	case yaml.MappingNode:
		for i := 0; i+1 < len(env.Content); i += 2 {
			out[env.Content[i].Value] = env.Content[i+1].Value
		}
	case yaml.SequenceNode:
		for _, item := range env.Content {
			name, value, ok := strings.Cut(item.Value, "=")
			if !ok {
				// A bare name passes the host's value through, which
				// this cannot see. Recorded as present and empty so
				// it is refused rather than defaulted.
				out[item.Value] = ""
				continue
			}
			out[name] = value
		}
	}
	return out
}

func appBackedServices(services map[string]*composeService) []*composeService {
	var out []*composeService
	for _, svc := range services {
		if svc.app != "" {
			out = append(out, svc)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].name < out[j].name })
	return out
}

// ----- stage three: what Prometheus scrapes -----

type scrapeTarget struct {
	job  string
	host string
	port int
}

func readTargets(path string) ([]scrapeTarget, error) {
	doc, err := readYAML(path)
	if err != nil {
		return nil, err
	}
	var out []scrapeTarget
	for _, job := range seqItems(mapValue(doc, "scrape_configs")) {
		name := ""
		if v := mapValue(job, "job_name"); v != nil {
			name = v.Value
		}
		for _, static := range seqItems(mapValue(job, "static_configs")) {
			for _, target := range seqItems(mapValue(static, "targets")) {
				host, portText, ok := strings.Cut(target.Value, ":")
				if !ok {
					return nil, fmt.Errorf("check-metrics-wiring: %s: job %q has target %q, which names no port", path, name, target.Value)
				}
				port, err := strconv.Atoi(portText)
				if err != nil {
					return nil, fmt.Errorf("check-metrics-wiring: %s: job %q has target %q, whose port is not a number", path, name, target.Value)
				}
				out = append(out, scrapeTarget{job: name, host: host, port: port})
			}
		}
	}
	return out, nil
}

// ----- the census -----

// PromQL has no parser among this module's dependencies, so the metric
// and label names in a query are pulled out with regular expressions.
// That is confined to the census on purpose: the failing half of this
// check reads Go syntax trees and YAML nodes only, so a query these
// misread can misstate a report and can never fail a build.
var (
	promName     = regexp.MustCompile(`[a-zA-Z_][a-zA-Z0-9_]*`)
	promSelector = regexp.MustCompile(`([a-zA-Z_][a-zA-Z0-9_]*)\s*\{([^}]*)\}`)
	promMatcher  = regexp.MustCompile(`([a-zA-Z_][a-zA-Z0-9_]*)\s*(?:=~|!~|!=|=)`)
	promBy       = regexp.MustCompile(`\bby\s*\(([^)]*)\)`)
	promGrouping = regexp.MustCompile(`\b(?:by|without|on|ignoring|group_left|group_right)\s*\(([^)]*)\)`)
	promRange    = regexp.MustCompile(`\[[^\]]*\]`)
)

// Labels Prometheus itself attaches or the query language reserves. A
// metric never declares these and a query that groups by one is right
// to: le is the histogram bucket boundary, job and instance come from
// the scrape configuration rather than from the instrumentation.
var ambientLabels = map[string]bool{"le": true, "job": true, "instance": true}

// Suffixes a histogram or summary publishes in addition to its declared
// name. A query naming one of these is querying the declared metric.
var seriesSuffixes = []string{"_bucket", "_sum", "_count"}

// declaration is one prometheus.New* call: where it is written, and the
// labels it hands the collector.
type declaration struct {
	where  string   // file:line of the constructor call
	labels []string // sorted, empty for a collector that takes no label list
}

// labelSet renders a declaration's labels for display and for deciding
// whether two declarations of one name differ in shape.
func (d declaration) labelSet() string {
	return "{" + strings.Join(d.labels, ",") + "}"
}

// declaredMetric is one metric as the Go sources declare it: the Name of
// a prometheus options literal and, for the vector constructors, the
// labels the constructor is given.
//
// A name is expected to be declared once and is carried as a list of
// declarations anyway, because a second one is the finding. For the
// census the labels are unioned across them, which is the forgiving
// direction: it can only remove a label finding, never invent one.
type declaredMetric struct {
	name     string
	labels   map[string]bool
	suffixed bool // histogram or summary, publishes _bucket / _sum / _count
	decls    []declaration
}

// where lists the places this metric is declared, in walk order.
func (m *declaredMetric) where() []string {
	var out []string
	for _, d := range m.decls {
		out = append(out, d.where)
	}
	return out
}

// duplicateDeclarations refuses a fully-qualified metric name that more
// than one collector declares.
//
// This is a failure and not a census line because it is not a gap
// between what is instrumented and what is queried: the client library
// admits one collector per name in the default registry, so the second
// registration panics at init and every binary that links both
// declarations dies there — including any binary that links two
// services' packages together, whether or not either service would ever
// have collided in production.
//
// Sharing the name across services is the intended design. Each service
// is scraped as its own job and the job label separates the series, so
// one query spans the deployment. The fix is therefore to declare the
// collector once in a package both services call into, never to give
// each service its own spelling of the name.
func duplicateDeclarations(declared map[string]*declaredMetric) error {
	var names []string
	for name, metric := range declared {
		if len(metric.decls) > 1 {
			names = append(names, name)
		}
	}
	if len(names) == 0 {
		return nil
	}
	sort.Strings(names)

	var findings []string
	for _, name := range names {
		metric := declared[name]
		shapes := map[string]bool{}
		var lines []string
		for _, d := range metric.decls {
			shapes[d.labelSet()] = true
			lines = append(lines, fmt.Sprintf("      %s  %s", d.where, d.labelSet()))
		}
		shape := "with the same label set, which is one collector duplicated"
		if len(shapes) > 1 {
			shape = "with differing label sets, which is one name given two incompatible collectors — a query written for either shape reads only the series of the one that registered"
		}
		findings = append(findings, fmt.Sprintf("%s is declared %d times, %s:\n%s",
			name, len(metric.decls), shape, strings.Join(lines, "\n")))
	}

	return fmt.Errorf("check-metrics-wiring: %d metric name(s) declared more than once:\n\n  %s\n\n"+
		"Two declarations of one name are two collectors. The default registry admits one\n"+
		"collector per fully-qualified name, so any binary that links both declarations panics\n"+
		"at init the moment the second registers — before any of its own code runs.\n"+
		"Sharing the name between services is correct and intended: each service is scraped as\n"+
		"its own job and the job label separates them, so one query covers the deployment.\n"+
		"Declare the collector once in a shared package and have each service call into it.\n"+
		"Do not resolve this by renaming the metric per service.",
		len(findings), strings.Join(findings, "\n  "))
}

func census(root string, declared map[string]*declaredMetric) error {
	queries, err := readQueries(root)
	if err != nil {
		return err
	}

	// The namespace our instrumentation owns, derived from what it
	// declares rather than written down. Everything else in a query
	// belongs to an exporter or to Prometheus, and this check has
	// nothing to say about whether those are emitted.
	namespaces := map[string]bool{}
	for name := range declared {
		prefix, _, ok := strings.Cut(name, "_")
		if ok {
			namespaces[prefix] = true
		}
	}

	unemitted := map[string]map[string]bool{} // metric -> files
	mismatched := map[string]map[string]bool{}
	referenced := map[string]bool{}
	outside := map[string]bool{}
	var deadFiles []string

	for _, q := range queries {
		queriesAMetric := false
		queriesADeclaredOne := false
		for _, expr := range q.exprs {
			group := groupLabels(expr)
			for _, name := range metricNames(expr) {
				metric, resolvedName, declares := resolve(declared, name)
				prefix, _, _ := strings.Cut(name, "_")
				if !namespaces[prefix] {
					// A series from an exporter or from Prometheus
					// itself. Whether it is emitted is that exporter's
					// invariant, so it is counted and named rather than
					// dropped in silence.
					if !declares {
						queriesAMetric = true
						outside[name] = true
					}
					continue
				}
				queriesAMetric = true
				if !declares {
					if unemitted[name] == nil {
						unemitted[name] = map[string]bool{}
					}
					unemitted[name][q.file] = true
					continue
				}
				queriesADeclaredOne = true
				referenced[resolvedName] = true
				for label := range labelsFor(expr, name, group) {
					if ambientLabels[label] || metric.labels[label] {
						continue
					}
					key := fmt.Sprintf("%s{%s}", metric.name, label)
					if mismatched[key] == nil {
						mismatched[key] = map[string]bool{}
					}
					mismatched[key][q.file] = true
				}
			}
		}
		if queriesAMetric && !queriesADeclaredOne {
			deadFiles = append(deadFiles, q.file)
		}
	}

	unused := map[string]map[string]bool{}
	for name, metric := range declared {
		if referenced[name] {
			continue
		}
		unused[name] = map[string]bool{}
		for _, where := range metric.where() {
			unused[name][where] = true
		}
	}

	fmt.Printf("check-metrics-wiring: census over %d declared metric(s) and %d query file(s) — reported, never failed: each line is a metric to implement or a query to rewrite, and a guard with more exemptions than findings stops being read\n",
		len(declared), len(queries))
	printCensus("metric name(s) an alert or dashboard queries that no service declares — the panel renders empty and the rule cannot fire", withFiles(unemitted))
	printCensus("label(s) a query selects on or groups by that the metric it names does not carry — the selector matches no series", withFiles(mismatched))
	printCensus("metric(s) declared in Go that no alert or dashboard queries", withFiles(unused))
	printCensus("query file(s) in which nothing any service declares appears at all", plain(deadFiles))
	if len(outside) > 0 {
		var names []string
		for name := range outside {
			names = append(names, name)
		}
		sort.Strings(names)
		fmt.Printf("  %d metric name(s) queried from outside this repository's namespace, left to whatever exports them: %s\n",
			len(names), strings.Join(names, ", "))
	}
	return nil
}

// PromQL words that are neither a series nor a call, so the "is it
// followed by a parenthesis" rule cannot tell them from a metric. The
// aggregation operators are here because they may be written with their
// grouping clause first — `sum by (path) (…)` — which puts something
// other than a parenthesis after the name.
var promKeywords = map[string]bool{
	"and": true, "or": true, "unless": true, "by": true, "without": true,
	"on": true, "ignoring": true, "group_left": true, "group_right": true,
	"offset": true, "bool": true, "inf": true, "nan": true,
	"sum": true, "min": true, "max": true, "avg": true, "group": true,
	"stddev": true, "stdvar": true, "count": true, "count_values": true,
	"bottomk": true, "topk": true, "quantile": true,
}

// metricNames are the series an expression reads.
//
// The regions that hold names which are not series are blanked first —
// quoted strings, the inside of a {…} selector, and the label list of a
// by/without/on/ignoring clause — because a label called `status` and a
// metric called `status` are the same characters and only their position
// tells them apart. What survives is an identifier that is not a
// keyword and is not immediately followed by a parenthesis, which would
// make it a function call.
func metricNames(expr string) []string {
	masked := maskNonSeries(expr)
	var out []string
	seen := map[string]bool{}
	for _, at := range promName.FindAllStringIndex(masked, -1) {
		name := masked[at[0]:at[1]]
		if promKeywords[name] || seen[name] {
			continue
		}
		if strings.HasPrefix(strings.TrimLeft(masked[at[1]:], " \t\n"), "(") {
			continue
		}
		seen[name] = true
		out = append(out, name)
	}
	return out
}

// maskNonSeries replaces every span that cannot hold a metric name with
// spaces, keeping offsets and the surrounding punctuation intact.
func maskNonSeries(expr string) string {
	out := []byte(expr)
	blank := func(from, to int) {
		for i := from; i < to && i < len(out); i++ {
			out[i] = ' '
		}
	}
	for _, at := range promSelector.FindAllStringSubmatchIndex(expr, -1) {
		blank(at[4], at[5])
	}
	for _, at := range promGrouping.FindAllStringSubmatchIndex(expr, -1) {
		blank(at[2], at[3])
	}
	// A range or offset holds a duration, whose unit letter is an
	// identifier as far as a scan is concerned.
	for _, at := range promRange.FindAllStringIndex(expr, -1) {
		blank(at[0]+1, at[1]-1)
	}
	quote := byte(0)
	for i := 0; i < len(out); i++ {
		switch {
		case quote != 0:
			if out[i] == quote {
				quote = 0
				continue
			}
			out[i] = ' '
		case out[i] == '"' || out[i] == '\'' || out[i] == '`':
			quote = out[i]
		}
	}
	return string(out)
}

// resolve maps a queried series name to the metric that declares it,
// following the suffixes a histogram publishes beyond its own name.
func resolve(declared map[string]*declaredMetric, name string) (*declaredMetric, string, bool) {
	if m, ok := declared[name]; ok {
		return m, name, true
	}
	for _, suffix := range seriesSuffixes {
		base, found := strings.CutSuffix(name, suffix)
		if !found {
			continue
		}
		if m, ok := declared[base]; ok && m.suffixed {
			return m, base, true
		}
	}
	return nil, "", false
}

// groupLabels are the labels an expression groups by.
//
// They are attributed to every metric in the expression rather than to
// the one the aggregation covers: without a parser the association is
// not derivable, and the expressions here aggregate over one metric
// each. An expression joining two metrics would report the labels of
// one against the other, which is a false line in a report and not a
// failed build.
func groupLabels(expr string) map[string]bool {
	out := map[string]bool{}
	for _, m := range promBy.FindAllStringSubmatch(expr, -1) {
		for _, label := range strings.Split(m[1], ",") {
			label = strings.TrimSpace(label)
			if label != "" {
				out[label] = true
			}
		}
	}
	return out
}

// labelsFor are the labels a query asks of one metric: the matchers in
// its own selector, plus the grouping the expression applies.
func labelsFor(expr, name string, group map[string]bool) map[string]bool {
	out := map[string]bool{}
	for label := range group {
		out[label] = true
	}
	for _, m := range promSelector.FindAllStringSubmatch(expr, -1) {
		if m[1] != name {
			continue
		}
		for _, matcher := range promMatcher.FindAllStringSubmatch(m[2], -1) {
			out[matcher[1]] = true
		}
	}
	return out
}

// declarationRoots are the directories a metric declaration can live in.
//
// A collector exported by more than one service is declared once in a
// shared package rather than once per service, so the declarations are
// not all under apps/. A root left out here is not read as empty, it is
// read as nothing at all: its metrics would be reported as queried by an
// alert and declared by nobody, and its labels would be unknown, so
// every label finding against them would silently disappear.
var declarationRoots = []string{"apps", "packages"}

// declaredMetrics reads every metric the Go sources declare: the Name
// field of a prometheus options literal, and the label slice the vector
// constructors take alongside it.
//
// The result is keyed by fully-qualified name across the whole
// repository, with no attempt to attribute a declaration to the services
// that link it. Which binaries a shared package reaches is an import
// question this does not ask; what the census needs, and what the
// duplicate rule needs, is which names exist and where each is written.
func declaredMetrics(root string) (map[string]*declaredMetric, error) {
	out := map[string]*declaredMetric{}
	fset := token.NewFileSet()
	for _, name := range declarationRoots {
		dir := filepath.Join(root, name)
		found, err := declaredMetricsIn(root, dir, fset, out)
		if err != nil {
			return nil, err
		}
		// Non-emptiness proof, per root. A root that matches nothing
		// answers exactly like a root with nothing wrong: with no
		// declarations the census knows no namespace, so every query
		// against them is attributed to some outside exporter, no label
		// can disagree with anything, and no name can be declared twice.
		// The whole report reads clean.
		if found == 0 {
			return nil, fmt.Errorf("check-metrics-wiring: no metric declaration was found under %s in %s, so nothing declared there was compared against anything queried.\n"+
				"Either the declarations moved, or they no longer have the shape this reads: a prometheus.New… call taking a prometheus.…Opts literal with a Name field.", name, root)
		}
	}
	return out, nil
}

// declaredMetricsIn adds the declarations under one directory to out and
// returns how many constructor calls it found there.
func declaredMetricsIn(root, dir string, fset *token.FileSet, out map[string]*declaredMetric) (int, error) {
	files, err := goFiles(dir)
	if err != nil {
		return 0, err
	}
	found := 0
	for _, file := range files {
		parsed, err := parser.ParseFile(fset, file, nil, 0)
		if err != nil {
			return 0, fmt.Errorf("check-metrics-wiring: %s: %w", relPath(root, file), err)
		}
		where := relPath(root, file)
		ast.Inspect(parsed, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok || !strings.HasPrefix(sel.Sel.Name, "New") {
				return true
			}
			name, kind, ok := optsName(call.Args)
			if !ok {
				return true
			}
			found++
			metric := out[name]
			if metric == nil {
				metric = &declaredMetric{name: name, labels: map[string]bool{}}
				out[name] = metric
			}
			metric.suffixed = metric.suffixed || kind == "Histogram" || kind == "Summary"
			// Only the vector constructors take labels, and only
			// their second argument is that list: a []string
			// literal anywhere else in the call is data.
			var labels []string
			if strings.HasSuffix(sel.Sel.Name, "Vec") && len(call.Args) > 1 {
				labels = stringSlice(call.Args[1])
				sort.Strings(labels)
				for _, label := range labels {
					metric.labels[label] = true
				}
			}
			metric.decls = append(metric.decls, declaration{
				where:  fmt.Sprintf("%s:%d", where, fset.Position(call.Pos()).Line),
				labels: labels,
			})
			return true
		})
	}
	return found, nil
}

// optsName reads the Name field of a prometheus.*Opts literal among a
// constructor's arguments, and which kind of options it is.
func optsName(args []ast.Expr) (string, string, bool) {
	for _, arg := range args {
		lit, ok := arg.(*ast.CompositeLit)
		if !ok {
			continue
		}
		sel, ok := lit.Type.(*ast.SelectorExpr)
		if !ok || !strings.HasSuffix(sel.Sel.Name, "Opts") {
			continue
		}
		for _, elt := range lit.Elts {
			kv, ok := elt.(*ast.KeyValueExpr)
			if !ok {
				continue
			}
			key, ok := kv.Key.(*ast.Ident)
			if !ok || key.Name != "Name" {
				continue
			}
			if name := stringLit(kv.Value); name != "" {
				return name, strings.TrimSuffix(sel.Sel.Name, "Opts"), true
			}
		}
	}
	return "", "", false
}

// queryFile is one file that queries metrics, and the expressions in it.
type queryFile struct {
	file  string
	exprs []string
}

func readQueries(root string) ([]queryFile, error) {
	var out []queryFile

	alertsPath := filepath.Join(root, "infra", "prometheus", "alerts.yml")
	doc, err := readYAML(alertsPath)
	if err != nil {
		return nil, err
	}
	var exprs []string
	for _, group := range seqItems(mapValue(doc, "groups")) {
		for _, rule := range seqItems(mapValue(group, "rules")) {
			if v := mapValue(rule, "expr"); v != nil {
				exprs = append(exprs, v.Value)
			}
		}
	}
	out = append(out, queryFile{file: relPath(root, alertsPath), exprs: exprs})

	dashboards := filepath.Join(root, "infra", "grafana", "dashboards")
	entries, err := os.ReadDir(dashboards)
	if err != nil {
		return nil, fmt.Errorf("check-metrics-wiring: %w", err)
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		full := filepath.Join(dashboards, entry.Name())
		raw, err := os.ReadFile(full) // #nosec G304 -- path built from a directory walk of this repository
		if err != nil {
			return nil, err
		}
		var doc any
		if err := json.Unmarshal(raw, &doc); err != nil {
			return nil, fmt.Errorf("check-metrics-wiring: %s: %w", relPath(root, full), err)
		}
		out = append(out, queryFile{file: relPath(root, full), exprs: exprValues(doc)})
	}
	return out, nil
}

// exprValues collects every "expr" string in a dashboard, at whatever
// depth. Panels nest inside rows and rows inside the dashboard, and a
// walk of the whole document reads a layout this has never seen.
func exprValues(node any) []string {
	var out []string
	switch v := node.(type) {
	case map[string]any:
		for key, value := range v {
			if key == "expr" {
				if text, ok := value.(string); ok && text != "" {
					out = append(out, text)
				}
				continue
			}
			out = append(out, exprValues(value)...)
		}
	case []any:
		for _, item := range v {
			out = append(out, exprValues(item)...)
		}
	}
	return out
}

func withFiles(index map[string]map[string]bool) []string {
	var out []string
	for key, files := range index {
		var names []string
		for file := range files {
			names = append(names, file)
		}
		sort.Strings(names)
		out = append(out, fmt.Sprintf("%-52s %s", key, strings.Join(names, ", ")))
	}
	sort.Strings(out)
	return out
}

func plain(values []string) []string {
	out := append([]string(nil), values...)
	sort.Strings(out)
	return out
}

func printCensus(what string, lines []string) {
	fmt.Printf("  %d %s\n", len(lines), what)
	for _, line := range lines {
		fmt.Printf("    %s\n", line)
	}
}

// ----- shared helpers -----

// portOf reads the port out of a bind address or a target.
//
// Only a literal is accepted. A value carrying a substitution expands
// from a file this cannot read, and an unresolved value has to be
// refused rather than assumed: the whole point of the join is that two
// places agreeing by coincidence is what went unnoticed.
func portOf(addr string) (int, error) {
	text := addr
	if at := strings.LastIndex(addr, ":"); at >= 0 {
		text = addr[at+1:]
	}
	if text == "" {
		return 0, fmt.Errorf("which names no port")
	}
	if strings.Contains(addr, "$") {
		return 0, fmt.Errorf("which is a substitution rather than a literal port")
	}
	port, err := strconv.Atoi(text)
	if err != nil || port < 1 || port > 65535 {
		return 0, fmt.Errorf("whose port %q is not a number between 1 and 65535", text)
	}
	return port, nil
}

// goFiles are an application's own Go sources: tests are excluded
// because a listener a test starts is not one the deployment scrapes.
func goFiles(dir string) ([]string, error) {
	var out []string
	err := filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch d.Name() {
			case "node_modules", "dist", "coverage", "testdata", ".git":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
			return nil
		}
		out = append(out, p)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("check-metrics-wiring: %w", err)
	}
	sort.Strings(out)
	return out, nil
}

func readYAML(path string) (*yaml.Node, error) {
	raw, err := os.ReadFile(path) // #nosec G304 -- path built from the repository root this runs in
	if err != nil {
		return nil, fmt.Errorf("check-metrics-wiring: %w", err)
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("check-metrics-wiring: %s: %w", path, err)
	}
	if len(doc.Content) == 0 {
		return nil, fmt.Errorf("check-metrics-wiring: %s: empty document", path)
	}
	return doc.Content[0], nil
}

// mapValue returns the value node for key in a YAML mapping, or nil.
func mapValue(node *yaml.Node, key string) *yaml.Node {
	if node == nil || node.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(node.Content); i += 2 {
		if node.Content[i].Value == key {
			return node.Content[i+1]
		}
	}
	return nil
}

// seqItems returns the elements of a YAML sequence, or nothing.
func seqItems(node *yaml.Node) []*yaml.Node {
	if node == nil || node.Kind != yaml.SequenceNode {
		return nil
	}
	return node.Content
}

func isSelector(expr ast.Expr, pkg, name string) bool {
	sel, ok := expr.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != name {
		return false
	}
	ident, ok := sel.X.(*ast.Ident)
	return ok && ident.Name == pkg
}

// stringLit is the value of a string literal expression, empty for
// anything else.
func stringLit(expr ast.Expr) string {
	lit, ok := expr.(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return ""
	}
	value, err := strconv.Unquote(lit.Value)
	if err != nil {
		return ""
	}
	return value
}

// stringLitRaw is a quoted literal with its quotes removed, for tags,
// which are written in backticks and are not valid Go string syntax to
// unquote twice.
func stringLitRaw(text string) string {
	if unquoted, err := strconv.Unquote(text); err == nil {
		return unquoted
	}
	return text
}

// stringSlice reads a []string{…} literal.
func stringSlice(expr ast.Expr) []string {
	lit, ok := expr.(*ast.CompositeLit)
	if !ok {
		return nil
	}
	if arr, ok := lit.Type.(*ast.ArrayType); !ok || !isIdent(arr.Elt, "string") {
		return nil
	}
	var out []string
	for _, elt := range lit.Elts {
		if value := stringLit(elt); value != "" {
			out = append(out, value)
		}
	}
	return out
}

func isIdent(expr ast.Expr, name string) bool {
	ident, ok := expr.(*ast.Ident)
	return ok && ident.Name == name
}

// repoRoot is the directory the three sources are read from. An
// argument overrides the search, which is what lets this be pointed at
// a tree it has never seen to confirm it reports emptiness there rather
// than success.
func repoRoot(args []string) (string, error) {
	if len(args) > 0 && args[0] != "" {
		return filepath.Abs(args[0])
	}
	wd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	dir := wd
	for {
		if _, err := os.Stat(filepath.Join(dir, "compose.yml")); err == nil {
			if _, err := os.Stat(filepath.Join(dir, "scripts", "check-metrics-wiring.go")); err == nil {
				return dir, nil
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("check-metrics-wiring: could not locate repository root from %s", wd)
		}
		dir = parent
	}
}

// relPath renders a path relative to the repository root, falling back
// to the absolute path when it is outside.
func relPath(root, p string) string {
	rel, err := filepath.Rel(root, p)
	if err != nil {
		return p
	}
	return rel
}
