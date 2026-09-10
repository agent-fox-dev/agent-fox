package codeimpl

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/agent-fox-dev/agentfox/afspec"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

// DefaultSpecDirName is where spec packages live under the repository root.
const DefaultSpecDirName = ".specs"

// SpecDirEnv overrides the spec root. The --specs-dir flag wins over it.
const SpecDirEnv = "AF_SPEC_DIR"

// ResolveSpecsDir picks the spec root: the flag, then the environment, then
// the repository's own .specs. A relative value is taken against root.
func ResolveSpecsDir(flag, root string) string {
	pick := func(v string) string {
		if filepath.IsAbs(v) {
			return filepath.Clean(v)
		}
		return filepath.Join(root, v)
	}
	if v := strings.TrimSpace(flag); v != "" {
		return pick(v)
	}
	if v := strings.TrimSpace(os.Getenv(SpecDirEnv)); v != "" {
		return pick(v)
	}
	return filepath.Join(root, DefaultSpecDirName)
}

// Locate turns the classified input into the absolute directory of one spec
// package.
//
// The input is a reference, not a document. A file — Resolve reads a
// regular file — names the package it is in. Text is tried as a directory
// path first, relative to the working directory and then to the repository,
// and then against the packages under the spec root as a directory name, a
// spec id or a spec name. A reference that matches two packages is refused
// naming both, and one that matches none is refused naming what is there.
func Locate(in toolio.Input, root, specsDir string) (string, error) {
	switch in.Kind {
	case toolio.KindIssue:
		return "", toolio.Usagef("impl takes a spec package, not an issue: %s names one; "+
			"give a spec directory, id or name", in.Origin)
	case toolio.KindFile:
		dir := filepath.Dir(in.Origin)
		abs, err := filepath.Abs(dir)
		if err != nil {
			return "", toolio.Usagef("resolving %s: %v", dir, err)
		}
		if !isSpecDir(abs) {
			return "", toolio.Usagef("%s is not inside a spec package: %s has no prd.md", in.Origin, abs)
		}
		return abs, nil
	}

	ref := strings.TrimSpace(in.Body)
	if strings.ContainsAny(ref, "\n") {
		return "", toolio.Usagef("the input is several lines; impl takes one reference to a spec package")
	}
	if ref == "" {
		return "", toolio.Usagef("no spec named: give a spec directory, id or name")
	}

	// A path, first. Relative to the working directory the way Resolve
	// stats a file, then relative to the repository the tool is working in.
	for _, candidate := range pathCandidates(ref, root) {
		if isSpecDir(candidate) {
			return candidate, nil
		}
	}

	metas, err := afspec.DiscoverSpecs(specsDir)
	if err != nil {
		return "", toolio.Usagef("%s does not name a spec package, and the spec root %s "+
			"could not be read: %v", ref, specsDir, err)
	}
	var matches []afspec.SpecMeta
	for _, m := range metas {
		if matchesRef(m, ref) {
			matches = append(matches, m)
		}
	}
	switch len(matches) {
	case 1:
		abs, err := filepath.Abs(matches[0].Dir)
		if err != nil {
			return "", toolio.Usagef("resolving %s: %v", matches[0].Dir, err)
		}
		return abs, nil
	case 0:
		return "", toolio.Usagef("%s names no spec package under %s; %s", ref, specsDir, available(metas))
	default:
		names := make([]string, 0, len(matches))
		for _, m := range matches {
			names = append(names, filepath.Base(m.Dir))
		}
		return "", toolio.Usagef("%s names %d spec packages (%s); give the directory name",
			ref, len(matches), strings.Join(names, ", "))
	}
}

func pathCandidates(ref, root string) []string {
	var out []string
	if filepath.IsAbs(ref) {
		return []string{filepath.Clean(ref)}
	}
	if abs, err := filepath.Abs(ref); err == nil {
		out = append(out, abs)
	}
	out = append(out, filepath.Join(root, ref))
	return out
}

func isSpecDir(dir string) bool {
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		return false
	}
	prd, err := os.Stat(filepath.Join(dir, "prd.md"))
	return err == nil && prd.Mode().IsRegular()
}

func matchesRef(m afspec.SpecMeta, ref string) bool {
	return filepath.Base(m.Dir) == ref || m.SpecID == ref || m.SpecName == ref
}

func available(metas []afspec.SpecMeta) string {
	if len(metas) == 0 {
		return "it holds no spec packages"
	}
	names := make([]string, 0, len(metas))
	for _, m := range metas {
		names = append(names, filepath.Base(m.Dir))
	}
	sort.Strings(names)
	return fmt.Sprintf("it holds %s", strings.Join(names, ", "))
}
