package fix

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agent-fox-dev/agentfox/internal/checks"
	"github.com/agent-fox-dev/agentfox/internal/gitx"
	"github.com/agent-fox-dev/agentfox/internal/testutil"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

// node is an entry of a tree: a file with its content and mode, or a link to a target.
type node struct {
	content string
	mode    os.FileMode
	link    string
}

func file(content string) node { return node{content: content, mode: 0o644} }
func exe(content string) node  { return node{content: content, mode: 0o755} }
func link(target string) node  { return node{link: target} }

func writeTree(t *testing.T, dir string, tree map[string]node) {
	t.Helper()
	for name, n := range tree {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if n.link != "" {
			if err := os.Symlink(n.link, path); err != nil {
				t.Fatal(err)
			}
			continue
		}
		if err := os.Chmod(testutil.WriteFile(t, dir, name, n.content), n.mode); err != nil {
			t.Fatal(err)
		}
	}
}

// exportOf is a directory holding the tree, as the box exports one.
func exportOf(t *testing.T, tree map[string]node) string {
	t.Helper()
	dir := t.TempDir()
	writeTree(t, dir, tree)
	return dir
}

func (r *repo) commitTree(t *testing.T, tree map[string]node) {
	t.Helper()
	writeTree(t, r.Dir, tree)
	testutil.Commit(t, r.Dir, "tree")
}

// snapshotOf describes the entries of dir, or of the paths of it when there are any: the mode
// and the content of a file, and the target of a link. .git and .agent-fox at the top are left
// out of a whole tree.
func snapshotOf(t *testing.T, dir string, paths ...string) map[string]string {
	t.Helper()
	if paths == nil {
		_ = filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
			rel, _ := filepath.Rel(dir, path)
			if d.IsDir() && (rel == ".git" || rel == ".agent-fox") {
				return filepath.SkipDir
			}
			if !d.IsDir() {
				paths = append(paths, filepath.ToSlash(rel))
			}
			return err
		})
	}
	out := map[string]string{}
	for _, p := range paths {
		full := filepath.Join(dir, filepath.FromSlash(p))
		switch info, err := os.Lstat(full); {
		case err != nil:
		case info.Mode()&os.ModeSymlink != 0:
			target, _ := os.Readlink(full)
			out[p] = "link:" + target
		case info.Mode().IsRegular():
			out[p] = fmt.Sprintf("file:%o:%s", info.Mode().Perm(), testutil.ReadFile(t, full))
		}
	}
	return out
}

// applying is the parameters of an apply of the export to the checkout, with the entries of
// the ledger collected.
func applying(r *repo, export string) (ApplyParams, *[]string) {
	var ledger []string
	return ApplyParams{Dir: r.Dir, Export: export, Git: r.Git, Record: func(kind string, data any) error {
		raw, err := json.Marshal(data)
		ledger = append(ledger, kind+" "+string(raw))
		return err
	}}, &ledger
}

func TestApply_MirrorsTheExportInTheWorkingTreeAndCommitsNothing(t *testing.T) {
	r := newRepo(t, map[string]string{".gitignore": "build/\n*.log\n", "old.txt": "old\n", "keep.txt": "keep\n", "changed.txt": "v1\n", "src/run.sh": "echo run\n"}, false)
	r.commitTree(t, map[string]node{"moved": link("old.txt")})
	head := r.git(t, "rev-parse", "HEAD")
	export := exportOf(t, map[string]node{
		".gitignore": file("build/\n*.log\n"), "keep.txt": file("keep\n"), "changed.txt": file("v2\n"),
		"src/run.sh": exe("echo run\n"), "new.txt": file("new\n"), "ln": link("keep.txt"), "moved": link("keep.txt"),
		".git/refs/heads/inbox": file("deadbeef\n"), ".git/HEAD": file("ref: refs/heads/inbox\n"), "vendor/lib/.git/hooks/pre-commit": exe("#!/bin/sh\n"),
		".agent-fox/run/x/hook.json": file("{}"), "build/out.o": file("x"), "debug.log": file("x"),
	})
	p, ledger := applying(r, export)

	res, err := Apply(context.Background(), p)

	// Written: new, changed, the mode, the link and the link that points elsewhere; deleted: old;
	// left out: two of .git, the nested .git, .agent-fox, build and the log.
	if err != nil || res != (ApplyResult{Written: 5, Deleted: 1, Ignored: 6}) {
		t.Errorf("Apply = %+v, %v", res, err)
	}
	want := map[string]string{
		".gitignore": "file:644:build/\n*.log\n", "new.txt": "file:644:new\n", "changed.txt": "file:644:v2\n", "src/run.sh": "file:755:echo run\n",
		"ln": "link:keep.txt", "moved": "link:keep.txt", "keep.txt": "file:644:keep\n",
	}
	if got := snapshotOf(t, r.Dir); !maps.Equal(got, want) {
		t.Errorf("the checkout holds %v, want %v", got, want)
	}
	if _, err := os.Lstat(filepath.Join(r.Dir, ".git", "refs", "heads", "inbox")); err == nil {
		t.Error("the export wrote into .git")
	}
	if len(*ledger) != 1 || (*ledger)[0] != `apply {"written":5,"deleted":1,"ignored":6}` {
		t.Errorf("ledger = %v", *ledger)
	}
	if r.git(t, "rev-parse", "HEAD") != head || r.git(t, "branch", "--list") != "* main" {
		t.Error("HEAD or the branches changed: an apply commits nothing")
	}
}

