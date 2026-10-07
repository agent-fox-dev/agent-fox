package conform

import (
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/agent-fox-dev/agentfox/internal/gitx"
	"github.com/agent-fox-dev/agentfox/internal/project"
)

// The structural checks for languages other than Go. Go is read with its
// own parser (scanGoFile); these read source text with patterns, which is a
// heuristic and says so: a function is found by its declaration and its
// extent by indentation (Python) or by matching braces outside strings and
// comments (everything else). What they report is the same as for Go — a
// function the change wrote or grew past --max-func-lines, and a test the
// change wrote that asserts nothing — so a Python or TypeScript change is
// held to the same two rules.

// fnSpan is one function: its name, its lines and whether it is a test.
type fnSpan struct {
	name     string
	from, to int
	test     bool
	body     string
}

// scanSourceFile applies the function checks to one non-Go source file.
func scanSourceFile(root, p string, added Added, maxLines int) []Finding {
	b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(p)))
	if err != nil {
		return nil
	}
	spans, asserts := sourceSpans(p, string(b))
	var out []Finding
	for _, fn := range spans {
		grown := added.Count(p, fn.from, fn.to)
		if grown == 0 {
			continue
		}
		if !fn.test {
			if length := fn.to - fn.from + 1; length > maxLines && length-grown <= maxLines {
				out = append(out, Finding{Check: CheckLongFunction, Path: p, Line: fn.from,
					Message: fmt.Sprintf("%s is %d lines long (the limit is %d); split it where its steps are",
						fn.name, length, maxLines)})
			}
			continue
		}
		if asserts != nil && !asserts.MatchString(fn.body) {
			out = append(out, Finding{Check: CheckNoAssertions, Path: p, Line: fn.from,
				Message: fmt.Sprintf("%s makes no assertion: nothing in it can fail, so it passes whatever "+
					"the code does", fn.name)})
		}
	}
	return out
}

var (
	pyDefRe    = regexp.MustCompile(`^(\s*)(?:async\s+)?def\s+(\w+)\s*\(`)
	pyAssertRe = regexp.MustCompile(`\bassert\b|pytest\.(raises|fail|warns)|self\.(assert\w*|fail)\b|\braise\b|\.assert_\w+\(`)

	jsFuncRe   = regexp.MustCompile(`\bfunction\s*\*?\s*(\w+)\s*\(|\b(?:const|let|var)\s+(\w+)\s*=\s*(?:async\s*)?(?:function\b|\([^)]*\)\s*(?::[^=]*)?=>|\w+\s*=>)`)
	jsTestRe   = regexp.MustCompile("\\b(?:it|test)\\s*\\(\\s*(['\"`])((?:[^'\"`\\\\]|\\\\.)*)['\"`]")
	jsAssertRe = regexp.MustCompile(`\bexpect\s*\(|\bassert\b|\.should\b|\bt\.(is|not|deepEqual|true|false|throws|snapshot)\b|\.(rejects|resolves)\b`)

	rustFnRe     = regexp.MustCompile(`\bfn\s+(\w+)`)
	rustTestRe   = regexp.MustCompile(`#\[(?:\w+::)*test\]`)
	rustAssertRe = regexp.MustCompile(`\b(debug_)?assert(_eq|_ne)?!|\bpanic!|\bunreachable!|should_panic|\.(unwrap_err|expect_err)\(`)

	kwFuncRe   = regexp.MustCompile(`\b(?:fun|func|function)\s+(\w+)\s*[(<]`)
	cFamilyRe  = regexp.MustCompile(`(?m)^[ \t]*(?:(?:public|private|protected|internal|static|final|abstract|override|virtual|async|synchronized|inline|const|extern|unsafe)\s+)*[\w<>\[\],.?*&:]+\s+(\w+)\s*\(`)
	notAFunc   = map[string]bool{"if": true, "for": true, "while": true, "switch": true, "catch": true, "return": true, "new": true, "else": true, "function": true, "sizeof": true}
	braceExts  = map[string]bool{".java": true, ".kt": true, ".cs": true, ".c": true, ".cc": true, ".cpp": true, ".h": true, ".hpp": true, ".swift": true, ".php": true, ".dart": true, ".scala": true}
	jsExts     = map[string]bool{".js": true, ".jsx": true, ".ts": true, ".tsx": true, ".mjs": true, ".cjs": true}
	kwFuncExts = map[string]bool{".kt": true, ".swift": true, ".php": true}
)

