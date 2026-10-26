package box

import (
	"archive/tar"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/agent-fox-dev/agentfox/internal/toolio"
	"github.com/agent-fox-dev/agentkit-go/sandbox"
	"golang.org/x/mod/modfile"
)

// vendorDir is where a local replace target outside the work tree is copied
// inside the box, so that go.mod can be rewritten to reach it with a path
// that exists there, whatever the provider: a sibling directory the host
// keeps next to the work tree is not something a box of any provider has.
const vendorDir = agentDir + "vendor"

// localReplace is one replace directive of the work tree's go.mod whose new
// path is a directory, not a module and version, and resolves outside dir.
type localReplace struct {
	oldPath, oldVersion string
	target              string // absolute, with its links resolved
}

// localReplacesOutside reads go.mod at the root of dir, and returns the
// replace directives whose new path is a local directory outside dir. A
// repository with no go.mod, or none of whose replace directives leave dir,
// returns nil: the common case, found with one read and one parse.
func localReplacesOutside(dir string) ([]localReplace, error) {
	data, err := os.ReadFile(filepath.Join(dir, "go.mod"))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, &toolio.Failure{Category: toolio.CategoryIO, Message: fmt.Sprintf("cannot read %s/go.mod: %v", dir, err)}
	}
	f, err := modfile.Parse("go.mod", data, nil)
	if err != nil {
		return nil, &toolio.Failure{Category: toolio.CategoryConfig, Message: fmt.Sprintf("cannot parse %s/go.mod: %v", dir, err)}
	}
	var out []localReplace
	for _, r := range f.Replace {
		if r.New.Version != "" {
			continue // a module and a version, not a local directory
		}
		abs, err := filepath.Abs(filepath.Join(dir, filepath.FromSlash(r.New.Path)))
		if err != nil {
			return nil, &toolio.Failure{Category: toolio.CategoryConfig, Message: fmt.Sprintf("%s/go.mod: replace %s => %s: %v", dir, r.Old.Path, r.New.Path, err)}
		}
		real, err := filepath.EvalSymlinks(abs)
		if err != nil {
			return nil, &toolio.Failure{Category: toolio.CategoryConfig, Message: fmt.Sprintf("%s/go.mod: replace %s => %s: %v", dir, r.Old.Path, r.New.Path, err)}
		}
		if real == dir || strings.HasPrefix(real, dir+string(filepath.Separator)) {
			continue // inside the work tree already, and so part of the sync
		}
		out = append(out, localReplace{oldPath: r.Old.Path, oldVersion: r.Old.Version, target: real})
	}
	return out, nil
}

// vendorPlan is where each local replace target lands inside the box: the
// fewest directories that cover every target, each under vendorDir named
// after its last path element, and the replace directive rewritten to the
// box path of the target inside its root.
type vendorPlan struct {
	roots    []string          // absolute host directories to copy, outermost first
	rootName map[string]string // root -> its name under vendorDir
	boxPath  map[localReplace]string
}

// plan finds the roots of replaces: a root is a target that is not itself
// inside another target, so that copying the roots copies every target, and
// names each root after its last path element, with -2, -3 added for a name
// another root already has.
func plan(replaces []localReplace) vendorPlan {
	p := vendorPlan{rootName: map[string]string{}, boxPath: map[localReplace]string{}}
	targets := make([]string, 0, len(replaces))
	for _, r := range replaces {
		if !slices.Contains(targets, r.target) {
			targets = append(targets, r.target)
		}
	}
	sort.Slice(targets, func(i, j int) bool { return len(targets[i]) < len(targets[j]) })
	for _, t := range targets {
		if !coveredByAny(p.roots, t) {
			p.roots = append(p.roots, t)
		}
	}
	used := map[string]bool{}
	for _, root := range p.roots {
		name := filepath.Base(root)
		for n := 2; used[name]; n++ {
			name = fmt.Sprintf("%s-%d", filepath.Base(root), n)
		}
		used[name] = true
		p.rootName[root] = name
	}
	for _, r := range replaces {
		root := coveringRoot(p.roots, r.target)
		rel, _ := filepath.Rel(root, r.target)
		boxPath := path.Join(vendorDir, p.rootName[root])
		if rel != "." {
			boxPath = path.Join(boxPath, filepath.ToSlash(rel))
		}
		// go.mod requires a directory replace to be rooted or start with . or ..;
		// a bare "relative/path" is refused as a module path instead.
		p.boxPath[r] = "./" + boxPath
	}
	return p
}

// coveredByAny says whether target is root or inside one of roots.
func coveredByAny(roots []string, target string) bool {
	return coveringRoot(roots, target) != ""
}

// coveringRoot is the root of roots that is target or an ancestor of it, or "".
func coveringRoot(roots []string, target string) string {
	for _, root := range roots {
		if target == root || strings.HasPrefix(target, root+string(filepath.Separator)) {
			return root
		}
	}
	return ""
}

