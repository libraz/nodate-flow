package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"
)

// main is not callable from a test — it binds ports, opens a database
// and blocks on a signal — so this asserts the wiring rather than the
// behaviour it produces.
func TestShutdownWaitsForEveryManagedResident(t *testing.T) {
	t.Parallel()

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "main.go", nil, 0)
	if err != nil {
		t.Fatalf("parse main.go: %v", err)
	}
	body := mainBody(t, file)
	shutdown := shutdownStatements(t, body)

	direct, groups := managedHandleSources(body)
	if len(direct)+len(groups) == 0 {
		t.Fatal("main.go starts no bgloop.StartManaged handles; shutdown cannot prove completion of resident loops")
	}
	if !managedStartsUseRootWorkContext(body) {
		t.Fatal("every bgloop.StartManaged resident must derive from the shared workCtx")
	}
	for _, handle := range direct {
		if !addManagedCall(shutdown, handle) {
			t.Errorf("managed handle %q is not registered in the shutdown resident set", handle)
		}
	}
	for _, group := range groups {
		if !managedGroupIsDrained(shutdown, group) {
			t.Errorf("managed handles appended to %q are not all registered and waited", group)
		}
	}

	addManaged := inlineFunction(body, "addManaged")
	if addManaged == nil || !residentFields(addManaged, "handle", "Stop", "Wait") {
		t.Fatal("addManaged must register each handle's Stop and Wait callbacks as a lifecycle.Resident")
	}

	wantResidents := map[string]string{
		"scheduler":      "Wait",
		"webhookWorker":  "Wait",
		"importWorker":   "Wait",
		"storageSweeper": "Wait",
		"agentPurger":    "Wait",
	}
	for component, waitMethod := range residentComponents(body) {
		if wantResidents[component] == waitMethod {
			delete(wantResidents, component)
		}
	}
	if len(wantResidents) != 0 {
		missing := make([]string, 0, len(wantResidents))
		for component, method := range wantResidents {
			missing = append(missing, component+"."+method)
		}
		t.Fatalf("resident components lack paired BeginStop and completion Wait callbacks: %s", strings.Join(missing, ", "))
	}

	if !shutdownConfigUsesResidents(body) {
		t.Fatal("lifecycle.NewShutdown is not configured with the resident set")
	}
	if !shutdownConfigUsesDeadline(body) {
		t.Fatal("lifecycle.NewShutdown must receive the first captured stop deadline")
	}
	if !hasShutdownCalls(shutdown) {
		t.Fatal("the post-listener shutdown sequence must call BeginStop and wait for lifecycle.Shutdown")
	}
	if !watchdogStoppedAfterDrain(shutdown) {
		t.Fatal("the startup deadline watchdog must be stopped after the coordinator finishes draining")
	}
	if hasLegacyStartCall(body) {
		t.Fatal("main.go still uses non-waitable bgloop.Start; use StartManaged and register its Wait")
	}
}

// TestShutdownEndsOpenStreams checks the API server closes the stream
// shutdown signal its router hands to SSE handlers; without both halves an
// open stream holds Server.Shutdown to its deadline.
func TestShutdownEndsOpenStreams(t *testing.T) {
	t.Parallel()

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "main.go", nil, 0)
	if err != nil {
		t.Fatalf("parse main.go: %v", err)
	}
	body := mainBody(t, file)

	installed, registered := false, false
	ast.Inspect(body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		base, ok := sel.X.(*ast.Ident)
		if !ok {
			return true
		}
		switch {
		case base.Name == "lifecycle" && sel.Sel.Name == "WithStreamShutdown":
			installed = true
		case base.Name == "srv" && sel.Sel.Name == "RegisterOnShutdown":
			registered = true
		}
		return true
	})
	if !installed {
		t.Error("the API router does not install lifecycle.WithStreamShutdown")
	}
	if !registered {
		t.Error("the API server does not close the stream shutdown signal through srv.RegisterOnShutdown")
	}
}

func mainBody(t *testing.T, file *ast.File) *ast.BlockStmt {
	t.Helper()
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if ok && fn.Recv == nil && fn.Name.Name == "main" {
			return fn.Body
		}
	}
	t.Fatal("main.go has no func main")
	return nil
}

func shutdownStatements(t *testing.T, body *ast.BlockStmt) []ast.Stmt {
	t.Helper()
	for i, stmt := range body.List {
		if _, ok := stmt.(*ast.SelectStmt); ok {
			return body.List[i+1:]
		}
	}
	t.Fatal("main no longer waits on a select before shutting down")
	return nil
}

