package conform

import (
	"bufio"
	"context"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/agent-fox-dev/agentfox/internal/gitx"
	"github.com/agent-fox-dev/agentfox/internal/project"
)

// The structural checks. Each is a fact about the files a change touched,
// established by this package rather than asked of a model, and each names
// one way a change that passes its tests can still be wrong.
const (
	CheckGofmt            = "gofmt"
	CheckVet              = "vet"
	CheckDuplicate        = "duplicate"
	CheckDiscardedError   = "discarded_error"
	CheckLaterTask        = "later_task"
	CheckUnused           = "unused"
	CheckLongFunction     = "long_function"
	CheckNoAssertions     = "no_assertions"
	CheckImportSuppressor = "import_suppressor"
	CheckGitInitBranch    = "git_init_branch"
	CheckFutureDate       = "future_date"
	CheckErrataCitation   = "errata_citation"
	// CheckFormat is a touched file the language's own formatter would
	// change: rustfmt, ruff or black, prettier. gofmt is CheckGofmt.
	CheckFormat = "format"
)

// DefaultMaxFuncLines is the length past which a function the change wrote
// or grew is reported.
const DefaultMaxFuncLines = 100

// duplicateWindow is how many consecutive meaningful lines two places must
// share to count as duplicated.
const duplicateWindow = 8

// Finding is one structural problem in the change.
type Finding struct {
	Check   string `json:"check" trust:"fact" description:"Which structural check found it."`
	Path    string `json:"path" trust:"fact" description:"The file, relative to the repository root."`
	Line    int    `json:"line,omitempty" description:"The line, when the check names one."`
	Message string `json:"message" trust:"fact" description:"What is wrong."`
}

// Ref is the finding's path:line.
func (f Finding) Ref() string {
	if f.Line > 0 {
		return fmt.Sprintf("%s:%d", f.Path, f.Line)
	}
	return f.Path
}

// ScanInput is what a structural scan looks at.
type ScanInput struct {
	// Root is the repository.
	Root string
	// Changed is every path the change touched, repository-relative.
	Changed []string
	// Added is what the change added, line by line.
	Added Added
	// Tracked is every file of the repository, for the duplication index.
	Tracked []string
	// Today is the date the run happens on; a later date written into a
	// decision record or an erratum is invented.
	Today time.Time
	// MaxFuncLines is the length past which a function is reported. Zero
	// means DefaultMaxFuncLines.
	MaxFuncLines int
	// Runner runs gofmt and go vet. Nil means those two are not run.
	Runner gitx.Runner
}

// Scan runs every structural check over the files the change touched.
func Scan(ctx context.Context, in ScanInput) []Finding {
	if in.MaxFuncLines <= 0 {
		in.MaxFuncLines = DefaultMaxFuncLines
	}
	var out []Finding
	var goFiles, otherFiles []string
	for _, p := range in.Changed {
		if _, err := os.Stat(filepath.Join(in.Root, filepath.FromSlash(p))); err != nil {
			continue // deleted by the change
		}
		if strings.HasSuffix(p, ".go") {
			goFiles = append(goFiles, p)
		} else if project.IsSourceFile(p) {
			otherFiles = append(otherFiles, p)
		}
		out = append(out, scanAddedText(p, in.Added[p], in.Today)...)
	}
	for _, p := range goFiles {
		out = append(out, scanGoFile(in.Root, p, in.Added, in.MaxFuncLines)...)
	}
	for _, p := range otherFiles {
		out = append(out, scanSourceFile(in.Root, p, in.Added, in.MaxFuncLines)...)
	}
	for _, p := range in.Changed {
		if isErrata(p) && len(in.Added[p]) > 0 {
			if f, ok := checkErrataCitation(in.Root, p); !ok {
				out = append(out, f)
			}
		}
	}
	out = append(out, duplicates(in.Root, in.Changed, in.Added, in.Tracked)...)
	if in.Runner != nil && len(goFiles) > 0 {
		out = append(out, gofmt(ctx, in.Runner, in.Root, goFiles)...)
		out = append(out, vet(ctx, in.Runner, in.Root, goFiles)...)
	}
	if in.Runner != nil && len(otherFiles) > 0 {
		out = append(out, formatChecks(ctx, in.Runner, in.Root, otherFiles)...)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Path != out[j].Path {
			return out[i].Path < out[j].Path
		}
		return out[i].Line < out[j].Line
	})
	return out
}