// sourceSpans finds a file's functions and the pattern a test of its language
// asserts with (nil when tests are not checked for it).
func sourceSpans(p, src string) ([]fnSpan, *regexp.Regexp) {
	ext := strings.ToLower(path.Ext(p))
	switch {
	case ext == ".py":
		return pythonSpans(src, project.IsTestPath(p)), pyAssertRe
	case jsExts[ext]:
		return jsSpans(src), jsAssertRe
	case ext == ".rs":
		return rustSpans(src), rustAssertRe
	case braceExts[ext]:
		re := cFamilyRe
		if kwFuncExts[ext] {
			re = kwFuncRe
		}
		return braceSpans(src, re, false), nil
	}
	return nil, nil
}

// pythonSpans are a Python file's functions, each running until the first
// non-blank line indented no deeper than its def. In a test file a function
// named test* is a test.
func pythonSpans(src string, testFile bool) []fnSpan {
	lines := strings.Split(src, "\n")
	var out []fnSpan
	for i, l := range lines {
		m := pyDefRe.FindStringSubmatch(l)
		if m == nil {
			continue
		}
		indent, end := len(m[1]), i
		for j := i + 1; j < len(lines); j++ {
			t := strings.TrimSpace(lines[j])
			if t == "" {
				continue
			}
			if len(lines[j])-len(strings.TrimLeft(lines[j], " \t")) <= indent {
				break
			}
			end = j
		}
		out = append(out, fnSpan{name: m[2], from: i + 1, to: end + 1,
			test: testFile && strings.HasPrefix(m[2], "test"),
			body: strings.Join(lines[i:end+1], "\n")})
	}
	return out
}

// jsSpans are a JavaScript or TypeScript file's named functions and its
// it()/test() blocks, which are its tests.
func jsSpans(src string) []fnSpan {
	var out []fnSpan
	for _, m := range jsFuncRe.FindAllStringSubmatchIndex(src, -1) {
		name := submatch(src, m, 1)
		if name == "" {
			name = submatch(src, m, 2)
		}
		if fn, ok := braceSpan(src, m[1], name, false); ok {
			out = append(out, fn)
		}
	}
	for _, m := range jsTestRe.FindAllStringSubmatchIndex(src, -1) {
		if fn, ok := braceSpan(src, m[1], submatch(src, m, 2), true); ok {
			fn.from = lineAt(src, m[0])
			out = append(out, fn)
		}
	}
	return out
}

// rustSpans are a Rust file's functions; one under a #[test] attribute is a
// test, wherever the file is.
func rustSpans(src string) []fnSpan {
	var out []fnSpan
	for _, m := range rustFnRe.FindAllStringSubmatchIndex(src, -1) {
		fn, ok := braceSpan(src, m[1], submatch(src, m, 1), false)
		if !ok {
			continue
		}
		lineStart := strings.LastIndexByte(src[:m[0]], '\n') + 1
		before := src[max(0, strings.LastIndex(src[:max(0, lineStart-1)], "\n\n")+1):lineStart]
		fn.test = rustTestRe.MatchString(before)
		if fn.test {
			fn.body = before + fn.body
		}
		out = append(out, fn)
	}
	return out
}

// braceSpans are the functions re finds whose body is a brace block.
func braceSpans(src string, re *regexp.Regexp, test bool) []fnSpan {
	var out []fnSpan
	for _, m := range re.FindAllStringSubmatchIndex(src, -1) {
		name := submatch(src, m, 1)
		if notAFunc[name] {
			continue
		}
		if fn, ok := braceSpan(src, m[1], name, test); ok {
			out = append(out, fn)
		}
	}
	return out
}

// braceSpan is the function whose declaration ends at from: its body is the
// first brace block after it, unless a ';' comes first (a declaration with no
// body).
func braceSpan(src string, from int, name string, test bool) (fnSpan, bool) {
	open := -1
	for i := from; i < len(src); i++ {
		if src[i] == ';' {
			return fnSpan{}, false
		}
		if src[i] == '{' {
			open = i
			break
		}
	}
	if open < 0 {
		return fnSpan{}, false
	}
	end, ok := matchBrace(src, open)
	if !ok {
		return fnSpan{}, false
	}
	start := strings.LastIndexByte(src[:from], '\n') + 1
	return fnSpan{name: name, from: lineAt(src, start), to: lineAt(src, end), test: test, body: src[start : end+1]}, true
}

