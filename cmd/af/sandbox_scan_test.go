package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// runners are the methods of the tests that run the shell with the arguments as they
// are given: the run of the environments and the Main of the app. The helpers loop,
// fix and fixRun of the environments run it for a command, and say which box
// themselves or not.
var runners = []string{"run", "runCtx", "Main"}

// helpers are the methods of the test environments that run af triage or af fix.
var helpers = []string{"loop", "fix", "fixRun"}

// unpinnedInvocations are the lines of src where a test runs af triage or af fix, a
// command that opens a box, and does not say which one: it passes no --sandbox, and
// the function that holds it sets no AF_SANDBOX. A call whose arguments are
// variables is pinned when the function has said --sandbox before it. Commands that
// do not open a box (brief, submit, land, and triage run, which works where it is)
// and runs that only print (--help, --schema) are not invocations. bare are the
// helpers of the environments that pin no box themselves: a call of one is an
// invocation like a run with the command word given. With lists, the argument lists
// that a test writes as []string literals are scanned as well (see
// unpinnedArgumentLists); they are in the tests of cmd/af, whose environments take them.
func unpinnedInvocations(t *testing.T, name, src string, bare []string, lists bool) []int {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, name, src, 0)
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	var lines []int
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		var sandboxSeen []token.Pos // where the function says --sandbox
		var envSet bool             // whether it sets AF_SANDBOX
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			if lit, ok := n.(*ast.BasicLit); ok && lit.Kind == token.STRING {
				switch s := unquote(lit); {
				case s == "--sandbox" || strings.HasPrefix(s, "--sandbox="):
					sandboxSeen = append(sandboxSeen, lit.Pos())
				case s == "AF_SANDBOX":
					envSet = true
				}
			}
			return true
		})
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok || !opensBox(call, bare) {
				return true
			}
			if envSet || mentions(call, func(s string) bool { return s == "--sandbox" || strings.HasPrefix(s, "--sandbox=") }) {
				return true
			}
			if hasVariableArgs(call) && slices.ContainsFunc(sandboxSeen, func(p token.Pos) bool { return p < call.Pos() }) {
				return true
			}
			lines = append(lines, fset.Position(call.Pos()).Line)
			return true
		})
		if lists {
			lines = append(lines, unpinnedArgumentLists(fset, fn.Body, sandboxSeen, envSet)...)
		}
	}
	slices.Sort(lines)
	return slices.Compact(lines)
}

// unpinnedArgumentLists are the lines of the argument lists of af triage or af fix that
// a test writes as a []string literal, to give them to a run later (a variable, a table
// of them): the literal starts with the command word and passes no --sandbox, the
// statement that holds it passes none, and the function has said none before it where
// the statement takes its arguments from a variable.
func unpinnedArgumentLists(fset *token.FileSet, body *ast.BlockStmt, sandboxSeen []token.Pos, envSet bool) []int {
	var lines []int
	var stack []ast.Node
	ast.Inspect(body, func(n ast.Node) bool {
		if n == nil {
			stack = stack[:len(stack)-1]
			return true
		}
		stack = append(stack, n)
		lit, ok := n.(*ast.CompositeLit)
		if !ok || envSet || !isStringSlice(lit, stack) || !listOpensBox(lit.Elts) {
			return true
		}
		says := func(s string) bool { return s == "--sandbox" || strings.HasPrefix(s, "--sandbox=") }
		if slices.ContainsFunc(composite(lit), says) {
			return true
		}
		for i := len(stack) - 1; i >= 0; i-- {
			stmt, ok := stack[i].(ast.Stmt)
			if !ok {
				continue
			}
			if containsLiteral(stmt, says) {
				return true
			}
			break
		}
		if slices.ContainsFunc(sandboxSeen, func(p token.Pos) bool { return p < lit.Pos() }) && statementTakesVariables(stack) {
			return true
		}
		lines = append(lines, fset.Position(lit.Pos()).Line)
		return true
	})
	return lines
}

// isStringSlice says whether the literal is a []string: written so, or elided in a
// [][]string or in a map of them (stack ends with the literal).
func isStringSlice(lit *ast.CompositeLit, stack []ast.Node) bool {
	if lit.Type != nil {
		return isStringSliceType(lit.Type)
	}
	for i := len(stack) - 2; i >= 0; i-- {
		outer, ok := stack[i].(*ast.CompositeLit)
		if !ok {
			continue // the key and value of a map entry stand between
		}
		switch typ := outer.Type.(type) {
		case *ast.ArrayType:
			return isStringSliceType(typ.Elt)
		case *ast.MapType:
			return isStringSliceType(typ.Value)
		}
		return false
	}
	return false
}

