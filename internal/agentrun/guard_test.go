package agentrun

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agentfox/agentkit-go/core"
)

func execCall(cmd string) core.BeforeToolCallContext {
	return core.BeforeToolCallContext{ToolName: "execute", Arguments: map[string]any{"command": cmd}}
}

func argvCall(words ...string) core.BeforeToolCallContext {
	raw := make([]any, len(words))
	for i, w := range words {
		raw[i] = w
	}
	return core.BeforeToolCallContext{ToolName: "run_command", Arguments: map[string]any{"argv": raw}}
}

func writeGuard(t *testing.T) core.BeforeToolCall {
	t.Helper()
	return Guard(GuardOptions{
		Programs:       []string{"git", "go", "make", "ls", "grep", "curl"},
		AllowOperators: true,
	})
}

func TestGuardRefusesMutatingGit(t *testing.T) {
	g := writeGuard(t)
	ctx := context.Background()

	refused := []core.BeforeToolCallContext{
		execCall("git commit -m x"),
		execCall("git -C . commit -m x"),
		execCall("git push origin main"),
		execCall("git pull"),
		execCall("git clone https://example.com/x"),
		execCall("git checkout -b other"),
		execCall("git branch -D other"),
		execCall("git rebase main"),
		execCall("git config user.name x"),
		// An environment assignment in front of the program does not hide it.
		execCall("GIT_AUTHOR_NAME=x git commit -m y"),
		// The second simple command on the line is the one that matters.
		execCall("ls; git push"),
		execCall("go test ./... && git push"),
		// `git -c` runs a program of the model's choosing before the verb.
		execCall("git -c core.pager=sh log"),
		argvCall("git", "commit", "-m", "x"),
	}
	for _, in := range refused {
		if d := g(ctx, in); !d.Block {
			t.Errorf("allowed: %v", in.Arguments)
		}
	}

	allowed := []core.BeforeToolCallContext{
		execCall("git status --porcelain"),
		execCall("git log --oneline -20"),
		execCall("git diff HEAD"),
		execCall("git show abc123"),
		execCall("git branch --list"),
		execCall("git branch -a"),
		execCall("git remote -v"),
		execCall("git config --get user.email"),
		execCall("git log | grep fix"),
		execCall("go test ./... 2>&1"),
		argvCall("git", "log", "--oneline"),
	}
	for _, in := range allowed {
		if d := g(ctx, in); d.Block {
			t.Errorf("refused %v: %s", in.Arguments, d.Reason)
		}
	}
}

// gh is refused outright, so every write to an issue goes through the audited
// path in Go rather than through a model's shell.
func TestGuardRefusesGH(t *testing.T) {
	g := writeGuard(t)
	d := g(context.Background(), execCall("gh issue comment 1 --body x"))
	if !d.Block {
		t.Fatal("gh was allowed")
	}
	if !strings.Contains(d.Reason, "audited") {
		t.Errorf("Reason = %q; it should say why", d.Reason)
	}
}

func TestGuardRefusesFindWriteFlags(t *testing.T) {
	g := Guard(GuardOptions{Programs: []string{"find"}, AllowOperators: true})
	ctx := context.Background()
	for _, cmd := range []string{
		"find . -name '*.tmp' -delete",
		"find . -exec rm {} ;",
		"find . -fprintf out.txt %p",
	} {
		if d := g(ctx, execCall(cmd)); !d.Block {
			t.Errorf("allowed: %s", cmd)
		}
	}
	if d := g(ctx, execCall("find . -name '*.go'")); d.Block {
		t.Errorf("refused a plain find: %s", d.Reason)
	}
}

// With operators allowed, the shipped policy only inspects the first program;
// every later segment has to be offered to it separately.
func TestGuardAppliesTheAllowlistToEverySegment(t *testing.T) {
	g := Guard(GuardOptions{Programs: []string{"go"}, AllowOperators: true})
	if d := g(context.Background(), execCall("go test ./... && curl http://example.com")); !d.Block {
		t.Error("curl survived behind an allowed program")
	}
}

// A read-only phase gets no operators, because a redirection is a write.
func TestGuardReadOnlyRefusesWriteToolsAndOperators(t *testing.T) {
	g := Guard(GuardOptions{Programs: []string{"git", "ls"}, ReadOnlyFiles: true})
	ctx := context.Background()

	for _, tool := range []string{"write_file", "edit_file"} {
		d := g(ctx, core.BeforeToolCallContext{ToolName: tool, Arguments: map[string]any{"path": "x"}})
		if !d.Block {
			t.Errorf("%s was allowed in a read-only phase", tool)
		}
	}
	if d := g(ctx, execCall("ls > listing.txt")); !d.Block {
		t.Error("a redirection was allowed in a read-only phase")
	}
	if d := g(ctx, execCall("git status")); d.Block {
		t.Errorf("a plain read command was refused: %s", d.Reason)
	}
}