// goWorkPath is the workspace file the box's go.work override is written to,
// next to go.mod. It is never git-tracked: unlike go.mod, a change to it must
// never look like part of the change a brain makes, so the override lives in
// a file nothing commits, not in go.mod itself.
const goWorkPath = "go.work"

// buildGoWork returns a go.work for dir's module that replaces p's targets
// with their path inside the box, leaving go.mod itself untouched. Go reads
// go.work's replace directives over go.mod's own for the same module, so the
// build inside the box resolves the targets the same way a build on the
// host's real tree does.
func buildGoWork(dir string, p vendorPlan) ([]byte, error) {
	data, err := os.ReadFile(filepath.Join(dir, "go.mod"))
	if err != nil {
		return nil, err
	}
	mod, err := modfile.Parse("go.mod", data, nil)
	if err != nil {
		return nil, err
	}
	wf, err := modfile.ParseWork(goWorkPath, nil, nil)
	if err != nil {
		return nil, err
	}
	if mod.Go != nil {
		if err := wf.AddGoStmt(mod.Go.Version); err != nil {
			return nil, err
		}
	}
	if mod.Module != nil {
		if err := wf.AddUse(".", mod.Module.Mod.Path); err != nil {
			return nil, err
		}
	}
	for r, boxPath := range p.boxPath {
		if err := wf.AddReplace(r.oldPath, r.oldVersion, boxPath, ""); err != nil {
			return nil, err
		}
	}
	wf.Cleanup()
	return modfile.Format(wf.Syntax), nil
}

// syncVendor copies the local replace targets of dir's go.mod that fall
// outside dir into the box under vendorDir, and sends a go.work that
// redirects those replaces to reach them there, so a build inside the box
// resolves the same modules a build on the host does. go.mod is never
// touched: go.work is a file nothing tracks, so the override can never be
// mistaken for part of a brain's own change. A repository with nothing to
// vendor is untouched: the common case costs one read and one parse of
// go.mod, no sync.
func (s *Session) syncVendor(ctx context.Context) error {
	replaces, err := localReplacesOutside(s.hostDir)
	if err != nil {
		return err
	}
	if len(replaces) == 0 {
		return nil
	}
	p := plan(replaces)
	goWork, err := buildGoWork(s.hostDir, p)
	if err != nil {
		return &toolio.Failure{Category: toolio.CategoryConfig, Message: fmt.Sprintf("cannot build a go.work for the box to reach the replaces of %s/go.mod: %v", s.hostDir, err)}
	}
	pr, pw := io.Pipe()
	go func() { pw.CloseWithError(tarVendor(pw, p, goWork)) }()
	_, err = s.sb.Sync(ctx, sandbox.Tar{R: pr, Path: "."})
	pr.CloseWithError(err)
	return err
}

// excludeVendor makes the box's git leave go.work and the vendored copies out
// of anything it finds: a brain that stages everything (git add -A) must not
// pick them up as if they were part of its change. It needs the box's
// execute tool, so it runs where scrubGit does, after the tools are ready,
// never during the sync that makes the box in the first place.
func (s *Session) excludeVendor(ctx context.Context) error {
	const command = `{ echo /` + goWorkPath + `; echo /` + vendorDir + `/; } >> .git/info/exclude`
	return s.prepare(ctx, "its git to leave the box's own go.work and vendored copies out of a change", command)
}

// tarVendor writes goWork as go.work and every root of p, excluding .git, as
// a tar stream rooted at the box's workspace.
func tarVendor(w io.Writer, p vendorPlan, goWork []byte) error {
	tw := tar.NewWriter(w)
	if err := writeTarFile(tw, goWorkPath, goWork); err != nil {
		return err
	}
	for _, root := range p.roots {
		if err := tarTree(tw, root, path.Join(vendorDir, p.rootName[root])); err != nil {
			return err
		}
	}
	return tw.Close()
}

// tarTree writes every regular file under root, excluding .git, to tw at
// boxRoot plus its path under root. A symbolic link is left out, as an
// export out of a box leaves one out: a vendored dependency is read, not
// linked.
func tarTree(tw *tar.Writer, root, boxRoot string) error {
	return filepath.WalkDir(root, func(full string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, full)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		if d.IsDir() {
			if d.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		if !d.Type().IsRegular() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		data, err := os.ReadFile(full)
		if err != nil {
			return err
		}
		hdr, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		hdr.Name = path.Join(boxRoot, filepath.ToSlash(rel))
		hdr.Uid, hdr.Gid, hdr.Uname, hdr.Gname = 0, 0, "", ""
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		_, err = tw.Write(data)
		return err
	})
}

// writeTarFile writes one file of data as name, a regular file at 0o644.
func writeTarFile(tw *tar.Writer, name string, data []byte) error {
	if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(data))}); err != nil {
		return err
	}
	_, err := tw.Write(data)
	return err
}
