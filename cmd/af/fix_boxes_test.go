package main

import (
	"context"
	"encoding/json"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/agent-fox-dev/agentkit-go/sandbox"
	"github.com/agent-fox-dev/agentkit-go/sandbox/local"

	"github.com/agent-fox-dev/agentfox/internal/box"
	"github.com/agent-fox-dev/agentfox/internal/brain"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
	"github.com/agent-fox-dev/agentfox/internal/triage/fakeforge"
)

// boxFixFiles are the files of the repository of these tests: the red fixture, a file to
// delete and a script whose executable bit is to be flipped, and a check of the gate that the
// files the apply step wrote are owned by the user who runs af: the check runs on the tree
// that was applied, before the commit, so a file that a box's user left owned by another
// fails the run where it matters (a landlock or podman box), and not after git has rewritten it.
func boxFixFiles() map[string]string {
	files := redFixFiles()
	files["docs/old.txt"] = "old\n"
	files["bin/run.sh"] = "#!/bin/sh\necho run\n"
	files["tests/owner_test.sh"] = "bad=$(find src tests docs bin -not -user \"$(id -un)\" 2>/dev/null | head -3)\n[ -z \"$bad\" ] || { echo \"not owned by the host user: $bad\"; exit 1; }\n"
	return files
}

// shellRound is a round of a brain with hands of its own: it does its work through the
// shell of the box, git among it (which no gate of af would allow it), and then submits.
func shellRound(t *testing.T, script, payload string, hash *string) func(context.Context, brain.Brief) (brain.Outcome, error) {
	return func(ctx context.Context, b brain.Brief) (brain.Outcome, error) {
		if code, out, err := b.Box.Exec(ctx, script, time.Minute); err != nil || code != 0 {
			t.Errorf("the brain's shell: %d, %v\n%s", code, err, out)
		}
		if hash != nil {
			_, out, _ := b.Box.Exec(ctx, "git rev-parse HEAD", time.Minute)
			*hash = strings.TrimSpace(out)
		}
		out, err := submitting(payload)(ctx, b)
		out.CostSource = brain.CostMeasured
		return out, err
	}
}

// The work of a brain that does the four kinds of change, the fix and a commit of its own.
const (
	fourChanges = `printf 'echo hello\n' > src/greet.sh
cat > tests/hello_test.sh <<'EOF'
[ "$(sh src/greet.sh)" = hello ] || { echo "FAIL: not hello"; exit 1; }
EOF
rm docs/old.txt
chmod +x bin/run.sh`
	commitTheWork = `
git add -A && git commit -q -m 'brain: the work, committed in the box'`
)

// ledgerEntries are the entries of a run's ledger of the kind.
func ledgerEntries(t *testing.T, runDir, kind string) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(string(readFile(t, filepath.Join(runDir, "ledger.jsonl")))), "\n") {
		var entry struct {
			Kind string
			Data map[string]any
		}
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			t.Fatal(err)
		}
		if entry.Kind == kind {
			out = append(out, entry.Data)
		}
	}
	return out
}

// TestFixUnderLocalAppliesTheExportOnceAndLandsAllFourChanges_TS_10_32 verifies TS-10-32:
// an edit, an addition, a deletion and a change of the executable bit that a brain made in
// the box all arrive in one commit with the trailer, the commit it made in the box is not
// on the branch, the files are the host user's, and the box was exported once.
// Verifies: 10-REQ-6.1
func TestFixUnderLocalAppliesTheExportOnceAndLandsAllFourChanges_TS_10_32(t *testing.T) {
	requireMake(t)
	fixFourChangesUnder(t, "local")
}

// fixFourChangesUnder runs af fix in the box of the provider with a brain that makes the four
// kinds of change in the box, and a commit of its own, and holds what lands to what 10-REQ-6.1
// says: one commit with the trailer, the four changes in it, the commit of the box not on the
// branch, the files the host user's, and the tree exported once.
func fixFourChangesUnder(t *testing.T, provider string) {
	t.Helper()
	e := newFixEnvWith(t, fakeforge.GitHub, boxFixFiles(), true)
	var inBox string
	spyRounds(t, shellRound(t, fourChanges+commitTheWork, spyReport, &inBox))

	code, env := e.fix(e.forge.IssueURL(7), "--brain", "spy", "--sandbox", provider)

	if inBox == "" {
		t.Fatal("the brain recorded no commit")
	}
	assertFourChangesLanded(t, e, code, env, inBox)
	if n := len(ledgerEntries(t, runDirOf(e, env), "box_export")); n != 1 {
		t.Errorf("%d box_export entries, want one: the apply step reads the tree through Export once", n)
	}
}

