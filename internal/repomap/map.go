// Package repomap renders a map of a repository that fits a token budget: the
// directories, the files and the declarations in them, as far as the budget
// allows, the same bytes for the same tree and budget.
package repomap

import (
	"cmp"
	"context"
	"fmt"
	"path"
	"slices"
	"strings"
	"unicode"
)

// The range of budgets, in tokens.
const (
	MinTokens = 200
	MaxTokens = 100000
)

// MaxOutlineFiles is how many files a view may have for the outliner to be
// called at all.
const MaxOutlineFiles = 5000

// MaxOutlineBytes is the size over which a file is not outlined.
const MaxOutlineBytes = 1 << 20

// WarnOutlineSkipped is the code of the warning that the outliner was not run.
const WarnOutlineSkipped = "map_outline_skipped"

// capsAtLevelThree are how many declarations of a file level 3 keeps, tried
// in turn.
var capsAtLevelThree = []int{20, 10, 5, 2, 1}

// Decl is one declaration of a file.
type Decl struct {
	Kind string
	Name string
	Line int
}

// Outliner lists the declarations of a file. An error means the file cannot be
// outlined, and the map lists it by name only.
type Outliner interface {
	Outline(ctx context.Context, path string) ([]Decl, error)
}

// Entry is a file of the view.
type Entry struct {
	Path string // slash-separated, relative to the root of the view
	Size int64  // negative when it is not known
}

// Warning is something the map did differently than asked.
type Warning struct {
	Code    string
	Message string
}

// Result is a map.
type Result struct {
	Map             string `json:"map"`
	Tokens          int    `json:"tokens"`
	EstimatedTokens int    `json:"estimated_tokens"`
	Level           int    `json:"level"`
	FilesTotal      int    `json:"files_total"`
	FilesOutlined   int    `json:"files_outlined"`
	Truncated       bool   `json:"truncated"`

	// Warnings are what the map did differently than asked; they are not part
	// of it.
	Warnings []Warning `json:"-"`
}

// BudgetError says that the budget is outside the range of budgets.
type BudgetError struct {
	Tokens int
}

func (e *BudgetError) Error() string {
	return fmt.Sprintf("a budget of %d tokens is outside the range from %d to %d", e.Tokens, MinTokens, MaxTokens)
}

// file is a file of the view, with its declarations if they were read.
type file struct {
	name  string // its name in its directory
	test  bool
	decls []Decl
}

// directory is a directory of the view that has files, and the files.
type directory struct {
	path  string // "." for the root
	files []file
}

// header is the line that names a directory.
func (d directory) header() string {
	if d.path == "." {
		return "./\n"
	}
	return clean(d.path) + "/\n"
}

// Build renders the map of files within the budget of tokens: the first level
// of the ladder whose output fits. The map is a function of the files, the
// declarations the outliner gives and the budget, and of nothing else, and it
// never takes more than the budget, with a token estimated as 4 bytes.
//
// The outliner is not called for a file over MaxOutlineBytes, and not at all
// when there are more than MaxOutlineFiles files or the names alone do not
// fit the budget (the result then has the warning map_outline_skipped). An
// error from it means the file is listed by name only, except the error of a
// context that has ended, which is the error of the build.
func Build(ctx context.Context, files []Entry, o Outliner, tokens int) (Result, error) {
	if tokens < MinTokens || tokens > MaxTokens {
		return Result{}, &BudgetError{Tokens: tokens}
	}
	budget := tokens * 4
	entries, dirs := group(files)
	res := Result{Tokens: tokens, FilesTotal: len(entries)}

	reason := ""
	if len(entries) > MaxOutlineFiles {
		reason = fmt.Sprintf("the view has %d files, more than the %d that are outlined", len(entries), MaxOutlineFiles)
	} else if _, fits := render(dirs, budget, nil); !fits {
		reason = "the names of the files alone do not fit the budget"
	}
	if reason != "" {
		res.Warnings = append(res.Warnings, Warning{Code: WarnOutlineSkipped, Message: "the declarations were not read: " + reason})
	} else {
		outlined, err := outlineAll(ctx, entries, dirs, o)
		if err != nil {
			return Result{}, err
		}
		res.FilesOutlined = outlined
	}

	res.Map, res.Level = ladder(dirs, budget, reason != "")
	res.EstimatedTokens = (len(res.Map) + 3) / 4
	res.Truncated = res.Level > 1
	return res, nil
}