var (
	// discardRe is an error thrown away by assignment to the blank
	// identifier: `_ = err`, `_ = f.Close()`, `_, _ = w.Write(b)`.
	discardRe = regexp.MustCompile(`^\s*_(\s*,\s*_)*\s*=\s*\S`)
	// logClaimRe is a comment that says the value is logged or reported.
	logClaimRe = regexp.MustCompile(`(?i)\b(log|logged|logs|logging|report|reported|warn)\b`)
	// laterTaskRe is a comment that defers to work this change does not do.
	laterTaskRe = regexp.MustCompile(`(?i)\b(later|future|subsequent|follow-?up) (task|spec|pr|pull request|change|commit)\b|\bnext task\b`)
	// suppressorRe is `var _ = pkg.Symbol`: an import kept alive by a
	// reference that tests nothing.
	suppressorRe = regexp.MustCompile(`^\s*var\s+_\s*=\s*[A-Za-z_]\w*\.[A-Za-z_]\w*\s*(//.*)?$`)
	// gitInitShellRe is git init in a shell line (`git -C d init`);
	// gitInitArgvRe is git init as an argv literal (`"git", "-C", d,
	// "init"`), the only form a program's source runs it in — a sentence that
	// mentions git init is not an invocation.
	gitInitShellRe = regexp.MustCompile(`\bgit(\s+-C\s+\S+)?\s+init\b`)
	gitInitArgvRe  = regexp.MustCompile(`"git",\s*("-C",\s*[^,]+,\s*)?"init"`)
	branchArgRe    = regexp.MustCompile(`(^|[\s"'])(-b|--initial-branch(=\S*)?)([\s"',]|$)`)
	dateRe         = regexp.MustCompile(`\b(20\d\d)-(\d\d)-(\d\d)\b`)
)

// scanAddedText is the line checks: the ones that only need the text the
// change added.
func scanAddedText(p string, lines map[int]string, today time.Time) []Finding {
	if len(lines) == 0 {
		return nil
	}
	var out []Finding
	code := project.IsSourceFile(p)
	dated := isErrata(p) || isDecisionRecord(p)
	for _, n := range sortedLines(lines) {
		text := lines[n]
		comment := commentOf(text)
		production := code && !project.IsTestPath(p)
		if production && discardRe.MatchString(text) && !strings.Contains(text, ":=") {
			claim := logClaimRe.MatchString(comment) ||
				logClaimRe.MatchString(commentOf(lines[n-1])) && strings.TrimSpace(codeOf(lines[n-1])) == ""
			if claim {
				out = append(out, Finding{Check: CheckDiscardedError, Path: p, Line: n,
					Message: "a value is discarded with `_ =` under a comment that says it is logged or reported; " +
						"log it, return it, or say plainly that it is ignored and why"})
			}
		}
		if production && strings.HasSuffix(p, ".rs") && rustDiscardRe.MatchString(text) {
			claim := logClaimRe.MatchString(comment) ||
				logClaimRe.MatchString(commentOf(lines[n-1])) && strings.TrimSpace(codeOf(lines[n-1])) == ""
			if claim {
				out = append(out, Finding{Check: CheckDiscardedError, Path: p, Line: n,
					Message: "a value is discarded with `let _ =` under a comment that says it is logged or " +
						"reported; log it, return it, or say plainly that it is ignored and why"})
			}
		}
		if production && comment != "" && laterTaskRe.MatchString(comment) {
			out = append(out, Finding{Check: CheckLaterTask, Path: p, Line: n,
				Message: fmt.Sprintf("the comment defers to work outside this change (%q); do the work, or "+
					"record it as an unmet requirement with a tracking issue", strings.TrimSpace(comment))})
		}
		if project.IsTestPath(p) && strings.HasSuffix(p, ".go") && suppressorRe.MatchString(text) {
			out = append(out, Finding{Check: CheckImportSuppressor, Path: p, Line: n,
				Message: "`var _ = pkg.Symbol` keeps an import alive without testing anything; use the symbol " +
					"in an assertion or drop the import"})
		}
		if shell := isShellFile(p); code || shell {
			if UnpinnedGitInit(text, shell) {
				out = append(out, Finding{Check: CheckGitInitBranch, Path: p, Line: n,
					Message: "git init without an explicit branch (-b): the branch depends on the machine's " +
						"init.defaultBranch, and a clean image does not have the author's"})
			}
		}
		if dated {
			for _, m := range dateRe.FindAllStringSubmatch(text, -1) {
				y, _ := strconv.Atoi(m[1])
				mo, _ := strconv.Atoi(m[2])
				d, _ := strconv.Atoi(m[3])
				when := time.Date(y, time.Month(mo), d, 0, 0, 0, 0, time.UTC)
				if !today.IsZero() && when.After(truncateDay(today)) {
					out = append(out, Finding{Check: CheckFutureDate, Path: p, Line: n,
						Message: fmt.Sprintf("%s is after today (%s); a date in a record is the date it was "+
							"written, not one a model chose", m[0], today.Format("2006-01-02"))})
				}
			}
		}
	}
	return out
}

