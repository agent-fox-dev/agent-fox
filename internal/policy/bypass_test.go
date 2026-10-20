package policy

import (
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strings"
	"testing"
)

// TestParseReadsWhatTheShellReads guards 06-REQ-7.1: the forms of quoting and
// expansion that change which program a word names are read as the shell
// reads them, and the constructs that hide a newline from the line are not
// taken for heredocs.
func TestParseReadsWhatTheShellReads(t *testing.T) {
	cases := []struct {
		src  string
		want [][]string
	}{
		// ANSI-C and locale quoting.
		{`$'git' push`, [][]string{{"git", "push"}}},
		{`$'\x67it' push`, [][]string{{"git", "push"}}},
		{`$'\147it' push`, [][]string{{"git", "push"}}},
		{`$'git' push`, [][]string{{"git", "push"}}},
		{`$"git" push`, [][]string{{"git", "push"}}},
		{`echo $'it\'s' $'a\tb' $'x\\y'`, [][]string{{"echo", "it's", "a\tb", `x\y`}}},
		{`echo $'a\0b'`, [][]string{{"echo", "a"}}},
		{`echo $'\''; git push; echo x # '`, [][]string{{"echo", "'"}, {"git", "push"}, {"echo", "x"}}},
		{`echo "$'a'"`, [][]string{{"echo", "$'a'"}}},
		// Brace expansion.
		{"{git,push}", [][]string{{"git", "push"}}},
		{"{make,check}", [][]string{{"make", "check"}}},
		{"echo {a,b}{1,2}", [][]string{{"echo", "a1", "a2", "b1", "b2"}}},
		{"echo x{,y} {a,{b,c}}", [][]string{{"echo", "x", "xy", "a", "b", "c"}}},
		{"echo {1..3} {c..a} {1..10..4} {01..03}", [][]string{{"echo", "1", "2", "3", "c", "b", "a", "1", "5", "9", "01", "02", "03"}}},
		{`echo "{a,b}" '{a,b}' \{a,b\} {a} {} ${x,y} a{b {a,b`, [][]string{{"echo", "{a,b}", "{a,b}", "{a,b}", "{a}", "{}", "${x,y}", "a{b", "{a,b"}}},
		{"echo a{b{c,d}", [][]string{{"echo", "a{bc", "a{bd"}}},
		{"echo $(echo {a,b}) {x,y}", [][]string{{"echo", "$(echo {a,b})", "x", "y"}, {"echo", "a", "b"}}},
		// The word a variable that splits at once names.
		{"git${IFS}push origin", [][]string{{"git", "push", "origin"}}},
		{"git$IFS'push'", [][]string{{"git", "push"}}},
		{"git$IFSx push", [][]string{{"git$IFSx", "push"}}},
		{`"git${IFS}push" x`, [][]string{{"git${IFS}push", "x"}}},
		{"echo $IFS a", [][]string{{"echo", "a"}}},
		// Backticks unescape their body before it is read.
		{"echo `echo \\`git push\\``", [][]string{{"echo", "`echo \\`git push\\``"}, {"echo", "`git push`"}, {"git", "push"}}},
		{"echo `echo a\\\\b \\$HOME`", [][]string{{"echo", "`echo a\\\\b \\$HOME`"}, {"echo", "ab", "$HOME"}}},
		// Arithmetic in the old and the new form is not a heredoc.
		{"(( 1 << 2 ))\ngit push", [][]string{{"git", "push"}}},
		{"echo $[1<<2]\ngit push", [][]string{{"echo", "$[1<<2]"}, {"git", "push"}}},
		{"echo $((1<<2))\ngit push", [][]string{{"echo", "$((1<<2))"}, {"git", "push"}}},
		{"(( x = $(git push) ))", [][]string{{"git", "push"}}},
		{"for ((i=0;i<1<<3;i++)); do git push; done", [][]string{{"for"}, {"do", "git", "push"}, {"done"}}},
		{"((cd x) && ls)", [][]string{{"cd", "x"}, {"ls"}}},
		{"((git push); (git status))", [][]string{{"git", "push"}, {"git", "status"}}},
		{"(( (git push) ); git status)", [][]string{{"git", "push"}, {"git", "status"}}},
		{"echo $((git push) )", [][]string{{"echo", "$((git push) )"}, {"git", "push"}}},
		{"echo $(( (1+2)*3 )) $((echo hi))", [][]string{{"echo", "$(( (1+2)*3 ))", "$((echo hi))"}}},
		{"echo $[ $(git push) + 1 ]", [][]string{{"echo", "$[ $(git push) + 1 ]"}, {"git", "push"}}},
	}
	for _, tc := range cases {
		if got := words(t, tc.src); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("parse(%q) = %q, want %q", tc.src, got, tc.want)
		}
	}
}

