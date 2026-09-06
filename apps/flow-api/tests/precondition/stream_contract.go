package precondition

// The stream half of this package: whether every event kind the SSE
// stream can put on the wire is one the browser is able to accept.
//
// The wire format is declared twice. `apps/flow-api/internal/stream`
// declares `Kind`, the closed set of families the stream emits, as Go
// constants; the web app declares `StreamKind` as a union of string
// literals and switches on it to decide which query keys an event
// invalidates. Nothing but a comment holds the two together, and a Go
// kind absent from the union is a type error nowhere:
//
//	Go        a constant of type Kind in the stream package, together
//	          with the wire string it is assigned. The string is what
//	          reaches the browser; the constant name is only how a
//	          failure points at the declaration.
//	union     a string literal between the `=` of the StreamKind type
//	          alias and the `;` that closes it. Nothing outside that
//	          span counts, because the same file quotes every kind
//	          again as a switch label.
//	admitted  a Go wire string that appears as a union member.
//
// A Go kind the union does not admit is a defect with no exemption. The
// reverse is not the same statement: a union member with no Go constant
// sends nothing and breaks nothing, so those carry a written reason
// instead — see [streamContractScope.Violations] for both directions and
// for what this does not look at.

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// The two declarations, relative to the repository root.
const (
	// streamKindPackageDir holds the Go constants. The whole package is
	// read rather than the one file that declares them today, so moving a
	// constant to a neighbouring file keeps it in scope instead of
	// silently dropping it out.
	streamKindPackageDir = "apps/flow-api/internal/stream"
	// streamUnionFile holds the frontend union and the switch that
	// consumes it.
	streamUnionFile = "apps/flow-web/src/features/realtime/event-to-keys.ts"
	// streamUnionTypeName is the type alias the union is declared as.
	streamUnionTypeName = "StreamKind"
	// streamKindTypeName is the Go type whose constants are the kinds.
	streamKindTypeName = "Kind"
)

// StreamKindConst is one Go stream kind: the constant a failure names and
// the wire string that reaches the browser.
type StreamKindConst struct {
	// Name is the Go constant.
	Name string
	// Wire is the string it is assigned, which is the value of the
	// `kind` field on the wire.
	Wire string
	// File and Line are the declaration.
	File string
	Line int
}

// Location renders the declaration for a failure message.
func (k StreamKindConst) Location() string {
	return k.File + ":" + strconv.Itoa(k.Line)
}

// UnionMember is one string literal the frontend union admits.
type UnionMember struct {
	// Wire is the literal's value.
	Wire string
	// File and Line are where it sits inside the union declaration.
	File string
	Line int
}

// Location renders the member for a failure message.
func (m UnionMember) Location() string {
	return m.File + ":" + strconv.Itoa(m.Line)
}

// UnionOnlyKind is one union member that no Go stream kind declares,
// together with why it has no counterpart.
type UnionOnlyKind struct {
	// Wire is the member.
	Wire string
	// Reason is what stands in place of a Go constant. It is carried in
	// the entry rather than in a comment above the list so it cannot end
	// up describing the neighbouring line.
	Reason string
}

// ContractViolation is one failure of the rule, carrying the message the
// test prints. The text is built here rather than at the call site so
// the control tests read the same words a real failure would.
type ContractViolation struct {
	// Wire is the kind the violation is about.
	Wire string
	// Message is the failure.
	Message string
}

// streamContractScope is the two declarations, read.
type streamContractScope struct {
	root string
	// kinds are the Go constants, sorted by constant name.
	kinds []StreamKindConst
	// members are the union's literals, in declaration order.
	members []UnionMember
	// unionLine is the line the type alias starts on, so a failure about
	// the union as a whole has somewhere to point.
	unionLine int
	// residue is whatever sits inside the union span that is neither a
	// string literal, a separator nor a comment. It is empty for a union
	// of string literals, and anything in it is a member the scan cannot
	// read — which would otherwise pass as admitting nothing.
	residue string
	// goFiles counts the package files read, and kindTypeDeclared records
	// whether the package still declares the type whose constants are
	// being looked for. An empty result and a derivation that has stopped
	// matching look the same from the outside; these tell them apart.
	goFiles          int
	kindTypeDeclared bool
}

