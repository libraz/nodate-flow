package kindscan

import (
	"go/ast"
	"go/types"
	"strings"
)

// KindField names a struct field that carries an event kind after the
// value has stopped being one.
//
// The columns an event lands in are plain text — `events.type` is
// VARCHAR, `notifications.event_type` likewise — so the params structs
// sqlc generates for them hold a string. Everything the [Kind] type
// buys is spent at that assignment: a literal, a local constant, a name
// assembled at run time all satisfy the field, and the kind reaches the
// row without any part of it having been declared. That is not a
// hypothetical; it is how `calendar.reminder` was written for as long as
// it existed.
//
// A field listed here must be written from a value that was an
// [github.com/libraz/nodate-flow/packages/go-shared/eventbus.Kind] —
// `string(eventbus.X)`. The conversion is the point: it forces the name
// through the registry, where a family covers it and the totality checks
// can see it.
type KindField struct {
	// Type is the fully qualified struct type, as the type checker spells
	// it. A type declared in the package being scanned is spelled with the
	// package's name rather than its import path, because that is the path
	// [Scan] type-checks it under; an imported type carries its full path.
	Type string
	// Field is the field's name in Go.
	Field string
}

// String renders the field as it appears in a message: the struct's own
// name and the field, without the import path that would bury it.
func (f KindField) String() string {
	return structName(f.Type) + "." + f.Field
}

// structName drops the import path from a qualified type name.
func structName(qualified string) string {
	if i := strings.LastIndex(qualified, "."); i >= 0 {
		return qualified[i+1:]
	}
	return qualified
}

// kindFields is the set every module scan holds to the rule.
//
// It names types in another module on purpose. The rule has to be the
// same wherever events are appended, and a list assembled per module is
// a rule that can be looser in the next one — the same reason
// [ScanModule] exists rather than each guard writing its own walk. These
// are strings the type checker compares against; nothing here imports
// flow-api.
//
// The list is not derived from anything, so a params struct added later
// for a query that also writes `events.type` is not covered until it is
// added here. [Packages] widens its walk by these names for the same
// reason it widens by "eventbus": a file that mentions neither cannot
// hold a write the scan would report.
var kindFields = []KindField{
	{Type: "github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated.AppendEventParams", Field: "Type"},
	{Type: "github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated.AppendAgentEventParams", Field: "Type"},
	{Type: "github.com/libraz/nodate-flow/apps/flow-api/internal/db/generated.CreateNotificationParams", Field: "EventType"},
}

// KindFields returns the set every module scan is held to, so the module
// that owns these structs can check the names still resolve. A string
// that no longer names a field turns the rule off in silence, which reads
// exactly like a scan that found nothing wrong.
func KindFields() []KindField {
	out := make([]KindField, len(kindFields))
	copy(out, kindFields)
	return out
}

// isKindField reports whether the named struct field is one the origin
// rule covers.
func isKindField(fields []KindField, typeName, field string) bool {
	for _, f := range fields {
		if f.Type == typeName && f.Field == field {
			return true
		}
	}
	return false
}

// fieldWrite is one assignment to a kind-bearing field that did not come
// from a Kind.
type fieldWrite struct {
	field KindField
	expr  ast.Expr
}

// compositeFieldWrites reports the kind-bearing fields lit sets from
// something other than a [Kind].
func compositeFieldWrites(info *types.Info, fields []KindField, lit *ast.CompositeLit) []fieldWrite {
	typeName, strct, ok := structOf(info.Types[lit].Type)
	if !ok {
		return nil
	}
	var out []fieldWrite
	for i, elt := range lit.Elts {
		var (
			name  string
			value ast.Expr
		)
		if kv, keyed := elt.(*ast.KeyValueExpr); keyed {
			id, isIdent := kv.Key.(*ast.Ident)
			if !isIdent {
				continue
			}
			name, value = id.Name, kv.Value
		} else {
			// A positional literal names no field, so the field is decided
			// by position. Params structs are written keyed everywhere, but
			// an unkeyed one is still a write and still has to answer.
			if i >= strct.NumFields() {
				continue
			}
			name, value = strct.Field(i).Name(), elt
		}
		if !isKindField(fields, typeName, name) || fromKind(info, value) {
			continue
		}
		out = append(out, fieldWrite{field: KindField{Type: typeName, Field: name}, expr: value})
	}
	return out
}