func TestApply_DeletesWhatTheExportLacksAndTheDirectoriesThatEmptied(t *testing.T) {
	r := newRepo(t, map[string]string{"old.txt": "old\n", "keep.txt": "keep\n", "gone/deep/only.txt": "x\n", "shared/a.txt": "a\n", "shared/b.txt": "b\n"}, false)
	if err := os.MkdirAll(filepath.Join(r.Dir, "scratch"), 0o755); err != nil { // an empty directory of the checkout's own
		t.Fatal(err)
	}
	p, _ := applying(r, exportOf(t, map[string]node{"keep.txt": file("keep\n"), "shared/a.txt": file("a\n")}))

	res, err := Apply(context.Background(), p)

	if err != nil || res != (ApplyResult{Deleted: 3}) {
		t.Errorf("Apply = %+v, %v, want 3 deleted and nothing written", res, err)
	}
	for name, want := range map[string]bool{"old.txt": false, "gone": false, "shared/b.txt": false, "keep.txt": true, "shared": true, "scratch": true} {
		if _, err := os.Lstat(filepath.Join(r.Dir, name)); (err == nil) != want {
			t.Errorf("%s is there: %v, want %v", name, err == nil, want)
		}
	}
}

// An export with no file to apply would delete the whole checkout.
func TestApply_AnExportWithNothingToApplyFailsAndChangesNothing(t *testing.T) {
	r := newRepo(t, map[string]string{"a.txt": "a\n", "src/b.txt": "b\n", ".gitignore": "build/\n"}, false)
	before := snapshotOf(t, r.Dir)
	for name, tree := range map[string]map[string]node{
		"nothing":              {},
		"only what is ignored": {"build/out.o": file("x")},
		"only git's and af's":  {".git/config": file("x"), ".agent-fox/gate.json": file("{}")},
	} {
		p, ledger := applying(r, exportOf(t, tree))

		res, err := Apply(context.Background(), p)

		if f := failure(t, err); f.Category != toolio.CategorySandbox || f.Retryable || res != (ApplyResult{}) || len(*ledger) != 0 {
			t.Errorf("%s: %+v, %+v, ledger %v, want a sandbox failure and nothing done", name, res, f, *ledger)
		}
		if after := snapshotOf(t, r.Dir); !maps.Equal(before, after) {
			t.Errorf("%s: the checkout changed: %v", name, after)
		}
	}
}

