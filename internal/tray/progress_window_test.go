package tray

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"runtime"
	"testing"
)

// TestProgressWindowDoesNotDoubleCreate is a structural regression test for
// the exact bug that shipped: "Deploy Cloudflare Worker opens multiple
// progress windows". The root cause was calling declarative.MainWindow's
// Create() to grab widget handles early, then calling that SAME
// declarative.MainWindow value's Run() to pump messages — but
// declarative.MainWindow.Run() unconditionally calls Create() again
// internally (see lxn/walk/declarative/mainwindow.go), building a second,
// independent native window every single time the dialog was shown.
//
// This doesn't require a real desktop (unlike ShowProcessDialogForTest /
// TestUIProgressWindowSingleInstance in main_test.go), so it runs in every
// `go test ./...` invocation, including -short and CI without a GUI
// session — it parses process_dialog.go's real source as an AST and
// asserts that no identifier which receives a `.Create()` call also
// receives a `.Run()` call within the same function body. That is
// precisely the shape of the bug: Create() then Run() on the same
// declarative value, instead of Run() on the *walk.MainWindow it produced.
func TestProgressWindowDoesNotDoubleCreate(t *testing.T) {
	path := progressDialogSourcePath(t)

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, path, nil, 0)
	if err != nil {
		t.Fatalf("failed to parse %s: %v", path, err)
	}

	foundCreateCall := false

	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}

		createIdents := receiverIdentsForMethod(fn.Body, "Create")
		runIdents := receiverIdentsForMethod(fn.Body, "Run")

		if len(createIdents) > 0 {
			foundCreateCall = true
		}

		for ident := range createIdents {
			if runIdents[ident] {
				t.Errorf(
					"func %s calls both %s.Create() and %s.Run() — this is the exact shape of the "+
						"duplicate-progress-window bug: declarative.MainWindow.Run() unconditionally calls "+
						"Create() again internally, so calling Create() explicitly and then Run() on the same "+
						"value opens a SECOND native window every time. Pump the message loop via the "+
						"already-created native window instead (e.g. the *walk.MainWindow assigned via AssignTo), "+
						"never via the declarative struct's own Run()",
					fn.Name.Name, ident, ident,
				)
			}
		}
	}

	if !foundCreateCall {
		t.Fatal("expected to find at least one *.Create() call in process_dialog.go — has newProgressWindow been restructured?")
	}
}

// progressDialogSourcePath resolves process_dialog.go's absolute path
// relative to this test file's own location (via runtime.Caller), which is
// robust regardless of the working directory `go test` happens to be
// invoked from.
func progressDialogSourcePath(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller(0) failed to resolve this test file's path")
	}
	return filepath.Join(filepath.Dir(thisFile), "process_dialog.go")
}

// receiverIdentsForMethod walks body and collects the set of identifier
// names that appear as the receiver of a call to a method named
// methodName, i.e. `<ident>.<methodName>(...)`.
func receiverIdentsForMethod(body ast.Node, methodName string) map[string]bool {
	idents := map[string]bool{}
	ast.Inspect(body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != methodName {
			return true
		}
		ident, ok := sel.X.(*ast.Ident)
		if !ok {
			return true
		}
		idents[ident.Name] = true
		return true
	})
	return idents
}
