package fix

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/agent-fox-dev/agentfox/internal/checks"
	"github.com/agent-fox-dev/agentfox/internal/gitx"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

// node is an entry of a tree to write: a file with its content and mode, or a
// symbolic link to a target.
type node struct {
	content string
	mode    os.FileMode
	link    string
}

func file(content string) node { return node{content: content, mode: 0o644} }
func exe(content string) node  { return node{content: content, mode: 0o755} }
func link(target string) node  { return node{link: target} }

// writeTree writes the nodes under dir.
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
		if err := os.WriteFile(path, []byte(n.content), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(path, n.mode); err != nil {
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

// commitTree writes the nodes into the checkout and commits them.
func commitTree(t *testing.T, r *testRepo, tree map[string]node) {
	t.Helper()
	writeTree(t, r.Dir, tree)
	gitDo(t, r.Dir, "add", "-A")
	gitDo(t, r.Dir, "commit", "-q", "--allow-empty", "-m", "chore: tree")
}

// snapshotOf describes the entries of the paths of dir: the content and the mode of
// a file, the target of a link, and nothing for a path that is not there.
func snapshotOf(t *testing.T, dir string, paths []string) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, p := range paths {
		full := filepath.Join(dir, filepath.FromSlash(p))
		info, err := os.Lstat(full)
		switch {
		case err != nil:
			continue
		case info.Mode()&os.ModeSymlink != 0:
			target, err := os.Readlink(full)
			if err != nil {
				t.Fatal(err)
			}
			out[p] = "link:" + target
		case info.Mode().IsRegular():
			data, err := os.ReadFile(full)
			if err != nil {
				t.Fatal(err)
			}
			out[p] = fmt.Sprintf("file:%o:%s", info.Mode().Perm(), data)
		}
	}
	return out
}

// allPaths lists the files and links under dir, except .git and .agent-fox at its root.
func allPaths(t *testing.T, dir string) []string {
	t.Helper()
	var paths []string
	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, path)
		if d.IsDir() && (rel == ".git" || rel == ".agent-fox") {
			return filepath.SkipDir
		}
		if !d.IsDir() {
			paths = append(paths, filepath.ToSlash(rel))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(paths)
	return paths
}

// applying is the parameters of an apply of the export to the repository, with the
// entries of the ledger collected.
func applying(r *testRepo, export string) (ApplyParams, *[]recorded) {
	var ledger []recorded
	return ApplyParams{Dir: r.Dir, Export: export, Git: r.Git, Record: func(kind string, data any) error {
		ledger = append(ledger, recorded{kind, data})
		return nil
	}}, &ledger
}

type recorded struct {
	kind string
	data any
}

func jsonOf(t *testing.T, v any) string {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// TestApplyWritesWhatDiffersSkipsWhatIsNotTheHostsAndNeverCommits_TS_09_32 verifies
// TS-09-32: new, changed and mode-changed files and a link are written, nothing under
// .git/ or .agent-fox/ or ignored reaches the host, the ledger notes the apply, and HEAD
// stays where it was, so a commit made inside the box is not on the branch.
// Verifies: 09-REQ-5.1, 09-REQ-5.5
func TestApplyWritesWhatDiffersSkipsWhatIsNotTheHostsAndNeverCommits_TS_09_32(t *testing.T) {
	r := newTestRepo(t, map[string]string{
		".gitignore": "build/\n*.log\n", "old.txt": "old\n", "keep.txt": "keep\n", "changed.txt": "v1\n", "src/run.sh": "echo run\n",
	}, false)
	commitTree(t, r, map[string]node{"moved": link("old.txt")})
	head := gitDo(t, r.Dir, "rev-parse", "HEAD")
	export := exportOf(t, map[string]node{
		".gitignore": file("build/\n*.log\n"), "keep.txt": file("keep\n"), "changed.txt": file("v2\n"),
		"src/run.sh": exe("echo run\n"), "new.txt": file("new\n"), "ln": link("keep.txt"), "moved": link("keep.txt"),
		".git/refs/heads/inbox": file("deadbeef\n"), ".git/HEAD": file("ref: refs/heads/inbox\n"), "vendor/lib/.git/hooks/pre-commit": exe("#!/bin/sh\n"),
		".agent-fox/run/x/hook.json": file("{}"), "build/out.o": file("x"), "debug.log": file("x"),
	})
	p, ledger := applying(r, export)

	res, err := Apply(context.Background(), p)

	if err != nil {
		t.Fatal(err)
	}
	if res != (ApplyResult{Written: 5, Deleted: 1, Ignored: 6}) {
		t.Errorf("result = %+v, want 5 written (new, changed, mode, link, retargeted link), 1 deleted and 6 left out (.git x2, a nested .git, .agent-fox, build, log)", res)
	}
	got := snapshotOf(t, r.Dir, []string{"new.txt", "changed.txt", "src/run.sh", "ln", "moved", "keep.txt", "old.txt"})
	want := map[string]string{
		"new.txt": "file:644:new\n", "changed.txt": "file:644:v2\n", "src/run.sh": "file:755:echo run\n", "ln": "link:keep.txt", "moved": "link:keep.txt", "keep.txt": "file:644:keep\n",
	}
	if !equalSnapshots(got, want) {
		t.Errorf("the checkout holds %v, want %v", got, want)
	}
	for _, left := range []string{"build/out.o", "debug.log", ".agent-fox/run/x/hook.json", ".git/refs/heads/inbox", "vendor/lib/.git/hooks/pre-commit"} {
		if _, err := os.Lstat(filepath.Join(r.Dir, filepath.FromSlash(left))); err == nil {
			t.Errorf("%s reached the checkout", left)
		}
	}
	if len(*ledger) != 1 || (*ledger)[0].kind != "apply" || jsonOf(t, (*ledger)[0].data) != `{"written":5,"deleted":1,"ignored":6}` {
		t.Errorf("ledger = %v, want apply {written 5, deleted 1, ignored 6}", *ledger)
	}
	if gitDo(t, r.Dir, "rev-parse", "HEAD") != head || strings.TrimSpace(gitDo(t, r.Dir, "branch", "--list")) != "* main" {
		t.Errorf("HEAD or the branches changed: the apply must not commit")
	}
	if status := gitDo(t, r.Dir, "status", "--porcelain"); !strings.Contains(status, "D old.txt") || !strings.Contains(status, "?? new.txt") || !strings.Contains(status, "M changed.txt") {
		t.Errorf("git status = %q, want the change in the working tree", status)
	}
}

func equalSnapshots(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

// TestApplyDeletesWhatTheExportLacksAndTheDirectoriesItEmptied_TS_09_33 verifies
// TS-09-33.
// Verifies: 09-REQ-5.2
func TestApplyDeletesWhatTheExportLacksAndTheDirectoriesItEmptied_TS_09_33(t *testing.T) {
	r := newTestRepo(t, map[string]string{"old.txt": "old\n", "keep.txt": "keep\n", "gone/deep/only.txt": "x\n", "shared/a.txt": "a\n", "shared/b.txt": "b\n"}, false)
	if err := os.MkdirAll(filepath.Join(r.Dir, "scratch"), 0o755); err != nil { // an empty directory of the checkout's own
		t.Fatal(err)
	}
	export := exportOf(t, map[string]node{"keep.txt": file("keep\n"), "shared/a.txt": file("a\n")})
	p, ledger := applying(r, export)

	res, err := Apply(context.Background(), p)

	if err != nil {
		t.Fatal(err)
	}
	if res.Deleted != 3 || res.Written != 0 {
		t.Errorf("result = %+v, want 3 deleted (old, only, b) and nothing written", res)
	}
	for _, gone := range []string{"old.txt", "gone", "shared/b.txt"} {
		if _, err := os.Lstat(filepath.Join(r.Dir, gone)); err == nil {
			t.Errorf("%s is still in the checkout", gone)
		}
	}
	for _, kept := range []string{"keep.txt", "shared/a.txt", "shared", "scratch"} {
		if _, err := os.Lstat(filepath.Join(r.Dir, kept)); err != nil {
			t.Errorf("%s was removed: %v", kept, err)
		}
	}
	if len(*ledger) != 1 || jsonOf(t, (*ledger)[0].data) != `{"written":0,"deleted":3,"ignored":0}` {
		t.Errorf("ledger = %v", *ledger)
	}
}

// TestAnExportWithNoFilesIsAFailureAndAppliesNothing_TS_09_35 verifies TS-09-35.
// Verifies: 09-REQ-5.4
func TestAnExportWithNoFilesIsAFailureAndAppliesNothing_TS_09_35(t *testing.T) {
	r := newTestRepo(t, map[string]string{"a.txt": "a\n", "src/b.txt": "b\n"}, false)
	commitTree(t, r, map[string]node{".gitignore": file("build/\n")})
	before := snapshotOf(t, r.Dir, allPaths(t, r.Dir))
	for name, tree := range map[string]map[string]node{
		"nothing":              {},
		"only what is ignored": {"build/out.o": file("x")},
		"only the git dir":     {".git/HEAD": file("ref: refs/heads/main\n")},
		"only the run dir":     {".agent-fox/run/x/brief.md": file("x")},
		"only what is left":    {".git/config": file("x"), ".agent-fox/gate.json": file("{}")},
	} {
		t.Run(name, func(t *testing.T) {
			p, ledger := applying(r, exportOf(t, tree))

			res, err := Apply(context.Background(), p)

			if f := failure(t, err); f.Category != toolio.CategorySandbox || f.Retryable {
				t.Errorf("failure = %+v, want sandbox", f)
			}
			if res != (ApplyResult{}) || len(*ledger) != 0 {
				t.Errorf("result %+v, ledger %v, want nothing", res, *ledger)
			}
			if after := snapshotOf(t, r.Dir, allPaths(t, r.Dir)); !equalSnapshots(before, after) {
				t.Errorf("the checkout changed: %v, was %v", after, before)
			}
		})
	}
}

// TestAfterAnApplyTheCheckoutIsTheExport_TS_09_34 verifies TS-09-34: for exports of
// regular files, executables and links over hosts of the same kind, with paths that
// change their kind between the two, the file view of the checkout less what its rules
// ignore is the export less what they ignore, in paths, contents, modes and link
// targets, and what the checkout ignores is left alone.
// Verifies: 09-REQ-5.3
func TestAfterAnApplyTheCheckoutIsTheExport_TS_09_34(t *testing.T) {
	rng := rand.New(rand.NewSource(34))
	for i := range 40 {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			hostTree, export := randomTree(rng), randomTree(rng)
			hostTree[".gitignore"], export[".gitignore"] = file("build/\n*.log\n"), file("build/\n*.log\n")
			r := newTestRepo(t, nil, false)
			commitTree(t, r, ignoredFree(hostTree))
			ignoredOnHost := map[string]node{"build/cache": file("cache"), "host.log": file("log")}
			writeTree(t, r.Dir, ignoredOnHost)
			dir := exportOf(t, export)
			p, _ := applying(r, dir)

			if _, err := Apply(context.Background(), p); err != nil {
				t.Fatal(err)
			}

			view, err := checks.FileView(context.Background(), r.Dir)
			if err != nil {
				t.Fatal(err)
			}
			want := snapshotOf(t, dir, ignoredFree2(allPaths(t, dir)))
			if got := snapshotOf(t, r.Dir, view); !equalSnapshots(got, want) {
				t.Errorf("the checkout holds\n%v\nwant\n%v", got, want)
			}
			if kept := snapshotOf(t, r.Dir, []string{"build/cache", "host.log"}); len(kept) != 2 {
				t.Errorf("what the checkout ignores was touched: %v", kept)
			}
		})
	}
}

// pool is the paths a random tree is made of: some are a file where others are a
// directory, so that two trees disagree about the kind of a path.
var pool = []string{"a", "a/b", "a/b/c", "a/d", "b", "b/e", "c.txt", "d.sh", "e/f.txt", "e/g/h", "build/x", "x.log", "sub/y.log", "z", "z2/z3"}

// randomTree is a tree of files, executables and links over the pool, in which no path
// is inside another.
func randomTree(rng *rand.Rand) map[string]node {
	tree := map[string]node{}
	contents := []string{"one\n", "two\n", "three\n"}
	targets := []string{"a", "z", "../x", "b/e", "c.txt"}
	for _, i := range rng.Perm(len(pool)) {
		path := pool[i]
		if rng.Intn(3) == 0 {
			continue
		}
		clash := false
		for other := range tree {
			if strings.HasPrefix(other, path+"/") || strings.HasPrefix(path, other+"/") {
				clash = true
			}
		}
		if clash {
			continue
		}
		switch rng.Intn(4) {
		case 0:
			tree[path] = link(targets[rng.Intn(len(targets))])
		case 1:
			tree[path] = exe(contents[rng.Intn(len(contents))])
		default:
			tree[path] = file(contents[rng.Intn(len(contents))])
		}
	}
	return tree
}

// ignoredFree is the tree without the nodes that the rules of .gitignore ignore,
// which a commit would leave out.
func ignoredFree(tree map[string]node) map[string]node {
	out := map[string]node{}
	for k, v := range tree {
		if !ignoredByTest(k) {
			out[k] = v
		}
	}
	return out
}

func ignoredFree2(paths []string) []string {
	return slices.DeleteFunc(slices.Clone(paths), ignoredByTest)
}

func ignoredByTest(path string) bool {
	return strings.HasPrefix(path, "build/") || strings.HasSuffix(path, ".log")
}

// TestApplyNeverWritesThroughALink guards the writes of an apply: a link of the
// checkout that the export has a file in place of is replaced and nothing is written to
// where it leads, and an export with a path beyond a link applies nothing, since git
// cannot say whether such a path is ignored.
func TestApplyNeverWritesThroughALink(t *testing.T) {
	outside := t.TempDir()
	r := newTestRepo(t, map[string]string{"keep.txt": "keep\n"}, false)
	commitTree(t, r, map[string]node{"out": link(outside), "inside": link("sub")})
	if err := os.MkdirAll(filepath.Join(r.Dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}

	t.Run("a link replaced by a file", func(t *testing.T) {
		export := exportOf(t, map[string]node{"keep.txt": file("keep\n"), "out": file("now a file\n"), "inside": link("sub")})
		p, _ := applying(r, export)

		if _, err := Apply(context.Background(), p); err != nil {
			t.Fatal(err)
		}

		if entries, _ := os.ReadDir(outside); len(entries) != 0 {
			t.Errorf("%d entries were written outside the checkout", len(entries))
		}
		if got := snapshotOf(t, r.Dir, []string{"out", "inside"}); got["out"] != "file:644:now a file\n" || got["inside"] != "link:sub" {
			t.Errorf("the checkout holds %v", got)
		}
	})
	t.Run("a link replaced by a directory", func(t *testing.T) {
		r2 := newTestRepo(t, map[string]string{"keep.txt": "keep\n"}, false)
		commitTree(t, r2, map[string]node{"out": link(outside)})
		export := exportOf(t, map[string]node{"keep.txt": file("keep\n"), "out/pwn.txt": file("pwn\n")})
		p, _ := applying(r2, export)

		res, err := Apply(context.Background(), p)

		if err != nil {
			t.Fatal(err)
		}
		if entries, _ := os.ReadDir(outside); len(entries) != 0 {
			t.Errorf("%d entries were written outside the checkout", len(entries))
		}
		if info, err := os.Lstat(filepath.Join(r2.Dir, "out")); err != nil || !info.IsDir() {
			t.Errorf("out is %v (%v), want a directory in place of the link", info, err)
		}
		if got := snapshotOf(t, r2.Dir, []string{"out/pwn.txt"}); got["out/pwn.txt"] != "file:644:pwn\n" || res.Written != 1 || res.Deleted != 1 {
			t.Errorf("the checkout holds %v, result %+v, want the file in the directory, 1 written and the link deleted", got, res)
		}
	})
	t.Run("a link that the checkout ignores and keeps", func(t *testing.T) {
		r3 := newTestRepo(t, map[string]string{".gitignore": "cache\n", "keep.txt": "keep\n"}, false)
		writeTree(t, r3.Dir, map[string]node{"cache": link(outside)})
		export := exportOf(t, map[string]node{".gitignore": file("cache\n"), "keep.txt": file("keep\n"), "cache/x": file("x\n")})
		p, _ := applying(r3, export)

		res, err := Apply(context.Background(), p)

		if err != nil {
			t.Fatal(err)
		}
		if entries, _ := os.ReadDir(outside); len(entries) != 0 || res.Ignored != 1 || res.Written != 0 {
			t.Errorf("%d entries outside, result %+v, want the file left out and nothing written", len(entries), res)
		}
		if got := snapshotOf(t, r3.Dir, []string{"cache"}); got["cache"] != "link:"+outside {
			t.Errorf("the link the checkout keeps is %v", got)
		}
	})
}

// TestApplyTwiceChangesNothingTheSecondTime guards that an apply writes only what
// differs, and keeps a tracked link that a rule ignores, which the box never held.
func TestApplyTwiceChangesNothingTheSecondTime(t *testing.T) {
	r := newTestRepo(t, map[string]string{".gitignore": "ignored-link\nsecret.cfg\n", "keep.txt": "keep\n"}, false)
	writeTree(t, r.Dir, map[string]node{"ignored-link": link("keep.txt"), "secret.cfg": file("x\n")})
	gitDo(t, r.Dir, "add", "-f", "ignored-link", "secret.cfg")
	gitDo(t, r.Dir, "commit", "-q", "--allow-empty", "-m", "chore: tracked and ignored")
	export := exportOf(t, map[string]node{".gitignore": file("ignored-link\nsecret.cfg\n"), "keep.txt": file("keep\n"), "new.txt": file("new\n"), "ln": link("new.txt")})
	p, _ := applying(r, export)

	first, err := Apply(context.Background(), p)
	second, err2 := Apply(context.Background(), p)

	if err != nil || err2 != nil {
		t.Fatal(err, err2)
	}
	if first.Written != 2 || first.Deleted != 1 {
		t.Errorf("first = %+v, want the new file and the link written, the tracked file that a rule ignores deleted as the brain deleted it, and the tracked link that a rule ignores kept", first)
	}
	if _, err := os.Lstat(filepath.Join(r.Dir, "secret.cfg")); err == nil {
		t.Error("secret.cfg, which the box held and the brain deleted, is still in the checkout")
	}
	if second.Written != 0 || second.Deleted != 0 {
		t.Errorf("second = %+v, want nothing to do", second)
	}
	if got := snapshotOf(t, r.Dir, []string{"ignored-link"}); got["ignored-link"] != "link:keep.txt" {
		t.Errorf("the tracked link was removed: %v", got)
	}
}

// TestApplyStopsBeforeItWritesWhenItCannotTellWhatIsIgnored guards the order of an
// apply: everything is decided before anything is written, and a stopped run writes
// nothing.
func TestApplyStopsBeforeItWritesWhenItCannotTellWhatIsIgnored(t *testing.T) {
	r := newTestRepo(t, map[string]string{"keep.txt": "keep\n", "old.txt": "old\n"}, false)
	export := exportOf(t, map[string]node{"keep.txt": file("changed\n"), "new.txt": file("new\n")})
	before := snapshotOf(t, r.Dir, allPaths(t, r.Dir))

	broken := gitx.New(r.Dir, func(ctx context.Context, dir string, argv []string, stdin ...string) (string, int, error) {
		if slices.Contains(argv, "check-ignore") {
			return "fatal: nope", 128, nil
		}
		return gitx.ExecRunner(ctx, dir, argv, stdin...)
	})
	p, ledger := applying(r, export)
	p.Git = broken
	if _, err := Apply(context.Background(), p); err == nil || len(*ledger) != 0 {
		t.Errorf("an apply that cannot tell what is ignored: %v, ledger %v, want an error", err, *ledger)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	p.Git = r.Git
	if _, err := Apply(ctx, p); err == nil || len(*ledger) != 0 {
		t.Errorf("a cancelled apply: %v, ledger %v, want an error", err, *ledger)
	}
	if after := snapshotOf(t, r.Dir, allPaths(t, r.Dir)); !equalSnapshots(before, after) {
		t.Errorf("a stopped apply changed the checkout: %v, was %v", after, before)
	}
}

// TestApplyComparesWholeFiles guards the comparison of content: files larger than the
// buffer that differ in the last byte, or in the middle, are written, and the same ones
// are not.
func TestApplyComparesWholeFiles(t *testing.T) {
	big := func(change int, to byte) string {
		b := []byte(strings.Repeat("0123456789abcdef", 8<<10)) // 128 KiB
		if change >= 0 {
			b[change] = to
		}
		return string(b)
	}
	r := newTestRepo(t, map[string]string{"same.bin": big(-1, 0), "tail.bin": big(-1, 0), "middle.bin": big(-1, 0), "longer.bin": big(-1, 0)}, false)
	export := exportOf(t, map[string]node{
		"same.bin": file(big(-1, 0)), "tail.bin": file(big(128<<10-1, 'X')), "middle.bin": file(big(70<<10, 'X')), "longer.bin": file(big(-1, 0) + "more"),
	})
	p, _ := applying(r, export)

	res, err := Apply(context.Background(), p)

	if err != nil || res.Written != 3 || res.Deleted != 0 {
		t.Fatalf("result = %+v, %v, want the three that differ written", res, err)
	}
	for _, name := range []string{"tail.bin", "middle.bin", "longer.bin", "same.bin"} {
		want, _ := os.ReadFile(filepath.Join(export, name))
		if got, _ := os.ReadFile(filepath.Join(r.Dir, name)); string(got) != string(want) {
			t.Errorf("%s differs from the export", name)
		}
	}
	if again, err := Apply(context.Background(), p); err != nil || again.Written != 0 {
		t.Errorf("second apply = %+v, %v, want nothing written", again, err)
	}
}

// TestMakeRoomReplacesAFileInTheWayAndRefusesALink guards the directories above a path.
func TestMakeRoomReplacesAFileInTheWayAndRefusesALink(t *testing.T) {
	dir := t.TempDir()
	outside := t.TempDir()
	writeTree(t, dir, map[string]node{"file": file("x"), "a": file("in the way"), "via": link(outside), "plain/dir/keep": file("keep")})
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()

	if err := makeRoom(root, "a/b/c.txt", false); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(filepath.Join(dir, "a", "b")); err != nil || !info.IsDir() {
		t.Errorf("a/b is %v (%v), want directories where the file was", info, err)
	}
	if err := makeRoom(root, "plain/dir/new.txt", false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "plain", "dir", "keep")); err != nil {
		t.Errorf("a directory that was there lost what it held: %v", err)
	}
	if err := makeRoom(root, "via/deep/x.txt", false); err == nil || failure(t, err).Category != toolio.CategoryIO {
		t.Errorf("a path beyond a link: %v, want a refusal", err)
	}
	if entries, _ := os.ReadDir(outside); len(entries) != 0 {
		t.Errorf("%d entries were made outside", len(entries))
	}
	if err := makeRoom(root, "file", true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(dir, "file")); err == nil {
		t.Error("what was at the path is still there")
	}
}

// TestTheLedgerOfAnApplyIsNoConditionOfIt guards the notice of a ledger that cannot be
// written, and an apply with no ledger.
func TestTheLedgerOfAnApplyIsNoConditionOfIt(t *testing.T) {
	r := newTestRepo(t, map[string]string{"keep.txt": "keep\n"}, false)
	export := exportOf(t, map[string]node{"keep.txt": file("keep\n"), "new.txt": file("new\n")})

	if res, err := Apply(context.Background(), ApplyParams{Dir: r.Dir, Export: export, Git: r.Git}); err != nil || res.Written != 1 {
		t.Errorf("no ledger: %+v, %v", res, err)
	}
	var warned []string
	p := ApplyParams{Dir: r.Dir, Export: export, Git: r.Git,
		Record: func(string, any) error { return os.ErrPermission },
		Warn:   func(code, _ string) { warned = append(warned, code) }}
	if _, err := Apply(context.Background(), p); err != nil || !slices.Equal(warned, []string{"ledger_unwritable"}) {
		t.Errorf("a ledger that cannot be written: %v, warnings %v, want the warning and no failure", err, warned)
	}
	p.Warn = nil
	if _, err := Apply(context.Background(), p); err != nil {
		t.Errorf("no way to warn: %v", err)
	}
}

// TestAnApplyThatIsStoppedBetweenFilesStopsBeforeTheNextOne guards the cancellation of an
// apply: the run is stopped as the plan is done, and nothing is deleted or written.
func TestAnApplyThatIsStoppedBetweenFilesStopsBeforeTheNextOne(t *testing.T) {
	for name, tree := range map[string]map[string]node{
		"with a deletion":   {"keep.txt": file("changed\n")},
		"with only a write": {"keep.txt": file("keep\n"), "old.txt": file("old\n"), "new.txt": file("new\n")},
	} {
		t.Run(name, func(t *testing.T) {
			r := newTestRepo(t, map[string]string{"keep.txt": "keep\n", "old.txt": "old\n"}, false)
			before := snapshotOf(t, r.Dir, allPaths(t, r.Dir))
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			stopper := gitx.New(r.Dir, func(c context.Context, dir string, argv []string, stdin ...string) (string, int, error) {
				out, code, err := gitx.ExecRunner(c, dir, argv, stdin...)
				if slices.Contains(argv, "--ignored") {
					cancel() // the run is stopped when the plan is done
				}
				return out, code, err
			})
			p, _ := applying(r, exportOf(t, tree))
			p.Git = stopper

			_, err := Apply(ctx, p)

			if err == nil || ctx.Err() == nil {
				t.Fatalf("a stopped apply: %v", err)
			}
			if after := snapshotOf(t, r.Dir, allPaths(t, r.Dir)); !equalSnapshots(before, after) {
				t.Errorf("a stopped apply changed the checkout: %v, was %v", after, before)
			}
		})
	}
}