// assertFourChangesLanded holds the result of a run to the landing of the four changes: an
// edit, an addition, a deletion and the executable bit, in one commit with the trailer, the
// commit made in the box (when there is one) not in the repository, and the files owned by
// the host user. It leaves the checkout on main.
func assertFourChangesLanded(t *testing.T, e *fixEnv, code int, env map[string]any, inBox string) {
	t.Helper()
	if code != 0 || env["status"] != "done" {
		t.Fatalf("exit code %d, status %v, error %v", code, env["status"], env["error"])
	}
	res := resultOf(t, env)
	branch, _ := res["branch"].(string)
	base, _ := res["base_head"].(string)
	if res["outcome"] != "landed" || !strings.HasPrefix(branch, "fix/7-") {
		t.Fatalf("result = %v, want landed on fix/7-…", res)
	}
	if n := len(strings.Fields(gitDo(t, e.dir, "rev-list", base+".."+branch))); n != 1 {
		t.Errorf("%d commits above the base, want one", n)
	}
	if msg := gitDo(t, e.dir, "log", "-1", "--format=%B", branch); !strings.Contains(msg, "Agent-Fox-Run: "+env["run_id"].(string)) {
		t.Errorf("message = %q, want the trailer", msg)
	}
	changes := map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(gitDo(t, e.dir, "diff", "--raw", "--no-abbrev", base+".."+branch)), "\n") {
		fields := strings.Fields(line) // :old new oldsha newsha status path
		if len(fields) == 7 {
			changes[fields[6]] = fields[4] + fields[0] + ">" + fields[1]
		} else if len(fields) == 6 {
			changes[fields[5]] = fields[4] + fields[0] + ">" + fields[1]
		}
	}
	for path, want := range map[string]string{
		"src/greet.sh":        "M:100644>100644",
		"tests/hello_test.sh": "A:000000>100644",
		"docs/old.txt":        "D:100644>000000",
		"bin/run.sh":          "M:100644>100755",
	} {
		if changes[path] != want {
			t.Errorf("%s: %q in the commit, want %q (all: %v)", path, changes[path], want, changes)
		}
	}
	if inBox != "" {
		if err := exec.Command("git", "-C", e.dir, "cat-file", "-e", inBox+"^{commit}").Run(); err == nil {
			t.Errorf("the commit made in the box, %s, is in the host's repository", inBox)
		}
	}
	e.onBase(t)
	for _, path := range []string{"src/greet.sh", "tests/hello_test.sh", "bin/run.sh"} {
		gitDo(t, e.dir, "checkout", "-q", branch)
		info, err := os.Stat(filepath.Join(e.dir, path))
		if err != nil {
			t.Fatal(err)
		}
		if owner := info.Sys().(*syscall.Stat_t).Uid; int(owner) != os.Getuid() {
			t.Errorf("%s is owned by %d, want the host user %d", path, owner, os.Getuid())
		}
	}
	gitDo(t, e.dir, "checkout", "-q", "main")
}

// sessions are the boxes that the brains of a test were given, to ask them afterwards what the
// loop did to them.
type sessions struct {
	mu   sync.Mutex
	seen []*box.Session
}

func (s *sessions) note(b *box.Session) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seen = append(s.seen, b)
}

// exports is how many times Export was asked of those boxes.
func (s *sessions) exports() (calls, boxes int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, b := range s.seen {
		calls += b.ExportCalls()
	}
	return calls, len(s.seen)
}

// watching is a brain that notes the box it is given and runs another.
type watching struct {
	inner brain.Brain
	s     *sessions
}

func (w watching) Run(ctx context.Context, b brain.Brief) (brain.Outcome, error) {
	w.s.note(b.Box)
	return w.inner.Run(ctx, b)
}