// group sorts the files into the directories they are in. Directories come in
// path order, a directory before its subdirectories, and files by name; a path
// given twice is one file.
func group(files []Entry) ([]Entry, []directory) {
	entries := slices.Clone(files)
	entries = slices.DeleteFunc(entries, func(e Entry) bool { return e.Path == "" })
	slices.SortStableFunc(entries, func(a, b Entry) int { return strings.Compare(a.Path, b.Path) })
	entries = slices.CompactFunc(entries, func(a, b Entry) bool { return a.Path == b.Path })

	byDir := map[string]*directory{}
	var dirs []*directory
	for _, e := range entries {
		dir := path.Dir(e.Path)
		d := byDir[dir]
		if d == nil {
			d = &directory{path: dir}
			byDir[dir] = d
			dirs = append(dirs, d)
		}
		d.files = append(d.files, file{name: path.Base(e.Path), test: isTest(e.Path)})
	}
	slices.SortFunc(dirs, func(a, b *directory) int { return compareDirs(a.path, b.path) })
	out := make([]directory, len(dirs))
	for i, d := range dirs {
		out[i] = *d
	}
	return entries, out
}

// compareDirs orders directories as a tree is walked: by the names in their
// paths, one after the other, so that a directory comes before its
// subdirectories, and the root first.
func compareDirs(a, b string) int {
	if a == "." || b == "." {
		return cmp.Compare(boolInt(b == "."), boolInt(a == "."))
	}
	return slices.Compare(strings.Split(a, "/"), strings.Split(b, "/"))
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// outlineAll reads the declarations of every file the outliner may be asked
// about, into dirs, in path order, and returns how many files it outlined.
func outlineAll(ctx context.Context, entries []Entry, dirs []directory, o Outliner) (int, error) {
	sizes := make(map[string]int64, len(entries))
	for _, e := range entries {
		sizes[e.Path] = e.Size
	}
	outlined := 0
	for di := range dirs {
		for fi := range dirs[di].files {
			f := &dirs[di].files[fi]
			full := path.Join(dirs[di].path, f.name)
			if sizes[full] > MaxOutlineBytes {
				continue
			}
			decls, err := o.Outline(ctx, full)
			if cerr := ctx.Err(); cerr != nil {
				return 0, cerr
			}
			if err != nil {
				continue
			}
			f.decls = decls
			outlined++
		}
	}
	return outlined, nil
}

// ladder returns the first level whose output fits the budget (bytes) and its
// number. With skipped set, the declarations were not read: only the levels
// that do not show them are tried.
func ladder(dirs []directory, budget int, skipped bool) (string, int) {
	if !skipped {
		if text, fits := render(dirs, budget, allDecls); fits {
			return text, 1
		}
		if text, fits := render(dirs, budget, untestedDecls(-1)); fits {
			return text, 2
		}
		for _, k := range capsAtLevelThree {
			if text, fits := render(dirs, budget, untestedDecls(k)); fits {
				return text, 3
			}
		}
	}
	if text, fits := render(dirs, budget, nil); fits {
		return text, 4
	}
	if text, fits := renderDirectories(dirs, budget); fits {
		return text, 5
	}
	return cutDirectories(dirs, budget), 6
}

// shown says which declarations of a file the map shows and how many it cuts
// away.
type shown func(f file) (decls []Decl, more int)

// allDecls shows every declaration of every file.
func allDecls(f file) ([]Decl, int) { return f.decls, 0 }

// untestedDecls shows the declarations of the files that are not tests, at most
// k of them when k is not negative, and none of those of tests.
func untestedDecls(k int) shown {
	return func(f file) ([]Decl, int) {
		switch {
		case f.test:
			return nil, 0
		case k >= 0 && len(f.decls) > k:
			return f.decls[:k], len(f.decls) - k
		}
		return f.decls, 0
	}
}

// render lays the directories and files out with the declarations that show
// chooses (none if it is nil), and reports whether the text fits the budget.
// It stops as soon as it does not.
func render(dirs []directory, budget int, show shown) (string, bool) {
	var b strings.Builder
	for _, d := range dirs {
		b.WriteString(d.header())
		for _, f := range d.files {
			b.WriteString("  ")
			b.WriteString(clean(f.name))
			if show != nil {
				decls, more := show(f)
				if items := declItems(decls, more); len(items) > 0 {
					b.WriteString(": ")
					b.WriteString(strings.Join(items, " · "))
				}
			}
			b.WriteByte('\n')
			if b.Len() > budget {
				return "", false
			}
		}
		if b.Len() > budget {
			return "", false
		}
	}
	return b.String(), b.Len() <= budget
}

// declItems are the declarations as the map writes them, and a last item that
// says how many were cut away.
func declItems(decls []Decl, more int) []string {
	items := make([]string, 0, len(decls)+1)
	for _, d := range decls {
		items = append(items, fmt.Sprintf("%s %s L%d", clean(d.Kind), clean(d.Name), d.Line))
	}
	if more > 0 {
		items = append(items, fmt.Sprintf("… +%d more", more))
	}
	return items
}

// dirLine is the line of a directory in the levels that list directories only.
func dirLine(d directory) string {
	return fmt.Sprintf("%s (%d files)\n", strings.TrimSuffix(d.header(), "\n"), len(d.files))
}

// renderDirectories lists the directories and how many files each has.
func renderDirectories(dirs []directory, budget int) (string, bool) {
	var b strings.Builder
	for _, d := range dirs {
		b.WriteString(dirLine(d))
		if b.Len() > budget {
			return "", false
		}
	}
	return b.String(), true
}

// cutDirectories lists as many directories as the budget allows, and then how
// many it left out. It always fits: the line that says what was left out is
// kept room for, whatever the names are, and a budget with no room even for
// that gets an empty map.
func cutDirectories(dirs []directory, budget int) string {
	reserve := len(fmt.Sprintf("… %d more directories\n", len(dirs)))
	var b strings.Builder
	listed := 0
	for i, d := range dirs {
		line := dirLine(d)
		need := b.Len() + len(line)
		if i < len(dirs)-1 {
			need += reserve
		}
		if need > budget {
			break
		}
		b.WriteString(line)
		listed++
	}
	if listed < len(dirs) {
		footer := fmt.Sprintf("… %d more directories\n", len(dirs)-listed)
		if b.Len()+len(footer) > budget {
			return ""
		}
		b.WriteString(footer)
	}
	return b.String()
}

// isTest reports whether a path is a test, as the revert check says.
func isTest(p string) bool { return testPath(p) }

// clean is a name as it is written in the map: a control character, which
// would end the line or hide in it, is replaced by a question mark, and bytes
// that are no text by a replacement character, which is what the document that
// carries the map would make of them, at three bytes for each.
func clean(s string) string {
	s = strings.ToValidUTF8(s, "\uFFFD")
	if !strings.ContainsFunc(s, isUnsafe) {
		return s
	}
	return strings.Map(func(r rune) rune {
		if isUnsafe(r) {
			return '?'
		}
		return r
	}, s)
}

func isUnsafe(r rune) bool {
	return unicode.IsControl(r) || unicode.Is(unicode.Zl, r) || unicode.Is(unicode.Zp, r)
}