// TestParseRefusesWhatItCannotBound guards 06-REQ-7.2: an expansion that
// would make more words than can be judged is not read.
func TestParseRefusesWhatItCannotBound(t *testing.T) {
	for _, src := range []string{
		"echo {1..100000}", "echo " + strings.Repeat("{a,b}", 20), `echo $'abc`, "echo $[1+",
	} {
		if cmds, err := parse(src); err == nil {
			t.Errorf("parse(%.40q) = %v, want an error", src, cmds)
		}
	}
}

// bypass is a line the policy must deny, whatever the rule says.
type bypass struct {
	command string
	rule    string
}

// TestEveryWayToNameAProgramIsJudged guards 06-REQ-7.1, 7.4 and 7.5: the
// program of a command is found however it is spelled, and what a shell or
// a builtin is told to read is read.
func TestEveryWayToNameAProgramIsJudged(t *testing.T) {
	var cases []expectation
	for _, c := range []bypass{
		// Spelling.
		{`$'git' push`, RuleGitMutation}, {`$'\x67it' push`, RuleGitMutation}, {`$"git" push`, RuleGitMutation},
		{`{git,push}`, RuleGitMutation}, {`git${IFS}push`, RuleGitMutation}, {`Git push`, RuleGitMutation}, {`GIT push`, RuleGitMutation},
		{`{make,check}`, RuleSuiteCommand}, {`{go,test,./...}`, RuleSuiteCommand}, {`MAKE check`, RuleSuiteCommand}, {`Make check`, RuleSuiteCommand},
		{"echo `echo \\`git push\\``", RuleGitMutation}, {`echo $'\''; git push; echo x # '`, RuleGitMutation},
		{"(( 1 << 2 ))\ngit push", RuleGitMutation}, {"echo $[1<<2]\ngit push", RuleHandsGuard},
		{"((git push); (git status))", RuleGitMutation}, {"echo $((git push) )", RuleGitMutation},
		// env -S splits its string into the command.
		{`env -S 'git push'`, RuleGitMutation}, {`env -iS 'git push'`, RuleGitMutation}, {`env --split-string='git push'`, RuleGitMutation},
		{`env --split-string 'git push'`, RuleGitMutation}, {`env -S'git push'`, RuleGitMutation}, {`env -S "make check"`, RuleSuiteCommand},
		{`env -u X -S 'git push' FOO=1`, RuleGitMutation}, {`builtin exec git push`, RuleGitMutation}, {`command builtin exec git push`, RuleGitMutation},
		// Scripts a shell or a builtin is given.
		{`bash --rcfile /dev/null -c 'git push'`, RuleGitMutation}, {`bash --init-file /dev/null -c 'git push'`, RuleGitMutation},
		{`bash -c -x 'git push'`, RuleGitMutation}, {`bash -O extglob -c 'git push'`, RuleGitMutation}, {`sh -c -- 'git push'`, RuleGitMutation},
		{`trap 'git push' EXIT`, RuleGitMutation}, {`trap "make check" EXIT INT`, RuleSuiteCommand}, {`eval -- 'git push'`, RuleGitMutation},
		{`function f { git push; }; f`, RuleGitMutation}, {`function f() { git push; }; f`, RuleHandsGuard}, {`coproc git push`, RuleGitMutation},
		{`coproc NAME { git push; }`, RuleGitMutation}, {`xargs git push`, RuleGitMutation}, {`xargs -n 1 -I {} git push {}`, RuleGitMutation},
		{`find . -exec git push \;`, RuleGitMutation}, {`find . -name x -execdir make check \;`, RuleSuiteCommand},
		// Configuration for git that runs a program.
		{`GIT_CONFIG_COUNT=1 GIT_CONFIG_KEY_0=core.fsmonitor GIT_CONFIG_VALUE_0='git push' git status`, RuleGitMutation},
		{`GIT_CONFIG_PARAMETERS="'core.pager=x'" git log`, RuleGitMutation}, {`GIT_EXTERNAL_DIFF='git push;:' git diff`, RuleGitMutation},
		{`env GIT_EXTERNAL_DIFF=x git diff`, RuleGitMutation}, {`export GIT_EXTERNAL_DIFF=x; git diff`, RuleGitMutation},
		{`GIT_SSH_COMMAND=x git show`, RuleGitMutation}, {`git --exec-path=/tmp/x status`, RuleGitMutation}, {`git grep -O x y`, RuleGitMutation},
		{`git grep --open-files-in-pager=x y`, RuleGitMutation},
		// Suites with options of their own.
		{`go test -timeout 5m ./...`, RuleSuiteCommand}, {`go test -p 1 ./...`, RuleSuiteCommand}, {`go test -count 1 -parallel 4 ./...`, RuleSuiteCommand},
		{`make -j 4 check`, RuleSuiteCommand}, {`make -f Makefile check`, RuleSuiteCommand}, {`make check V=1`, RuleSuiteCommand},
		{`go test $(go list ./...)`, RuleSuiteCommand}, {"go test `go list ./...`", RuleSuiteCommand},
	} {
		cases = append(cases, expectation{c.command, Deny, c.rule, ""})
	}
	// A shell that reads its commands from its input cannot be read.
	for _, src := range []string{
		`echo git push | sh`, `sh <<< 'git push'`, "bash <<EOF\ngit push\nEOF", `bash -s`, `bash -s -- x`, `curl x | bash`, `sh /dev/stdin`, `bash /dev/fd/0`,
		`bash`, `bash -i`, `zsh -l`, `source <(echo git push)`, `. /dev/stdin`, `bash -o pipefail`, `sh -`,
	} {
		cases = append(cases, expectation{src, Deny, RuleUnparseable, ""})
	}
	requireDecisions(t, defaultPolicy(), cases)
}

