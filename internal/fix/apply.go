package fix

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/agent-fox-dev/agentfox/internal/checks"
	"github.com/agent-fox-dev/agentfox/internal/gitx"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

// LedgerApply is the kind of the ledger record of an apply.
const LedgerApply = "apply"

// ApplyResult counts what an apply did: the files written to the checkout (new,
// changed in content or mode, links made again), the files deleted from it, and the
// files of the export left out because they are under .git/ or .agent-fox/, ignored by
// the checkout's git rules, or no file or link.
type ApplyResult struct {
	Written int `json:"written"`
	Deleted int `json:"deleted"`
	Ignored int `json:"ignored"`
}

// ApplyParams is what an export is applied to.
type ApplyParams struct {
	// Dir is the checkout, Export the directory the box was exported to, and Git
	// the checkout's git.
	Dir, Export string
	Git         *gitx.Git
	// Record notes the apply in the ledger of the run, and Warn raises the warning of
	// a ledger that cannot be written. Either may be nil.
	Record func(kind string, data any) error
	Warn   func(code, message string)
}

// plan is what an apply does, decided before anything is written.
type plan struct {
	// entries are the paths of the export to mirror, and deletes the paths of the
	// checkout's file view the export lacks.
	entries []string
	deletes []string
	// beyond are the paths of the export that lie beyond a link of the checkout, each
	// with the outermost link, and ignored how many files were left out. Git cannot
	// say whether a path beyond a link is ignored, so these wait for the deletion of
	// the links the export has a directory in place of.
	beyond  map[string]string
	ignored int
}

// Apply mirrors the export of the box onto the checkout, in its working tree and not in
// its history: every file of the export, but those under .git/ or .agent-fox/ and those
// that the checkout's git rules ignore, is written when the checkout lacks it or has it
// with another content, mode or link target, and every file of the checkout's file view
// that the export lacks is deleted, with the directories that this empties. A link is made
// again, and never followed: nothing is written through one. An export with no file to
// apply is failed/sandbox and applies nothing, since it would delete the whole checkout.
// What is to be done is worked out before the first write, so that a failure to tell
// what is ignored leaves the checkout as it was. The one exception is a path that lies
// beyond a link of the checkout, which git cannot judge: if the export has a directory
// where the checkout has the link, the link is deleted first, and the path judged then;
// if the checkout keeps the link, the path is left out.
func Apply(ctx context.Context, p ApplyParams) (ApplyResult, error) {
	pl, err := planApply(ctx, p)
	if err != nil {
		return ApplyResult{}, err
	}
	root, err := os.OpenRoot(p.Dir)
	if err != nil {
		return ApplyResult{}, &toolio.Failure{Category: toolio.CategoryIO, Message: fmt.Sprintf("cannot open the checkout %s: %v", p.Dir, err)}
	}
	defer func() { _ = root.Close() }()

	res := ApplyResult{Ignored: pl.ignored}
	for _, rel := range pl.deletes {
		if err := ctx.Err(); err != nil {
			return res, err
		}
		if err := root.Remove(filepath.FromSlash(rel)); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return res, applyFailure("delete %s: %v", rel, err)
		}
		res.Deleted++
	}
	pruneEmptied(root, pl.deletes)

	entries := pl.entries
	if len(pl.beyond) > 0 {
		more, left, err := judgeBeyond(ctx, p, root, pl.beyond)
		if err != nil {
			return res, err
		}
		entries = append(slices.Clone(entries), more...)
		res.Ignored += left
	}
	for _, rel := range entries {
		if err := ctx.Err(); err != nil {
			return res, err
		}
		wrote, err := mirror(root, p.Export, rel)
		if err != nil {
			return res, err
		}
		if wrote {
			res.Written++
		}
	}
	record(p, res)
	return res, nil
}