// UnpinnedGitInit reports whether line runs git init without naming the
// initial branch: in a shell line when shell is true, as an argv literal
// otherwise. A comment is not a command.
//
// A clone is not reported: it checks out the remote's HEAD, which the
// fixture's own init already pins, and -b on a clone of an empty repository
// fails rather than pins anything.
func UnpinnedGitInit(line string, shell bool) bool {
	cmd := codeOf(line)
	re := gitInitArgvRe
	if shell {
		re = gitInitShellRe
	}
	return re.MatchString(cmd) && !branchArgRe.MatchString(cmd)
}

func isShellFile(p string) bool {
	switch path.Ext(p) {
	case ".sh", ".bash", ".mk":
		return true
	}
	return path.Base(p) == "Makefile"
}

func truncateDay(t time.Time) time.Time {
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

// commentOf is the comment part of a line, for the comment styles the
// common languages use; "" when there is none. It does not parse strings, so
// a "//" inside one counts — the price of working on any language.
func commentOf(line string) string {
	for _, marker := range []string{"//", "#", "/*", "--"} {
		if i := strings.Index(line, marker); i >= 0 {
			if marker == "#" && i > 0 && !isSpace(line[i-1]) {
				continue
			}
			if marker == "--" && strings.TrimSpace(line[:i]) != "" {
				continue
			}
			return strings.TrimSpace(line[i+len(marker):])
		}
	}
	if t := strings.TrimSpace(line); strings.HasPrefix(t, "*") {
		return strings.TrimSpace(strings.TrimPrefix(t, "*"))
	}
	return ""
}

func codeOf(line string) string {
	if i := strings.Index(line, "//"); i >= 0 {
		return line[:i]
	}
	if i := strings.Index(line, "#"); i >= 0 {
		return line[:i]
	}
	return line
}

func isSpace(b byte) bool { return b == ' ' || b == '\t' }

func isErrata(p string) bool {
	return strings.HasSuffix(strings.ToLower(p), ".md") && hasSegment(p, "errata")
}

func isDecisionRecord(p string) bool {
	return strings.HasSuffix(strings.ToLower(p), ".md") && (hasSegment(p, "adr") || hasSegment(p, "decisions"))
}

func hasSegment(p, seg string) bool {
	for _, s := range strings.Split(strings.ToLower(path.Dir(p)), "/") {
		if s == seg {
			return true
		}
	}
	return false
}

func sortedLines(lines map[int]string) []int {
	ns := make([]int, 0, len(lines))
	for n := range lines {
		ns = append(ns, n)
	}
	slices.Sort(ns)
	return ns
}

// scanGoFile is the checks that need the parsed file: long functions, test
// functions with no assertion, and unexported declarations the change added
// that nothing in the package uses.
func scanGoFile(root, p string, added Added, maxLines int) []Finding {
	fset := token.NewFileSet()
	full := filepath.Join(root, filepath.FromSlash(p))
	file, err := parser.ParseFile(fset, full, nil, parser.SkipObjectResolution)
	if err != nil {
		return nil // gofmt and vet report what does not parse
	}
	var out []Finding
	isTest := strings.HasSuffix(p, "_test.go")
	for _, decl := range file.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		from, to := fset.Position(fn.Pos()).Line, fset.Position(fn.End()).Line
		grown := added.Count(p, from, to)
		if grown == 0 {
			continue
		}
		// Reported when the change wrote it, or grew it past the limit; a
		// function that was already long and was only touched is not this
		// change's to split.
		if length := to - from + 1; length > maxLines && length-grown <= maxLines {
			out = append(out, Finding{Check: CheckLongFunction, Path: p, Line: from,
				Message: fmt.Sprintf("%s is %d lines long (the limit is %d); split it where its steps are",
					fn.Name.Name, length, maxLines)})
		}
		if isTest && isTestFunc(fn) && !asserts(fn) {
			out = append(out, Finding{Check: CheckNoAssertions, Path: p, Line: from,
				Message: fmt.Sprintf("%s makes no assertion: nothing in it can fail, so it passes whatever "+
					"the code does", fn.Name.Name)})
		}
	}
	if !isTest {
		out = append(out, unusedDecls(root, p, fset, file, added)...)
	}
	return out
}

