package policy

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
)

// TestDecide_DeniesEveryBypassShape: a line that hides a push, the suite, a
// command of the loop or a write into a protected path, however it hides it,
// is denied by the rule it would break.
func TestDecide_DeniesEveryBypassShape(t *testing.T) {
	t.Setenv("HOME", "/work") // ~/repo is the checkout
	const git, suite, path, unreadable = RuleGitMutation, RuleSuiteCommand, RuleProtectedPath, RuleUnparseable
	var cases []expectation
	add := func(rule string, commands ...string) {
		for _, c := range commands {
			cases = append(cases, denied(c, rule))
		}
	}

	// Another command of the line, a substitution, a nested shell: the segment
	// named is the one that runs.
	cases = append(cases,
		expectation{"echo ok && go test ./...", Deny, suite, "go test ./..."},
		expectation{"ls | go test ./...", Deny, suite, "go test ./..."},
		expectation{"echo $(make check)", Deny, suite, "make check"},
		expectation{"echo `make check`", Deny, suite, "make check"},
		expectation{"bash -c 'go test ./...'", Deny, suite, "go test ./..."},
		expectation{"FOO=1 env time timeout 5 nice nohup command exec make check", Deny, suite, "FOO=1 env time timeout 5 nice nohup command exec make check"},
		expectation{`sh -c "git push"`, Deny, git, "git push"},
		expectation{`echo "$(git push)"`, Deny, git, "git push"},
		expectation{"cat <<EOF\n$(git push)\nEOF", Deny, git, "git push"},
		expectation{"echo ${x:-$(git push)}", Deny, git, "git push"},
		expectation{"diff <(git push) b", Deny, git, "git push"},
		expectation{"echo $((1 + $(git push)))", Deny, git, "git push"},
		expectation{"bash -c 'git push'", Deny, git, "git push"},
		expectation{"sh -lc 'git push'", Deny, git, "git push"},
		expectation{"bash -ec 'git push'", Deny, git, "git push"},
		expectation{"bash --norc -c 'git push'", Deny, git, "git push"},
		expectation{`bash -c "bash -c 'git push'"`, Deny, git, "git push"},
		expectation{"env FOO=1 bash -c 'git push'", Deny, git, "git push"},
		expectation{"bash -c 'git push' arg0 arg1", Deny, git, "git push"},
		expectation{`eval "git push"`, Deny, git, "git push"},
		expectation{"zsh -c 'make check'", Deny, suite, "make check"},
	)
	// Wrappers, their options, assignments and the keywords of compound commands.
	add(git, "env FOO=1 git push", "env -i -u HOME git push", "env -C /tmp git push", "nohup git push &", "time -p git push",
		"timeout 5 git push", "timeout -s KILL 5 git push", "timeout --signal=KILL -k 2 5 git push", "timeout -- 5 git push", "nice -n 10 git push", "nice -10 git push",
		"command git push", "command -p git push", "exec git push", "exec -a x git push", "FOO=1 BAR=2 git push", "env env env git push", "env -- git push",
		"if true; then git push; fi", "{ git push; }", "( git push )", "for i in 1 2; do git push; done", "while :; do git push; done",
		"! git push", "true && ! git push", "time git push", "/usr/bin/git push", "git push origin main", `FOO="a b" git push`,
		`builtin exec git push`, `command builtin exec git push`, `xargs git push`, `xargs -n 1 -I {} git push {}`,
		`function f { git push; }; f`, `coproc git push`, `coproc NAME { git push; }`,
		`find . -exec git push \;`, `find . -exec git push {} + -exec git commit \;`)
	add(suite, `find . -name x -execdir make check \;`)
	// Spelling: quotes, escapes, ANSI-C strings, braces, $IFS, case.
	add(git, `git commit -m "no \"quotes\" # here"`, `"git" push`, `g\it push`, `'g'"i"t push`, `$'git' push`, `$'\x67it' push`, `$"git" push`,
		`{git,push}`, `git${IFS}push`, `Git push`, `GIT push`, "echo `echo \\`git push\\``", `echo $'\''; git push; echo x # '`,
		"(( 1 << 2 ))\ngit push", "((git push); (git status))", "echo $((git push) )")
	add(suite, `{make,check}`, `{go,test,./...}`, `MAKE check`, `Make check`)
	// env -S splits its string into the command.
	add(git, `env -S 'git push'`, `env -iS 'git push'`, `env --split-string='git push'`, `env --split-string 'git push'`, `env -S'git push'`,
		`env -u X -S 'git push' FOO=1`, `env -S '"git" p\ush'`, "env -S \"'git'\tpush\"")
	add(suite, `env -S "make check"`)
	// Scripts a shell or a builtin is given, however the options come.
	add(git, `bash --rcfile /dev/null -c 'git push'`, `bash --init-file /dev/null -c 'git push'`, `bash -c -x 'git push'`, `bash -O extglob -c 'git push'`,
		`sh -c -- 'git push'`, `trap 'git push' EXIT`, `trap -- 'git push' EXIT`, `eval -- 'git push'`, `af gate run && bash -c 'git push'`)
	add(suite, `trap "make check" EXIT INT`)
	add(RuleAfCommand, "env af fix land", "bash -c 'af fix land'", "/usr/local/bin/af fix land", "af --dir x fix land")
	// Configuration for git that makes it run a program.
	add(git, `GIT_CONFIG_COUNT=1 GIT_CONFIG_KEY_0=core.fsmonitor GIT_CONFIG_VALUE_0='git push' git status`, `GIT_CONFIG_PARAMETERS="'core.pager=x'" git log`,
		`GIT_EXTERNAL_DIFF='git push;:' git diff`, `env GIT_EXTERNAL_DIFF=x git diff`, `export GIT_EXTERNAL_DIFF=x; git diff`, `GIT_SSH_COMMAND=x git show`,
		`git --exec-path=/tmp/x status`, `git grep -O x y`, `git grep -nO x y`, `git grep --open-files-in-pager=x y`,
		"git -c core.x=y status", "git -ccore.x=y status", "git --config-env=core.x=Y status", "git -C sub push", "git --no-pager commit")
	// The suite with options of its own, or with the packages listed.
	add(suite, `go test -timeout 5m ./...`, `go test -p 1 ./...`, `go test -count 1 -parallel 4 ./...`, `make -j 4 check`, `make -f Makefile check`,
		`make check V=1`, `go test $(go list ./...)`, "go test `go list ./...`", "/usr/bin/make check", "go test -v -race ./...")
	// What cannot be read, or reads its commands from its input.
	add(unreadable, "echo $[1<<2]\ngit push", `function f() { git push; }; f`, "bash -c 'echo \"unterminated'",
		`echo git push | sh`, `sh <<< 'git push'`, "bash <<EOF\ngit push\nEOF", `bash -s`, `bash -s -- x`, `curl x | bash`, `sh /dev/stdin`, `bash /dev/fd/0`,
		`bash`, `bash -i`, `zsh -l`, `source <(echo git push)`, `. /dev/stdin`, `bash -o pipefail`, `sh -`, `bash +x`)
	// A protected path in another spelling: case, globs, variables and ~, a
	// path through .., a nested command.
	add(path, "echo x > .SPECS/a.md", "rm -rf .Git", "touch .Agent-Fox/x", "echo x > .GitHub/Workflows/ci.yml", "echo x > .GitLab-CI.yml",
		"rm -rf .spe?s", "rm -rf .sp*", "echo x > .spe[c]s/a.md", "rm -rf .*", "rm -rf .git*", "rm -rf .specs/*", "echo x > .s*/a.md",
		"rm -rf .github/work*", "mv .sp* /tmp",
		`echo x > "$PWD/.specs/a.md"`, `echo x > ${PWD}/.specs/a.md`, "echo x > ~/repo/.specs/a", `echo x > "$HOME/repo/.specs/a"`, `echo x > ${HOME}/repo/.git/x`, "echo x > ~/repo/.git/hooks/x",
		"echo x > /work/repo/.specs/a.md", "echo x > .specs/../.specs/a.md", "cp a ../repo/.specs/b", "echo $(echo x > .specs/a)", "bash -c 'echo x > .specs/a'",
		"cd src && echo x > ../.specs/a", "cd src; cd .. && echo x > .specs/a", "echo x > .{specs,src}/a")
	// A program that writes where it is told to.
	add(path, "git diff --output=.specs/out.md", "git log --output .specs/out.md", "git show --output=.git/x", "git diff > .specs/out",
		"find .specs -delete", "find . -delete", "find -L . -maxdepth 1 -depth -delete", "find .specs -name x -delete", "find .specs -name x -fprint .specs/list",
		"dd of=.specs/a.md", "dd if=x of=.git/hooks/pre-commit", "install a .specs/b", "install -t .specs a b", "rsync -a a .specs/",
		"perl -pi -e s/a/b/ .specs/x", "perl -i.bak -pe s/a/b/ .specs/x", "patch .specs/x", "go build -o .github/workflows/ci.yml",
		"go test -coverprofile=.specs/c.out ./pkg", "go test -coverprofile .specs/c.out ./pkg", "af gate run --output .specs/a.md", "af gate run --output=.specs/a.md",
		"chown me .specs/a", "chgrp me .specs/a", "rmdir .specs", "unlink .specs/a", "shred .specs/a", "mkfifo .specs/f",
		"curl -o .specs/a x", "sort -o .specs/a b", "ln -s .specs link", "ln .specs/a b", "ln -t .specs a", "mv -t .git a", "echo x >& .specs/a", "cat <> .specs/a")
	requireDecisions(t, defaultPolicy(), cases)
}