func isStringSliceType(expr ast.Expr) bool {
	arr, ok := expr.(*ast.ArrayType)
	if !ok || arr.Len != nil {
		return false
	}
	elem, ok := arr.Elt.(*ast.Ident)
	return ok && elem.Name == "string"
}

// composite is the string literals among the elements of a composite literal.
func composite(lit *ast.CompositeLit) []string {
	var out []string
	for _, e := range lit.Elts {
		if b, ok := e.(*ast.BasicLit); ok && b.Kind == token.STRING {
			out = append(out, unquote(b))
		}
	}
	return out
}

func containsLiteral(n ast.Node, match func(string) bool) bool {
	found := false
	ast.Inspect(n, func(n ast.Node) bool {
		if b, ok := n.(*ast.BasicLit); ok && b.Kind == token.STRING && match(unquote(b)) {
			found = true
		}
		return !found
	})
	return found
}

// statementTakesVariables says whether the innermost call around the literal takes
// the rest of its arguments from a variable: append([]string{"fix"}, args...).
func statementTakesVariables(stack []ast.Node) bool {
	for i := len(stack) - 2; i >= 0; i-- {
		if call, ok := stack[i].(*ast.CallExpr); ok {
			return hasVariableArgs(call)
		}
	}
	return false
}

// listOpensBox says whether the elements of a []string literal are the arguments of an
// af triage or af fix that opens a box: the command word is the first element, or
// follows af, and what follows it is the input or a flag. A name that follows it, a
// variable that may hold a command that opens no box, and a command that opens none
// are not, nor is a list of --help or --schema.
func listOpensBox(elts []ast.Expr) bool {
	for len(elts) > 0 {
		if b, ok := elts[0].(*ast.BasicLit); ok && b.Kind == token.STRING && (unquote(b) == "af" || unquote(b) == "") {
			elts = elts[1:]
			continue
		}
		break
	}
	if len(elts) < 2 {
		return false
	}
	word, ok := elts[0].(*ast.BasicLit)
	if !ok || word.Kind != token.STRING || (unquote(word) != "triage" && unquote(word) != "fix") {
		return false
	}
	for _, e := range elts {
		if b, ok := e.(*ast.BasicLit); ok && b.Kind == token.STRING && slices.Contains([]string{"--help", "-h", "--schema", "--version"}, unquote(b)) {
			return false
		}
	}
	switch next := elts[1].(type) {
	case *ast.BasicLit:
		if next.Kind != token.STRING {
			return false
		}
		s := unquote(next)
		if slices.Contains([]string{"brief", "submit", "land"}, s) || (unquote(word) == "triage" && s == "run") {
			return false
		}
		return s == "-" || strings.HasPrefix(s, "--") || strings.ContainsAny(s, "/: ")
	case *ast.Ident:
		return false
	}
	return true
}

func unquote(lit *ast.BasicLit) string {
	s, err := strconv.Unquote(lit.Value)
	if err != nil {
		return lit.Value
	}
	return s
}

// literals are the string literals of the arguments of a call, in source order.
func literals(call *ast.CallExpr) []string {
	var out []string
	for _, arg := range call.Args {
		ast.Inspect(arg, func(n ast.Node) bool {
			if lit, ok := n.(*ast.BasicLit); ok && lit.Kind == token.STRING {
				out = append(out, unquote(lit))
			}
			return true
		})
	}
	return out
}

func mentions(call *ast.CallExpr, match func(string) bool) bool {
	return slices.ContainsFunc(literals(call), match)
}

// hasVariableArgs says whether the call takes the rest of its arguments from a variable:
// a spread slice. A name among the arguments is an input or a value and says nothing of
// the flags.
func hasVariableArgs(call *ast.CallExpr) bool { return call.Ellipsis.IsValid() }

// isHelp says the argument asks for a text and not for a run.
func isHelp(s string) bool { return s == "--help" || s == "-h" || s == "--schema" || s == "--version" }

// takesTheTest says whether the first argument of the call is the test, t.
func takesTheTest(call *ast.CallExpr) bool {
	if len(call.Args) == 0 {
		return false
	}
	id, ok := call.Args[0].(*ast.Ident)
	return ok && (id.Name == "t" || id.Name == "tb")
}