// watchScripted puts the scripted brain in the registry of the fix commands behind a wrapper
// that keeps the box it is given.
func watchScripted(t *testing.T) *sessions {
	t.Helper()
	s := &sessions{}
	entry, err := brain.Default.Lookup(brain.ScriptedName)
	if err != nil {
		t.Fatal(err)
	}
	made := entry.New
	entry.New = func(o brain.Options) (brain.Brain, error) {
		inner, err := made(o)
		return watching{inner: inner, s: s}, err
	}
	old := fixRegistry
	reg := brain.NewRegistry()
	reg.Register(entry)
	fixRegistry = reg
	t.Cleanup(func() { fixRegistry = old })
	return s
}

// watchRounds wraps the rounds of a spy so that each notes the box it is given.
func watchRound(s *sessions, round func(context.Context, brain.Brief) (brain.Outcome, error)) func(context.Context, brain.Brief) (brain.Outcome, error) {
	return func(ctx context.Context, b brain.Brief) (brain.Outcome, error) {
		s.note(b.Box)
		return round(ctx, b)
	}
}

// TestFixUnderNoneLandsFromTheCheckoutAndNeverExports_TS_10_33 verifies TS-10-33: a
// scripted brain under none lands one commit with the trailer, no tree is exported, and
// what the brain wrote under a protected path is put back on the checkout.
// Verifies: 10-REQ-6.2
func TestFixUnderNoneLandsFromTheCheckoutAndNeverExports_TS_10_33(t *testing.T) {
	requireMake(t)
	t.Run("scripted", func(t *testing.T) {
		e := newFixEnvWith(t, fakeforge.GitHub, redFixFiles(), true)
		watched := watchScripted(t)
		steps := script(t, writeStep("src/greet.sh", "echo hello\n"), writeStep("tests/hello_test.sh", helloTest), submitStep(spyReport, "accepted"))

		code, env := e.fix(e.forge.IssueURL(7), "--brain", "scripted", "--script", steps, "--sandbox", "none")

		if code != 0 || env["status"] != "done" {
			t.Fatalf("exit code %d, status %v, error %v", code, env["status"], env["error"])
		}
		res := resultOf(t, env)
		branch, _ := res["branch"].(string)
		base, _ := res["base_head"].(string)
		if res["outcome"] != "landed" || !strings.HasPrefix(branch, "fix/7-") {
			t.Fatalf("result = %v", res)
		}
		if n := len(strings.Fields(gitDo(t, e.dir, "rev-list", base+".."+branch))); n != 1 {
			t.Errorf("%d commits above the base, want one", n)
		}
		if msg := gitDo(t, e.dir, "log", "-1", "--format=%B", branch); !strings.Contains(msg, "Agent-Fox-Run: "+env["run_id"].(string)) {
			t.Errorf("message = %q, want the trailer", msg)
		}
		if n := len(ledgerEntries(t, runDirOf(e, env), "box_export")); n != 0 {
			t.Errorf("%d box_export entries, want none: Export is never called in place", n)
		}
		if calls, boxes := watched.exports(); calls != 0 || boxes == 0 {
			t.Errorf("Export was asked %d times of the %d boxes the brain was given, want none", calls, boxes)
		}
		for _, name := range []string{"gate.json", "guard.json"} {
			if _, err := os.Stat(filepath.Join(e.dir, ".agent-fox", name)); err == nil {
				t.Errorf(".agent-fox/%s, which af placed for the rounds, is left on the checkout as if it were the person's", name)
			}
		}
		if box, _ := res["box"].(map[string]any); box["provider"] != "none" || box["network"] != "host" || box["image"] != "" {
			t.Errorf("box = %v, want none, no image, network host", res["box"])
		}
		if !slices.Contains(warningCodes(env), "box_unconfined") {
			t.Errorf("warnings = %v, want box_unconfined", warningCodes(env))
		}
		e.onBase(t)
	})
	t.Run("a gate.json of the person's", func(t *testing.T) {
		e := newFixEnvWith(t, fakeforge.GitHub, redFixFiles(), true)
		if code, env := e.run("", "lang", "detect", "--write", "--dir", e.dir); code != 0 {
			t.Fatalf("lang detect --write: exit code %d, error %v", code, env["error"])
		}
		mine := readFile(t, filepath.Join(e.dir, ".agent-fox", "gate.json"))
		steps := script(t, writeStep("src/greet.sh", "echo hello\n"), writeStep("tests/hello_test.sh", helloTest), submitStep(spyReport, "accepted"))

		code, env := e.fix(e.forge.IssueURL(7), "--brain", "scripted", "--script", steps, "--sandbox", "none")

		if code != 0 || env["status"] != "done" {
			t.Fatalf("exit code %d, status %v, error %v", code, env["status"], env["error"])
		}
		if got := readFile(t, filepath.Join(e.dir, ".agent-fox", "gate.json")); string(got) != string(mine) {
			t.Errorf("gate.json = %q, want the person's file as it was: af placed none of its own", got)
		}
	})
	t.Run("a protected path", func(t *testing.T) {
		e := newFixEnvWith(t, fakeforge.GitHub, redFixFiles(), true)
		files := map[string]string{"src/greet.sh": "echo hello\n", "tests/hello_test.sh": helloTest, ".github/workflows/ci.yml": "name: planted\n"}
		watched := &sessions{}
		spyRounds(t, watchRound(watched, spyRound(t, files, spyReport, 0, 1)))

		code, env := e.fix(e.forge.IssueURL(7), "--brain", "spy", "--sandbox", "none")

		if code != 0 {
			t.Fatalf("exit code %d, error %v", code, env["error"])
		}
		res := resultOf(t, env)
		branch, _ := res["branch"].(string)
		if !slices.Contains(warningCodes(env), "protected_path_reverted") {
			t.Errorf("warnings = %v, want protected_path_reverted: the revert ran on the checkout", warningCodes(env))
		}
		if tree := gitDo(t, e.dir, "ls-tree", "-r", "--name-only", branch); strings.Contains(tree, ".github/workflows/ci.yml") {
			t.Errorf("the tree of the commit holds the planted workflow:\n%s", tree)
		}
		if n := len(ledgerEntries(t, runDirOf(e, env), "box_export")); n != 0 {
			t.Errorf("%d box_export entries, want none", n)
		}
		if calls, boxes := watched.exports(); calls != 0 || boxes == 0 {
			t.Errorf("Export was asked %d times of the %d boxes the brain was given, want none", calls, boxes)
		}
		e.onBase(t)
	})
}