// parseStreamContract reads the Go constants and the frontend union.
func parseStreamContract(root string) (*streamContractScope, error) {
	scope := &streamContractScope{root: root}
	if err := scope.readGoKinds(); err != nil {
		return nil, err
	}
	if err := scope.readUnion(); err != nil {
		return nil, err
	}
	return scope, nil
}

// readGoKinds reads every `Name Kind = "wire"` constant out of the stream
// package.
//
// The constants are read from the syntax tree, not from the text. Every
// kind's doc comment quotes the families it fires on and one of them
// quotes another kind's wire string outright, so a text scan would take a
// sentence about a kind as the declaration of one.
func (s *streamContractScope) readGoKinds() error {
	dir := filepath.Join(s.root, filepath.FromSlash(streamKindPackageDir))
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		rel := streamKindPackageDir + "/" + name
		fset := token.NewFileSet()
		parsed, perr := parser.ParseFile(fset, filepath.Join(dir, name), nil, 0)
		if perr != nil {
			return perr
		}
		s.goFiles++
		s.readFileKinds(parsed, fset, rel)
	}
	sort.Slice(s.kinds, func(i, j int) bool { return s.kinds[i].Name < s.kinds[j].Name })
	return nil
}

// readFileKinds collects one file's kind constants and notes whether it
// declares the kind type itself.
//
// A constant's type carries over inside a const block: the first spec
// names it and the rest may leave it out. Following the carry-over can in
// principle pick up an untyped sibling declared in the same block, which
// reports a string that is not a kind. That is the safe direction — a
// false report is read and deleted, whereas a kind skipped for having no
// type of its own is exactly the silence this rule exists to break.
func (s *streamContractScope) readFileKinds(file *ast.File, fset *token.FileSet, rel string) {
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok {
			continue
		}
		if gen.Tok == token.TYPE {
			for _, spec := range gen.Specs {
				if ts, isType := spec.(*ast.TypeSpec); isType && ts.Name.Name == streamKindTypeName {
					s.kindTypeDeclared = true
				}
			}
			continue
		}
		if gen.Tok != token.CONST {
			continue
		}
		typed := false
		for _, spec := range gen.Specs {
			vs, isValue := spec.(*ast.ValueSpec)
			if !isValue {
				continue
			}
			if vs.Type != nil {
				ident, isIdent := vs.Type.(*ast.Ident)
				typed = isIdent && ident.Name == streamKindTypeName
			}
			if !typed || len(vs.Names) != 1 || len(vs.Values) != 1 {
				continue
			}
			lit, isLit := vs.Values[0].(*ast.BasicLit)
			if !isLit || lit.Kind != token.STRING {
				continue
			}
			wire, uerr := strconv.Unquote(lit.Value)
			if uerr != nil {
				continue
			}
			s.kinds = append(s.kinds, StreamKindConst{
				Name: vs.Names[0].Name,
				Wire: wire,
				File: rel,
				Line: fset.Position(vs.Pos()).Line,
			})
		}
	}
}

// readUnion reads the string literals the frontend union admits.
//
// The read is anchored to the type alias: it begins at the `=` and ends
// at the `;` that closes the declaration, and only literals between the
// two count. The anchor is the whole of the discrimination. Every kind
// the union admits is quoted a second time in the same file as a switch
// label, and several are quoted a third time in the doc comments, so a
// scan of the file at large would answer "admitted" for a kind the union
// does not declare — which is the one reading under which this rule
// passes on the defect it exists for.
//
// Comments inside the span are skipped for the same reason: a kind named
// in prose beside the union is not a member of it.
func (s *streamContractScope) readUnion() error {
	path := filepath.Join(s.root, filepath.FromSlash(streamUnionFile))
	raw, err := os.ReadFile(path) //#nosec G304 -- repository path resolved at test time
	if err != nil {
		return err
	}
	text := string(raw)

	head, ok := unionDeclarationIndex(text, streamUnionTypeName)
	if !ok {
		return fmt.Errorf("precondition: %s declares no %s type", streamUnionFile, streamUnionTypeName)
	}
	s.unionLine = 1 + strings.Count(text[:head], "\n")
	assign := strings.IndexByte(text[head:], '=')
	if assign < 0 {
		return fmt.Errorf("precondition: the %s declaration in %s has no body", streamUnionTypeName, streamUnionFile)
	}

	line := s.unionLine + strings.Count(text[head:head+assign], "\n")
	body := text[head+assign+1:]
	var residue strings.Builder
	for i := 0; i < len(body); i++ {
		c := body[i]
		switch {
		case c == '\n':
			line++
		case c == ';':
			s.residue = residue.String()
			return nil
		case c == '/' && i+1 < len(body) && body[i+1] == '/':
			for i < len(body) && body[i] != '\n' {
				i++
			}
			line++
		case c == '/' && i+1 < len(body) && body[i+1] == '*':
			end := strings.Index(body[i+2:], "*/")
			if end < 0 {
				return fmt.Errorf("precondition: unterminated comment inside the %s declaration in %s",
					streamUnionTypeName, streamUnionFile)
			}
			line += strings.Count(body[i:i+2+end+2], "\n")
			i += 2 + end + 1
		case isQuoteByte(c):
			value, next, closed := readQuoted(body, i)
			if !closed {
				return fmt.Errorf("precondition: unterminated string inside the %s declaration in %s",
					streamUnionTypeName, streamUnionFile)
			}
			s.members = append(s.members, UnionMember{Wire: value, File: streamUnionFile, Line: line})
			line += strings.Count(body[i:next], "\n")
			i = next - 1
		case c == '|' || c == ' ' || c == '\t' || c == '\r':
		default:
			residue.WriteByte(c)
		}
	}
	return fmt.Errorf("precondition: the %s declaration in %s is not terminated",
		streamUnionTypeName, streamUnionFile)
}