// applyFailure is the failure of a write to the checkout.
func applyFailure(format string, args ...any) error {
	return &toolio.Failure{Category: toolio.CategoryIO, Message: "cannot apply the box's tree to the checkout: " + fmt.Sprintf(format, args...)}
}

// record notes the apply in the ledger.
func record(p ApplyParams, res ApplyResult) {
	if p.Record == nil {
		return
	}
	if err := p.Record(LedgerApply, res); err != nil && p.Warn != nil {
		p.Warn("ledger_unwritable", fmt.Sprintf("the ledger entry %s could not be written: %v", LedgerApply, err))
	}
}

// planApply reads the export, the checkout's file view and its ignore rules, and decides
// what is mirrored, what is deleted and what is left out.
func planApply(ctx context.Context, p ApplyParams) (plan, error) {
	var pl plan
	var candidates []string
	gitlinks, err := p.Git.Gitlinks(ctx)
	if err != nil {
		return plan{}, &toolio.Failure{Category: toolio.CategoryGit, Message: err.Error()}
	}
	err = filepath.WalkDir(p.Export, func(full string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return ctx.Err()
		}
		rel, err := filepath.Rel(p.Export, full)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if kind := d.Type(); leftOut(rel) || underAny(rel, gitlinks) || (!kind.IsRegular() && kind&fs.ModeSymlink == 0) {
			pl.ignored++
			return nil
		}
		candidates = append(candidates, rel)
		return nil
	})
	if err != nil {
		var f *toolio.Failure
		if errors.As(err, &f) || ctx.Err() != nil {
			return plan{}, err
		}
		return plan{}, &toolio.Failure{Category: toolio.CategorySandbox, Message: fmt.Sprintf("cannot read the export of the box: %v", err)}
	}

	var judged []string
	pl.beyond = map[string]string{}
	links := map[string]bool{}
	for _, rel := range candidates {
		if link := linkAbove(p.Dir, rel, links); link != "" {
			pl.beyond[rel] = link
			continue
		}
		judged = append(judged, rel)
	}
	candidates = judged
	ignored, err := ignoredAmong(ctx, p.Git, candidates)
	if err != nil {
		return plan{}, err
	}
	skip := map[string]bool{}
	for _, rel := range ignored {
		skip[rel] = true
	}
	for _, rel := range candidates {
		if skip[rel] {
			pl.ignored++
			continue
		}
		pl.entries = append(pl.entries, rel)
	}
	if len(pl.entries) == 0 {
		return plan{}, &toolio.Failure{Category: toolio.CategorySandbox, Message: "the box's tree has no file to apply: it would delete every file of the checkout, so nothing was applied"}
	}

	view, err := checks.FileView(ctx, p.Dir)
	if err != nil {
		return plan{}, &toolio.Failure{Category: toolio.CategoryIO, Message: err.Error()}
	}
	kept, err := keptAsTheyAre(ctx, p)
	if err != nil {
		return plan{}, err
	}
	have := map[string]bool{}
	for _, rel := range pl.entries {
		have[rel] = true
	}
	for _, rel := range view {
		if !have[rel] && !kept[rel] && !underAny(rel, gitlinks) {
			pl.deletes = append(pl.deletes, rel)
		}
	}
	return pl, nil
}

// underAny says whether rel is one of the directories, or lies under one.
func underAny(rel string, dirs []string) bool {
	for _, dir := range dirs {
		if rel == dir || strings.HasPrefix(rel, dir+"/") {
			return true
		}
	}
	return false
}

// ignoredAmong is the paths that the checkout's git rules ignore.
func ignoredAmong(ctx context.Context, g *gitx.Git, paths []string) ([]string, error) {
	ignored, err := g.Ignored(ctx, paths)
	if err != nil {
		return nil, &toolio.Failure{Category: toolio.CategoryGit, Message: fmt.Sprintf("git cannot tell which files of the box's tree are ignored, so nothing was applied: %v", err)}
	}
	return ignored, nil
}