// For trees of files, executables and links whose paths change their kind between the
// checkout and the export, the file view of the checkout is the export afterwards, less what
// the rules ignore, and what the checkout ignores, or tracks and ignores, is left alone.
func TestApply_LeavesTheCheckoutEqualToTheExport(t *testing.T) {
	pool := []string{"a", "a/b", "a/b/c", "a/d", "b", "b/e", "c.txt", "d.sh", "e/f.txt", "e/g/h", "build/x", "x.log", "sub/y.log", "z", "z2/z3"}
	ignored := func(p string) bool { return strings.HasPrefix(p, "build/") || strings.HasSuffix(p, ".log") }
	rng := rand.New(rand.NewSource(34))
	randomTree := func() map[string]node {
		tree := map[string]node{".gitignore": file("build/\n*.log\n")}
		for _, i := range rng.Perm(len(pool)) {
			p := pool[i]
			clash := rng.Intn(3) == 0
			for other := range tree {
				clash = clash || strings.HasPrefix(other, p+"/") || strings.HasPrefix(p, other+"/")
			}
			switch k := rng.Intn(4); {
			case clash:
			case k == 0:
				tree[p] = link([]string{"a", "z", "../x", "b/e", "c.txt"}[rng.Intn(5)])
			case k == 1:
				tree[p] = exe([]string{"one\n", "two\n"}[rng.Intn(2)])
			default:
				tree[p] = file([]string{"one\n", "two\n", "three\n"}[rng.Intn(3)])
			}
		}
		return tree
	}
	for i := range 12 {
		host, export := randomTree(), exportOf(t, randomTree())
		r := newRepo(t, nil, false)
		maps.DeleteFunc(host, func(p string, _ node) bool { return ignored(p) })
		r.commitTree(t, host)
		writeTree(t, r.Dir, map[string]node{"build/tracked.o": file("tracked"), "tracked.log": file("tracked log")})
		r.git(t, "add", "-f", "build/tracked.o", "tracked.log")
		testutil.Commit(t, r.Dir, "tracked and ignored")
		writeTree(t, r.Dir, map[string]node{"build/cache": file("cache"), "host.log": file("log")})
		p, _ := applying(r, export)

		if _, err := Apply(context.Background(), p); err != nil {
			t.Fatalf("case %d: %v", i, err)
		}

		view, err := checks.FileView(context.Background(), r.Dir)
		if err != nil {
			t.Fatal(err)
		}
		want := snapshotOf(t, export)
		maps.DeleteFunc(want, func(p, _ string) bool { return ignored(p) })
		want["build/tracked.o"], want["tracked.log"] = "file:644:tracked", "file:644:tracked log"
		if got := snapshotOf(t, r.Dir, view...); !maps.Equal(got, want) {
			t.Errorf("case %d: the checkout holds\n%v\nwant\n%v", i, got, want)
		}
		if kept := snapshotOf(t, r.Dir, "build/cache", "host.log"); len(kept) != 2 {
			t.Errorf("case %d: what the checkout ignores was touched: %v", i, kept)
		}
	}
}

func TestApply_NeverWritesThroughALink(t *testing.T) {
	outside := t.TempDir()
	for name, tc := range map[string]struct {
		files  map[string]string
		links  map[string]node
		export map[string]node
		want   map[string]string
		res    ApplyResult
	}{
		"a link replaced by a file": {
			links:  map[string]node{"out": link(outside), "inside": link("keep.txt")},
			export: map[string]node{"keep.txt": file("keep\n"), "out": file("now a file\n"), "inside": link("keep.txt")},
			want:   map[string]string{"out": "file:644:now a file\n", "inside": "link:keep.txt"}, res: ApplyResult{Written: 1},
		},
		"a link replaced by a directory": {
			links:  map[string]node{"out": link(outside)},
			export: map[string]node{"keep.txt": file("keep\n"), "out/pwn.txt": file("pwn\n")},
			want:   map[string]string{"out/pwn.txt": "file:644:pwn\n"}, res: ApplyResult{Written: 1, Deleted: 1},
		},
		// Git cannot say whether a path beyond a link is ignored: the link the checkout keeps
		// keeps what is beyond it out.
		"a link the checkout ignores and keeps": {
			files:  map[string]string{".gitignore": "cache\n"},
			export: map[string]node{".gitignore": file("cache\n"), "keep.txt": file("keep\n"), "cache/x": file("x\n")},
			want:   map[string]string{"cache": "link:" + outside}, res: ApplyResult{Ignored: 1},
		},
	} {
		t.Run(name, func(t *testing.T) {
			files := map[string]string{"keep.txt": "keep\n"}
			maps.Copy(files, tc.files)
			r := newRepo(t, files, false)
			if tc.links != nil {
				r.commitTree(t, tc.links)
			} else {
				writeTree(t, r.Dir, map[string]node{"cache": link(outside)})
			}
			p, _ := applying(r, exportOf(t, tc.export))

			res, err := Apply(context.Background(), p)

			if err != nil || res != tc.res {
				t.Errorf("Apply = %+v, %v, want %+v", res, err, tc.res)
			}
			if entries, _ := os.ReadDir(outside); len(entries) != 0 {
				t.Errorf("%d entries were written outside the checkout", len(entries))
			}
			for path, want := range tc.want {
				if got := snapshotOf(t, r.Dir, path)[path]; got != want {
					t.Errorf("%s = %q, want %q", path, got, want)
				}
			}
		})
	}
}