func TestGuardCountsBlocks(t *testing.T) {
	var blocked []string
	g := Guard(GuardOptions{
		Programs:       []string{"git"},
		AllowOperators: true,
		OnBlock:        func(name, reason string) { blocked = append(blocked, name+": "+reason) },
	})
	g(context.Background(), execCall("git push"))
	if len(blocked) != 1 {
		t.Fatalf("OnBlock called %d times", len(blocked))
	}
	if !strings.Contains(blocked[0], "execute") {
		t.Errorf("message = %q", blocked[0])
	}
}

func TestShellSegments(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"ls", []string{"ls"}},
		{"ls; git push", []string{"ls", "git push"}},
		{"go test ./... && git push", []string{"go test ./...", "git push"}},
		{"go test ./... | grep FAIL", []string{"go test ./...", "grep FAIL"}},
		{"go test ./... 2>&1", []string{"go test ./... 2>&1"}}, // a redirection, not a boundary
		{"echo $(git push)", []string{"echo", "git push"}},
		{"echo 'a; b'", []string{"echo 'a; b'"}}, // nothing splits inside single quotes
		// The classifier is not a parser: it leaves the closing bracket on the
		// fragment, which commandWords then trims. The point is that the
		// substituted command is a segment of its own and gets classified.
		{"echo \"$(git push)\"", []string{"echo \"", "git push)\""}},
	}
	for _, c := range cases {
		got := ShellSegments(c.in)
		if len(got) != len(c.want) {
			t.Errorf("ShellSegments(%q) = %q, want %q", c.in, got, c.want)
			continue
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("ShellSegments(%q) = %q, want %q", c.in, got, c.want)
				break
			}
		}
	}
}

func TestShellSegmentsHeredoc(t *testing.T) {
	goBody := "package main\n\nimport \"fmt\"\n\n/ comment\nfunc main() { fmt.Println(1) }\n"
	cases := []struct {
		name string
		in   string
		want []string
	}{
		{"quoted delimiter", "cat > f <<'EOF'\n" + goBody + "EOF\n",
			[]string{"cat > f <<'EOF'"}},
		{"unquoted delimiter", "cat > f <<EOF\n" + goBody + "EOF",
			[]string{"cat > f <<EOF"}},
		{"double-quoted delimiter", "cat > f <<\"EOF\"\n" + goBody + "EOF\n",
			[]string{"cat > f <<\"EOF\""}},
		{"dash strips tabs", "cat <<-EOF\n\tpackage x\n\tEOF\nls",
			[]string{"cat <<-EOF", "ls"}},
		{"without dash tabs do not terminate", "cat <<EOF\nbody\n\tEOF\nEOF\nls",
			[]string{"cat <<EOF", "ls"}},
		{"commands after the terminator", "cat <<'EOF' > f\nimport x\nEOF\ngit push && ls",
			[]string{"cat <<'EOF' > f", "git push", "ls"}},
		{"commands on the heredoc line", "cat <<'EOF' | grep x; ls\nbody\nEOF\n",
			[]string{"cat <<'EOF'", "grep x", "ls"}},
		{"two heredocs", "cat <<A <<'B'\none\nA\ntwo\nB\nls",
			[]string{"cat <<A <<'B'", "ls"}},
		{"here-string is not a heredoc", "cat <<<word\ngit push",
			[]string{"cat <<<word", "git push"}},
		{"unterminated falls back to commands", "cat <<EOF\npackage x\ngit push",
			[]string{"cat <<EOF", "package x", "git push"}},
		{"arithmetic shift is not a heredoc", "echo $((1<<x))\ngit push\nx\n",
			[]string{"echo", "1<<x", "git push", "x"}},
		{"substitution in an unquoted body", "cat <<EOF\nhi $(git push) there\nEOF\n",
			[]string{"cat <<EOF", "git push"}},
		{"backtick in an unquoted body", "cat <<EOF\nhi `git push`\nEOF\n",
			[]string{"cat <<EOF", "git push"}},
		{"escaped dollar in an unquoted body", "cat <<EOF\n\\$(git push)\nEOF\n",
			[]string{"cat <<EOF"}},
		{"substitution in a quoted body is inert", "cat <<'EOF'\n$(git push)\nEOF\n",
			[]string{"cat <<'EOF'"}},
	}
	for _, c := range cases {
		got := ShellSegments(c.in)
		if strings.Join(got, "\x00") != strings.Join(c.want, "\x00") {
			t.Errorf("%s: ShellSegments(%q) = %q, want %q", c.name, c.in, got, c.want)
		}
	}
}