// managedHandleSources finds direct managed-handle variables and slices that
// collect handles returned by StartManaged.
func managedHandleSources(body *ast.BlockStmt) (direct, groups []string) {
	seenDirect, seenGroups := map[string]bool{}, map[string]bool{}
	ast.Inspect(body, func(n ast.Node) bool {
		assign, ok := n.(*ast.AssignStmt)
		if !ok || !containsManagedStart(assign.Rhs) {
			return true
		}
		for _, lhs := range assign.Lhs {
			ident, ok := lhs.(*ast.Ident)
			if !ok || ident.Name == "_" {
				continue
			}
			if containsAppendWithManagedStart(assign.Rhs, ident.Name) {
				if !seenGroups[ident.Name] {
					seenGroups[ident.Name] = true
					groups = append(groups, ident.Name)
				}
			} else if containsDirectManagedStart(assign.Rhs) && !seenDirect[ident.Name] {
				seenDirect[ident.Name] = true
				direct = append(direct, ident.Name)
			}
		}
		return true
	})
	return direct, groups
}

func containsManagedStart(exprs []ast.Expr) bool {
	return containsDirectManagedStart(exprs)
}

func containsDirectManagedStart(exprs []ast.Expr) bool {
	for _, expr := range exprs {
		found := false
		ast.Inspect(expr, func(n ast.Node) bool {
			if isManagedStart(n) {
				found = true
				return false
			}
			return true
		})
		if found {
			return true
		}
	}
	return false
}

func containsAppendWithManagedStart(exprs []ast.Expr, group string) bool {
	for _, expr := range exprs {
		call, ok := expr.(*ast.CallExpr)
		if !ok {
			continue
		}
		ident, ok := call.Fun.(*ast.Ident)
		if !ok || ident.Name != "append" || len(call.Args) < 2 {
			continue
		}
		first, ok := call.Args[0].(*ast.Ident)
		if ok && first.Name == group && containsDirectManagedStart(call.Args[1:]) {
			return true
		}
	}
	return false
}

func isManagedStart(n ast.Node) bool {
	call, ok := n.(*ast.CallExpr)
	if !ok {
		return false
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "StartManaged" {
		return false
	}
	pkg, ok := sel.X.(*ast.Ident)
	return ok && pkg.Name == "bgloop"
}

func managedStartsUseRootWorkContext(body *ast.BlockStmt) bool {
	found, valid := false, true
	ast.Inspect(body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok || !isManagedStart(call) {
			return true
		}
		found = true
		if len(call.Args) == 0 {
			valid = false
			return false
		}
		ctx, ok := call.Args[0].(*ast.Ident)
		if !ok || ctx.Name != "workCtx" {
			valid = false
			return false
		}
		return true
	})
	return found && valid
}

func addManagedCall(stmts []ast.Stmt, handle string) bool {
	for _, stmt := range stmts {
		found := false
		ast.Inspect(stmt, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			fn, ok := call.Fun.(*ast.Ident)
			if !ok || fn.Name != "addManaged" || len(call.Args) != 1 {
				return true
			}
			arg, ok := call.Args[0].(*ast.Ident)
			found = ok && arg.Name == handle
			return !found
		})
		if found {
			return true
		}
	}
	return false
}

func managedGroupIsDrained(stmts []ast.Stmt, group string) bool {
	for _, stmt := range stmts {
		rangeStmt, ok := stmt.(*ast.RangeStmt)
		if !ok {
			continue
		}
		rangeExpr, ok := rangeStmt.X.(*ast.Ident)
		if !ok || rangeExpr.Name != group {
			continue
		}
		value, ok := rangeStmt.Value.(*ast.Ident)
		if !ok {
			continue
		}
		if addManagedCall([]ast.Stmt{rangeStmt.Body}, value.Name) {
			return true
		}
	}
	return false
}

func inlineFunction(body *ast.BlockStmt, name string) *ast.BlockStmt {
	var result *ast.BlockStmt
	ast.Inspect(body, func(n ast.Node) bool {
		assign, ok := n.(*ast.AssignStmt)
		if !ok {
			return true
		}
		for i, lhs := range assign.Lhs {
			ident, ok := lhs.(*ast.Ident)
			if !ok || ident.Name != name || i >= len(assign.Rhs) {
				continue
			}
			lit, ok := assign.Rhs[i].(*ast.FuncLit)
			if ok {
				result = lit.Body
				return false
			}
		}
		return true
	})
	return result
}

func residentFields(body *ast.BlockStmt, receiver, stopMethod, waitMethod string) bool {
	stopFound, waitFound := false, false
	ast.Inspect(body, func(n ast.Node) bool {
		lit, ok := n.(*ast.CompositeLit)
		if !ok || !isLifecycleResident(lit.Type) {
			return true
		}
		for _, elt := range lit.Elts {
			kv, ok := elt.(*ast.KeyValueExpr)
			if !ok {
				continue
			}
			key, ok := kv.Key.(*ast.Ident)
			if !ok {
				continue
			}
			selector, ok := kv.Value.(*ast.SelectorExpr)
			if !ok {
				continue
			}
			base, ok := selector.X.(*ast.Ident)
			if !ok || base.Name != receiver {
				continue
			}
			if key.Name == "BeginStop" && selector.Sel.Name == stopMethod {
				stopFound = true
			}
			if key.Name == "Wait" && selector.Sel.Name == waitMethod {
				waitFound = true
			}
		}
		return true
	})
	return stopFound && waitFound
}