// TestACommitOnTheBaseBranchIsFoldedIntoAfsCommit_TS_10_36 verifies TS-10-36: a brain that
// edits and commits on the base branch under none has the commit taken off it, its change
// is inside the one commit of af, and foreign_commits_reset is raised once, with the count.
// Verifies: 10-REQ-6.5
func TestACommitOnTheBaseBranchIsFoldedIntoAfsCommit_TS_10_36(t *testing.T) {
	requireMake(t)
	e := newFixEnvWith(t, fakeforge.GitHub, redFixFiles(), true)
	var foreign string
	spyRounds(t, shellRound(t, `printf 'echo hello\n' > src/greet.sh
cat > tests/hello_test.sh <<'EOF'
[ "$(sh src/greet.sh)" = hello ] || { echo "FAIL: not hello"; exit 1; }
EOF
git add -A && git commit -q -m 'brain: committed on the base branch'`, spyReport, &foreign))

	code, env := e.fix(e.forge.IssueURL(7), "--brain", "spy", "--sandbox", "none")

	if code != 0 || env["status"] != "done" {
		t.Fatalf("exit code %d, status %v, error %v", code, env["status"], env["error"])
	}
	res := resultOf(t, env)
	branch, _ := res["branch"].(string)
	base, _ := res["base_head"].(string)
	if n := len(strings.Fields(gitDo(t, e.dir, "rev-list", base+".."+branch))); n != 1 {
		t.Errorf("%d commits above the base, want af's one", n)
	}
	if out := gitDo(t, e.dir, "branch", "--contains", foreign); strings.Contains(out, "main") || strings.Contains(out, branch) {
		t.Errorf("the foreign commit %s is on %q", foreign, out)
	}
	if got := strings.TrimSpace(gitDo(t, e.dir, "rev-parse", "main")); got != base {
		t.Errorf("main is at %s, want the base %s", got, base)
	}
	if show := gitDo(t, e.dir, "show", "--stat", "--format=", branch); !strings.Contains(show, "src/greet.sh") || !strings.Contains(show, "tests/hello_test.sh") {
		t.Errorf("af's commit does not hold the change of the foreign one:\n%s", show)
	}
	var warned []map[string]any
	for _, w := range env["warnings"].([]any) {
		if w.(map[string]any)["code"] == "foreign_commits_reset" {
			warned = append(warned, w.(map[string]any))
		}
	}
	if len(warned) != 1 || !strings.Contains(warned[0]["message"].(string), "1 commit") {
		t.Errorf("warnings = %v, want one foreign_commits_reset that counts 1", warned)
	}
	if entries := ledgerEntries(t, runDirOf(e, env), "foreign_commits_reset"); len(entries) != 1 || entries[0]["count"] != float64(1) {
		t.Errorf("ledger entries = %v, want foreign_commits_reset with count 1", entries)
	}
	e.onBase(t)
}

