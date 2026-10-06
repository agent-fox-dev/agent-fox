package project

import (
	"os"
	"path/filepath"
	"strings"
)

// ReadRoot is a directory outside the repository the model may read but not
// change: the local directory a go.mod `replace` points at. A repository that
// develops a dependency beside itself (`replace example.com/lib => ../lib`)
// is built from that directory, so its API is what the work calls, and a
// model confined to the repository could only guess at it.
type ReadRoot struct {
	// Module is the module path the replace directive names.
	Module string
	// Path is the directory as go.mod writes it, e.g. ../agentkit-go.
	Path string
	// Abs is the directory, absolute and symlink-resolved.
	Abs string
}

// ReadRoots are the local replace targets of root's go.mod that exist, are
// directories and lie outside root, in the order go.mod lists them. A target
// under another one is covered by it and not listed again. A repository with
// no go.mod, or none that replaces a module by a local directory, has none.
func ReadRoots(root string) []ReadRoot {
	b, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		return nil
	}
	rootAbs := canonical(root)
	var found []ReadRoot
	inBlock := false
	for _, line := range strings.Split(string(b), "\n") {
		if i := strings.Index(line, "//"); i >= 0 {
			line = line[:i]
		}
		line = strings.TrimSpace(line)
		switch {
		case inBlock && line == ")":
			inBlock = false
			continue
		case inBlock:
		case strings.HasPrefix(line, "replace"):
			line = strings.TrimSpace(strings.TrimPrefix(line, "replace"))
			if line == "(" {
				inBlock = true
				continue
			}
		default:
			continue
		}
		if r, ok := localReplace(line, root, rootAbs); ok {
			found = append(found, r)
		}
	}
	var out []ReadRoot
	for _, r := range found {
		covered := false
		for _, o := range found {
			if o.Abs != r.Abs && within(o.Abs, r.Abs) {
				covered = true
				break
			}
		}
		if !covered && !containsAbs(out, r.Abs) {
			out = append(out, r)
		}
	}
	return out
}

// localReplace reads one replace directive, `old [v] => new [v]`, and reports
// it when new is a local directory outside the repository.
func localReplace(directive, root, rootAbs string) (ReadRoot, bool) {
	left, right, ok := strings.Cut(directive, "=>")
	if !ok {
		return ReadRoot{}, false
	}
	lhs, rhs := strings.Fields(left), strings.Fields(right)
	// A target with a version is a module, not a directory.
	if len(lhs) == 0 || len(rhs) != 1 {
		return ReadRoot{}, false
	}
	target := rhs[0]
	if !strings.HasPrefix(target, "./") && !strings.HasPrefix(target, "../") && !filepath.IsAbs(target) {
		return ReadRoot{}, false
	}
	dir := target
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(root, filepath.FromSlash(dir))
	}
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		return ReadRoot{}, false
	}
	abs := canonical(dir)
	if within(rootAbs, abs) {
		return ReadRoot{}, false
	}
	return ReadRoot{Module: lhs[0], Path: target, Abs: abs}, true
}

// canonical is p absolute and symlink-resolved: its deepest existing
// ancestor is resolved and the rest rejoined, so a path that does not exist
// yet compares with a resolved root.
func canonical(p string) string {
	abs, err := filepath.Abs(p)
	if err != nil {
		return filepath.Clean(p)
	}
	tail := ""
	for cur := abs; ; {
		if r, err := filepath.EvalSymlinks(cur); err == nil {
			return filepath.Join(r, tail)
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return abs
		}
		tail = filepath.Join(filepath.Base(cur), tail)
		cur = parent
	}
}

// within reports whether p is dir or under it.
func within(dir, p string) bool {
	rel, err := filepath.Rel(dir, p)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) &&
		!filepath.IsAbs(rel)
}

func containsAbs(rs []ReadRoot, abs string) bool {
	for _, r := range rs {
		if r.Abs == abs {
			return true
		}
	}
	return false
}

// UnderReadRoot reports whether p, an absolute path, is under one of roots
// once both are symlink-resolved: the check a guard makes before letting a
// read reach outside the repository.
func UnderReadRoot(roots []string, p string) bool {
	cp := canonical(p)
	for _, r := range roots {
		if within(canonical(r), cp) {
			return true
		}
	}
	return false
}