// argsOpenBox says whether the arguments of a call of a test hold the command word of af
// triage or af fix as a string and, after it, a flag of the command line: a call that is
// given a name among others, such as checkSchema(t, "fix", result), is no invocation.
func argsOpenBox(args []ast.Expr) bool {
	for i, arg := range args {
		if b, ok := arg.(*ast.BasicLit); ok && b.Kind == token.STRING && (unquote(b) == "triage" || unquote(b) == "fix") {
			return listOpensBox(args[i:]) && slices.ContainsFunc(args[i:], func(a ast.Expr) bool {
				flag, ok := a.(*ast.BasicLit)
				return ok && flag.Kind == token.STRING && strings.HasPrefix(unquote(flag), "--")
			})
		}
	}
	return false
}

// opensBox says whether the call runs af triage or af fix in the way that opens a box.
func opensBox(call *ast.CallExpr, bare []string) bool {
	var name string
	switch fun := call.Fun.(type) {
	case *ast.SelectorExpr:
		name = fun.Sel.Name
	case *ast.Ident:
		name = fun.Name
	}
	lits := literals(call)
	if !slices.Contains(runners, name) && !slices.Contains(bare, name) {
		// Any other call of a test that is given the command words, such as a helper that runs
		// the built binary (afJSON(t, bin, dir, "fix", url)), is an invocation when it has them
		// the way a command line does: the command word, then the input or a flag.
		return takesTheTest(call) && !slices.ContainsFunc(lits, isHelp) && argsOpenBox(call.Args)
	}
	if slices.ContainsFunc(lits, func(s string) bool { return s == "--help" || s == "-h" || s == "--schema" || s == "--version" }) {
		return false
	}
	if slices.Contains(bare, name) {
		return true
	}
	// run, runCtx and Main take the command word among their first literals.
	lits = slices.DeleteFunc(slices.Clone(lits), func(s string) bool { return s == "af" || s == "" })
	if len(lits) > 0 && lits[0] == "help" {
		return false
	}
	for i, word := range lits[:min(len(lits), 2)] {
		if word != "triage" && word != "fix" {
			continue
		}
		next := ""
		if i+1 < len(lits) {
			next = lits[i+1]
		}
		switch {
		case slices.Contains([]string{"brief", "submit", "land"}, next):
			return false
		case word == "triage" && next == "run":
			return false
		}
		return true
	}
	return false
}