// checkoutState is the state of the repository that a run must not change once the brain has
// left it: every ref with what it points at, the branch HEAD is on, the index and the tree.
func checkoutState(t *testing.T, dir string) string {
	t.Helper()
	return strings.Join([]string{
		gitDo(t, dir, "for-each-ref", "--format=%(refname) %(objectname)"),
		gitDo(t, dir, "rev-parse", "--abbrev-ref", "HEAD"), gitDo(t, dir, "rev-parse", "HEAD"),
		gitDo(t, dir, "status", "--porcelain=v1", "--untracked-files=all"),
		gitDo(t, dir, "diff", "--cached", "--raw"), gitDo(t, dir, "diff", "--raw"),
	}, "\n--\n")
}

// TestABranchSwitchADetachedHeadOrRewrittenHistoryIsRefusedWithTheTreeUntouched_TS_10_37
// verifies TS-10-37: each of the three stops the run as failed/git, not retryable, names
// where the checkout was and where it is, leaves the tree and the refs as the brain left
// them, and lands and parks nothing.
// Verifies: 10-REQ-6.6, 10-REQ-6.5
func TestABranchSwitchADetachedHeadOrRewrittenHistoryIsRefusedWithTheTreeUntouched_TS_10_37(t *testing.T) {
	requireMake(t)
	for name, move := range map[string]string{
		"a branch switch":    `git checkout -q -b elsewhere && printf 'echo hello\n' > src/greet.sh`,
		"a detached HEAD":    `git checkout -q --detach && printf 'echo hello\n' > src/greet.sh`,
		"rewritten history":  `git commit -q --amend --allow-empty -m rewritten && printf 'echo hello\n' > src/greet.sh`,
		"a switch to a name": `git checkout -q -b main2 && git commit -q --allow-empty -m other`,
	} {
		t.Run(name, func(t *testing.T) {
			e := newFixEnvWith(t, fakeforge.GitHub, redFixFiles(), true)
			// What the brain left, as git shows it the moment it ends: the refs, where HEAD
			// is, the index and the tree.
			var left string
			round := shellRound(t, move, spyReport, nil)
			spyRounds(t, func(ctx context.Context, b brain.Brief) (brain.Outcome, error) {
				out, err := round(ctx, b)
				left = checkoutState(t, e.dir)
				return out, err
			})
			base := strings.TrimSpace(gitDo(t, e.dir, "rev-parse", "HEAD"))

			code, env := e.fix(e.forge.IssueURL(7), "--brain", "spy", "--sandbox", "none")

			errObj := refusedBy(t, code, env, 1, "git")
			if errObj["retryable"] != false || env["status"] != "failed" {
				t.Errorf("status %v, error %v, want failed/git and not retryable", env["status"], errObj)
			}
			head := strings.TrimSpace(gitDo(t, e.dir, "rev-parse", "HEAD"))
			msg, _ := errObj["message"].(string)
			for _, want := range []string{"main", base, head} {
				if !strings.Contains(msg, want) {
					t.Errorf("message = %q, want it to name %s", msg, want)
				}
			}
			for _, branch := range e.branches() {
				if strings.HasPrefix(branch, "fix/") {
					t.Errorf("branch %s: nothing is landed", branch)
				}
			}
			if subject := gitDo(t, e.dir, "log", "-1", "--format=%s"); strings.HasPrefix(subject, "wip:") || len(e.remoteBranches(t)) != 1 {
				t.Errorf("subject %q, remote branches %v: nothing is parked or pushed", subject, e.remoteBranches(t))
			}
			if n := len(e.forge.Writes()); n != 0 {
				t.Errorf("%d writes to the forge", n)
			}
			if slices.Contains(warningCodes(env), "foreign_commits_reset") {
				t.Errorf("warnings = %v, want no foreign_commits_reset", warningCodes(env))
			}
			if name != "a switch to a name" {
				if data, _ := os.ReadFile(filepath.Join(e.dir, "src", "greet.sh")); string(data) != "echo hello\n" {
					t.Errorf("src/greet.sh = %q, want what the brain left", data)
				}
			}
			if after := checkoutState(t, e.dir); after != left {
				t.Errorf("the checkout is not as the brain left it:\nleft:\n%s\nafter:\n%s", left, after)
			}
		})
	}
}

