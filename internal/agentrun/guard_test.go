package agentrun

import (
	"context"
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
		OnBlock:        func(s string) { blocked = append(blocked, s) },
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