// matchBrace is the index of the brace that closes the one at open, outside
// string literals and comments.
func matchBrace(src string, open int) (int, bool) {
	depth := 0
	for i := open; i < len(src); i++ {
		switch c := src[i]; {
		case c == '/' && i+1 < len(src) && src[i+1] == '/':
			for i < len(src) && src[i] != '\n' {
				i++
			}
		case c == '/' && i+1 < len(src) && src[i+1] == '*':
			end := strings.Index(src[i+2:], "*/")
			if end < 0 {
				return 0, false
			}
			i += end + 3
		case c == '"' || c == '`':
			for i++; i < len(src) && src[i] != c; i++ {
				if src[i] == '\\' {
					i++
				}
			}
		case c == '{':
			depth++
		case c == '}':
			depth--
			if depth == 0 {
				return i, true
			}
		}
	}
	return 0, false
}

func submatch(src string, m []int, n int) string {
	if 2*n+1 >= len(m) || m[2*n] < 0 {
		return ""
	}
	return src[m[2*n]:m[2*n+1]]
}

// lineAt is the 1-based line of byte offset i.
func lineAt(src string, i int) int { return strings.Count(src[:i], "\n") + 1 }

// rustDiscardRe is a Rust value thrown away with `let _ =`.
var rustDiscardRe = regexp.MustCompile(`^\s*let\s+_\s*=\s*\S`)

// formatters run each language's formatter over the touched files, when it
// is installed (it answers --version): the check gofmt is for Go.
var formatters = []struct {
	exts  map[string]bool
	tools []formatter
}{
	{map[string]bool{".rs": true}, []formatter{{"rustfmt", []string{"rustfmt", "--check"}, regexp.MustCompile(`^Diff in (.+?)(?::\d+:| at line \d+:)`), "rustfmt"}}},
	{map[string]bool{".py": true}, []formatter{
		{"ruff", []string{"ruff", "format", "--check"}, regexp.MustCompile(`^Would reformat: (.+)$`), "ruff format"},
		{"black", []string{"black", "--check"}, regexp.MustCompile(`^would reformat (.+)$`), "black"},
	}},
	{jsExts, []formatter{{"prettier", []string{"prettier", "--check"}, regexp.MustCompile(`^\[warn\] (\S+\.\w+)$`), "prettier --write"}}},
}

type formatter struct {
	name string
	argv []string
	line *regexp.Regexp
	fix  string
}

// formatChecks runs the first installed formatter of each language over its
// touched files and reports each file it would change.
func formatChecks(ctx context.Context, r gitx.Runner, root string, files []string) []Finding {
	var out []Finding
	for _, f := range formatters {
		var mine []string
		for _, p := range files {
			if f.exts[strings.ToLower(path.Ext(p))] {
				mine = append(mine, p)
			}
		}
		if len(mine) == 0 {
			continue
		}
		for _, tool := range f.tools {
			if _, code, err := r(ctx, root, []string{tool.name, "--version"}); err != nil || code != 0 {
				continue
			}
			text, _, err := r(ctx, root, append(append([]string(nil), tool.argv...), mine...))
			if err != nil {
				break
			}
			seen := map[string]bool{}
			for _, line := range strings.Split(text, "\n") {
				m := tool.line.FindStringSubmatch(strings.TrimSpace(line))
				if m == nil {
					continue
				}
				p := filepath.ToSlash(strings.TrimSpace(m[1]))
				if rel, err := filepath.Rel(root, m[1]); err == nil && filepath.IsAbs(m[1]) {
					p = filepath.ToSlash(rel)
				}
				if seen[p] {
					continue
				}
				seen[p] = true
				out = append(out, Finding{Check: CheckFormat, Path: p,
					Message: fmt.Sprintf("not formatted as %s formats it; run %s on it", tool.name, tool.fix)})
			}
			break
		}
	}
	return out
}