// TestAForeignCommitIsTakenOffTheBranchEvenWhenTheRunIsStopped guards 10-REQ-6.5 for the
// stop: a person who interrupts the run as the brain ends has not asked for its commit to stay
// on their branch. The check of the checkout runs on a context the stop did not reach, the
// commit is taken off main, and the run parks the change it holds on a local branch, as it
// parks one when it is stopped under any other box.
// Verifies: 10-REQ-6.5
func TestAForeignCommitIsTakenOffTheBranchEvenWhenTheRunIsStopped(t *testing.T) {
	requireMake(t)
	e := newFixEnvWith(t, fakeforge.GitHub, redFixFiles(), true)
	baseHead := strings.TrimSpace(gitDo(t, e.dir, "rev-parse", "HEAD"))
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	var foreign string
	round := shellRound(t, `printf 'echo hello\n' > src/greet.sh
git add -A && git commit -q -m 'brain: committed on the base branch'`, spyReport, &foreign)
	spyRounds(t, func(c context.Context, b brain.Brief) (brain.Outcome, error) {
		out, err := round(c, b)
		cancel() // the run is stopped as the brain ends
		return out, err
	})

	code, env := e.runCtx(ctx, "", "fix", e.forge.IssueURL(7), "--dir", e.dir, "--brain", "spy", "--sandbox", "none")

	if errObj, _ := env["error"].(map[string]any); code == 0 || errObj["category"] != "cancelled" {
		t.Errorf("exit code %d, error %v, want the run to end as cancelled", code, env["error"])
	}
	if foreign == "" {
		t.Fatal("the brain recorded no commit")
	}
	if got := strings.TrimSpace(gitDo(t, e.dir, "rev-parse", "main")); got != baseHead {
		t.Errorf("main is at %s, want the base %s: the commit of the brain stayed on the branch of the person", got, baseHead)
	}
	if on := gitDo(t, e.dir, "branch", "--contains", foreign); strings.Contains(on, "main") {
		t.Errorf("the foreign commit %s is on %q", foreign, on)
	}
	var parked []string
	for _, b := range e.branches() {
		if b != "main" {
			parked = append(parked, b)
		}
	}
	if len(parked) != 1 || !strings.Contains(gitDo(t, e.dir, "show", "--stat", "--format=", parked[0]), "src/greet.sh") {
		t.Errorf("branches %v: want the change parked on one local branch", e.branches())
	}
}

// TestACheckoutThatTheBrainMovedIsNotParkedWhenTheRunIsStoppedToo guards 10-REQ-6.6 for the
// stop: a checkout on another branch is left as the brain left it and nothing is committed on
// it, whether or not the run was stopped as well.
// Verifies: 10-REQ-6.6
func TestACheckoutThatTheBrainMovedIsNotParkedWhenTheRunIsStoppedToo(t *testing.T) {
	requireMake(t)
	e := newFixEnvWith(t, fakeforge.GitHub, redFixFiles(), true)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	round := shellRound(t, `git checkout -q -b elsewhere && printf 'echo hello\n' > src/greet.sh`, spyReport, nil)
	spyRounds(t, func(c context.Context, b brain.Brief) (brain.Outcome, error) {
		out, err := round(c, b)
		cancel()
		return out, err
	})

	e.runCtx(ctx, "", "fix", e.forge.IssueURL(7), "--dir", e.dir, "--brain", "spy", "--sandbox", "none")

	if now := strings.TrimSpace(gitDo(t, e.dir, "rev-parse", "--abbrev-ref", "HEAD")); now != "elsewhere" {
		t.Errorf("checked out %q, want the branch the brain left", now)
	}
	if got := strings.Fields(gitDo(t, e.dir, "rev-list", "--all")); len(got) != 1 {
		t.Errorf("%d commits in the repository, want only the first: nothing is committed or parked on a checkout that was moved", len(got))
	}
	if status := gitDo(t, e.dir, "status", "--porcelain"); !strings.Contains(status, "src/greet.sh") {
		t.Errorf("status = %q, want the edit of the brain left in the tree", status)
	}
}