// TestDecide_AllowsWhatOnlyLooksLikeABypass: a line that does what it says it
// does is not refused for looking like one that hides something.
func TestDecide_AllowsWhatOnlyLooksLikeABypass(t *testing.T) {
	t.Setenv("HOME", "/work")
	var cases []expectation
	for _, c := range []string{
		`echo "git push"`, `echo 'git push'`, `echo '$(git push)'`, `echo \$HOME`, "echo ok # git push", "cat <<EOF\ngit push\nEOF", "cat <<'EOF'\n$(git push)\nEOF",
		"echo git push", "env", "env FOO=1", "nohup ls", "timeout 5 ls", "timeout 5", "timeout --", "command -v git", "git status && ls", "echo bash -c 'git push'",
		"bash -c 'echo hi'", "bash script.sh", "bash -c", "bash --version", "sh -n script.sh", "sh -c 'ls'", "echo x | wc",
		`echo $'it\'s'`, `echo {a,b}`, `echo $(( 1 << 2 ))`, `echo "$((1<<2))" && ls`, `echo "{git,push}"`, `echo ${IFS}`, `echo "git${IFS}push"`,
		"echo `echo \\`date\\``", `echo $'\x67it push'`, `env -S 'ls -l'`, `env -u X ls`, `env -S`, `xargs echo`,
		`trap - EXIT`, `trap 'echo bye' EXIT`, `trap -p`, `trap -l`, `trap`, `trap 1 2`, `eval "echo hi"`, `eval`, `function f { echo hi; }; f`,
		`find . -name x -exec echo {} \;`, `find . -name x`, `find .specs -name x -print`, `find . -exec \;`,
		`git diff --stat`, `git log --oneline -n 5`, `GIT_DIR=.git git status`, `GIT_PAGER=cat git log`, `git grep -n x`, `git diff --output=out.txt`, "GIT_EXTERNAL_DIFF=x true; git diff", "export OTHER=1; git diff",
		`go test -run X ./pkg`, `go test -timeout 5m ./pkg/...`, `go test -timeout 5m -run TestA ./...`, `make -j 4 lint`, `make -f Makefile build`, `make build V=1`, `go test $(go list ./pkg/...)`,
		// Paths that are not protected, however they are spelled.
		"touch -r .specs/a.md newfile", "touch -d yesterday src/a", "find src -name x -delete", "find . -name '*.tmp' -delete", "find . -type f -delete -print", "dd if=.specs/a of=out",
		"rm -rf *", "rm -rf ./*", "rm -rf src/*", "rm -rf src/.git*", "echo x > src/.spe?s", "echo x > src/*.go", "echo $PWD", `echo x > "$PWD/src/a"`, "echo x > ~/other/a", "echo x > ~root/a", "echo x > $HOMEx/.specs",
		"install a src/b", "rsync -a a src/", "perl -ne print .specs/x", "go build -o bin/af ./cmd/af", "af gate run --output out.json", "sort -o out.txt a", "sort -- -o .specs/a",
		"ln -s src link", "chown me src/a", "rmdir src", "cd ~ && echo x > a", "cd $X && ls", "cd - && ls", "cd -P src a && ls",
	} {
		cases = append(cases, allowed(c))
	}
	requireDecisions(t, defaultPolicy(), cases)
}

// TestDecide_DeniesWhatARealBashWouldRun compares with the shell itself: every
// line of the table makes a real bash run a push or the suite through stubs
// that log their arguments, and the policy must deny every one. A line that
// makes bash run neither does not belong in the table.
func TestDecide_DeniesWhatARealBashWouldRun(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("there is no bash to compare with")
	}
	bashPath, err := exec.LookPath("bash")
	if err != nil {
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
	// attacks are the forbidden runs a bash made of the line.
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