// On a file system that does not tell .GIT from .git, an export with .GIT/hooks/pre-commit
// would write a hook that the commit of the landing runs.
func TestApply_LeavesOutTheDirectoriesOfGitAndAfWhateverTheirCase(t *testing.T) {
	r := newRepo(t, map[string]string{"keep.txt": "keep\n"}, false)
	p, _ := applying(r, exportOf(t, map[string]node{
		"keep.txt": file("changed\n"), ".GIT/hooks/pre-commit": exe("#!/bin/sh\n"), ".Git/config": file("x"), ".AGENT-FOX/run/x/hook.json": file("{}"), "sub/.GIT/HEAD": file("x"),
	}))

	res, err := Apply(context.Background(), p)

	if err != nil || res != (ApplyResult{Written: 1, Ignored: 4}) {
		t.Errorf("Apply = %+v, %v, want the changed file written and the four of git's and af's left out", res, err)
	}
	if got := snapshotOf(t, r.Dir); len(got) != 1 {
		t.Errorf("the checkout holds %v", got)
	}
}

// What is under a gitlink is another repository's: the apply neither writes into it nor
// deletes it.
func TestApply_LeavesTheFilesOfASubmoduleAlone(t *testing.T) {
	r := newRepo(t, map[string]string{"keep.txt": "keep\n", "old.txt": "old\n"}, false)
	sub := newRepo(t, map[string]string{"lib.txt": "lib\n"}, false)
	r.git(t, "-c", "protocol.file.allow=always", "submodule", "add", "-q", sub.Dir, "vendor/lib")
	testutil.Commit(t, r.Dir, "submodule")
	p, _ := applying(r, exportOf(t, map[string]node{
		".gitmodules": file(r.read(t, ".gitmodules")), "keep.txt": file("changed\n"), "vendor/lib/lib.txt": file("changed in the box\n"), "vendor/lib/new.txt": file("new\n"),
	}))

	res, err := Apply(context.Background(), p)

	if err != nil || res.Written != 1 || res.Deleted != 1 {
		t.Errorf("Apply = %+v, %v, want keep.txt written, old.txt deleted, and nothing done under the submodule", res, err)
	}
	if got := snapshotOf(t, r.Dir, "vendor/lib/lib.txt", "vendor/lib/new.txt"); !maps.Equal(got, map[string]string{"vendor/lib/lib.txt": "file:644:lib\n"}) {
		t.Errorf("the submodule holds %v", got)
	}
}

// The export has no file the rules ignore, so what the checkout tracks and a rule ignores is
// not the brain's to delete.
func TestApply_WritesOnlyWhatDiffersAndKeepsWhatIsTrackedAndIgnored(t *testing.T) {
	big := func(at int, to byte) string {
		b := []byte(strings.Repeat("0123456789abcdef", 8<<10)) // 128 KiB, beyond the buffer of a comparison
		if at >= 0 {
			b[at] = to
		}
		return string(b)
	}
	r := newRepo(t, map[string]string{".gitignore": "ignored-link\nsecret.cfg\n", "same.bin": big(-1, 0), "tail.bin": big(-1, 0), "middle.bin": big(-1, 0), "longer.bin": big(-1, 0)}, false)
	writeTree(t, r.Dir, map[string]node{"ignored-link": link("same.bin"), "secret.cfg": file("x\n")})
	r.git(t, "add", "-f", "ignored-link", "secret.cfg")
	testutil.Commit(t, r.Dir, "tracked and ignored")
	p, _ := applying(r, exportOf(t, map[string]node{
		".gitignore": file("ignored-link\nsecret.cfg\n"), "same.bin": file(big(-1, 0)), "tail.bin": file(big(128<<10-1, 'X')), "middle.bin": file(big(70<<10, 'X')),
		"longer.bin": file(big(-1, 0) + "more"), "new.txt": file("new\n"), "ln": link("new.txt"),
	}))

	first, err := Apply(context.Background(), p)
	second, err2 := Apply(context.Background(), p)

	if err != nil || err2 != nil || first != (ApplyResult{Written: 5}) || second != (ApplyResult{}) {
		t.Errorf("first %+v (%v), second %+v (%v), want the three that differ, the file and the link written once, and nothing deleted", first, err, second, err2)
	}
	if got := snapshotOf(t, r.Dir, "tail.bin", "secret.cfg", "ignored-link"); got["tail.bin"] != "file:644:"+big(128<<10-1, 'X') || got["secret.cfg"] != "file:644:x\n" || got["ignored-link"] != "link:same.bin" {
		t.Errorf("tail.bin differs from the export, or what is tracked and ignored was touched")
	}
}