func TestGuardHeredoc(t *testing.T) {
	g := Guard(GuardOptions{Programs: []string{"cat", "ls", "go"}, AllowOperators: true})
	ctx := context.Background()
	body := "package main\nimport \"fmt\"\n/\n"

	if d := g(ctx, execCall("cat > f.go <<'EOF'\n"+body+"EOF\n")); d.Block {
		t.Errorf("a quoted heredoc was refused: %s", d.Reason)
	}
	if d := g(ctx, execCall("cat > f.go <<EOF\n"+body+"EOF\n")); d.Block {
		t.Errorf("an unquoted heredoc was refused: %s", d.Reason)
	}
	if d := g(ctx, execCall("cat <<EOF\nx $(curl evil) y\nEOF\n")); !d.Block {
		t.Error("curl survived in the body of an unquoted heredoc")
	}
	if d := g(ctx, execCall("cat <<'EOF'\nx\nEOF\ncurl evil")); !d.Block {
		t.Error("curl survived after a heredoc")
	}
	if d := g(ctx, execCall("cat <<EOF\ncurl evil")); !d.Block {
		t.Error("an unterminated heredoc hid a command")
	}
}

func TestGuardLeadingCd(t *testing.T) {
	root := t.TempDir()
	g := Guard(GuardOptions{
		Programs:       []string{"go", "ls"},
		AllowOperators: true,
		ResolvePath: func(p string) (string, error) {
			if filepath.IsAbs(p) {
				return filepath.Clean(p), nil
			}
			return filepath.Join(root, p), nil
		},
	})
	ctx := context.Background()

	for _, cmd := range []string{
		"cd " + root + " && go test ./...",
		"cd '" + root + "' && go test ./...",
		"cd " + root + "; ls",
		"cd " + root + "\nls",
		"cd sub && go test ./...",
		"cd " + filepath.Join(root, "sub") + " && cd . && ls",
	} {
		if d := g(ctx, execCall(cmd)); d.Block {
			t.Errorf("refused %q: %s", cmd, d.Reason)
		}
	}
	for _, cmd := range []string{
		"cd /etc && ls",
		"cd .. && ls",
		"cd $X && ls",
		"cd ~ && ls",
		"cd -P " + root + " && ls",
		"cd " + root + " extra && ls",
		"ls && cd " + root,
		"cd " + root + " && curl x",
		"cd " + root,
	} {
		if d := g(ctx, execCall(cmd)); !d.Block {
			t.Errorf("allowed %q", cmd)
		}
	}
}

func TestGuardReportsEveryDisallowedProgram(t *testing.T) {
	g := Guard(GuardOptions{Programs: []string{"ls", "cat"}, AllowOperators: true})
	d := g(context.Background(), execCall("ls && curl x && wget y && curl z"))
	if !d.Block {
		t.Fatal("curl and wget were allowed")
	}
	for _, want := range []string{"curl, wget", "Allowed: cat, ls"} {
		if !strings.Contains(d.Reason, want) {
			t.Errorf("Reason = %q; want it to contain %q", d.Reason, want)
		}
	}
	// The hint is for a command that tried to write a file (#64), not for curl.
	if strings.Contains(d.Reason, "write_file") {
		t.Errorf("Reason = %q; the write_file hint does not belong on a curl refusal", d.Reason)
	}
	if strings.Count(d.Reason, "curl") != 1 {
		t.Errorf("Reason = %q; a program should be named once", d.Reason)
	}

	ro := Guard(GuardOptions{Programs: []string{"ls"}, ReadOnlyFiles: true, AllowOperators: true})
	d = ro(context.Background(), execCall("curl x"))
	if !d.Block || strings.Contains(d.Reason, "write_file") {
		t.Errorf("a read-only phase was pointed at write_file: %q", d.Reason)
	}
}

func TestReadOnlyProgramsAreHarmless(t *testing.T) {
	has := func(name string) bool {
		for _, p := range ReadOnlyPrograms {
			if p == name {
				return true
			}
		}
		return false
	}
	for _, n := range []string{"echo", "printf", "pwd", "true", "test", "du", "git", "cat"} {
		if !has(n) {
			t.Errorf("%s is missing from ReadOnlyPrograms", n)
		}
	}
	// These run other programs, run arbitrary code, delete, or (find) are
	// excluded on purpose.
	for _, n := range []string{"env", "rm", "perl", "find", "cd"} {
		if has(n) {
			t.Errorf("%s must not be in ReadOnlyPrograms", n)
		}
	}
}