func residentComponents(body *ast.BlockStmt) map[string]string {
	components := map[string]string{}
	collect := func(lit *ast.CompositeLit) {
		var stopReceiver, waitReceiver, waitMethod string
		for _, elt := range lit.Elts {
			kv, ok := elt.(*ast.KeyValueExpr)
			if !ok {
				continue
			}
			key, ok := kv.Key.(*ast.Ident)
			if !ok {
				continue
			}
			selector, ok := kv.Value.(*ast.SelectorExpr)
			if !ok {
				continue
			}
			base, ok := selector.X.(*ast.Ident)
			if !ok {
				continue
			}
			switch key.Name {
			case "BeginStop":
				stopReceiver = base.Name
			case "Wait":
				waitReceiver, waitMethod = base.Name, selector.Sel.Name
			}
		}
		if stopReceiver != "" && stopReceiver == waitReceiver {
			components[stopReceiver] = waitMethod
		}
	}
	ast.Inspect(body, func(n ast.Node) bool {
		lit, ok := n.(*ast.CompositeLit)
		if !ok {
			return true
		}
		if isLifecycleResident(lit.Type) {
			collect(lit)
			return false
		}
		if array, ok := lit.Type.(*ast.ArrayType); ok && isLifecycleResident(array.Elt) {
			for _, element := range lit.Elts {
				if resident, ok := element.(*ast.CompositeLit); ok {
					collect(resident)
				}
			}
			return false
		}
		return true
	})
	return components
}

func isLifecycleResident(expr ast.Expr) bool {
	sel, ok := expr.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "Resident" {
		return false
	}
	pkg, ok := sel.X.(*ast.Ident)
	return ok && pkg.Name == "lifecycle"
}

func shutdownConfigUsesResidents(body *ast.BlockStmt) bool {
	found := false
	ast.Inspect(body, func(n ast.Node) bool {
		lit, ok := n.(*ast.CompositeLit)
		if !ok {
			return true
		}
		sel, ok := lit.Type.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "ShutdownConfig" {
			return true
		}
		pkg, ok := sel.X.(*ast.Ident)
		if !ok || pkg.Name != "lifecycle" {
			return true
		}
		for _, elt := range lit.Elts {
			kv, ok := elt.(*ast.KeyValueExpr)
			if !ok {
				continue
			}
			key, ok := kv.Key.(*ast.Ident)
			value, valueOK := kv.Value.(*ast.Ident)
			if ok && valueOK && key.Name == "Residents" && value.Name == "residents" {
				found = true
				return false
			}
		}
		return true
	})
	return found
}

func shutdownConfigUsesDeadline(body *ast.BlockStmt) bool {
	found := false
	ast.Inspect(body, func(n ast.Node) bool {
		lit, ok := n.(*ast.CompositeLit)
		if !ok || !isShutdownConfig(lit.Type) {
			return true
		}
		for _, elt := range lit.Elts {
			kv, ok := elt.(*ast.KeyValueExpr)
			if !ok {
				continue
			}
			key, keyOK := kv.Key.(*ast.Ident)
			value, valueOK := kv.Value.(*ast.Ident)
			if keyOK && valueOK && key.Name == "Deadline" && value.Name == "deadline" {
				found = true
				return false
			}
		}
		return true
	})
	return found
}

func isShutdownConfig(expr ast.Expr) bool {
	sel, ok := expr.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "ShutdownConfig" {
		return false
	}
	pkg, ok := sel.X.(*ast.Ident)
	return ok && pkg.Name == "lifecycle"
}

func hasShutdownCalls(stmts []ast.Stmt) bool {
	begin, wait := false, false
	ast.Inspect(&ast.BlockStmt{List: stmts}, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		base, ok := sel.X.(*ast.Ident)
		if !ok || base.Name != "shutdown" {
			return true
		}
		if sel.Sel.Name == "BeginStop" {
			begin = true
		}
		if sel.Sel.Name == "Shutdown" {
			wait = true
		}
		return true
	})
	return begin && wait
}

func watchdogStoppedAfterDrain(stmts []ast.Stmt) bool {
	shutdownIndex, stopIndex := -1, -1
	for i, stmt := range stmts {
		ast.Inspect(stmt, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			if ident, ok := call.Fun.(*ast.Ident); ok && ident.Name == "stopDeadlineWatchdog" {
				stopIndex = i
			}
			if selector, ok := call.Fun.(*ast.SelectorExpr); ok && selector.Sel.Name == "Shutdown" {
				base, ok := selector.X.(*ast.Ident)
				if ok && base.Name == "shutdown" {
					shutdownIndex = i
				}
			}
			return true
		})
	}
	return shutdownIndex >= 0 && stopIndex > shutdownIndex
}

func hasLegacyStartCall(body *ast.BlockStmt) bool {
	found := false
	ast.Inspect(body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "Start" {
			return true
		}
		pkg, ok := sel.X.(*ast.Ident)
		found = ok && pkg.Name == "bgloop"
		return !found
	})
	return found
}
