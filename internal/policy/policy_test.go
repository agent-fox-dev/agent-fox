package policy

import (
	"math/rand/v2"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestDecide_JudgesOnlyShellAndWriteTools(t *testing.T) {
	for _, tool := range []string{"Bash", "Write", "read_file", "search_files", "mcp__x", "run_command", "list_files", ""} {
		d := Decide(defaultPolicy(), dir, tool, map[string]any{"command": "git push", "file_path": ".specs/x", "path": "/etc/x", "argv": []any{"git", "push"}})
		if d != (Decision{Decision: Allow}) {
			t.Errorf("%q: %+v, want an allowance with nothing else", tool, d)
		}
	}
}

// TestDecide_DeniesByEveryRuleOfTheClosedSet: each rule has a call it
// denies, and every denial names one of them.
func TestDecide_DeniesByEveryRuleOfTheClosedSet(t *testing.T) {
	rules := []string{RuleUnparseable, RuleHandsGuard, RuleGitMutation, RuleSuiteCommand, RuleAfCommand, RuleProgramNotAllowed, RuleProtectedPath, RuleOutsideDir}
	p := defaultPolicy()
	p.AllowPrograms = []string{"ls"}
	seen := map[string]bool{}
	for _, call := range []struct {
		tool  string
		input map[string]any
	}{
		{"execute", map[string]any{"command": `echo "x`}},
		{"execute", map[string]any{"command": "PATH=/x ls"}},
		{"execute", map[string]any{"command": "git push"}},
		{"execute", map[string]any{"command": "make check"}},
		{"execute", map[string]any{"command": "af fix land"}},
		{"execute", map[string]any{"command": "curl x"}},
		{"execute", map[string]any{"command": "ls > .specs/a"}},
		{"write_file", map[string]any{"path": "/etc/x"}},
		{"write_file", map[string]any{"path": ".specs/x"}},
		{"edit_file", nil},
	} {
		d := Decide(p, dir, call.tool, call.input)
		if d.Decision != Deny || !slices.Contains(rules, d.Rule) {
			t.Errorf("%s %v: %+v", call.tool, call.input, d)
		}
		requireShape(t, call.tool, d)
		seen[d.Rule] = true
	}
	for _, rule := range rules {
		if !seen[rule] {
			t.Errorf("no call is denied by %s", rule)
		}
	}
}

func TestDecide_FirstDenialOfTheLineWins(t *testing.T) {
	requireDecisions(t, defaultPolicy(), []expectation{
		{"git push && make check", Deny, RuleGitMutation, "git push"},
		{"make check && git push", Deny, RuleSuiteCommand, "make check"},
		{"echo ok\nmake check\ngit push", Deny, RuleSuiteCommand, "make check"},
		{"git push > .specs/x", Deny, RuleGitMutation, ""},
		{"make check > .specs/x", Deny, RuleSuiteCommand, ""},
		{"go test ./pkg -run X; git diff", Allow, "", ""},
	})
}

// TestDecide_DeniesWhatCannotBeRead: a line that cannot be read with
// certainty, nests without end or carries no command runs nothing.
func TestDecide_DeniesWhatCannotBeRead(t *testing.T) {
	for _, src := range []string{
		`echo "abc`, `echo 'abc`, `echo $(ls`, "echo `ls", `echo ${x`, `echo <(ls`, `(ls`, `echo $(( 1`, `echo "$(ls"`, "echo a)",
		"case x in a) git push ;; esac", "cat <<EOF\nnot terminated, git push\n", "bash -c '`",
		strings.Repeat("$(", 200) + "ls" + strings.Repeat(")", 200),
		strings.Repeat("$(", 5000) + "ls" + strings.Repeat(")", 5000),
		strings.Repeat("( ", 5000) + "ls" + strings.Repeat(" )", 5000),
		strings.Repeat("${x:-", 5000),
		strings.Repeat("bash -c ", 70) + "ls",
		strings.Repeat("eval ", 70) + "ls",
	} {
		start := time.Now()
		d := bash(defaultPolicy(), src)
		if d.Decision != Deny || d.Rule != RuleUnparseable {
			t.Errorf("%.40q: %+v, want a denial by unparseable", src, d)
		}
		requireShape(t, src, d)
		if took := time.Since(start); took > 5*time.Second {
			t.Errorf("%.30q... took %s", src, took)
		}
	}
	// Parentheses the shell reads as arithmetic run no program.
	if d := bash(defaultPolicy(), strings.Repeat("(", 5000)+"ls"+strings.Repeat(")", 5000)); d.Decision != Deny {
		t.Errorf("deep arithmetic: %+v, want a denial", d)
	}
	for _, src := range []string{`echo "it's"`, `echo 'a"b'`, `echo a\ b`, `echo "a" 'b'`, `echo $(echo ")")`, `echo "$(echo ")")"`} {
		if d := bash(defaultPolicy(), src); d.Rule == RuleUnparseable {
			t.Errorf("%q is unparseable: %+v", src, d)
		}
	}
	for _, input := range []map[string]any{nil, {}, {"command": 7}, {"command": nil}, {"cmd": "ls"}} {
		if d := Decide(defaultPolicy(), dir, "execute", input); d.Decision != Deny || d.Rule != RuleUnparseable || d.Segment == "" || d.Reason == "" {
			t.Errorf("execute with %v: %+v, want a denial by unparseable", input, d)
		}
	}
	if d := bash(defaultPolicy(), "   "); d.Decision != Deny || d.Segment != "(empty command)" {
		t.Errorf("a blank line: %+v", d)
	}
}

// TestDecide_JudgesALongLineInGoodTimeAndRefusesALongerOne: a line up to the
// limit is read; one over it is refused unread, with a segment cut short.
func TestDecide_JudgesALongLineInGoodTimeAndRefusesALongerOne(t *testing.T) {
	unit := "echo 'a b' \"c d\" $(ls) | cat > /tmp/x; "
	src := strings.Repeat(unit, 1_500)
	if len(src) > maxCommandBytes {
		t.Fatalf("the test command is %d bytes, over the limit", len(src))
	}
	start := time.Now()
	if d := bash(defaultPolicy(), src); d.Decision != Allow {
		t.Errorf("decision = %+v", d)
	}
	if took := time.Since(start); took > 3*time.Second {
		t.Errorf("a command of %d bytes took %s", len(src), took)
	}

	for _, n := range []int{maxCommandBytes + 1, 1 << 20} {
		src := strings.Repeat("é", 1) + strings.Repeat(unit, n/len(unit)+1)
		start := time.Now()
		d := bash(defaultPolicy(), src)
		if d.Decision != Deny || d.Rule != RuleUnparseable || !strings.HasSuffix(d.Segment, "…") || len(d.Segment) > 300 || !strings.Contains(d.Reason, "bytes") {
			t.Errorf("%d bytes: %+v", len(src), d)
		}
		if took := time.Since(start); took > time.Second {
			t.Errorf("%d bytes took %s to refuse", len(src), took)
		}
	}
	// A segment is cut at a character, never inside one.
	if got := shorten(strings.Repeat("a", 199) + "éé"); got != strings.Repeat("a", 199)+"…" {
		t.Errorf("shorten = %q", got)
	}
}

func TestDecide_NeverPanicsAndAnswersInShape(t *testing.T) {
	alphabet := []string{"git", " ", "push", "'", `"`, "`", "$(", ")", "(", "{", "}", "${", ";", "&", "|", "<", ">", "<<", "EOF", "\n", "\\", "-c", "bash", "sh", "=", "FOO=1", "#", "make", "check", ".specs/", "af", "fix", "land", "2>", "&>", "<(", "$((", "))"}
	rng := rand.New(rand.NewPCG(6, 7))
	for i := range 4000 {
		var b strings.Builder
		for range 1 + rng.IntN(14) {
			b.WriteString(alphabet[rng.IntN(len(alphabet))])
		}
		src := b.String()
		d := func() Decision {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("#%d: %q panics: %v", i, src, r)
				}
			}()
			return bash(defaultPolicy(), src)
		}()
		requireShape(t, src, d)
	}
}