// A command hidden inside a substitution is still classified, which is what
// the odd-looking segment above buys.
func TestGuardRefusesACommandInsideASubstitution(t *testing.T) {
	g := writeGuard(t)
	if d := g(context.Background(), execCall(`echo "$(git push)"`)); !d.Block {
		t.Error("git push survived inside a command substitution")
	}
}

func TestCommandVectorsDropsEnvironmentAssignments(t *testing.T) {
	got := CommandVectors("execute", map[string]any{"command": "FOO=bar BAZ=1 git status"})
	if len(got) != 1 || got[0][0] != "git" {
		t.Fatalf("got %v, want the git invocation with the assignments dropped", got)
	}
	// A path with a slash before the "=" is a program, not an assignment.
	got = CommandVectors("execute", map[string]any{"command": "./scripts/x=y.sh"})
	if len(got) != 1 || got[0][0] != "./scripts/x=y.sh" {
		t.Fatalf("got %v", got)
	}
}

// A protected directory is refused to the file tools whatever spelling the
// model uses for the path, and the rest of the tree stays writable.
func TestGuardRefusesWritesUnderProtectedPaths(t *testing.T) {
	root := t.TempDir()
	g := Guard(GuardOptions{
		Programs:       []string{"git"},
		AllowOperators: true,
		ProtectedPaths: []string{filepath.Join(root, ".specs", "09_agent_mode")},
		ResolvePath: func(p string) (string, error) {
			if filepath.IsAbs(p) {
				return filepath.Clean(p), nil
			}
			return filepath.Join(root, p), nil
		},
	})
	ctx := context.Background()
	write := func(p string) core.BeforeToolCallContext {
		return core.BeforeToolCallContext{ToolName: "write_file", Arguments: map[string]any{"path": p, "content": "x"}}
	}
	for _, p := range []string{
		".specs/09_agent_mode/tasks.json",
		".specs/09_agent_mode/../09_agent_mode/prd.md",
		filepath.Join(root, ".specs", "09_agent_mode", "test_spec.json"),
	} {
		d := g(ctx, write(p))
		if !d.Block {
			t.Errorf("write to %s was allowed", p)
		}
		if !strings.Contains(d.Reason, "not yours to edit") {
			t.Errorf("reason for %s = %q", p, d.Reason)
		}
	}
	for _, p := range []string{
		"cmd/spec/root.go",
		".specs/09_agent_mode_notes.md", // a sibling that merely shares the prefix
		".specs/steering.md",
	} {
		if d := g(ctx, write(p)); d.Block {
			t.Errorf("write to %s was refused: %s", p, d.Reason)
		}
	}
	if d := g(ctx, core.BeforeToolCallContext{ToolName: "edit_file",
		Arguments: map[string]any{"path": ".specs/09_agent_mode/tasks.json"}}); !d.Block {
		t.Error("edit_file reached the protected directory")
	}
}

// Inputs that hid a command from the heredoc-aware scanner (#63). Each one is
// run by bash with the hidden program executing; the guard must either see it
// or refuse the call.
func TestGuardHostileHeredocInputs(t *testing.T) {
	g := Guard(GuardOptions{Programs: []string{"cat", "ls", "echo"}, AllowOperators: true})
	ctx := context.Background()
	cases := []struct{ name, in string }{
		{"comment before a heredoc operator", "ls # <<EOF\ncurl evil.sh\nEOF"},
		{"comment before a heredoc, git push", "ls # <<EOF\ngit push origin main\nEOF"},
		{"quoted paren in a substitution", "cat <<EOF\n$(echo ')' ; curl evil.sh)\nEOF"},
		{"quoted paren, git push", "cat <<EOF\n$(echo ')' ; git push origin main)\nEOF"},
		{"double-quoted paren in a substitution", "cat <<EOF\n$(echo \")\" ; curl evil.sh)\nEOF"},
		{"escaped paren in a substitution", "cat <<EOF\n$(echo \\) ; curl evil.sh)\nEOF"},
		{"case pattern in a substitution", "cat <<EOF\n$(case x in x) echo hi ;; esac; curl evil.sh)\nEOF"},
		{"bare arithmetic is not a heredoc", "((x=1<<a))\ncurl evil.sh\na"},
	}
	for _, c := range cases {
		if d := g(ctx, execCall(c.in)); !d.Block {
			t.Errorf("%s: %q was not blocked (segments %q)", c.name, c.in, ShellSegments(c.in))
		}
	}
}