func isTestFunc(fn *ast.FuncDecl) bool {
	if fn.Recv != nil || !strings.HasPrefix(fn.Name.Name, "Test") || fn.Type.Params == nil ||
		len(fn.Type.Params.List) != 1 {
		return false
	}
	return testingParam(fn.Type.Params.List[0].Type)
}

func testingParam(e ast.Expr) bool {
	star, ok := e.(*ast.StarExpr)
	if ok {
		e = star.X
	}
	sel, ok := e.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	pkg, ok := sel.X.(*ast.Ident)
	return ok && pkg.Name == "testing" && (sel.Sel.Name == "T" || sel.Sel.Name == "TB")
}

// failureMethods are the testing.T methods that fail a test.
var failureMethods = map[string]bool{
	"Error": true, "Errorf": true, "Fatal": true, "Fatalf": true, "Fail": true, "FailNow": true,
}

// asserts reports whether a test function can fail: it calls a failing
// method of testing.T, or hands its *testing.T to something else — a helper,
// an assert library — that can.
func asserts(fn *ast.FuncDecl) bool {
	names := map[string]bool{}
	collect := func(fields *ast.FieldList) {
		if fields == nil {
			return
		}
		for _, f := range fields.List {
			if testingParam(f.Type) {
				for _, n := range f.Names {
					names[n.Name] = true
				}
			}
		}
	}
	collect(fn.Type.Params)
	found := false
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		if found {
			return false
		}
		switch x := n.(type) {
		case *ast.FuncLit:
			collect(x.Type.Params)
		case *ast.CallExpr:
			if sel, ok := x.Fun.(*ast.SelectorExpr); ok && failureMethods[sel.Sel.Name] {
				found = true
				return false
			}
			for _, arg := range x.Args {
				if id, ok := arg.(*ast.Ident); ok && names[id.Name] {
					found = true
					return false
				}
			}
		}
		return true
	})
	return found
}

// unusedDecls finds the unexported top-level names the change added to p that
// no file of the package mentions a second time.
func unusedDecls(root, p string, fset *token.FileSet, file *ast.File, added Added) []Finding {
	type decl struct {
		name string
		line int
	}
	var decls []decl
	add := func(id *ast.Ident) {
		if id == nil || id.IsExported() || id.Name == "_" || id.Name == "init" || id.Name == "main" {
			return
		}
		line := fset.Position(id.Pos()).Line
		if _, ok := added[p][line]; ok {
			decls = append(decls, decl{id.Name, line})
		}
	}
	for _, d := range file.Decls {
		switch x := d.(type) {
		case *ast.FuncDecl:
			if x.Recv == nil {
				add(x.Name)
			}
		case *ast.GenDecl:
			for _, s := range x.Specs {
				switch sp := s.(type) {
				case *ast.TypeSpec:
					add(sp.Name)
				case *ast.ValueSpec:
					for _, n := range sp.Names {
						add(n)
					}
				}
			}
		}
	}
	if len(decls) == 0 {
		return nil
	}
	uses := map[string]int{}
	dir := filepath.Dir(filepath.Join(root, filepath.FromSlash(p)))
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") {
			continue
		}
		f, err := parser.ParseFile(token.NewFileSet(), filepath.Join(dir, e.Name()), nil, parser.SkipObjectResolution)
		if err != nil {
			continue
		}
		ast.Inspect(f, func(n ast.Node) bool {
			if id, ok := n.(*ast.Ident); ok {
				uses[id.Name]++
			}
			return true
		})
	}
	var out []Finding
	for _, d := range decls {
		if uses[d.name] <= 1 {
			out = append(out, Finding{Check: CheckUnused, Path: p, Line: d.line,
				Message: fmt.Sprintf("%s is declared and never used in its package; remove it or use it", d.name)})
		}
	}
	return out
}