// TestTheScanFindsTheInvocationsThatDoNotSayWhichBox_TS_10_62 verifies TS-10-62 for the
// scan itself: a call of af triage or af fix with no --sandbox is found, and the
// commands that open no box, the runs that only print, and a call that is pinned by
// its arguments, by a variable, or by AF_SANDBOX are not.
// Verifies: 10-REQ-10.2
func TestTheScanFindsTheInvocationsThatDoNotSayWhichBox_TS_10_62(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
		want []int // lines of the file, whose body is on line 3
		// elsewhere says the file is not in cmd/af, so that loop, fix and fixRun are not
		// the helpers of an environment that pin nothing.
		elsewhere bool
	}{
		{"loop with no box", `e.loop("--brain", "scripted")`, []int{3}, false},
		{"fix with no box", `e.fix(url, "--brain", "spy")`, []int{3}, false},
		{"fix run with no box", `e.fixRun(brief, "--brain", "scripted")`, []int{3}, false},
		{"run of triage with no box", `e.run("", "triage", url, "--dir", e.dir)`, []int{3}, false},
		{"run of fix after a stdin text", `e.run("the text", "fix", "--dir", e.dir)`, []int{3}, false},
		{"runCtx of fix", `e.runCtx(ctx, "", "fix", url)`, []int{3}, false},
		{"Main with af first", `app.Main(ctx, []string{"af", "fix", url}, in, out, errb)`, []int{3}, false},
		{"pinned loop", `e.loop("--sandbox", "local")`, nil, false},
		{"pinned with an equals sign", `e.loop("--sandbox=none")`, nil, false},
		{"pinned in an appended list", `e.run("", append([]string{"fix", url, "--sandbox", "local"}, more...)...)`, nil, false},
		{"fix run pinned", `e.fixRun(brief, "--sandbox", "local")`, nil, false},
		{"triage brief opens no box", `e.run("", "triage", "brief", url)`, nil, false},
		{"triage submit opens no box", `e.run(payload, "triage", "submit", "--run-id", id)`, nil, false},
		{"fix land opens no box", `e.run("", "fix", "land", "--run-id", id)`, nil, false},
		{"triage run works where it is", `e.run("", "triage", "run", "--run-id", id)`, nil, false},
		{"fix run is a box", `e.run("", "fix", "run", "--run-id", id)`, []int{3}, false},
		{"help prints", `e.run("", "fix", "--help")`, nil, false},
		{"the help command prints", `app.Main(ctx, []string{"af", "help", "fix", "run"}, in, out, errb)`, nil, false},
		{"schema prints", `e.run("", "--schema")`, nil, false},
		{"another command", `e.run("", "gate", "run")`, nil, false},
		{"a method of that name outside cmd/af", `s.loop(); s.fix(); s.fixRun()`, nil, true},
		{"run outside cmd/af is still found", `s.run("", "fix", url)`, []int{3}, true},
		{"a variable after a pin", `args := []string{"--sandbox", "local"}; e.loop(args...)`, nil, false},
		{"a variable with no pin", `args := []string{"--brain", "spy"}; e.loop(args...)`, []int{3}, false},
		{"a pin after the call is none", `e.loop(args...); _ = "--sandbox"`, []int{3}, false},
		{"an earlier pin does not pin a call of literals", `e.run("", "fix", url, "--sandbox", "local"); e.run("", "fix", url)`, []int{3}, false},
		{"a plain function with a command line", `afJSON(t, bin, "fix", e.u(), "--dir", dir)`, []int{3}, false},
		{"a plain function with a command line, pinned", `afJSON(t, bin, "fix", e.u(), "--dir", dir, "--sandbox", "local")`, nil, false},
		{"a plain function that is given a name", `checkSchema(t, "fix", fix.Result{}); valid(t, "triage", resultOf(t, env))`, nil, false},
		{"a function that does not take the test", `hasNext(env, "af", "fix", e.u(), "--dir", dir)`, nil, false},
		{"AF_SANDBOX in the function", `t.Setenv("AF_SANDBOX", "none"); e.loop("--brain", "spy")`, nil, false},
		{"a list of arguments for a later run", `args := []string{"triage", e.forge.IssueURL(7), "--dir", dir}; e.run("", args...)`, []int{3}, false},
		{"a list that is pinned", `args := []string{"triage", url, "--sandbox", "local"}; e.run("", args...)`, nil, false},
		{"a list of af first", `args := []string{"af", "fix", e.forge.IssueURL(7)}; _ = args`, []int{3}, false},
		{"a list of a text and of the dash", "a := []string{\"fix\", \"the greeting says helo\", \"--dir\", dir}\nb := []string{\"triage\", \"-\"}\n_, _ = a, b", []int{3, 4}, false},
		{"a list elided in a list of them", `for _, args := range [][]string{{"triage", "--brain", "x"}, {"triage", "brief"}} { _ = args }`, []int{3}, false},
		{"a list elided in a map of them", `m := map[string][]string{"a": {"fix", e.u(), "--dir", dir}, "b": {"fix", "brief", e.u()}}; _ = m`, []int{3}, false},
		{"a list written in full inside a struct", `_ = []struct{ a []string }{{[]string{"fix", e.u()}}}`, []int{3}, false},
		{"a list with a name after the command", `names := []string{"fix", "fix_brief", "--x"}; _ = names`, nil, false},
		{"a list with a variable after the command", `args := []string{"triage", command, "--dir", dir}; _ = args`, nil, false},
		{"a list outside cmd/af is none", `args := []string{"triage", e.forge.IssueURL(7), "--dir", dir}; _ = args`, nil, true},
		{"a list of a command that opens no box", `args := []string{"triage", "brief", url}; _ = args`, nil, false},
		{"a list of the help", `args := []string{"af", "help", "fix"}; _ = args`, nil, false},
		{"a list of triage run", `args := []string{"triage", "run"}; _ = args`, nil, false},
		{"a list of another command", `args := []string{"gate", "run"}; _ = args`, nil, false},
		{"a list pinned by the statement", `e.run("", append([]string{"fix", e.u()}, "--sandbox", "local")...)`, nil, false},
		{"a list appended to a pinned variable", `scripted := []string{"--sandbox", "local"}; e.run("", append([]string{"triage", e.u()}, scripted...)...)`, nil, false},
		{"a list appended to a variable with no pin", `scripted := []string{"--brain", "spy"}; e.run("", append([]string{"triage", e.u()}, scripted...)...)`, []int{3}, false},
		{"a list in a table", `for _, args := range [][]string{{"triage", e.u()}, []string{"fix", e.u()}} { e.run("", args...) }`, []int{3}, false},
		{"a list and AF_SANDBOX", `t.Setenv("AF_SANDBOX", "none"); args := []string{"fix", e.u()}; _ = args`, nil, false},
		{"strings that are not a list of arguments", `names := []int{1, 2}; words := []string{"fixed", "triaged"}; _, _ = names, words`, nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			src := "package p\nfunc f() {\n" + tc.body + "\n}\n"
			bare := helpers
			if tc.elsewhere {
				bare = nil
			}
			got := unpinnedInvocations(t, "snippet.go", src, bare, !tc.elsewhere)
			if !slices.Equal(got, tc.want) {
				t.Errorf("unpinned invocations = %v, want %v for\n%s", got, tc.want, tc.body)
			}
		})
	}
}