// unionDeclarationIndex finds the `type <name>` that opens the union and
// returns the index just past the name.
//
// Both sides are checked for an identifier character so a type whose name
// merely contains the one being looked for cannot stand in for it.
func unionDeclarationIndex(text, name string) (int, bool) {
	needle := "type " + name
	from := 0
	for {
		at := strings.Index(text[from:], needle)
		if at < 0 {
			return 0, false
		}
		at += from
		end := at + len(needle)
		beforeOK := at == 0 || !isIdentByte(text[at-1])
		afterOK := end >= len(text) || !isIdentByte(text[end])
		if beforeOK && afterOK {
			return end, true
		}
		from = at + 1
	}
}

// readQuoted reads the string literal starting at start and returns its
// value, the index just past the closing quote, and whether it closed.
func readQuoted(text string, start int) (string, int, bool) {
	quote := text[start]
	var out strings.Builder
	for i := start + 1; i < len(text); i++ {
		switch text[i] {
		case '\\':
			if i+1 < len(text) {
				out.WriteByte(text[i+1])
				i++
			}
		case quote:
			return out.String(), i + 1, true
		default:
			out.WriteByte(text[i])
		}
	}
	return "", len(text), false
}

// isIdentByte reports whether the byte can appear inside an identifier.
func isIdentByte(b byte) bool {
	return b == '_' || b == '$' ||
		(b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9')
}

// Admits reports whether the union has the wire string as a member.
func (s *streamContractScope) Admits(wire string) bool {
	for _, member := range s.members {
		if member.Wire == wire {
			return true
		}
	}
	return false
}

// Declared returns the Go kind carrying the wire string, if any.
func (s *streamContractScope) Declared(wire string) (StreamKindConst, bool) {
	for _, kind := range s.kinds {
		if kind.Wire == wire {
			return kind, true
		}
	}
	return StreamKindConst{}, false
}

// Violations holds the two declarations to each other, given the written
// reasons for the members that have no Go counterpart.
//
// The asymmetry is deliberate and is the substance of the rule.
//
// A Go kind the union does not admit takes no exemption. The server can
// send it, and when it does the browser does not ignore it: keysForEvent
// switches on the union, falls off the end, and returns undefined, so the
// reader that iterates the result throws. That throw is caught by the
// reconnect loop, which discards it and reconnects — so the kind costs a
// dropped connection every time it is published, along with any frame
// buffered behind it, while the connection stays healthy enough that the
// polling fallback never engages.
//
// A union member with no Go kind is the opposite: nothing sends it, so
// nothing happens. Several are anticipating families the stream maps to
// "" today. Each one carries the reason it has no counterpart, and an
// entry is held to the tree from both sides — a member that gains a Go
// kind, or one that leaves the union, fails as stale. A list that only
// grows is documentation; one that rejects its own stale entries is a
// check.
//
// What this does not look at, so a green run is read for what it is:
//
//   - Whether the two sides mean the same thing by a kind. A wire string
//     present on both sides passes however wrongly keysForEvent maps it:
//     invalidating the wrong query key, or a key prefix that matches
//     nothing, is invisible here.
//   - Whether keysForEvent has a case per union member. It does not need
//     checking: the function declares a return type that excludes
//     undefined and the project compiles under `strict`, so a union member
//     with no case makes the end of the function reachable and TypeScript
//     rejects the file. That guarantee runs one way only — it says nothing
//     about a kind that never entered the union, which is this rule.
//   - Whether anything ever publishes a declared kind. The stream tap
//     decides that from the family table, and the family table has checks
//     of its own; a kind nothing routes to is still required here, because
//     what breaks the browser is the kind arriving, not how often.
//   - Anything outside the two declarations: the SSE handler's framing,
//     the notifier, the reconnect loop's own behaviour, and every other
//     consumer of the wire format.
//   - Any second frontend. Only the one union is read, so a kind admitted
//     there and nowhere else reads as admitted everywhere.
func (s *streamContractScope) Violations(entries []UnionOnlyKind) []ContractViolation {
	var out []ContractViolation

	for _, kind := range s.kinds {
		if s.Admits(kind.Wire) {
			continue
		}
		out = append(out, ContractViolation{
			Wire: kind.Wire,
			Message: fmt.Sprintf("%s declares %s (%q), which the %s union in %s:%d does not admit.\n"+
				"  The browser does not ignore the kind: keysForEvent switches on the union, has no case for "+
				"it, and returns undefined, so the SSE reader throws where it iterates the result and the "+
				"reconnect loop discards the throw. Every event of this kind costs the connection and any "+
				"frame buffered behind it, while the connection stays healthy enough that the polling "+
				"fallback never engages.\n"+
				"  Add %q to the union and give keysForEvent the keys it invalidates. There is no exemption "+
				"list for this direction: a kind the server can send and the browser cannot accept is a "+
				"defect, not a choice.",
				kind.Location(), kind.Name, kind.Wire, streamUnionTypeName, streamUnionFile, s.unionLine,
				kind.Wire),
		})
	}

	explained := map[string]bool{}
	for _, entry := range entries {
		explained[entry.Wire] = true
	}
	for _, member := range s.members {
		if _, ok := s.Declared(member.Wire); ok {
			continue
		}
		if explained[member.Wire] {
			continue
		}
		out = append(out, ContractViolation{
			Wire: member.Wire,
			Message: fmt.Sprintf("the %s union at %s admits %q, which no %s constant in %s declares and "+
				"unionOnlyKinds does not explain.\n"+
				"  A member with no Go counterpart sends nothing, so it is not wrong by itself — "+
				"streamKindForFamily maps several families to \"\" and the union may be anticipating one. "+
				"An unexplained one is indistinguishable from a kind that was dropped on the Go side and "+
				"left here.\n"+
				"  Add it to unionOnlyKinds with the reason nothing on the Go side carries it, or delete the "+
				"member and its keysForEvent case.",
				streamUnionTypeName, member.Location(), member.Wire, streamKindTypeName,
				streamKindPackageDir),
		})
	}

	for _, entry := range entries {
		if kind, ok := s.Declared(entry.Wire); ok {
			out = append(out, ContractViolation{
				Wire: entry.Wire,
				Message: fmt.Sprintf("unionOnlyKinds lists %q as having no Go counterpart, reason %q, but %s "+
					"declares it as %s.\n"+
					"  Drop the entry: a list that keeps an entry after the kind gains a Go constant records "+
					"what was once true and checks nothing.",
					entry.Wire, entry.Reason, kind.Location(), kind.Name),
			})
			continue
		}
		if !s.Admits(entry.Wire) {
			out = append(out, ContractViolation{
				Wire: entry.Wire,
				Message: fmt.Sprintf("unionOnlyKinds lists %q, which the %s union in %s no longer admits; drop "+
					"the stale entry.",
					entry.Wire, streamUnionTypeName, streamUnionFile),
			})
			continue
		}
		if strings.TrimSpace(entry.Reason) == "" {
			out = append(out, ContractViolation{
				Wire: entry.Wire,
				Message: fmt.Sprintf("unionOnlyKinds lists %q with no reason; an entry without one is "+
					"indistinguishable from a member nobody looked at.",
					entry.Wire),
			})
		}
	}

	return out
}
