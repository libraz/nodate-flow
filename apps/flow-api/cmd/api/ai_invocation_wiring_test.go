package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

// Passing the shared logger directly preserves every attribution field,
// including fields added later; a handwritten record copy silently drops them.
func TestAutonomousAIRunnersWireInvocationLogger(t *testing.T) {
	t.Parallel()
	file, err := parser.ParseFile(token.NewFileSet(), "main.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	foundConstructor := false
	found := map[string]bool{"ai.AgentExecutor": false, "signaljudge.Runner": false}
	ast.Inspect(file, func(node ast.Node) bool {
		if assignment, ok := node.(*ast.AssignStmt); ok && len(assignment.Lhs) == 1 && len(assignment.Rhs) == 1 {
			name, named := assignment.Lhs[0].(*ast.Ident)
			call, called := assignment.Rhs[0].(*ast.CallExpr)
			if named && called && name.Name == "invocationLogger" {
				if selector, ok := call.Fun.(*ast.SelectorExpr); ok {
					if pkg, ok := selector.X.(*ast.Ident); ok && pkg.Name == "router" && selector.Sel.Name == "NewDBInvocationLogger" && len(call.Args) == 2 {
						db, dbOK := call.Args[0].(*ast.Ident)
						publisher, publisherOK := call.Args[1].(*ast.Ident)
						foundConstructor = dbOK && publisherOK && db.Name == "db" && publisher.Name == "aiInvocationPublisher"
					}
				}
			}
		}
		literal, ok := node.(*ast.CompositeLit)
		if !ok {
			return true
		}
		selector, ok := literal.Type.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		pkg, ok := selector.X.(*ast.Ident)
		if !ok {
			return true
		}
		key := pkg.Name + "." + selector.Sel.Name
		if _, wanted := found[key]; !wanted {
			return true
		}
		for _, element := range literal.Elts {
			field, ok := element.(*ast.KeyValueExpr)
			if !ok {
				continue
			}
			name, ok := field.Key.(*ast.Ident)
			if !ok || name.Name != "Log" {
				continue
			}
			logger, ok := field.Value.(*ast.Ident)
			if !ok || logger.Name != "invocationLogger" {
				t.Errorf("%s must receive the complete shared invocation logger", key)
				continue
			}
			found[key] = true
		}
		return true
	})
	if !foundConstructor {
		t.Error("shared invocation logger must use the DB and stream publisher")
	}
	for name, wired := range found {
		if !wired {
			t.Errorf("%s has no shared logger", name)
		}
	}
}
