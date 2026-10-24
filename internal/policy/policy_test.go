package policy

import (
	"math/rand/v2"
	"reflect"
	"strings"
	"testing"
	"time"
)

// dir is the checkout of the tests.
const dir = "/work/repo"

// defaultPolicy is a policy with the suite of a Go project that has a Makefile,
// the default protected paths and no allowlist of programs.
func defaultPolicy() Policy {
	return Policy{
		Suite:          [][]string{{"make", "check"}, {"make", "test"}, {"go", "test", "./..."}, {"go", "vet", "./..."}},
		ProtectedPaths: DefaultProtectedPaths(),
	}
}

// bash decides a Bash call of command.
func bash(p Policy, command string) Decision {
	return Decide(p, dir, "execute", map[string]any{"command": command})
}

// words lists the words of every command parse finds, in order.
func words(t *testing.T, src string) [][]string {
	t.Helper()
	cmds, err := parse(src)
	if err != nil {
		t.Fatalf("parse(%q): %v", src, err)
	}
	out := [][]string{}
	for _, c := range cmds {
		out = append(out, c.words)
	}
	return out
}

// TestParse guards 06-REQ-7.1: the reader reads POSIX quoting and finds
// every simple command a line runs, the ones in substitutions included.
func TestParse(t *testing.T) {
	cases := []struct {
		src  string
		want [][]string
	}{
		{"ls -l", [][]string{{"ls", "-l"}}},
		{`echo "a b" 'c d' e\ f`, [][]string{{"echo", "a b", "c d", "e f"}}},
		{`echo "it's" 'say "hi"'`, [][]string{{"echo", "it's", `say "hi"`}}},
		{"a && b || c; d | e & f\ng", [][]string{{"a"}, {"b"}, {"c"}, {"d"}, {"e"}, {"f"}, {"g"}}},
		{"a |& b", [][]string{{"a"}, {"b"}}},
		{"echo $(git push)", [][]string{{"echo", "$(git push)"}, {"git", "push"}}},
		{"echo `make check`", [][]string{{"echo", "`make check`"}, {"make", "check"}}},
		{`echo "x $(a) y"`, [][]string{{"echo", "x $(a) y"}, {"a"}}},
		{`echo "x ` + "`a`" + ` y"`, [][]string{{"echo", "x `a` y"}, {"a"}}},
		{`echo '$(a)' \$HOME`, [][]string{{"echo", "$(a)", "$HOME"}}},
		{"echo $(a $(b))", [][]string{{"echo", "$(a $(b))"}, {"a", "$(b)"}, {"b"}}},
		{"(a; b) && c", [][]string{{"a"}, {"b"}, {"c"}}},
		{"{ a; }", [][]string{{"{", "a"}, {"}"}}},
		{"x=1 y=2 cmd arg", [][]string{{"x=", "y=", "cmd", "arg"}}},
		{"echo ${x:-$(date)}", [][]string{{"echo", "${x:-$(date)}"}, {"date"}}},
		{"echo $((1+2)) $(( $(b) * 2 ))", [][]string{{"echo", "$((1+2))", "$(( $(b) * 2 ))"}, {"b"}}},
		{"diff <(a) <(b)", [][]string{{"diff", "<(a)", "<(b)"}, {"a"}, {"b"}}},
		{"echo a # git push", [][]string{{"echo", "a"}}},
		{"echo a#b", [][]string{{"echo", "a#b"}}},
		{"ec\\\nho hi", [][]string{{"echo", "hi"}}},
		{`echo "a > b" a\>b`, [][]string{{"echo", "a > b", "a>b"}}},
		{`echo a\`, [][]string{{"echo", `a\`}}},
		{`"git" push`, [][]string{{"git", "push"}}},
		{`g\it push`, [][]string{{"git", "push"}}},
		{`''`, [][]string{{""}}},
		{"", [][]string{}},
		{"  \n ; \n", [][]string{}},
	}
	for _, tc := range cases {
		if got := words(t, tc.src); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("parse(%q) = %q, want %q", tc.src, got, tc.want)
		}
	}
}

// TestParseRedirections guards 06-REQ-7.8: the redirections of a command, with
// their descriptors, and the heredocs, whose bodies are data.
func TestParseRedirections(t *testing.T) {
	cmds, err := parse("echo hi > out.txt 2>&1 < in 3>>log &> all 2> err >| forced")
	if err != nil {
		t.Fatal(err)
	}
	want := []redirect{{"", ">", "out.txt"}, {"2", ">&", "1"}, {"", "<", "in"}, {"3", ">>", "log"}, {"", "&>", "all"}, {"2", ">", "err"}, {"", ">|", "forced"}}
	if len(cmds) != 1 || !reflect.DeepEqual(cmds[0].words, []string{"echo", "hi"}) || !reflect.DeepEqual(cmds[0].redirects, want) {
		t.Errorf("parse = %+v, want echo hi with %+v", cmds, want)
	}

	cmds, err = parse("echo a>b")
	if err != nil || len(cmds) != 1 || !reflect.DeepEqual(cmds[0].words, []string{"echo", "a"}) || cmds[0].redirects[0].target != "b" {
		t.Errorf("parse(echo a>b) = %+v, %v", cmds, err)
	}

	for src, want := range map[string][][]string{
		"cat <<EOF\ngit push\nEOF\nls":               {{"cat"}, {"ls"}},
		"cat <<'EOF'\n$(git push)\nEOF\nls":          {{"cat"}, {"ls"}},
		"cat <<EOF\n$(git push)\nEOF\nls":            {{"cat"}, {"git", "push"}, {"ls"}},
		"cat <<-EOF\n\tgit push\n\tEOF\nls":          {{"cat"}, {"ls"}},
		"cat <<EOF | wc\nbody `a`\nEOF\nls":          {{"cat"}, {"wc"}, {"a"}, {"ls"}},
		"x=$(cat <<EOF\ninside\nEOF\n) git status":   {{"x=", "git", "status"}, {"cat"}},
		`cat <<"E O F"` + "\n$(git push)\nE O F\nls": {{"cat"}, {"ls"}},
	} {
		if got := words(t, src); !reflect.DeepEqual(got, want) {
			t.Errorf("parse(%q) = %q, want %q", src, got, want)
		}
	}
}

// TestParseSegmentText guards 06-REQ-7.1: a command's text is as written,
// trimmed, so that a denial can show the segment.
func TestParseSegmentText(t *testing.T) {
	cmds, err := parse("  echo ok  &&   FOO=1 env make check  ; echo $(go test ./...)")
	if err != nil {
		t.Fatal(err)
	}
	var texts []string
	for _, c := range cmds {
		texts = append(texts, c.text)
	}
	want := []string{"echo ok", "FOO=1 env make check", "echo $(go test ./...)", "go test ./..."}
	if !reflect.DeepEqual(texts, want) {
		t.Errorf("texts = %q, want %q", texts, want)
	}
}

// TestUnparseable_TS_06_48 verifies TS-06-48:
// an unparseable command is denied with rule unparseable.
// Verifies: 06-REQ-7.2
func TestUnparseable_TS_06_48(t *testing.T) {
	for _, src := range []string{
		`echo "abc`, `echo 'abc`, `echo $(ls`, "echo `ls", `echo ${x`, `echo <(ls`, `(ls`, `echo $(( 1`, `echo "$(ls"`, "echo a)", "case x in a) git push ;; esac", "cat <<EOF\nnot terminated, git push\n",
		strings.Repeat("$(", 200) + "ls" + strings.Repeat(")", 200),
	} {
		d := bash(defaultPolicy(), src)

		if d.Decision != Deny || d.Rule != RuleUnparseable || d.Reason == "" || d.Segment == "" {
			t.Errorf("%.40q: %+v, want a denial by unparseable with a reason and a segment", src, d)
		}
	}
	for _, src := range []string{`echo "it's"`, `echo 'a"b'`, `echo a\ b`, `echo "a" 'b'`, `echo $(echo ")")`, `echo "$(echo ")")"`} {
		if d := bash(defaultPolicy(), src); d.Rule == RuleUnparseable {
			t.Errorf("%q is unparseable: %+v", src, d)
		}
	}
}

// expectation is what a command should be decided as.
type expectation struct {
	command  string
	decision string
	rule     string
	segment  string // "" when the test does not look at it
}

// requireDecisions decides each command under p and compares.
func requireDecisions(t *testing.T, p Policy, cases []expectation) {
	t.Helper()
	for _, tc := range cases {
		d := bash(p, tc.command)
		if d.Decision != tc.decision || d.Rule != tc.rule || (tc.segment != "" && d.Segment != tc.segment) {
			t.Errorf("%q: %s by %q on %q (%s), want %s by %q on %q", tc.command, d.Decision, d.Rule, d.Segment, d.Reason, tc.decision, tc.rule, tc.segment)
		}
		if tc.decision == Deny && (d.Rule == "" || d.Segment == "" || d.Reason == "") {
			t.Errorf("%q: a denial without a rule, a segment or a reason: %+v", tc.command, d)
		}
		if tc.decision == Allow && (d.Rule != "" || d.Segment != "" || d.Reason != "") {
			t.Errorf("%q: an allowance that carries %+v", tc.command, d)
		}
	}
}

// TestSegmentsAndWrappers_TS_06_47 verifies TS-06-47:
// commands are split into segments, wrappers and assignments are skipped, and
// nested command contents are inspected.
// Verifies: 06-REQ-7.1
func TestSegmentsAndWrappers_TS_06_47(t *testing.T) {
	requireDecisions(t, defaultPolicy(), []expectation{
		{"echo ok && go test ./...", Deny, RuleSuiteCommand, "go test ./..."},
		{"FOO=1 env time timeout 5 nice nohup command exec make check", Deny, RuleSuiteCommand, "FOO=1 env time timeout 5 nice nohup command exec make check"},
		{"ls | go test ./...", Deny, RuleSuiteCommand, "go test ./..."},
		{"echo $(make check)", Deny, RuleSuiteCommand, "make check"},
		{"echo `make check`", Deny, RuleSuiteCommand, "make check"},
		{"bash -c 'go test ./...'", Deny, RuleSuiteCommand, "go test ./..."},
		{`sh -c "git push"`, Deny, RuleGitMutation, "git push"},
		{"go test ./pkg -run X; git diff", Allow, "", ""},
		// The first denial wins.
		{"git push && make check", Deny, RuleGitMutation, "git push"},
		{"make check && git push", Deny, RuleSuiteCommand, "make check"},
		{"echo ok\nmake check\ngit push", Deny, RuleSuiteCommand, "make check"},
	})
}

// TestWrappersAndKeywords guards 06-REQ-7.1: the options of a wrapper are
// skipped with it, and so are the words of compound commands, so that the
// program is found.
func TestWrappersAndKeywords(t *testing.T) {
	var cases []expectation
	for _, src := range []string{
		"env FOO=1 git push", "env -i -u HOME git push", "env -C /tmp git push", "nohup git push &", "time -p git push",
		"timeout 5 git push", "timeout -s KILL 5 git push", "timeout --signal=KILL -k 2 5 git push", "nice -n 10 git push", "nice -10 git push",
		"command git push", "command -p git push", "exec git push", "exec -a x git push", "FOO=1 BAR=2 git push", "env env env git push",
		"if true; then git push; fi", "{ git push; }", "( git push )", "for i in 1 2; do git push; done", "while :; do git push; done",
		"! git push", "true && ! git push", "time git push", "/usr/bin/git push", "git push origin main",
	} {
		cases = append(cases, expectation{src, Deny, RuleGitMutation, ""})
	}
	for _, src := range []string{"echo git push", "env", "env FOO=1", "nohup ls", "timeout 5 ls", "command -v git", "git status && ls"} {
		cases = append(cases, expectation{src, Allow, "", ""})
	}
	// An assignment on its own runs nothing, which the hands' guard refuses.
	cases = append(cases, expectation{"FOO=1", Deny, RuleHandsGuard, "FOO=1"})
	requireDecisions(t, defaultPolicy(), cases)
}

// TestQuotingDecidesWhatRuns guards 06-REQ-7.1: text in quotes is data, and
// quoting a program name does not change which program runs.
func TestQuotingDecidesWhatRuns(t *testing.T) {
	requireDecisions(t, defaultPolicy(), []expectation{
		{`echo "git push"`, Allow, "", ""},
		{`echo 'git push'`, Allow, "", ""},
		{`echo '$(git push)'`, Allow, "", ""},
		{`echo \$HOME`, Allow, "", ""},
		{`git commit -m "no \"quotes\" # here"`, Deny, RuleGitMutation, ""},
		{`"git" push`, Deny, RuleGitMutation, ""},
		{`g\it push`, Deny, RuleGitMutation, ""},
		{`'g'"i"t push`, Deny, RuleGitMutation, ""},
		{`echo "$(git push)"`, Deny, RuleGitMutation, "git push"},
		{`FOO="a b" git push`, Deny, RuleGitMutation, ""},
		{"echo ok # git push", Allow, "", ""},
		{"cat <<EOF\ngit push\nEOF", Allow, "", ""},
		{"cat <<EOF\n$(git push)\nEOF", Deny, RuleGitMutation, "git push"},
		{"cat <<'EOF'\n$(git push)\nEOF", Allow, "", ""},
		{"echo ${x:-$(git push)}", Deny, RuleGitMutation, "git push"},
		{"diff <(git push) b", Deny, RuleGitMutation, "git push"},
		{"echo $((1 + $(git push)))", Deny, RuleGitMutation, "git push"},
	})
}

// TestNestedShells guards 06-REQ-7.1: the script of sh, bash or zsh -c is
// read as commands, however deeply it is nested.
func TestNestedShells(t *testing.T) {
	requireDecisions(t, defaultPolicy(), []expectation{
		{"bash -c 'git push'", Deny, RuleGitMutation, "git push"},
		{"sh -c 'git push'", Deny, RuleGitMutation, "git push"},
		{"zsh -c 'make check'", Deny, RuleSuiteCommand, "make check"},
		{"sh -lc 'git push'", Deny, RuleGitMutation, "git push"},
		{"bash -ec 'git push'", Deny, RuleGitMutation, "git push"},
		{"bash --norc -c 'git push'", Deny, RuleGitMutation, "git push"},
		{`bash -c "bash -c 'git push'"`, Deny, RuleGitMutation, "git push"},
		{"env FOO=1 bash -c 'git push'", Deny, RuleGitMutation, "git push"},
		{"bash -c 'git push' arg0 arg1", Deny, RuleGitMutation, "git push"},
		{`eval "git push"`, Deny, RuleGitMutation, "git push"},
		{"bash -c 'echo hi'", Allow, "", ""},
		{"bash script.sh", Allow, "", ""},
		{"bash -c", Allow, "", ""},
		{"echo bash -c 'git push'", Allow, "", ""},
		{"bash -c 'echo \"unterminated'", Deny, RuleUnparseable, ""},
	})
}

// TestHandsGuardAdapter guards 06-REQ-7.3 on the real adapter: the hands'
// guard is a floor under the rules, for what it knows and they do not.
func TestHandsGuardAdapter(t *testing.T) {
	for _, command := range []string{"ls -l", "FOO=1 ls", "go test ./... -run X", "echo $(date)", "cat <<EOF\nbody\nEOF", "./run.sh --flag", "a && b || c"} {
		if reason, refused := handsGuard(command); refused {
			t.Errorf("the hands' guard refuses %q: %s", command, reason)
		}
	}
	for command, mention := range map[string]string{
		"PATH=/tmp/evil ls":          "PATH",
		"FOO=1 LD_PRELOAD=x ls":      "LD_PRELOAD",
		"HOME=/tmp ls":               "HOME",
		`echo "unterminated`:         "",
		"":                           "no program",
		"BASH_ENV=/tmp/x bash -c ls": "BASH_ENV",
	} {
		reason, refused := handsGuard(command)
		if !refused || reason == "" || !strings.Contains(reason, mention) {
			t.Errorf("handsGuard(%q) = %q, %v; want a refusal that mentions %q", command, reason, refused, mention)
		}
	}
}

// stubHands replaces the adapter for a test.
func stubHands(t *testing.T, f func(string) (string, bool)) {
	t.Helper()
	old := handsGuard
	handsGuard = f
	t.Cleanup(func() { handsGuard = old })
}

// TestHandsGuardIsADenyOnlyFirstLayer_TS_06_49 verifies TS-06-49:
// a refusal from the hands' guard is denied with rule hands_guard and its
// reason passed through, and an allow from the hands does not override later
// rules.
// Verifies: 06-REQ-7.3
func TestHandsGuardIsADenyOnlyFirstLayer_TS_06_49(t *testing.T) {
	const reason = "guard.Restricted: the reason of the hands"
	stubHands(t, func(command string) (string, bool) {
		if strings.Contains(command, "rm -rf /") {
			return reason, true
		}
		return "", false
	})

	d := bash(defaultPolicy(), "rm -rf /")
	if d.Decision != Deny || d.Rule != RuleHandsGuard || d.Reason != reason || d.Segment == "" {
		t.Errorf("rm -rf /: %+v, want a denial by hands_guard with exactly the hands' reason", d)
	}
	requireDecisions(t, defaultPolicy(), []expectation{
		{"git push", Deny, RuleGitMutation, ""},
		{"make check", Deny, RuleSuiteCommand, ""},
		{"ls", Allow, "", ""},
	})

	// Before the rules of the segments: a line the hands refuse is refused by
	// them even when a rule would have refused it.
	if d := bash(defaultPolicy(), "git push; rm -rf /"); d.Rule != RuleHandsGuard {
		t.Errorf("git push; rm -rf /: %+v, want the hands' guard, which comes first", d)
	}
	// And after our own reading of the line.
	if d := bash(defaultPolicy(), `rm -rf / "unterminated`); d.Rule != RuleUnparseable {
		t.Errorf("an unparseable line with a refusal from the hands: %+v, want unparseable first", d)
	}
}

// TestGitIsAllowlisted_TS_06_50 verifies TS-06-50:
// git is allowlisted by read-only subcommand, branch and remote only in
// listing forms, and -c is denied.
// Verifies: 06-REQ-7.4
func TestGitIsAllowlisted_TS_06_50(t *testing.T) {
	var cases []expectation
	for _, sub := range []string{"status", "log", "diff", "show", "blame", "ls-files", "ls-tree", "rev-parse", "rev-list", "grep", "describe", "cat-file", "shortlog", "merge-base", "name-rev", "for-each-ref", "show-ref"} {
		cases = append(cases, expectation{"git " + sub, Allow, "", ""}, expectation{"git " + sub + " -x --some-flag arg", Allow, "", ""})
	}
	for _, src := range []string{
		"git branch", "git branch -a", "git branch -r -v", "git branch -vv", "git branch --list", "git branch --list 'feat*'", "git branch --show-current",
		"git branch --contains abc123", "git branch --merged main", "git branch --format='%(refname)' --sort=-committerdate",
		"git remote", "git remote -v", "git remote show origin", "git remote get-url origin", "git remote get-url --push origin",
		"git --version", "git", "git -C sub status", "git --no-pager log", "git -P diff", "git --git-dir=.git status", "git --git-dir .git log",
		"git --work-tree=. diff", "git --no-optional-locks status", "git diff > out.txt", "git log | head",
	} {
		cases = append(cases, expectation{src, Allow, "", ""})
	}
	for _, src := range []string{
		"git branch -D x", "git branch -d x", "git branch -m old new", "git branch feature", "git branch feature main", "git branch -c a b", "git branch -u origin/main",
		"git branch --delete x", "git branch --set-upstream-to=origin/main", "git branch --edit-description",
		"git remote add o u", "git remote remove o", "git remote rm o", "git remote rename a b", "git remote set-url o u", "git remote prune o", "git remote update",
		"git commit -m x", "git push", "git push origin main", "git -c core.x=y status", "git -ccore.x=y status", "git --config-env=core.x=Y status",
		"git checkout main", "git reset --hard", "git add -A", "git merge x", "git rebase x", "git stash", "git tag v1", "git config user.name x", "git clean -fd",
		"git fetch", "git pull", "git switch main", "git restore f", "git apply p", "git am p", "git cherry-pick x", "git revert x", "git worktree add x", "git gc",
		"git status && git push", "git -C sub push", "git --no-pager commit",
	} {
		cases = append(cases, expectation{src, Deny, RuleGitMutation, ""})
	}
	requireDecisions(t, defaultPolicy(), cases)

	d := bash(defaultPolicy(), "git commit -m x")
	if !strings.Contains(d.Reason, "af makes every commit, branch and push") {
		t.Errorf("reason = %q, want it to say that af makes every commit, branch and push", d.Reason)
	}
	if d := bash(defaultPolicy(), "git -c core.x=y status"); !strings.Contains(d.Reason, "af makes every commit, branch and push") {
		t.Errorf("reason of -c = %q", d.Reason)
	}
}

// TestSuiteCommandsAreDenied_TS_06_51 verifies TS-06-51:
// a segment equal to a suite form with dash-tokens stripped is denied naming
// af gate run, while targeted runs are allowed.
// Verifies: 06-REQ-7.5
func TestSuiteCommandsAreDenied_TS_06_51(t *testing.T) {
	requireDecisions(t, defaultPolicy(), []expectation{
		{"make check", Deny, RuleSuiteCommand, "make check"},
		{"make -j4 check", Deny, RuleSuiteCommand, "make -j4 check"},
		{"go test ./...", Deny, RuleSuiteCommand, "go test ./..."},
		{"go test -count=1 ./...", Deny, RuleSuiteCommand, "go test -count=1 ./..."},
		{"go test -v -race ./...", Deny, RuleSuiteCommand, ""},
		{"go vet ./...", Deny, RuleSuiteCommand, ""},
		{"make test", Deny, RuleSuiteCommand, ""},
		{"/usr/bin/make check", Deny, RuleSuiteCommand, ""},
		{"go test ./internal/x -run TestY", Allow, "", ""},
		{"go test -run TestY ./internal/x", Allow, "", ""},
		{"go test -run TestY ./...", Allow, "", ""},
		{"go test ./internal/...", Allow, "", ""},
		{"make build", Allow, "", ""},
		{"make lint", Allow, "", ""},
		{"git diff", Allow, "", ""},
		{"echo make check", Allow, "", ""},
	})

	d := bash(defaultPolicy(), "go test -count=1 ./...")
	for _, want := range []string{"go test -count=1 ./...", "af gate run", "tree hash", "targeted"} {
		if !strings.Contains(d.Reason, want) {
			t.Errorf("reason %q does not mention %q", d.Reason, want)
		}
	}

	// A form with flags of its own: mvn -q test.
	p := Policy{Suite: [][]string{{"mvn", "-q", "test"}, {"./mvnw", "test"}}, ProtectedPaths: DefaultProtectedPaths()}
	requireDecisions(t, p, []expectation{
		{"mvn test", Deny, RuleSuiteCommand, ""},
		{"mvn -q test", Deny, RuleSuiteCommand, ""},
		{"./mvnw -B test", Deny, RuleSuiteCommand, ""},
		{"mvn -q test Foo", Allow, "", ""},
	})
}

// TestAfCommands_TS_06_52 verifies TS-06-52:
// only the listed af subcommands are allowed to a brain and the rest are
// denied as loop commands.
// Verifies: 06-REQ-7.6
func TestAfCommands_TS_06_52(t *testing.T) {
	var cases []expectation
	for _, src := range []string{
		"af lang detect", "af gate run", "af revert-check", "af --version",
		"af gate run --dir .", "af lang detect --write", "/usr/local/bin/af gate run",
	} {
		cases = append(cases, expectation{src, Allow, "", ""})
	}
	for _, src := range []string{
		"af fix land", "af run", "af triage run", "af fix run", "af triage brief", "af fix submit", "af map", "af gate compare", "af --schema", "af", "af triage", "af fix", "af gate", "af fix land --dry-run", "af serve", "af help", "af --help",
		"af --dir x fix land", "af landed", "/usr/local/bin/af fix land", "env af fix land", "bash -c 'af fix land'",
	} {
		cases = append(cases, expectation{src, Deny, RuleAfCommand, ""})
	}
	requireDecisions(t, defaultPolicy(), cases)

	if d := bash(defaultPolicy(), "af fix land"); !strings.Contains(d.Reason, "loop") {
		t.Errorf("reason = %q, want it to say the command belongs to the loop", d.Reason)
	}
}

// TestAllowPrograms_TS_06_53 verifies TS-06-53:
// when allow_programs is set, other programs are denied except af and git,
// and an empty list means no allowlist.
// Verifies: 06-REQ-7.7
func TestAllowPrograms_TS_06_53(t *testing.T) {
	a := defaultPolicy()
	a.AllowPrograms = []string{"go", "ls"}
	requireDecisions(t, a, []expectation{
		{"ls", Allow, "", ""},
		{"go build ./...", Allow, "", ""},
		{"af gate run", Allow, "", ""},
		{"git status", Allow, "", ""},
		{"curl x", Deny, RuleProgramNotAllowed, "curl x"},
		{"ls && curl x", Deny, RuleProgramNotAllowed, "curl x"},
		{"echo $(curl x)", Deny, RuleProgramNotAllowed, ""},
		{"FOO=1 env ls", Allow, "", ""},
		{"FOO=1 env curl x", Deny, RuleProgramNotAllowed, ""},
		{"/usr/bin/ls", Deny, RuleProgramNotAllowed, ""},
		{"./git status", Deny, RuleProgramNotAllowed, ""},
		{"bash -c 'ls'", Deny, RuleProgramNotAllowed, "bash -c 'ls'"},
	})

	b := defaultPolicy()
	b.AllowPrograms = []string{}
	requireDecisions(t, b, []expectation{{"curl x", Allow, "", ""}, {"anything --at all", Allow, "", ""}})
	requireDecisions(t, defaultPolicy(), []expectation{{"curl x", Allow, "", ""}})
}

// TestProtectedPaths_TS_06_54 verifies TS-06-54:
// redirections and write-style commands targeting protected paths are denied
// after resolution against --dir.
// Verifies: 06-REQ-7.8
func TestProtectedPaths_TS_06_54(t *testing.T) {
	var cases []expectation
	for _, src := range []string{
		"echo x > .specs/a.md", "echo x >> ./.github/workflows/ci.yml", "rm .agent-fox/gate/labels.json", "mv a .git/hooks/x", "cp a ../repo/.specs/b",
		"tee .gitlab-ci.yml", "touch .specs/n", "mkdir .specs/d", "truncate -s0 .git/x", "ln -s a .specs/l", "chmod +x .git/x", "sed -i s/a/b/ .specs/x.md",
		// More of the same.
		"echo x 2> .git/log", "echo x &> .specs/o", "echo x >| .specs/o", "echo x 1>> .specs/o", "echo x > /work/repo/.specs/a.md", "echo x > .specs/../.specs/a.md",
		"echo x | tee -a .git/config", "tee .git/a .specs/b", "rm -rf .git", "rm -rf .specs/", "rm -f src/a.go .specs/b", "mv .specs/a /tmp/b", "mv a b .git/",
		"cp -r a .specs/", "cp -t .specs/ a b", "cp --target-directory=.specs a", "ln a .git/hook", "ln -sf a b .specs/dir", "chmod 600 .git/config", "chmod -R u+w .specs",
		"sed -i.bak s/a/b/ .specs/x", "sed -ni p .specs/x", "sed -e s/a/b/ -i .specs/x", "sed --in-place s/a/b/ .specs/x", "truncate --size=0 .specs/x", "mkdir -p .specs/a/b",
		"touch -t 200001010000 .git/x", "git diff > .specs/out", "echo $(echo x > .specs/a)", "bash -c 'echo x > .specs/a'", "cd src && echo x > ../.specs/a",
		"echo x > .gitlab-ci.yml", "echo x > .git/HEAD", "rm .agent-fox/gate/*/result.json",
	} {
		cases = append(cases, expectation{src, Deny, RuleProtectedPath, ""})
	}
	for _, src := range []string{
		"cat .specs/x.md", "echo x > src/a.go", "sed s/a/b/ .specs/x.md", "sed -n p .specs/x.md", "echo x > .specs/../src/a.go", "cat < .specs/x", "ls .specs",
		"cp .specs/a /tmp/b", "cp .specs/a .specs/b src/", "rm -rf src", "echo x > /tmp/out", "echo x > /work/other/.specs/a", "touch ../repo2/.specs/x",
		"echo x > .specsx/a", "echo x > a.specs", "echo x > .gitignore", "echo x > .github/CODEOWNERS", "echo x > .gitlab-ci.yml.bak", "mkdir src/.specs", "touch .github",
		"grep x .git/config", "git status > /dev/null", "echo x 2>&1", "echo x >&2", "tee", "rm", "echo '> .specs/a'", `echo "rm .specs/a"`, "ls > ./out.txt",
	} {
		cases = append(cases, expectation{src, Allow, "", ""})
	}
	requireDecisions(t, defaultPolicy(), cases)

	// Rules before this one win.
	requireDecisions(t, defaultPolicy(), []expectation{
		{"git push > .specs/x", Deny, RuleGitMutation, ""},
		{"make check > .specs/x", Deny, RuleSuiteCommand, ""},
	})

	// Other protected paths, set by the policy.
	p := defaultPolicy()
	p.ProtectedPaths = []string{"docs/frozen/", "LICENSE"}
	requireDecisions(t, p, []expectation{
		{"echo x > docs/frozen/a", Deny, RuleProtectedPath, ""},
		{"rm LICENSE", Deny, RuleProtectedPath, ""},
		{"echo x > .specs/a", Allow, "", ""},
		{"echo x > docs/other", Allow, "", ""},
	})
	p.ProtectedPaths = nil
	requireDecisions(t, p, []expectation{{"echo x > .specs/a", Allow, "", ""}})
}

// TestDecisionShape guards 06-REQ-7.1: a decision always has a rule, a
// segment and a reason when it is a denial, and none when it is not.
func TestDecisionShape(t *testing.T) {
	p := defaultPolicy()
	p.AllowPrograms = []string{"ls", "echo", "go", "cat"}
	for _, src := range []string{"ls", "git push", "make check", `echo "x`, "af fix land", "curl x", "echo x > .specs/a", "echo $(git push)", "ls; ls; git push; ls"} {
		d := bash(p, src)
		switch d.Decision {
		case Allow:
			if d.Rule != "" || d.Segment != "" || d.Reason != "" {
				t.Errorf("%q: an allowance with %+v", src, d)
			}
		case Deny:
			if d.Rule == "" || d.Segment == "" || d.Reason == "" {
				t.Errorf("%q: a denial with %+v", src, d)
			}
		default:
			t.Errorf("%q: decision %q", src, d.Decision)
		}
	}
}

// TestShellCallWithoutACommand guards 06-REQ-7.2: a shell call that carries no
// command cannot be read, and is denied like one that cannot be parsed.
func TestShellCallWithoutACommand(t *testing.T) {
	for _, input := range []map[string]any{nil, {}, {"command": 7}, {"command": nil}, {"cmd": "ls"}} {
		d := Decide(defaultPolicy(), dir, "execute", input)
		if d.Decision != Deny || d.Rule != RuleUnparseable || d.Segment == "" || d.Reason == "" {
			t.Errorf("execute with %v: %+v, want a denial by unparseable", input, d)
		}
	}
	for _, tool := range []string{"Bash", "read_file", "search_files", "mcp__x__y"} {
		if d := Decide(defaultPolicy(), dir, tool, map[string]any{"command": "git push", "file_path": ".specs/x"}); d.Decision != Allow {
			t.Errorf("%s: %+v, want an allowance: only shell and write tools are judged", tool, d)
		}
	}
}

// TestDeepNestingIsDeniedNotCrashed guards 06-REQ-10.3: a command nested to
// any depth is denied and does not exhaust the stack.
func TestDeepNestingIsDeniedNotCrashed(t *testing.T) {
	for _, src := range []string{
		strings.Repeat("$(", 5000) + "ls" + strings.Repeat(")", 5000),
		strings.Repeat("( ", 5000) + "ls" + strings.Repeat(" )", 5000),
		strings.Repeat("bash -c '", 1) + strings.Repeat("`", 1),
		strings.Repeat("${x:-", 5000),
	} {
		start := time.Now()
		d := bash(defaultPolicy(), src)
		if d.Decision != Deny || d.Rule != RuleUnparseable {
			t.Errorf("%.30q...: %+v, want a denial by unparseable", src, d)
		}
		if time.Since(start) > 5*time.Second {
			t.Errorf("%.30q... took %s", src, time.Since(start))
		}
	}
}

// TestDeepParensAreArithmetic guards 06-REQ-8.1: parentheses that the shell
// reads as arithmetic run nothing and are read in good time, and a line of
// them is denied all the same, as it runs no program.
func TestDeepParensAreArithmetic(t *testing.T) {
	src := strings.Repeat("(", 5000) + "ls" + strings.Repeat(")", 5000)
	start := time.Now()

	d := bash(defaultPolicy(), src)

	if d.Decision != Deny || d.Rule == "" {
		t.Errorf("%.30q...: %+v, want a denial", src, d)
	}
	if time.Since(start) > 5*time.Second {
		t.Errorf("%.30q... took %s", src, time.Since(start))
	}
}

// TestLargeCommandIsFast guards 06-REQ-8.1: a command of the size the hands'
// guard can read in good time is judged in good time.
func TestLargeCommandIsFast(t *testing.T) {
	src := strings.Repeat("echo 'a b' \"c d\" $(ls) | cat > /tmp/x; ", 1_500)
	if len(src) > maxCommandBytes {
		t.Fatalf("the test command is %d bytes, over the limit", len(src))
	}
	start := time.Now()

	d := bash(defaultPolicy(), src)

	if d.Decision != Allow {
		t.Errorf("decision = %+v", d)
	}
	if time.Since(start) > 3*time.Second {
		t.Errorf("a command of %d bytes took %s", len(src), time.Since(start))
	}
}

// TestOversizedCommandIsDenied guards 06-REQ-8.1: a command too long to be read
// in good time is denied and not read, whatever its size.
func TestOversizedCommandIsDenied(t *testing.T) {
	for _, n := range []int{maxCommandBytes + 1, 1 << 20} {
		unit := "echo 'a b' \"c d\" $(ls) | cat > /tmp/x; "
		src := strings.Repeat(unit, n/len(unit)+1)
		start := time.Now()

		d := bash(defaultPolicy(), src)

		if d.Decision != Deny || d.Rule != RuleUnparseable || d.Segment == "" || len(d.Segment) > 300 || !strings.Contains(d.Reason, "bytes") {
			t.Errorf("%d bytes: %+v", len(src), d)
		}
		if time.Since(start) > time.Second {
			t.Errorf("%d bytes took %s to refuse", len(src), time.Since(start))
		}
	}
}

// TestDecideNeverPanics guards 06-REQ-10.3: whatever the text, Decide
// returns a decision of the right shape.
func TestDecideNeverPanics(t *testing.T) {
	alphabet := []string{"git", " ", "push", "'", `"`, "`", "$(", ")", "(", "{", "}", "${", ";", "&", "|", "<", ">", "<<", "EOF", "\n", "\\", "-c", "bash", "sh", "=", "FOO=1", "#", "make", "check", ".specs/", "af", "fix", "land", "2>", "&>", "<(", "$((", "))"}
	rng := rand.New(rand.NewPCG(6, 7))
	for i := range 4000 {
		var b strings.Builder
		for range 1 + rng.IntN(14) {
			b.WriteString(alphabet[rng.IntN(len(alphabet))])
		}
		src := b.String()
		d := func() (d Decision) {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("#%d: %q panics: %v", i, src, r)
				}
			}()
			return bash(defaultPolicy(), src)
		}()
		if d.Decision != Allow && d.Decision != Deny {
			t.Fatalf("#%d: %q: decision %q", i, src, d.Decision)
		}
		if d.Decision == Deny && (d.Rule == "" || d.Segment == "" || d.Reason == "") {
			t.Fatalf("#%d: %q: a denial with %+v", i, src, d)
		}
	}
}

// FuzzDecide is the same property for the fuzzer.
func FuzzDecide(f *testing.F) {
	for _, seed := range []string{"git push", "echo $(make check)", `bash -c 'git "push'`, "cat <<EOF\n$(ls)\nEOF", "a;;b", "echo ${x:-$(y)}"} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, src string) {
		d := bash(defaultPolicy(), src)
		if d.Decision != Allow && d.Decision != Deny {
			t.Fatalf("%q: decision %q", src, d.Decision)
		}
		if d.Decision == Deny && (d.Rule == "" || d.Segment == "" || d.Reason == "") {
			t.Fatalf("%q: a denial with %+v", src, d)
		}
	})
}

// TestIsProtectedIsTheRuleAWriteIsJudgedBy guards the question a measuring asks of the
// paths of a change: which of them are protected, by the rules the hook applies to writes.
func TestIsProtectedIsTheRuleAWriteIsJudgedBy(t *testing.T) {
	protected := DefaultProtectedPaths()
	for rel, want := range map[string]bool{
		".specs/a.md": true, ".specs/deep/er/b.md": true, ".specs": true, ".agent-fox/gate.json": true, ".git/config": true,
		".github/workflows/ci.yml": true, ".gitlab-ci.yml": true, ".SPECS/a.md": true, ".Github/Workflows/ci.yml": true,
		"./.specs/a.md": true, "src/../.specs/a.md": true,
		".github/CODEOWNERS": false, ".github": false, "src/.specs/a.md": false, ".gitlab-ci.yml.bak": false, "specs/a.md": false, "README.md": false, ".gitignore": false,
	} {
		if got := IsProtected(protected, rel); got != want {
			t.Errorf("IsProtected(%q) = %v, want %v", rel, got, want)
		}
	}
	if IsProtected(nil, ".specs/a.md") || IsProtected([]string{"docs/"}, ".specs/a.md") || !IsProtected([]string{"docs/"}, "docs/a.md") || !IsProtected([]string{"go.lock"}, "go.lock") {
		t.Error("IsProtected does not follow the list it is given")
	}
}
