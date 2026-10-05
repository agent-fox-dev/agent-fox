package conform

import (
	"fmt"
	"hash/fnv"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/agent-fox-dev/agentfox/internal/project"
)

// maxIndexedFileBytes bounds what one file contributes to the duplication
// index: generated and vendored blobs are not where a change copies from.
const maxIndexedFileBytes = 512 << 10

// importSpecRe is a line of an import block: a quoted path, perhaps named.
// Two files importing the same packages are not a copy.
var importSpecRe = regexp.MustCompile(`^(\w+ |\. |_ )?"[^"]*"$`)

// meaningful is one line that counts toward a duplicate: its text with the
// whitespace collapsed, and where it is.
type meaningful struct {
	text string
	line int
}

// meaningfulLines drops what every file shares: blank lines, comments, and
// lines that are only punctuation.
func meaningfulLines(src string) []meaningful {
	var out []meaningful
	for i, raw := range strings.Split(src, "\n") {
		t := strings.Join(strings.Fields(raw), " ")
		if t == "" || strings.Trim(t, "{}()[];,") == "" {
			continue
		}
		if strings.HasPrefix(t, "//") || strings.HasPrefix(t, "#") || strings.HasPrefix(t, "*") ||
			strings.HasPrefix(t, "/*") || strings.HasPrefix(t, "import ") || strings.HasPrefix(t, "package ") ||
			importSpecRe.MatchString(t) {
			continue
		}
		out = append(out, meaningful{t, i + 1})
	}
	return out
}

func windowHash(lines []meaningful) uint64 {
	h := fnv.New64a()
	for _, l := range lines {
		h.Write([]byte(l.text))
		h.Write([]byte{'\n'})
	}
	return h.Sum64()
}

func addedIn(added Added, p string, win []meaningful) int {
	n := 0
	for _, l := range win {
		if _, ok := added[p][l.line]; ok {
			n++
		}
	}
	return n
}

type site struct {
	path string
	line int
}

// duplicates reports where the change wrote code that already exists
// elsewhere in the repository: duplicateWindow consecutive meaningful lines,
// at least half of them added, that match a window in another place. Tests are
// not checked — a fixture repeated per test is a style, not a second
// implementation.
func duplicates(root string, changed []string, added Added, tracked []string) []Finding {
	exts := map[string]bool{}
	var subjects []string
	for _, p := range changed {
		if project.IsSourceFile(p) && !project.IsTestPath(p) && len(added[p]) > 0 {
			subjects = append(subjects, p)
			exts[path.Ext(p)] = true
		}
	}
	if len(subjects) == 0 {
		return nil
	}

	read := func(p string) []meaningful {
		full := filepath.Join(root, filepath.FromSlash(p))
		info, err := os.Stat(full)
		if err != nil || info.IsDir() || info.Size() > maxIndexedFileBytes {
			return nil
		}
		b, err := os.ReadFile(full)
		if err != nil {
			return nil
		}
		return meaningfulLines(string(b))
	}

	index := map[uint64][]site{}
	for _, p := range tracked {
		if !exts[path.Ext(p)] || project.IsTestPath(p) {
			continue
		}
		lines := read(p)
		for i := 0; i+duplicateWindow <= len(lines); i++ {
			h := windowHash(lines[i : i+duplicateWindow])
			index[h] = append(index[h], site{p, lines[i].line})
		}
	}

	var out []Finding
	for _, p := range subjects {
		lines := read(p)
		reportedUntil := 0
		for i := 0; i+duplicateWindow <= len(lines); i++ {
			win := lines[i : i+duplicateWindow]
			// At least half the window is new: the copy is what the change
			// wrote, not a line it added beside an older look-alike.
			if win[0].line <= reportedUntil || addedIn(added, p, win)*2 < len(win) {
				continue
			}
			var other *site
			for _, s := range index[windowHash(win)] {
				// The window itself, or one overlapping it, is not a copy.
				if s.path == p && s.line >= win[0].line-duplicateWindow && s.line <= win[len(win)-1].line {
					continue
				}
				other = &s
				break
			}
			if other == nil {
				continue
			}
			out = append(out, Finding{Check: CheckDuplicate, Path: p, Line: win[0].line,
				Message: fmt.Sprintf("these lines repeat %s:%d; extract the shared code rather than keep "+
					"two copies", other.path, other.line)})
			reportedUntil = win[len(win)-1].line
		}
	}
	return out
}