func FuzzDecide(f *testing.F) {
	for _, seed := range []string{"git push", "echo $(make check)", `bash -c 'git "push'`, "cat <<EOF\n$(ls)\nEOF", "a;;b", "echo ${x:-$(y)}"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, src string) {
		requireShape(t, src, bash(defaultPolicy(), src))
	})
}

// TestGitRule_AllowsReadingAndRefusesTheRest: the read-only subcommands, and
// branch and remote in their listing forms, may run; anything that changes
// the repository or its configuration is af's.
func TestGitRule_AllowsReadingAndRefusesTheRest(t *testing.T) {
	var cases []expectation
	for _, sub := range []string{"status", "log", "diff", "show", "blame", "ls-files", "ls-tree", "rev-parse", "rev-list", "grep", "describe", "cat-file", "shortlog", "merge-base", "name-rev", "for-each-ref", "show-ref"} {
		cases = append(cases, allowed("git "+sub), allowed("git "+sub+" -x --some-flag arg"))
	}
	for _, c := range []string{
		"git branch", "git branch -a", "git branch -r -v", "git branch -vv", "git branch --list", "git branch --list 'feat*'", "git branch -l 'feat*'", "git branch -al 'x'",
		"git branch --show-current", "git branch --contains abc123", "git branch --merged main", "git branch --merged", "git branch --no-merged --all",
		"git branch --format='%(refname)' --sort=-committerdate", "git branch --format x --points-at HEAD",
		"git remote", "git remote -v", "git remote show origin", "git remote get-url origin", "git remote get-url --push origin",
		"git --version", "git", "git -C sub status", "git --no-pager log", "git -P diff", "git --git-dir=.git status", "git --git-dir .git log",
		"git --work-tree=. diff", "git --no-optional-locks status", "git diff > out.txt", "git log | head",
	} {
		cases = append(cases, allowed(c))
	}
	for _, c := range []string{
		"git branch -D x", "git branch -d x", "git branch -m old new", "git branch feature", "git branch feature main", "git branch -c a b", "git branch -u origin/main",
		"git branch --delete x", "git branch --set-upstream-to=origin/main", "git branch --edit-description", "git branch -ad x",
		"git remote add o u", "git remote remove o", "git remote rm o", "git remote rename a b", "git remote set-url o u", "git remote prune o", "git remote update",
		"git commit -m x", "git push", "git push origin main", "git checkout main", "git reset --hard", "git add -A", "git merge x", "git rebase x", "git stash",
		"git tag v1", "git config user.name x", "git clean -fd", "git fetch", "git pull", "git switch main", "git restore f", "git apply p", "git am p",
		"git cherry-pick x", "git revert x", "git worktree add x", "git gc", "git status && git push",
	} {
		cases = append(cases, denied(c, RuleGitMutation))
	}
	requireDecisions(t, defaultPolicy(), cases)

	for _, c := range []string{"git commit -m x", "git -c core.x=y status"} {
		if d := bash(defaultPolicy(), c); !strings.Contains(d.Reason, "af makes every commit, branch and push") {
			t.Errorf("%s: reason = %q, want it to say that af makes every commit, branch and push", c, d.Reason)
		}
	}
}