// bareHelpers are the helpers loop, fix and fixRun that the source declares as methods
// and whose body does not say --sandbox: they run the command as the test gives it, so
// that the test has to say which box. A helper that says it, as the default of the
// tests that pass nothing, is the pin of all their calls.
func bareHelpers(t *testing.T, name, src string) []string {
	t.Helper()
	file, err := parser.ParseFile(token.NewFileSet(), name, src, 0)
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	var bare []string
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Recv == nil || fn.Body == nil || !slices.Contains(helpers, fn.Name.Name) {
			continue
		}
		pins := false
		ast.Inspect(fn.Body, func(n ast.Node) bool {
			if lit, ok := n.(*ast.BasicLit); ok && lit.Kind == token.STRING && unquote(lit) == "--sandbox" {
				pins = true
			}
			return true
		})
		if !pins {
			bare = append(bare, fn.Name.Name)
		}
	}
	return bare
}

// TestAHelperThatPinsTheBoxIsThePinOfItsCalls_TS_10_62 verifies the reading of the
// helpers of the environments: a helper that passes --sandbox itself makes its calls
// pinned, and one that does not leaves them to say.
// Verifies: 10-REQ-10.2
func TestAHelperThatPinsTheBoxIsThePinOfItsCalls_TS_10_62(t *testing.T) {
	src := `package p
func (e *env) fix(input string, args ...string) { e.run("", append([]string{"fix", input, "--sandbox", "local"}, args...)...) }
func (e *env) loop(args ...string)               { e.run("", append([]string{"triage"}, args...)...) }
func (e *env) fixRun(args ...string)             { e.run("", "fix", "run") }
func fix(args ...string)                         { _ = "--sandbox" }
func (e *env) other()                            { _ = "--sandbox" }
`
	got := bareHelpers(t, "env.go", src)
	slices.Sort(got)
	if want := []string{"fixRun", "loop"}; !slices.Equal(got, want) {
		t.Errorf("bare helpers = %v, want %v: fix pins, loop and fixRun do not, and a function without a receiver is none", got, want)
	}
}

// TestEveryTestThatRunsTriageOrFixSaysWhichBox_TS_10_62 verifies TS-10-62: with the
// default box now the isolating provider of the platform, every test of cmd/af and of
// internal/* that runs af triage or af fix without exercising another provider passes
// --sandbox local, so that make check needs no engine and no Landlock.
// Verifies: 10-REQ-10.2
func TestEveryTestThatRunsTriageOrFixSaysWhichBox_TS_10_62(t *testing.T) {
	var found []string
	var bare []string // the helpers of the environments of cmd/af that pin nothing
	for _, root := range []string{".", filepath.Join("..", "..", "internal")} {
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || !strings.HasSuffix(path, "_test.go") || filepath.Base(path) == "sandbox_scan_test.go" {
				return nil
			}
			src, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			if root == "." {
				bare = append(bare, bareHelpers(t, path, string(src))...)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, root := range []string{".", filepath.Join("..", "..", "internal")} {
		helpersHere := bare
		if root != "." {
			helpersHere = nil
		}
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || !strings.HasSuffix(path, "_test.go") || filepath.Base(path) == "sandbox_scan_test.go" {
				return nil
			}
			src, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			for _, line := range unpinnedInvocations(t, path, string(src), helpersHere, root == ".") {
				found = append(found, fmt.Sprintf("%s:%d", filepath.ToSlash(path), line))
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if len(found) > 0 {
		t.Errorf("%d tests run af triage or af fix without --sandbox, so they open the default box of the platform; pass --sandbox local:\n%s", len(found), strings.Join(found, "\n"))
	}
}