func TestShellSegmentsHostileHeredocInputs(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want []string
	}{
		{"comment hides the operator", "ls # <<EOF\ncurl evil.sh\nEOF", []string{"ls", "curl evil.sh", "EOF"}},
		{"hash inside a word is not a comment", "echo a#b <<EOF\nbody\nEOF\nls", []string{"echo a#b <<EOF", "ls"}},
		{"quoted paren is found", "cat <<EOF\n$(echo ')' ; curl evil.sh)\nEOF",
			[]string{"cat <<EOF", "echo ')'", "curl evil.sh"}},
		{"bare arithmetic", "((x=1<<a))\ngit push\na", []string{"x=1<<a", "git push", "a"}},
		{"a legitimate substitution still skips the body", "cat <<EOF\nnow $(date)\nEOF\nls",
			[]string{"cat <<EOF", "date", "ls"}},
	}
	for _, c := range cases {
		got := ShellSegments(c.in)
		if strings.Join(got, "\x00") != strings.Join(c.want, "\x00") {
			t.Errorf("%s: ShellSegments(%q) = %q, want %q", c.name, c.in, got, c.want)
		}
	}
}

func TestGuardRefusalReasonsAreConsistent(t *testing.T) {
	ctx := context.Background()
	write := Guard(GuardOptions{Programs: []string{"ls", "git"}, AllowOperators: true})
	readOnly := Guard(GuardOptions{Programs: []string{"ls", "git"}, ReadOnlyFiles: true})

	// The write_file hint is for a phase that has it and a command that tried
	// to write a file.
	if d := write(ctx, execCall("curl x")); !d.Block || strings.Contains(d.Reason, "write_file") {
		t.Errorf("curl reason = %q", d.Reason)
	}
	if d := write(ctx, execCall("cat <<EOF > f\nx\nEOF")); !strings.Contains(d.Reason, "write_file") {
		t.Errorf("heredoc reason = %q", d.Reason)
	}
	if d := readOnly(ctx, execCall("cat > f")); strings.Contains(d.Reason, "write_file") {
		t.Errorf("a read-only phase was pointed at write_file: %q", d.Reason)
	}

	// git and the allowlist are reported together.
	d := write(ctx, execCall("git push && curl x"))
	if !strings.Contains(d.Reason, "git") || !strings.Contains(d.Reason, "curl") {
		t.Errorf("combined reason = %q", d.Reason)
	}

	// The floor policy's wording is restated.
	d = readOnly(ctx, execCall("ls | wc"))
	if !d.Block || strings.Contains(d.Reason, "guard.Restricted") {
		t.Errorf("operator reason = %q", d.Reason)
	}
}

// In a read-only phase the shell is held to the workspace like the file
// tools are (#67).
func TestReadOnlyShellIsConfinedToTheWorkspace(t *testing.T) {
	root := t.TempDir()
	resolve := func(p string) (string, error) {
		if !filepath.IsAbs(p) {
			p = filepath.Join(root, p)
		}
		return filepath.Clean(p), nil
	}
	g := Guard(GuardOptions{Programs: []string{"cat", "ls", "grep", "rg", "head"}, ReadOnlyFiles: true, ResolvePath: resolve})
	ctx := context.Background()

	for _, cmd := range []string{
		"cat /etc/passwd",
		"ls /root/go/pkg/mod",
		"grep -r foo /root/go/pkg/mod",
		"grep foo ../agentkit-go",
		"cat ~/.ssh/config",
		"cat $HOME/.bashrc",
		"head -n 5 ../../outside.txt",
		"rg -e foo /usr/lib",
		"cat " + filepath.Join(root, "..", "other"),
	} {
		d := g(ctx, execCall(cmd))
		if !d.Block || !strings.Contains(d.Reason, "outside the workspace") {
			t.Errorf("%q: block=%v reason=%q", cmd, d.Block, d.Reason)
		}
	}
	for _, cmd := range []string{
		"cat go.mod",
		"ls internal/agentrun",
		"cat " + filepath.Join(root, "go.mod"),
		`grep "/api/v1" -r .`,
		"rg /usr/bin internal",
		"head -n 5 README.md",
	} {
		if d := g(ctx, execCall(cmd)); d.Block {
			t.Errorf("%q was refused: %s", cmd, d.Reason)
		}
	}

	// A phase that writes is not held to it: a build reads outside the repo.
	w := Guard(GuardOptions{Programs: []string{"cat"}, AllowOperators: true, ResolvePath: resolve})
	if d := w(ctx, execCall("cat /etc/hosts")); d.Block {
		t.Errorf("a write phase was confined: %s", d.Reason)
	}
}