// TestGitRule_ConfigurationReachesGitThroughTheEnvironmentItIsExportedTo: an
// assignment in front of one command is not the environment of the next one,
// unless it is exported.
func TestGitRule_ConfigurationReachesGitThroughTheEnvironmentItIsExportedTo(t *testing.T) {
	requireDecisions(t, defaultPolicy(), []expectation{
		allowed("GIT_EXTERNAL_DIFF=x true; git diff"),
		{"export GIT_EXTERNAL_DIFF=x; git diff", Deny, RuleGitMutation, "git diff"},
		{"declare -x GIT_EXTERNAL_DIFF=x; git log", Deny, RuleGitMutation, "git log"},
		{"typeset GIT_ASKPASS+=x; git log", Deny, RuleGitMutation, "git log"},
		allowed("export OTHER=1; git diff"),
	})
}

// TestSuiteRule_DeniesTheFullSuiteAndAllowsATargetedRun: a command that is a
// suite form once its flags, their values and assignments are set aside is
// sent to af gate run.
func TestSuiteRule_DeniesTheFullSuiteAndAllowsATargetedRun(t *testing.T) {
	requireDecisions(t, defaultPolicy(), []expectation{
		{"make check", Deny, RuleSuiteCommand, "make check"},
		{"make -j4 check", Deny, RuleSuiteCommand, "make -j4 check"},
		{"go test ./...", Deny, RuleSuiteCommand, "go test ./..."},
		{"go test -count=1 ./...", Deny, RuleSuiteCommand, "go test -count=1 ./..."},
		denied("go vet ./...", RuleSuiteCommand),
		denied("make test", RuleSuiteCommand),
		allowed("go test ./internal/x -run TestY"),
		allowed("go test -run TestY ./internal/x"),
		allowed("go test -run TestY ./..."),
		allowed("go test ./internal/..."),
		allowed("make build"),
		allowed("make lint"),
		allowed("git diff"),
		allowed("echo make check"),
	})

	d := bash(defaultPolicy(), "go test -count=1 ./...")
	for _, want := range []string{"go test -count=1 ./...", "af gate run", "tree hash", "targeted"} {
		if !strings.Contains(d.Reason, want) {
			t.Errorf("reason %q does not mention %q", d.Reason, want)
		}
	}

	// A form with flags of its own, and an empty form, which matches nothing.
	p := Policy{Suite: [][]string{{"mvn", "-q", "test"}, {"./mvnw", "test"}, {}}}
	requireDecisions(t, p, []expectation{
		denied("mvn test", RuleSuiteCommand),
		denied("mvn -q test", RuleSuiteCommand),
		denied("./mvnw -B test", RuleSuiteCommand),
		allowed("mvn -q test Foo"),
	})
}

