package conform

import (
	"strconv"
	"strings"
)

// Added is what a change added, per file: the new line numbers and their
// text, from a unified diff with no context. A file the change deleted has
// no entry.
type Added map[string]map[int]string

// ParseAdded reads the added lines out of `git diff -U0` output.
func ParseAdded(patch string) Added {
	out := Added{}
	var file string
	next := 0
	for _, line := range strings.Split(patch, "\n") {
		switch {
		case strings.HasPrefix(line, "diff --git "):
			file, next = "", 0
		case strings.HasPrefix(line, "+++ "):
			p := strings.TrimPrefix(line, "+++ ")
			if p == "/dev/null" {
				file = ""
				continue
			}
			file = strings.TrimPrefix(unquote(p), "b/")
			if out[file] == nil {
				out[file] = map[int]string{}
			}
		case strings.HasPrefix(line, "@@"):
			next = hunkStart(line)
		case strings.HasPrefix(line, "+") && file != "" && next > 0:
			out[file][next] = line[1:]
			next++
		}
	}
	return out
}

// Touches reports whether any of lines from..to (inclusive) of path was
// added.
func (a Added) Touches(path string, from, to int) bool { return a.Count(path, from, to) > 0 }

// Count is how many of lines from..to (inclusive) of path were added.
func (a Added) Count(path string, from, to int) int {
	n := 0
	for line := range a[path] {
		if line >= from && line <= to {
			n++
		}
	}
	return n
}

// hunkStart is the first new line number of a hunk header
// "@@ -a,b +c,d @@".
func hunkStart(header string) int {
	_, rest, ok := strings.Cut(header, " +")
	if !ok {
		return 0
	}
	num, _, _ := strings.Cut(rest, " ")
	num, _, _ = strings.Cut(num, ",")
	n, err := strconv.Atoi(num)
	if err != nil {
		return 0
	}
	return n
}

func unquote(p string) string {
	if strings.HasPrefix(p, `"`) {
		if s, err := strconv.Unquote(p); err == nil {
			return s
		}
	}
	return p
}
