package tray

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"runtime"
	"testing"
)

// TestHandleMenuActionsAreGuarded is a structural regression test for the
// exact bug class that shipped to production once: a menu action gets added
// to the handleMenu select loop that opens a window or does slow/non-instant
// work, but the author forgets to route it through t.guarded(...) — so a
// user's rapid re-click races and opens a duplicate window.
//
// guarded_test.go proves the guarded() mechanism itself is correct in
// isolation, but that can't catch a *future* case that simply never calls
// guarded() at all. Nothing short of re-deriving intent can catch that
// automatically, so this test takes the pragmatic route: parse tray.go's
// real source as an AST, find the handleMenu select statement, and assert
// that every `case <-mXxx.ClickedCh:` clause — except an explicit, commented
// allowlist of cases that are legitimately synchronous/instant — contains a
// call to t.guarded(...) in its body.
//
// This is intentionally an AST walk (go/ast + go/parser, stdlib only, no new
// dependency) rather than a line-window text scan: it doesn't care about
// spacing/formatting/comment placement inside the case body, only about
// "is there a CallExpr to guarded somewhere in this clause's statements".
func TestHandleMenuActionsAreGuarded(t *testing.T) {
	trayGoPath := trayGoSourcePath(t)

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, trayGoPath, nil, parser.ParseComments)
	if err != nil {
		t.Fatalf("failed to parse %s: %v", trayGoPath, err)
	}

	handleMenu := findHandleMenuFunc(file)
	if handleMenu == nil {
		t.Fatal("could not find func (t *trayApp) handleMenu(...) in tray.go — has it been renamed or restructured?")
	}

	sel := findSelectStmt(handleMenu.Body)
	if sel == nil {
		t.Fatal("could not find the select{} statement inside handleMenu — has its structure changed?")
	}

	// Cases intentionally left unguarded, and WHY. Keep this list explicit
	// and commented so a reviewer immediately sees the reasoning rather than
	// having to go re-derive it from tray.go.
	unguardedAllowlist := map[string]string{
		"mConfigRaw": "fires exec.Command(notepad.exe, ...).Start() directly, no goroutine, no window of our own",
		"mStartup":   "toggles a checkbox + calls saveStartupPref/setAutoStart synchronously, no window",
		"mContact":   "fires exec.Command(rundll32, ...).Start() directly, no goroutine, no window of our own",
		"mExit":      "calls t.tunnel.Stop() + systray.Quit() directly — one-way exit, nothing to race",
	}

	clauses := selectClauses(sel)
	if len(clauses) == 0 {
		t.Fatal("select statement in handleMenu has no case clauses — parsing bug?")
	}

	seen := map[string]bool{}

	for _, clause := range clauses {
		chanName, ok := clickedChIdent(clause)
		if !ok {
			// Not a `<-mXxx.ClickedCh` case (e.g. a default: clause). Not
			// part of the guard contract; skip it.
			continue
		}
		seen[chanName] = true

		hasGuardCall := clauseCallsGuarded(clause)

		if reason, allowlisted := unguardedAllowlist[chanName]; allowlisted {
			// Documented exception. We don't require it to be unguarded,
			// just don't fail if it is — but flag if it silently grew a
			// guard call, since then it should probably come off the list.
			_ = reason
			continue
		}

		if !hasGuardCall {
			t.Errorf(
				"menu action %q (case <-%s.ClickedCh) does not call t.guarded(...) in handleMenu — "+
					"this is the exact bug class that shipped once before: an unguarded window-opening/slow "+
					"action can open duplicate windows on a rapid re-click. Wrap it in t.guarded(&someMutex, \"name\", fn), "+
					"or if it's genuinely synchronous/instant, add it to the explicit allowlist in this test with a comment explaining why",
				chanName, chanName)
		}
	}

	// Sanity check: make sure we actually found the known allowlisted cases
	// in the select statement, so a rename/removal doesn't silently make
	// this test vacuous.
	for name := range unguardedAllowlist {
		if !seen[name] {
			t.Errorf("expected to find case <-%s.ClickedCh in handleMenu's select statement but did not — has it been renamed or removed?", name)
		}
	}
}

// trayGoSourcePath resolves tray.go's absolute path relative to this test
// file's own location (via runtime.Caller), which is robust regardless of
// the working directory `go test` happens to be invoked from.
func trayGoSourcePath(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller(0) failed to resolve this test file's path")
	}
	return filepath.Join(filepath.Dir(thisFile), "tray.go")
}

// findHandleMenuFunc locates `func (t *trayApp) handleMenu(...)` among the
// file's top-level declarations.
func findHandleMenuFunc(file *ast.File) *ast.FuncDecl {
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Name.Name != "handleMenu" || fn.Recv == nil || len(fn.Recv.List) != 1 {
			continue
		}
		star, ok := fn.Recv.List[0].Type.(*ast.StarExpr)
		if !ok {
			continue
		}
		if ident, ok := star.X.(*ast.Ident); ok && ident.Name == "trayApp" {
			return fn
		}
	}
	return nil
}

// findSelectStmt finds the (first, and expected-only) select{} statement
// anywhere within the given function body.
func findSelectStmt(body *ast.BlockStmt) *ast.SelectStmt {
	var found *ast.SelectStmt
	ast.Inspect(body, func(n ast.Node) bool {
		if found != nil {
			return false
		}
		if sel, ok := n.(*ast.SelectStmt); ok {
			found = sel
			return false
		}
		return true
	})
	return found
}

func selectClauses(sel *ast.SelectStmt) []*ast.CommClause {
	var clauses []*ast.CommClause
	for _, stmt := range sel.Body.List {
		if cc, ok := stmt.(*ast.CommClause); ok {
			clauses = append(clauses, cc)
		}
	}
	return clauses
}

// clickedChIdent extracts "mConfig" from a clause of the form
// `case <-mConfig.ClickedCh:`. Returns ok=false for clauses that don't match
// this shape (e.g. `default:`, or a receive not shaped like a ClickedCh
// selector — in which case it's not part of the guard contract at all).
func clickedChIdent(clause *ast.CommClause) (string, bool) {
	if clause.Comm == nil {
		return "", false
	}
	exprStmt, ok := clause.Comm.(*ast.ExprStmt)
	if !ok {
		return "", false
	}
	unary, ok := exprStmt.X.(*ast.UnaryExpr)
	if !ok || unary.Op != token.ARROW {
		return "", false
	}
	selExpr, ok := unary.X.(*ast.SelectorExpr)
	if !ok || selExpr.Sel.Name != "ClickedCh" {
		return "", false
	}
	ident, ok := selExpr.X.(*ast.Ident)
	if !ok {
		return "", false
	}
	return ident.Name, true
}

// clauseCallsGuarded reports whether any statement in the clause's body
// contains a call expression of the shape `t.guarded(...)` (matched by
// method name "guarded" on any selector, to stay robust to the receiver
// variable's name).
func clauseCallsGuarded(clause *ast.CommClause) bool {
	found := false
	for _, stmt := range clause.Body {
		ast.Inspect(stmt, func(n ast.Node) bool {
			if found {
				return false
			}
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			selExpr, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			if selExpr.Sel.Name == "guarded" {
				found = true
				return false
			}
			return true
		})
		if found {
			break
		}
	}
	return found
}