// TestAfRule_AllowsOnlyWhatABrainMayRun: lang detect, gate run and
// revert-check; the rest of af is the loop's.
func TestAfRule_AllowsOnlyWhatABrainMayRun(t *testing.T) {
	var cases []expectation
	for _, c := range []string{"af lang detect", "af gate run", "af revert-check", "af --version", "af gate run --dir .", "af lang detect --write", "/usr/local/bin/af gate run"} {
		cases = append(cases, allowed(c))
	}
	for _, c := range []string{
		"af fix land", "af run", "af triage run", "af fix run", "af triage brief", "af fix submit", "af map", "af gate compare", "af --schema", "af", "af triage",
		"af fix", "af gate", "af fix land --dry-run", "af serve", "af help", "af --help", "af landed",
	} {
		cases = append(cases, denied(c, RuleAfCommand))
	}
	requireDecisions(t, defaultPolicy(), cases)

	if d := bash(defaultPolicy(), "af fix land"); !strings.Contains(d.Reason, "loop") {
		t.Errorf("reason = %q, want it to say the command belongs to the loop", d.Reason)
	}
	if d := bash(defaultPolicy(), "af --help"); !strings.Contains(d.Reason, "af with no command it may run") {
		t.Errorf("reason = %q, want it to say af was given no command", d.Reason)
	}
}

// TestProgramRule_AllowsOnlyTheListAndAfAndGitAndTheBuiltins: with an
// allowlist, a program off it is denied, af and git by their names or the
// system's paths are not, nor are builtins that start no program; an empty
// list is no allowlist.
func TestProgramRule_AllowsOnlyTheListAndAfAndGitAndTheBuiltins(t *testing.T) {
	p := defaultPolicy()
	p.AllowPrograms = []string{"go", "make", "ls"}
	requireDecisions(t, p, []expectation{
		allowed("ls"),
		allowed("go build ./..."),
		allowed("FOO=1 env ls"),
		allowed("af gate run"),
		allowed("/usr/local/bin/af gate run"),
		allowed("git status"),
		allowed("/usr/bin/git status"),
		allowed("GIT status"),
		allowed("cd sub && make lint"),
		allowed("for f in a b; do echo $f; done"),
		allowed("true && [ -f x ] && test -d y && printf z"),
		allowed("export A=1; read x; set -e; unset A"),
		allowed(`trap 'echo bye' EXIT`),
		{"curl x", Deny, RuleProgramNotAllowed, "curl x"},
		{"ls && curl x", Deny, RuleProgramNotAllowed, "curl x"},
		denied("echo $(curl x)", RuleProgramNotAllowed),
		denied("FOO=1 env curl x", RuleProgramNotAllowed),
		denied("/usr/bin/ls", RuleProgramNotAllowed),
		denied("./git status", RuleProgramNotAllowed),
		denied("./af gate run", RuleProgramNotAllowed),
		denied("source x.sh", RuleProgramNotAllowed),
		{"bash -c 'ls'", Deny, RuleProgramNotAllowed, "bash -c 'ls'"},
		{`trap 'curl x' EXIT`, Deny, RuleProgramNotAllowed, "curl x"},
		{`eval "curl x"`, Deny, RuleProgramNotAllowed, "curl x"},
		{"env -S 'curl x'", Deny, RuleProgramNotAllowed, "env -S 'curl x'"},
	})

	p.AllowPrograms = []string{}
	requireDecisions(t, p, []expectation{allowed("curl x"), allowed("anything --at all")})
}
