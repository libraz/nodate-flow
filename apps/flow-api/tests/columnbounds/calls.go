package columnbounds

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"sort"
	"strings"
)

// InputRef names one input type inside one handler package. The package is
// part of the identity because two packages spell the same noun and reach
// different statements with it.
type InputRef struct {
	Scope string
	Owner string
}

// CallIndex holds, for each input type, the identifiers of the methods the
// function that takes it calls.
//
// It is the second kind of evidence about where a field lands, and it is
// independent of the first: it reads what the handler does rather than what
// its type is called. That matters because an operation's name is under no
// obligation to spell a write — presigning an upload, proposing a plan and
// applying a recurrence all store something, and none of them says so in a
// verb this repository could enumerate. The next name nobody thought of
// would be back outside any such list, whereas a handler that writes has to
// call the statement that writes.
type CallIndex map[InputRef]map[string]bool

// Methods returns the calls recorded for one input type.
func (c CallIndex) Methods(scope, owner string) map[string]bool {
	return c[InputRef{Scope: scope, Owner: owner}]
}

// HandlerCallIndex reads the calls every input type's handler makes, across
// one handler tree.
func HandlerCallIndex(root, rel string) (CallIndex, error) {
	packages, order, err := readPackages(root, rel)
	if err != nil {
		return nil, err
	}
	out := CallIndex{}
	for _, pkg := range order {
		found, perr := ParseHandlerCalls(pkg, packages[pkg])
		if perr != nil {
			return nil, perr
		}
		for ref, methods := range found {
			if _, seen := out[ref]; !seen {
				out[ref] = map[string]bool{}
			}
			for name := range methods {
				out[ref][name] = true
			}
		}
	}
	return out, nil
}

// ParseHandlerCalls reads one handler package's calls, given its files keyed
// by path. It is exported so the control can drive a source it states in
// full through the same derivation the tree goes through.
//
// A handler is written two ways here — a method or plain function taking the
// input, and a closure returned by a factory that takes the dependencies —
// so the function is found by its parameter rather than by its position: any
// function, named or literal, that takes a pointer to a type named *Input.
// The calls of a closure nested inside a matching function are its calls
// too, which is what makes the factory form work without naming it.
//
// The reach stops at that function. Where a handler here delegates its write
// it does so to another package's constructor rather than to a helper beside
// it, and following that would mean deriving this surface's answer from code
// the handler tree does not contain — the statements are that package's, and
// which of its columns a field lands in is a question about it rather than
// about the handler. So the operations built that way stay unresolved, which
// the report says plainly.
func ParseHandlerCalls(pkg string, sources map[string]string) (CallIndex, error) {
	fset := token.NewFileSet()
	paths := make([]string, 0, len(sources))
	for path := range sources {
		paths = append(paths, path)
	}
	sort.Strings(paths)

	files := make([]*ast.File, 0, len(paths))
	for _, path := range paths {
		file, err := parser.ParseFile(fset, path, sources[path], 0)
		if err != nil {
			return nil, fmt.Errorf("parse %s: %w", path, err)
		}
		files = append(files, file)
	}

	out := CallIndex{}
	for _, file := range files {
		ast.Inspect(file, func(n ast.Node) bool {
			params, body := functionParts(n)
			if body == nil {
				return true
			}
			owners := inputParams(params)
			if len(owners) == 0 {
				return true
			}
			made := callsIn(body)
			for _, owner := range owners {
				ref := InputRef{Scope: pkg, Owner: owner}
				if _, seen := out[ref]; !seen {
					out[ref] = map[string]bool{}
				}
				for name := range made {
					out[ref][name] = true
				}
			}
			return true
		})
	}
	return out, nil
}

// callsIn reads the names a function body calls through a selector,
// including from any closure written inside it.
//
// A generated query method is always reached that way, whichever receiver it
// is on: the handler holds its queries in a dependency struct, and inside a
// transaction in a second one bound to the tx. What the selector's left side
// is called says nothing, so it is not read — the method's own name is the
// name the statement carries.
func callsIn(body *ast.BlockStmt) map[string]bool {
	out := map[string]bool{}
	ast.Inspect(body, func(n ast.Node) bool {
		if call, ok := n.(*ast.CallExpr); ok {
			if fun, isSelector := call.Fun.(*ast.SelectorExpr); isSelector {
				out[fun.Sel.Name] = true
			}
		}
		return true
	})
	return out
}

// functionParts returns the parameters and body of a node that is a
// function, named or literal, and a nil body for anything else.
func functionParts(n ast.Node) (*ast.FieldList, *ast.BlockStmt) {
	switch fn := n.(type) {
	case *ast.FuncDecl:
		if fn.Type == nil {
			return nil, nil
		}
		return fn.Type.Params, fn.Body
	case *ast.FuncLit:
		if fn.Type == nil {
			return nil, nil
		}
		return fn.Type.Params, fn.Body
	default:
		return nil, nil
	}
}

// inputParams returns the input types a parameter list takes a pointer to.
// The suffix is what an input is called in both handler trees, and taking
// one by pointer is how a request reaches the function that answers it.
func inputParams(params *ast.FieldList) []string {
	if params == nil {
		return nil
	}
	var out []string
	for _, field := range params.List {
		star, ok := field.Type.(*ast.StarExpr)
		if !ok {
			continue
		}
		ident, ok := star.X.(*ast.Ident)
		if !ok || !strings.HasSuffix(ident.Name, "Input") {
			continue
		}
		out = append(out, ident.Name)
	}
	return out
}