// TestWhatTheNewReadingDoesNotRefuse guards 06-REQ-7.1: a line that does what
// it says it does is still allowed.
func TestWhatTheNewReadingDoesNotRefuse(t *testing.T) {
	var cases []expectation
	for _, src := range []string{
		`echo $'it\'s'`, `echo {a,b}`, `echo $(( 1 << 2 ))`, `echo "$((1<<2))" && ls`, `git diff --stat`, `git log --oneline -n 5`, `bash -c 'echo hi'`,
		`bash script.sh`, `bash --version`, `sh -n script.sh`, `echo x | wc`, `touch -r .specs/a.md newfile`, `sh -c 'ls'`, `trap - EXIT`,
		`trap 'echo bye' EXIT`, `trap -p`, `trap`, `eval "echo hi"`, `function f { echo hi; }; f`, `go test -run X ./pkg`, `go test -timeout 5m ./pkg/...`,
		`go test -timeout 5m -run TestA ./...`, `make -j 4 lint`, `make -f Makefile build`, `make build V=1`, `go test $(go list ./pkg/...)`,
		`env -S 'ls -l'`, `env -u X ls`, `xargs echo`, `find . -name x -exec echo {} \;`, `find . -name x`, `find .specs -name x -print`,
		`GIT_DIR=.git git status`, `GIT_PAGER=cat git log`, `git grep -n x`, `git diff --output=out.txt`, `echo "{git,push}"`,
		`echo ${IFS}`, `echo "git${IFS}push"`,
		"echo `echo \\`date\\``", `echo $'\x67it push'`,
	} {
		cases = append(cases, expectation{src, Allow, "", ""})
	}
	requireDecisions(t, defaultPolicy(), cases)
}