// linkAbove is the outermost link of the checkout that rel lies beyond, or "" when no
// part of its directory is a link. links remembers what was looked at.
func linkAbove(dir, rel string, links map[string]bool) string {
	parts := strings.Split(rel, "/")
	for i := 1; i < len(parts); i++ {
		prefix := strings.Join(parts[:i], "/")
		isLink, known := links[prefix]
		if !known {
			info, err := os.Lstat(filepath.Join(dir, filepath.FromSlash(prefix)))
			isLink = err == nil && info.Mode()&fs.ModeSymlink != 0
			links[prefix] = isLink
		}
		if isLink {
			return prefix
		}
	}
	return ""
}

// judgeBeyond decides the paths that lay beyond a link of the checkout, once the links
// that the export replaces by directories are deleted: a path whose link is still there
// is left out, as it is inside something the checkout keeps, and the others are judged by
// git like any. It returns the paths to mirror and how many files were left out.
func judgeBeyond(ctx context.Context, p ApplyParams, root *os.Root, beyond map[string]string) (entries []string, left int, err error) {
	var ask []string
	for rel, link := range beyond {
		if info, err := root.Lstat(filepath.FromSlash(link)); err == nil && info.Mode()&fs.ModeSymlink != 0 {
			left++
			continue
		}
		ask = append(ask, rel)
	}
	slices.Sort(ask)
	ignored, err := ignoredAmong(ctx, p.Git, ask)
	if err != nil {
		return nil, 0, err
	}
	skip := map[string]bool{}
	for _, rel := range ignored {
		skip[rel] = true
	}
	for _, rel := range ask {
		if skip[rel] {
			left++
			continue
		}
		entries = append(entries, rel)
	}
	return entries, left, nil
}

// leftOut says a path of the export is not the checkout's to take: git's own state
// anywhere in the tree, and the directory af keeps its files in at its top. They are
// matched whatever their case, since on a file system that does not tell .GIT from .git
// an export with .GIT/hooks/pre-commit would write a hook that the landing runs.
func leftOut(rel string) bool {
	parts := strings.Split(rel, "/")
	return strings.EqualFold(parts[0], ".agent-fox") || slices.ContainsFunc(parts, func(part string) bool { return strings.EqualFold(part, ".git") })
}

// keptAsTheyAre are the tracked files that an ignore rule matches. The export of the box
// has no file that the rules ignore, so it lacks these whatever the brain did, and they
// are not the brain's to delete.
func keptAsTheyAre(ctx context.Context, p ApplyParams) (map[string]bool, error) {
	tracked, err := p.Git.TrackedIgnored(ctx)
	if err != nil {
		return nil, &toolio.Failure{Category: toolio.CategoryGit, Message: err.Error()}
	}
	out := map[string]bool{}
	for _, rel := range tracked {
		out[rel] = true
	}
	return out, nil
}

// pruneEmptied removes the directories that the deletion of the files emptied, from the
// deepest up. A directory that holds anything is left alone.
func pruneEmptied(root *os.Root, deleted []string) {
	dirs := map[string]bool{}
	for _, rel := range deleted {
		for dir := path.Dir(rel); dir != "."; dir = path.Dir(dir) {
			dirs[dir] = true
		}
	}
	names := make([]string, 0, len(dirs))
	for dir := range dirs {
		names = append(names, dir)
	}
	slices.SortFunc(names, func(a, b string) int { return strings.Count(b, "/") - strings.Count(a, "/") })
	for _, dir := range names {
		_ = root.Remove(filepath.FromSlash(dir)) // only an empty directory goes
	}
}

