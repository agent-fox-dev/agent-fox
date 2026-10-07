package project

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
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
	// Source is the manifest that declares it: go.mod, package.json,
	// Cargo.toml or requirements.txt.
	Source string
}

// ReadRoots are the local dependencies of root that exist, are directories and
// lie outside it: go.mod's local replace targets, package.json's file: and
// link: dependencies, Cargo.toml's path dependencies and requirements.txt's
// local (-e) paths, in that order. A target under another one is covered by
// it and not listed again. A repository with none has none.
func ReadRoots(root string) []ReadRoot {
	rootAbs := canonical(root)
	found := goReplaceRoots(root, rootAbs)
	found = append(found, otherRoots(root, rootAbs)...)
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

// goReplaceRoots are go.mod's local replace targets.
func goReplaceRoots(root, rootAbs string) []ReadRoot {
	b, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		return nil
	}
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
	return found
}

var (
	// cargoPathRe is a Cargo dependency with a local path:
	// `name = { path = "../lib", ... }`.
	cargoPathRe = regexp.MustCompile(`(?m)^\s*([A-Za-z0-9_-]+)\s*=\s*\{[^}\n]*\bpath\s*=\s*"([^"]+)"`)
	// pipLocalRe is a requirements line that installs a local directory:
	// `-e ../lib`, `--editable ../lib` or a bare relative path.
	pipLocalRe = regexp.MustCompile(`^(?:-e|--editable)?\s*(\.\.?/\S+|/\S+)\s*$`)
)

// otherRoots are the local dependencies the other ecosystems' manifests
// declare.
func otherRoots(root, rootAbs string) []ReadRoot {
	var found []ReadRoot
	add := func(name, target, source string) {
		if r, ok := localDir(name, target, source, root, rootAbs); ok {
			found = append(found, r)
		}
	}
	if b, err := os.ReadFile(filepath.Join(root, "package.json")); err == nil {
		var pkg map[string]json.RawMessage
		if json.Unmarshal(b, &pkg) == nil {
			for _, field := range []string{"dependencies", "devDependencies", "peerDependencies", "optionalDependencies"} {
				var deps map[string]string
				if json.Unmarshal(pkg[field], &deps) != nil {
					continue
				}
				names := make([]string, 0, len(deps))
				for n := range deps {
					names = append(names, n)
				}
				sort.Strings(names)
				for _, n := range names {
					for _, prefix := range []string{"file:", "link:"} {
						if target, ok := strings.CutPrefix(deps[n], prefix); ok {
							add(n, target, "package.json")
						}
					}
				}
			}
		}
	}
	if b, err := os.ReadFile(filepath.Join(root, "Cargo.toml")); err == nil {
		for _, m := range cargoPathRe.FindAllStringSubmatch(string(b), -1) {
			add(m[1], m[2], "Cargo.toml")
		}
	}
	if b, err := os.ReadFile(filepath.Join(root, "requirements.txt")); err == nil {
		for _, line := range strings.Split(string(b), "\n") {
			if m := pipLocalRe.FindStringSubmatch(strings.TrimSpace(line)); m != nil {
				add(m[1], m[1], "requirements.txt")
			}
		}
	}
	return found
}

// localDir is target, declared as dependency name by source, as a read root
// when it is an existing directory outside the repository.
func localDir(name, target, source, root, rootAbs string) (ReadRoot, bool) {
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
	return ReadRoot{Module: name, Path: target, Abs: abs, Source: source}, true
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
	return ReadRoot{Module: lhs[0], Path: target, Abs: abs, Source: "go.mod"}, true
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