// TestAllowProgramsKnowsWhatItIs guards 06-REQ-7.7: af and git are always
// allowed, by their path too, and so are the builtins that start no program.
func TestAllowProgramsKnowsWhatItIs(t *testing.T) {
	p := defaultPolicy()
	p.AllowPrograms = []string{"make", "go"}
	requireDecisions(t, p, []expectation{
		{"/usr/bin/git status", Allow, "", ""},
		{"/usr/local/bin/af gate run", Allow, "", ""},
		{"GIT status", Allow, "", ""},
		{"cd sub && make lint", Allow, "", ""},
		{"for f in a b; do echo $f; done", Allow, "", ""},
		{"true && [ -f x ] && test -d y && printf z", Allow, "", ""},
		{"export A=1; read x; set -e; unset A", Allow, "", ""},
		{`trap 'echo bye' EXIT`, Allow, "", ""},
		{`trap 'ls' EXIT`, Deny, RuleProgramNotAllowed, "ls"},
		{`eval "ls"`, Deny, RuleProgramNotAllowed, "ls"},
		{"./git status", Deny, RuleProgramNotAllowed, ""},
		{"./af gate run", Deny, RuleProgramNotAllowed, ""},
		{"ls", Deny, RuleProgramNotAllowed, "ls"},
		{"source x.sh", Deny, RuleProgramNotAllowed, ""},
		{"env -S 'ls -l'", Deny, RuleProgramNotAllowed, "env -S 'ls -l'"},
	})
}

// TestProtectedPathsInOtherSpellings guards 06-REQ-7.8: a path is protected
// however it is written.
func TestProtectedPathsInOtherSpellings(t *testing.T) {
	t.Setenv("HOME", "/work")
	var cases []expectation
	for _, src := range []string{
		// Case.
		"echo x > .SPECS/a.md", "rm -rf .Git", "touch .Agent-Fox/x", "echo x > .GitHub/Workflows/ci.yml", "echo x > .GitLab-CI.yml",
		// Globs.
		"rm -rf .spe?s", "rm -rf .sp*", "echo x > .spe[c]s/a.md", "rm -rf .*", "rm -rf .git*", "rm -rf .specs/*", "echo x > .s*/a.md",
		"rm -rf .github/work*", "mv .sp* /tmp",
		// Variables and ~.
		`echo x > "$PWD/.specs/a.md"`, `echo x > ${PWD}/.specs/a.md`, "echo x > ~/repo/.specs/a", `echo x > "$HOME/repo/.specs/a"`, "echo x > ~/repo/.git/hooks/x",
		// Writers that are not on the first list.
		"git diff --output=.specs/out.md", "git log --output .specs/out.md", "git show --output=.git/x", "find .specs -delete", "find . -delete", "find . -maxdepth 1 -depth -delete", "find .specs -name x -delete",
		"find .specs -name x -fprint .specs/list", "dd of=.specs/a.md", "dd if=x of=.git/hooks/pre-commit", "install a .specs/b", "install -t .specs a b",
		"rsync -a a .specs/", "perl -pi -e s/a/b/ .specs/x", "perl -i.bak -pe s/a/b/ .specs/x", "patch .specs/x", "go build -o .github/workflows/ci.yml",
		"go test -coverprofile=.specs/c.out ./pkg", "go test -coverprofile .specs/c.out ./pkg", "af map --output .specs/a.md", "af map --output=.specs/a.md",
		"af guard check --emit-events .specs/a.md", "chown me .specs/a", "chgrp me .specs/a", "rmdir .specs", "unlink .specs/a", "shred .specs/a", "mkfifo .specs/f",
		"curl -o .specs/a x", "sort -o .specs/a b", "ln -s .specs link", "ln .specs/a b",
	} {
		cases = append(cases, expectation{src, Deny, RuleProtectedPath, ""})
	}
	for _, src := range []string{
		"touch -r .specs/a.md newfile", "touch -d yesterday src/a", "git diff --output=out.txt", "find src -name x -delete", "find . -name '*.tmp' -delete", "find . -type f -delete -print", "find . -name x", "dd if=.specs/a of=out",
		"rm -rf *", "rm -rf ./*", "rm -rf src/*", "rm -rf src/.git*", "echo x > src/.spe?s", "echo x > src/*.go", "echo $PWD", `echo x > "$PWD/src/a"`, "echo x > ~/other/a", "echo x > ~root/a",
		"install a src/b", "rsync -a a src/", "perl -ne print .specs/x", "go build -o bin/af ./cmd/af", "af map --output out.json", "sort -o out.txt a",
		"ln -s src link", "chown me src/a", "rmdir src",
	} {
		cases = append(cases, expectation{src, Allow, "", ""})
	}
	requireDecisions(t, defaultPolicy(), cases)

	// Writes by the tools that write files.
	for _, file := range []string{".SPECS/x.md", ".specs/../.specs/x.md", "/work/repo/.Git/x", "./.specs/./x.md"} {
		d := Decide(defaultPolicy(), dir, "Write", map[string]any{"file_path": file})
		if d.Decision != Deny || d.Rule != RuleProtectedPath {
			t.Errorf("Write %s: %+v, want a denial by protected_path", file, d)
		}
	}
}