// assignFieldWrites reports the kind-bearing fields stmt sets from
// something other than a [Kind]. It covers the form the composite pass
// cannot see: a params struct filled in after it is declared.
func assignFieldWrites(info *types.Info, fields []KindField, stmt *ast.AssignStmt) []fieldWrite {
	// A multi-value right-hand side pairs one call with several targets,
	// so there is no expression per field to judge. Nothing produces a
	// kind that way today, and guessing would report the call rather than
	// the value.
	if len(stmt.Lhs) != len(stmt.Rhs) {
		return nil
	}
	var out []fieldWrite
	for i, lhs := range stmt.Lhs {
		sel, ok := ast.Unparen(lhs).(*ast.SelectorExpr)
		if !ok {
			continue
		}
		selection, ok := info.Selections[sel]
		if !ok {
			continue
		}
		recv := selection.Recv()
		if ptr, isPtr := types.Unalias(recv).(*types.Pointer); isPtr {
			recv = ptr.Elem()
		}
		typeName, _, ok := structOf(recv)
		if !ok {
			continue
		}
		name := sel.Sel.Name
		if !isKindField(fields, typeName, name) || fromKind(info, stmt.Rhs[i]) {
			continue
		}
		out = append(out, fieldWrite{field: KindField{Type: typeName, Field: name}, expr: stmt.Rhs[i]})
	}
	return out
}

// structOf returns the qualified name and underlying struct of a named
// struct type.
func structOf(t types.Type) (string, *types.Struct, bool) {
	if t == nil {
		return "", nil, false
	}
	named, ok := types.Unalias(t).(*types.Named)
	if !ok {
		return "", nil, false
	}
	strct, ok := named.Underlying().(*types.Struct)
	if !ok {
		return "", nil, false
	}
	obj := named.Obj()
	name := obj.Name()
	if obj.Pkg() != nil {
		name = obj.Pkg().Path() + "." + name
	}
	return name, strct, true
}

// fromKind reports whether expr's value was an event kind immediately
// before it became a string.
//
// Only the conversion at the write site counts. A string that was a Kind
// several statements ago is a string here, and following it back would
// mean evaluating the program; the narrow rule costs a caller one
// conversion and answers without guessing. Conversions nest, so
// `string(kind)` and the redundant `string(string(kind))` both pass, and
// a call that merely returns a string does not.
func fromKind(info *types.Info, expr ast.Expr) bool {
	expr = ast.Unparen(expr)
	if tv, ok := info.Types[expr]; ok && isKindType(tv.Type) {
		return true
	}
	call, ok := expr.(*ast.CallExpr)
	if !ok || len(call.Args) != 1 {
		return false
	}
	if tv, ok := info.Types[call.Fun]; !ok || !tv.IsType() {
		return false
	}
	return fromKind(info, call.Args[0])
}

// isKindType reports whether t is the event-kind type, seen through any
// alias a package declares to spare its call sites a second import.
func isKindType(t types.Type) bool {
	if t == nil {
		return false
	}
	named, ok := types.Unalias(t).(*types.Named)
	return ok && named.String() == KindTypeName
}

// fieldMarkers returns the struct names [Packages] widens its walk by, so
// a file that writes one of these fields is type-checked even when it
// never mentions the eventbus — which is exactly the file this rule
// exists for.
func fieldMarkers(fields []KindField) []string {
	out := make([]string, 0, len(fields))
	for _, f := range fields {
		out = append(out, structName(f.Type))
	}
	return out
}