// What is to be done is worked out before the first write: an apply that cannot tell what is
// ignored, or that is stopped, leaves the checkout as it was.
func TestApply_DecidesEverythingBeforeTheFirstWrite(t *testing.T) {
	for name, tc := range map[string]struct {
		cancelOn string // the git command the run is stopped at; "-" before the apply
		failOn   string // the git command that fails
		keepOld  bool   // the export deletes nothing
	}{
		"git cannot tell what is ignored": {failOn: "check-ignore"},
		"git cannot list the gitlinks":    {failOn: "--stage"},
		"stopped before it starts":        {cancelOn: "-"},
		"stopped once the plan is made":   {cancelOn: "--ignored"},
		"stopped with only writes to do":  {cancelOn: "--ignored", keepOld: true},
	} {
		t.Run(name, func(t *testing.T) {
			r := newRepo(t, map[string]string{"keep.txt": "keep\n", "old.txt": "old\n"}, false)
			before := snapshotOf(t, r.Dir)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if tc.cancelOn == "-" {
				cancel()
			}
			export := map[string]node{"keep.txt": file("changed\n"), "new.txt": file("new\n")}
			if tc.keepOld {
				export["old.txt"] = file("old\n")
			}
			p, ledger := applying(r, exportOf(t, export))
			p.Git = gitx.New(r.Dir, func(c context.Context, dir string, argv []string, stdin ...string) (string, int, error) {
				if has(argv, tc.failOn) {
					return "fatal: nope", 128, nil
				}
				out, code, err := gitx.ExecRunner(c, dir, argv, stdin...)
				if has(argv, tc.cancelOn) {
					cancel() // once git has answered
				}
				return out, code, err
			})

			_, err := Apply(ctx, p)

			if err == nil || len(*ledger) != 0 {
				t.Errorf("Apply = %v, ledger %v, want an error and no entry", err, *ledger)
			}
			if after := snapshotOf(t, r.Dir); !maps.Equal(before, after) {
				t.Errorf("the checkout changed: %v", after)
			}
		})
	}
}

func TestApply_ALedgerThatCannotBeWrittenIsAWarning(t *testing.T) {
	r := newRepo(t, map[string]string{"keep.txt": "keep\n"}, false)
	export := exportOf(t, map[string]node{"keep.txt": file("keep\n"), "new.txt": file("new\n")})
	var warned []string
	p := ApplyParams{Dir: r.Dir, Export: export, Git: r.Git,
		Record: func(string, any) error { return os.ErrPermission },
		Warn:   func(code, _ string) { warned = append(warned, code) }}

	if _, err := Apply(context.Background(), p); err != nil || len(warned) != 1 || warned[0] != "ledger_unwritable" {
		t.Errorf("Apply = %v, warnings %v, want the warning and no failure", err, warned)
	}
	p.Warn, p.Record = nil, nil
	if _, err := Apply(context.Background(), p); err != nil {
		t.Errorf("an apply with no ledger: %v", err)
	}
}

func TestMakeRoom_ReplacesAFileInTheWayAndRefusesALink(t *testing.T) {
	dir, outside := t.TempDir(), t.TempDir()
	writeTree(t, dir, map[string]node{"file": file("x"), "a": file("in the way"), "via": link(outside), "plain/dir/keep": file("keep")})
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()

	for _, rel := range []string{"a/b/c.txt", "plain/dir/new.txt"} {
		if err := makeRoom(root, rel, false); err != nil {
			t.Errorf("makeRoom(%s) = %v", rel, err)
		}
	}
	if err := makeRoom(root, "file", true); err != nil {
		t.Errorf("makeRoom(file) = %v", err)
	}
	if err := makeRoom(root, "via/deep/x.txt", false); err == nil || failure(t, err).Category != toolio.CategoryIO {
		t.Errorf("a path beyond a link: %v, want an io failure", err)
	}

	if info, err := os.Stat(filepath.Join(dir, "a", "b")); err != nil || !info.IsDir() {
		t.Errorf("a/b is %v (%v), want directories where the file was", info, err)
	}
	if got := snapshotOf(t, dir); got["plain/dir/keep"] == "" || got["file"] != "" {
		t.Errorf("the tree holds %v, want plain/dir/keep kept and file gone", got)
	}
	if entries, _ := os.ReadDir(outside); len(entries) != 0 {
		t.Errorf("%d entries were made outside", len(entries))
	}
}