// TestTheBoxOfTheRunIsInTheLedgerAndTheResult_TS_10_14 verifies TS-10-14: the box_open
// entry of the ledger has the image, the network and the placement, and the result of the
// run says the box: the provider, the image and the network.
// Verifies: 10-REQ-2.6
func TestTheBoxOfTheRunIsInTheLedgerAndTheResult_TS_10_14(t *testing.T) {
	requireMake(t)
	isoboxFor(t)                  // a provider that isolates: the shell of the fix brief is a tool, so its network is open
	isolatingStandIn(t, "podman") // the name that gets an image, over a box that is the local one
	const named = "registry.example/tools:v1"
	for _, tc := range []struct{ name, provider, network, image, imageVar string }{
		{"local", "local", "host", "", ""}, {"none", "none", "host", "", ""}, {"isobox", "isobox", "open", "", ""},
		{"podman", "podman", "open", brain.DefaultImage, ""}, {"podman with AF_SANDBOX_IMAGE", "podman", "open", named, named},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newFixEnvWith(t, fakeforge.GitHub, redFixFiles(), true)
			t.Setenv("AF_SANDBOX_IMAGE", tc.imageVar)
			spyRounds(t, spyRound(t, spyFixed, spyReport, 0, 1))

			code, env := e.fix(e.forge.IssueURL(7), "--brain", "spy", "--sandbox", tc.provider)

			if code != 0 {
				t.Fatalf("exit code %d, error %v", code, env["error"])
			}
			box, _ := resultOf(t, env)["box"].(map[string]any)
			if box["provider"] != tc.provider || box["image"] != tc.image || box["network"] != tc.network || len(box) != 3 {
				t.Errorf("box = %v, want {provider: %s, image: %q, network: %s}", box, tc.provider, tc.image, tc.network)
			}
			entries := ledgerEntries(t, runDirOf(e, env), "box_open")
			if len(entries) != 1 {
				t.Fatalf("%d box_open entries", len(entries))
			}
			for _, key := range []string{"provider", "image", "network", "placement"} {
				if _, ok := entries[0][key]; !ok {
					t.Errorf("box_open = %v, has no %s", entries[0], key)
				}
			}
			if entries[0]["provider"] != tc.provider || entries[0]["network"] != tc.network || entries[0]["image"] != tc.image || entries[0]["placement"] != "host" {
				t.Errorf("box_open = %v, want %s, image %q, network %s, placement host", entries[0], tc.provider, tc.image, tc.network)
			}
		})
	}
}

// standIn notes what a stand-in provider was made and asked for.
type standIn struct {
	builds  int
	extraRO []string
	spec    sandbox.Spec
}

// capturing is the local provider under the name of an isolating one: it notes the spec it is
// asked for and creates without the parts only the real provider can honour.
type capturing struct {
	sandbox.Provider
	in *standIn
}

func (c capturing) Create(ctx context.Context, spec sandbox.Spec) (sandbox.Sandbox, error) {
	c.in.spec = spec
	spec.Image, spec.RequireLandlock = "", false
	spec.Network = sandbox.Network{Mode: sandbox.NetworkUnrestricted}
	return c.Provider.Create(ctx, spec)
}

// isolatingStandIn replaces the provider of the name, for the rest of the test, with the
// local one that says it isolates, so that a run that names landlock or podman gets the
// request it computes and no kernel or engine is asked for.
func isolatingStandIn(t *testing.T, name string) *standIn {
	t.Helper()
	original, err := box.Default.Lookup(name)
	if err != nil {
		t.Fatal(err)
	}
	in := &standIn{}
	box.Default.Register(box.Entry{Name: name, Build: func(cfg box.ProviderConfig) (sandbox.Provider, error) {
		in.builds++
		in.extraRO = cfg.ExtraRO
		return capturing{Provider: local.Unisolated(), in: in}, nil
	}})
	t.Cleanup(func() { box.Default.Register(original) })
	return in
}

// isoboxFor registers a box provider that is the local one and says it isolates, so that a
// brain that runs in a box is placed in it.
func isoboxFor(t *testing.T) {
	t.Helper()
	local, err := box.Default.Lookup("local")
	if err != nil {
		t.Fatal(err)
	}
	entry := local
	entry.Name, entry.Unconfined = "isobox", false
	box.Default.Register(entry)
}