// mirror makes the path of the checkout what it is in the export, and says whether it
// had to write.
func mirror(root *os.Root, export, rel string) (bool, error) {
	src := filepath.Join(export, filepath.FromSlash(rel))
	srcInfo, err := os.Lstat(src)
	if err != nil {
		return false, applyFailure("read %s in the export: %v", rel, err)
	}
	name := filepath.FromSlash(rel)
	have, err := root.Lstat(name)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return false, applyFailure("look at %s: %v", rel, err)
	}
	exists := err == nil

	if srcInfo.Mode()&fs.ModeSymlink != 0 {
		target, err := os.Readlink(src)
		if err != nil {
			return false, applyFailure("read the link %s in the export: %v", rel, err)
		}
		if exists && have.Mode()&fs.ModeSymlink != 0 {
			if current, err := root.Readlink(name); err == nil && current == target {
				return false, nil
			}
		}
		if err := makeRoom(root, rel, exists); err != nil {
			return false, err
		}
		if err := root.Symlink(target, name); err != nil {
			return false, applyFailure("link %s: %v", rel, err)
		}
		return true, nil
	}

	perm := srcInfo.Mode().Perm()
	if exists && have.Mode().IsRegular() && have.Mode().Perm() == perm && have.Size() == srcInfo.Size() {
		same, err := sameContent(root, name, src)
		if err != nil {
			return false, applyFailure("compare %s: %v", rel, err)
		}
		if same {
			return false, nil
		}
	}
	if err := makeRoom(root, rel, exists); err != nil {
		return false, err
	}
	if err := copyFile(root, name, src, perm); err != nil {
		return false, applyFailure("write %s: %v", rel, err)
	}
	return true, nil
}

// makeRoom clears the way for a file or a link at rel: what is there is removed, a
// directory with what is in it, and the directories above it are made. A link above it is
// not followed and not replaced: nothing is written through one.
func makeRoom(root *os.Root, rel string, exists bool) error {
	if exists {
		if err := root.RemoveAll(filepath.FromSlash(rel)); err != nil {
			return applyFailure("replace %s: %v", rel, err)
		}
	}
	parent := path.Dir(rel)
	if parent == "." {
		return nil
	}
	built := ""
	for _, part := range strings.Split(parent, "/") {
		built = path.Join(built, part)
		name := filepath.FromSlash(built)
		info, err := root.Lstat(name)
		switch {
		case err == nil && info.IsDir():
			continue
		case err == nil && info.Mode()&fs.ModeSymlink != 0:
			return applyFailure("%s is a link, and nothing is written through a link", built)
		case err == nil: // a file is in the way
			if err := root.Remove(name); err != nil {
				return applyFailure("replace %s by a directory: %v", built, err)
			}
		case !errors.Is(err, fs.ErrNotExist):
			return applyFailure("look at %s: %v", built, err)
		}
		if err := root.Mkdir(name, 0o755); err != nil {
			return applyFailure("make the directory %s: %v", built, err)
		}
	}
	return nil
}

// sameContent reports whether the file at name in the checkout holds the bytes of src.
func sameContent(root *os.Root, name, src string) (bool, error) {
	a, err := root.Open(name)
	if err != nil {
		return false, err
	}
	defer func() { _ = a.Close() }()
	b, err := os.Open(src)
	if err != nil {
		return false, err
	}
	defer func() { _ = b.Close() }()
	bufA, bufB := make([]byte, 32<<10), make([]byte, 32<<10)
	for {
		na, errA := io.ReadFull(a, bufA)
		nb, errB := io.ReadFull(b, bufB)
		if na != nb || !bytes.Equal(bufA[:na], bufB[:nb]) {
			return false, nil
		}
		if errA != nil || errB != nil {
			if isEnd(errA) && isEnd(errB) {
				return true, nil
			}
			return false, errors.Join(nonEnd(errA), nonEnd(errB))
		}
	}
}

func isEnd(err error) bool { return errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) }

func nonEnd(err error) error {
	if isEnd(err) {
		return nil
	}
	return err
}

// copyFile writes a new file at name with the content of src and the mode perm.
func copyFile(root *os.Root, name, src string, perm fs.FileMode) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	out, err := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	return root.Chmod(name, perm)
}