// errataRefRe is a path:line citation of a code file.
var errataRefRe = regexp.MustCompile("`?([A-Za-z0-9_./-]+\\.[A-Za-z0-9]+):(\\d+)")

// testRefRe is a reference to a test: a test function name, a test id, or a
// JavaScript test named by its it(), test() or describe() call.
var testRefRe = regexp.MustCompile(`\bTest[A-Z0-9_]\w*|\bTS-[A-Za-z0-9_]+-\d+\b|\btest_\w+|` +
	"\\b(?:it|test|describe)\\(\\s*['\"`]")

// checkErrataCitation holds an erratum to what makes it one: a citation of
// the code line that delivers the behaviour, and of the test that proves it.
// An entry that cites neither is restating a design decision, which belongs
// in the PRD.
func checkErrataCitation(root, p string) (Finding, bool) {
	b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(p)))
	if err != nil {
		return Finding{}, true
	}
	text := string(b)
	code, test := false, testRefRe.MatchString(text)
	for _, m := range errataRefRe.FindAllStringSubmatch(text, -1) {
		ref, line := m[1], m[2]
		if strings.HasSuffix(strings.ToLower(ref), ".md") {
			continue
		}
		n, _ := strconv.Atoi(line)
		if fileHasLine(filepath.Join(root, filepath.FromSlash(ref)), n) {
			code = true
			if project.IsTestPath(ref) {
				test = true
			}
		}
	}
	if code && test {
		return Finding{}, true
	}
	var missing []string
	if !code {
		missing = append(missing, "a path:line in the code that resolves")
	}
	if !test {
		missing = append(missing, "the test that proves the delivered behaviour")
	}
	return Finding{Check: CheckErrataCitation, Path: p,
		Message: "the erratum does not cite " + strings.Join(missing, " or ") +
			"; an erratum records what the code does, with the line and the test that show it"}, false
}

func fileHasLine(path string, n int) bool {
	if n <= 0 {
		return false
	}
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64<<10), 4<<20)
	for i := 1; sc.Scan(); i++ {
		if i == n {
			return true
		}
	}
	return false
}

// gofmt lists the touched Go files gofmt would change.
func gofmt(ctx context.Context, r gitx.Runner, root string, files []string) []Finding {
	if _, err := exec.LookPath("gofmt"); err != nil {
		return nil
	}
	out, _, err := r(ctx, root, append([]string{"gofmt", "-l"}, files...))
	if err != nil {
		return nil
	}
	var res []Finding
	for _, line := range strings.Split(out, "\n") {
		if p := strings.TrimSpace(line); p != "" && !strings.Contains(p, ":") {
			res = append(res, Finding{Check: CheckGofmt, Path: filepath.ToSlash(p),
				Message: "not gofmt-formatted; run gofmt -w on it"})
		}
	}
	return res
}

// vetLineRe is a `go vet` diagnostic: path:line:col: message.
var vetLineRe = regexp.MustCompile(`^(?:\./)?([^\s:]+\.go):(\d+)(?::\d+)?: (.+)$`)

// vet runs go vet over the packages of the touched files and keeps what it
// reports about those files.
func vet(ctx context.Context, r gitx.Runner, root string, files []string) []Finding {
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		return nil
	}
	if _, err := exec.LookPath("go"); err != nil {
		return nil
	}
	touched := map[string]bool{}
	var pkgs []string
	for _, f := range files {
		touched[f] = true
		pkg := "./" + path.Dir(f)
		if !slices.Contains(pkgs, pkg) {
			pkgs = append(pkgs, pkg)
		}
	}
	out, _, err := r(ctx, root, append([]string{"go", "vet"}, pkgs...))
	if err != nil {
		return nil
	}
	var res []Finding
	for _, line := range strings.Split(out, "\n") {
		m := vetLineRe.FindStringSubmatch(strings.TrimSpace(line))
		if m == nil {
			continue
		}
		p := filepath.ToSlash(m[1])
		if rel, err := filepath.Rel(root, m[1]); err == nil && filepath.IsAbs(m[1]) {
			p = filepath.ToSlash(rel)
		}
		if !touched[p] {
			continue
		}
		n, _ := strconv.Atoi(m[2])
		res = append(res, Finding{Check: CheckVet, Path: p, Line: n, Message: m[3]})
	}
	return res
}