// TestTheDriverInABoxIsNotGivenTheStateOfTheRunAndClaudeIsNotOfferedTheTool guards the two
// facts the loop passes to a round from the brain that runs it: the driver of AgentKit that
// runs in the box finds no fix.json in its run directory (it would be the round of the host),
// and a brain that submits with the command alone is not told of the tool.
func TestTheDriverInABoxIsNotGivenTheStateOfTheRunAndClaudeIsNotOfferedTheTool(t *testing.T) {
	requireMake(t)
	isoboxFor(t)
	for _, tc := range []struct {
		name         string
		entry        brain.Entry
		wantState    bool
		wantToolText bool
	}{
		{"the driver in the box", brain.Entry{Name: "spy", DriverInBox: true}, false, true},
		{"a brain with hands of its own", brain.Entry{Name: "spy", OwnHands: true, CommandRoute: true}, true, false},
		{"a brain on the host", brain.Entry{Name: "spy"}, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newFixEnvWith(t, fakeforge.GitHub, redFixFiles(), true)
			var hasState, mentionsTool bool
			var provider string
			s := &spyBrain{}
			s.run = func(ctx context.Context, b brain.Brief) (brain.Outcome, error) {
				_, err := fs.Stat(b.Box.FS(ctx), ".agent-fox/run/"+b.RunID+"/fix.json")
				hasState, mentionsTool, provider = err == nil, strings.Contains(b.Task, "submit_fix"), b.Box.Provider()
				return brain.Outcome{StopReason: brain.StopEnded}, nil
			}
			old := fixRegistry
			reg := brain.NewRegistry()
			entry := tc.entry
			entry.New = func(brain.Options) (brain.Brain, error) { return s, nil }
			reg.Register(entry)
			fixRegistry = reg
			t.Cleanup(func() { fixRegistry = old })

			e.fix(e.forge.IssueURL(7), "--brain", "spy", "--sandbox", "isobox")

			if provider != "isobox" {
				t.Fatalf("the brain ran in the box %q", provider)
			}
			if hasState != tc.wantState || mentionsTool != tc.wantToolText {
				t.Errorf("fix.json in the box: %v, the task names submit_fix: %v; want %v and %v", hasState, mentionsTool, tc.wantState, tc.wantToolText)
			}
		})
	}
}

// TestTheBoxRequestOfAFixRunSaysWhetherTheProviderWasChosenByDefault guards the flag that
// makes the failure to open a default provider name --sandbox and AF_SANDBOX.
func TestTheBoxRequestOfAFixRunSaysWhetherTheProviderWasChosenByDefault(t *testing.T) {
	local, _ := box.Default.Lookup("local")
	entry, _ := brain.Default.Lookup(brain.ScriptedName)
	dir := t.TempDir()
	rd, err := toolio.NewRunDir(dir, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	r := &toolio.Run{Ctx: context.Background(), Dir: dir, RunDir: rd}
	for _, source := range []string{brain.SandboxFlag, brain.SandboxEnv, brain.SandboxDefault} {
		req := fixBoxRequest(r, rd, entry, local, &brain.Settings{Sandbox: "local", SandboxSource: source}, brain.Options{})
		if req.Defaulted != (source != brain.SandboxFlag) || req.Provider != "local" || req.RunID != rd.ID || req.Tool != "fix" || req.HostDir != dir {
			t.Errorf("source %s: request = %+v", source, req)
		}
	}
}

// TestTheBrainsAreMadeFromTheIdentityOfTheBuild guards the options of a brain: the model of
// AF_MODEL, and the version and commit that --version prints, so that the af of an image
// that is another can be told.
func TestTheBrainsAreMadeFromTheIdentityOfTheBuild(t *testing.T) {
	t.Setenv("AF_MODEL", "m-test")
	oldV, oldC := version, commit
	version, commit = "v9.9.9", "c0ffee"
	t.Cleanup(func() { version, commit = oldV, oldC })

	opts := baseOptions()

	if opts.Model != "m-test" || opts.AFVersion != "v9.9.9" || opts.AFCommit != "c0ffee" {
		t.Errorf("options = %+v, want the model of AF_MODEL and the identity of the build", opts)
	}
	if got := fixBrainOptions(); got.AFVersion != "v9.9.9" || got.Model != "m-test" {
		t.Errorf("fixBrainOptions = %+v", got)
	}
}