// TestProtectedPathsBehindLinks guards 06-REQ-7.8 and 8.2: a path that leads to
// a protected one by a link, or by another spelling of the checkout, is
// protected.
func TestProtectedPathsBehindLinks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("links need privileges there")
	}
	real, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	checkout := filepath.Join(real, "repo")
	for _, d := range []string{".specs", "src"} {
		if err := os.MkdirAll(filepath.Join(checkout, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	alias := filepath.Join(real, "alias")
	for link, target := range map[string]string{filepath.Join(checkout, "link"): ".specs", filepath.Join(checkout, "src", "up"): "../.specs", alias: checkout} {
		if err := os.Symlink(target, link); err != nil {
			t.Fatal(err)
		}
	}
	p := defaultPolicy()

	for _, tc := range []struct {
		dir, command string
		want         string
	}{
		{checkout, "echo x > link/x.md", Deny},
		{checkout, "rm link/x.md", Deny},
		{checkout, "rm -rf link", Deny},
		{checkout, "echo x > src/up/x.md", Deny},
		{checkout, "cd src && echo x > up/x.md", Deny},
		{checkout, "echo x > " + filepath.Join(checkout, ".specs", "x.md"), Deny},
		{alias, "echo x > " + filepath.Join(checkout, ".specs", "x.md"), Deny},
		{alias, "echo x > .specs/x.md", Deny},
		{checkout, "echo x > " + filepath.Join(alias, ".specs", "x.md"), Deny},
		{checkout, "echo x > src/a.go", Allow},
		{alias, "echo x > src/a.go", Allow},
		{alias, "echo x > " + filepath.Join(checkout, "src", "a.go"), Allow},
	} {
		d := Decide(p, tc.dir, "Bash", map[string]any{"command": tc.command})
		if d.Decision != tc.want || tc.want == Deny && d.Rule != RuleProtectedPath {
			t.Errorf("in %s: %s: %+v, want %s", tc.dir, tc.command, d, tc.want)
		}
	}
	for _, tc := range []struct {
		dir, file string
		want      string
	}{
		{checkout, "link/x.md", Deny},
		{checkout, filepath.Join(checkout, "link", "x.md"), Deny},
		{alias, filepath.Join(checkout, ".specs", "x.md"), Deny},
		{checkout, filepath.Join(alias, ".specs", "x.md"), Deny},
		{checkout, "src/up/x.md", Deny},
		{checkout, "src/a.go", Allow},
		{alias, filepath.Join(checkout, "src", "a.go"), Allow},
	} {
		d := Decide(p, tc.dir, "Write", map[string]any{"file_path": tc.file})
		if d.Decision != tc.want || tc.want == Deny && d.Rule != RuleProtectedPath {
			t.Errorf("Write in %s: %s: %+v, want %s", tc.dir, tc.file, d, tc.want)
		}
	}
}

// TestProtectedPathsAreMatchedCleaned guards 06-REQ-8.1: an entry of
// protected_paths protects what it names, however it is spelled.
func TestProtectedPathsAreMatchedCleaned(t *testing.T) {
	for _, entry := range []string{"docs", "docs/", "./docs/", "docs//", "docs/.", "a/../docs", "./docs", "DOCS/"} {
		p := Policy{ProtectedPaths: []string{entry}}
		requireDecisions(t, p, []expectation{
			{"echo x > docs/a.md", Deny, RuleProtectedPath, ""},
			{"rm -rf docs", Deny, RuleProtectedPath, ""},
			{"rm -rf *", Deny, RuleProtectedPath, ""},
			{"rm -rf d*", Deny, RuleProtectedPath, ""},
			{"echo x > src/a.md", Allow, "", ""},
			{"echo x > docsx/a.md", Allow, "", ""},
		})
	}
}

// TestGitConfigFromTheEnvironment guards 06-REQ-7.4: configuration that makes
// git run a program is refused where it is set for git, and where it is
// exported for the commands after it.
func TestGitConfigFromTheEnvironment(t *testing.T) {
	// The assignment in front of one command is not the environment of the
	// next one, unless it is exported.
	requireDecisions(t, defaultPolicy(), []expectation{
		{"GIT_EXTERNAL_DIFF=x true; git diff", Allow, "", ""},
		{"export GIT_EXTERNAL_DIFF=x; git diff", Deny, RuleGitMutation, "git diff"},
		{"declare -x GIT_EXTERNAL_DIFF=x; git log", Deny, RuleGitMutation, "git log"},
		{"export OTHER=1; git diff", Allow, "", ""},
	})
}

// TestTheLineIsReadByTheShellToo is the differential test: a line that makes a
// real bash run what the policy is there to stop must be denied. The programs
// are stubs that write what they were asked to do, and every line of the table
// has to make a bash run one of the three forms, or it proves nothing.
func TestTheLineIsReadByTheShellToo(t *testing.T) {
	bashPath, err := exec.LookPath("bash")
	if err != nil || runtime.GOOS == "windows" {
		t.Skip("there is no bash to compare with")
	}
	bin := t.TempDir()
	logPath := filepath.Join(t.TempDir(), "log")
	for _, name := range []string{"git", "make", "go", "af"} {
		stub := "#!/bin/sh\necho \"" + name + " $*\" >> \"$AF_STUB_LOG\"\n"
		if err := os.WriteFile(filepath.Join(bin, name), []byte(stub), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// attacks lists what a bash made the stubs do that the policy forbids: a
	// push, the suite.
	attacks := func(line string) []string {
		t.Helper()
		_ = os.Remove(logPath)
		cmd := exec.Command(bashPath, "-c", line)
		cmd.Dir = t.TempDir()
		cmd.Env = []string{"PATH=" + bin + ":/usr/bin:/bin", "HOME=" + cmd.Dir, "AF_STUB_LOG=" + logPath}
		_ = cmd.Run() // a line may fail; what matters is what ran
		data, _ := os.ReadFile(logPath)
		var out []string
		for _, entry := range strings.Split(strings.TrimSpace(string(data)), "\n") {
			f := strings.Fields(entry)
			switch {
			case len(f) > 1 && f[0] == "git" && f[1] == "push",
				len(f) > 1 && f[0] == "make" && slices.Contains(f, "check"),
				len(f) > 2 && f[0] == "go" && f[1] == "test" && slices.Contains(f, "./..."):
				out = append(out, entry)
			}
		}
		return out
	}

	for _, line := range []string{
		`$'git' push`, `$'\x67it' push`, `$"git" push`, `{git,push}`, `{make,check}`, `{go,test,./...}`, `git${IFS}push`,
		`env -S 'git push'`, `env -u X -S 'git push'`,
		`echo git push | sh`, `sh <<< 'git push'`, "bash <<EOF\ngit push\nEOF",
		`bash --rcfile /dev/null -c 'git push'`, `bash --init-file /dev/null -c 'git push'`, `bash -c -x 'git push'`,
		`trap 'git push' EXIT`, `function f { git push; }; f`, `builtin exec git push`, `command git push`,
		"echo `echo \\`git push\\``", `echo $'\''; git push; echo x # '`,
		"(( 1 << 2 ))\ngit push", "echo $[1<<2]\ngit push",
		`echo x | xargs git push`, `find . -maxdepth 0 -exec git push \;`,
		`eval 'git push'`, `eval -- 'git push'`, `bash -c "bash -c 'git push'"`, `for ((i=0;i<1;i++)); do git push; done`,
		`go test -timeout 5m ./...`, `make -j 4 check`, `make check V=1`,
	} {
		ran := attacks(line)
		if len(ran) == 0 {
			t.Errorf("%q: bash does not run a push or the suite, so the line does not belong in this table", line)
			continue
		}
		if d := bash(defaultPolicy(), line); d.Decision != Deny {
			t.Errorf("%q: allowed, but bash runs %q", line, ran)
		}
	}
}
