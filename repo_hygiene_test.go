package agentfox

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agent-fox-dev/agentfox/internal/conform"
)

// trackedFiles lists what git tracks, relative to the repository root.
func trackedFiles(t *testing.T, root string) []string {
	t.Helper()
	out, err := exec.Command("git", "-C", root, "ls-files", "-z").Output()
	if err != nil {
		t.Skipf("not a git checkout: %v", err)
	}
	var files []string
	for _, f := range strings.Split(string(out), "\x00") {
		if f != "" {
			files = append(files, f)
		}
	}
	return files
}

// A compiled binary is never tracked: it adds its size to every clone, goes
// stale on the next change, and belongs to a build, not to a spec.
func TestNoCompiledBinaryIsTracked(t *testing.T) {
	root := findWorkspaceRoot(t)
	magics := map[string][]byte{
		"ELF":             {0x7f, 'E', 'L', 'F'},
		"Mach-O (64-bit)": {0xcf, 0xfa, 0xed, 0xfe},
		"Mach-O (32-bit)": {0xce, 0xfa, 0xed, 0xfe},
		"Mach-O (fat)":    {0xca, 0xfe, 0xba, 0xbe},
		"PE":              {'M', 'Z'},
	}
	for _, f := range trackedFiles(t, root) {
		fh, err := os.Open(filepath.Join(root, f))
		if err != nil {
			continue // a tracked file deleted in this working tree
		}
		head := make([]byte, 4)
		n, _ := fh.Read(head)
		_ = fh.Close()
		for kind, magic := range magics {
			if n >= len(magic) && bytes.HasPrefix(head[:n], magic) {
				t.Errorf("%s is a tracked %s binary: build output belongs in bin/ or dist/, which are ignored", f, kind)
			}
		}
	}
}

// Files that were committed by mistake stay out: binaries built into the
// working directory, leftovers of test runs, and an editor workspace that names
// sibling checkouts of one machine.
func TestEarlierStrayFilesAreNotTracked(t *testing.T) {
	root := findWorkspaceRoot(t)
	stray := map[string]bool{
		"impl": true, "cmd/fix/fix": true,
		"internal/toolio/f": true, "internal/toolio/o": true,
		"coder.code-workspace": true,
	}
	for _, f := range trackedFiles(t, root) {
		if stray[f] {
			t.Errorf("%s is tracked, but is not a deliverable of any spec", f)
		}
	}
}

// What `go build ./cmd/<tool>` leaves in the working directory is ignored, for
// each of the four tools, from the repository root and from the tool's own
// directory.
func TestToolBinariesAreIgnored(t *testing.T) {
	root := findWorkspaceRoot(t)
	for _, tool := range []string{"fix", "impl", "spec", "triage"} {
		for _, path := range []string{tool, filepath.Join("cmd", tool, tool)} {
			err := exec.Command("git", "-C", root, "check-ignore", "-q", path).Run()
			var ee *exec.ExitError
			switch {
			case err == nil:
			case errors.As(err, &ee) && ee.ExitCode() == 1:
				t.Errorf("%s is not ignored: a `go build` would leave it as untracked noise", path)
			default:
				// Not a git work tree (a copy of the sources, say): nothing to ask.
				t.Skipf("cannot ask git about %s: %v", path, err)
			}
		}
	}
}

// .gitignore is the project's, not one contributor's: personal patterns belong
// in .git/info/exclude.
func TestGitignoreHoldsNoPersonalPatterns(t *testing.T) {
	root := findWorkspaceRoot(t)
	body, err := os.ReadFile(filepath.Join(root, ".gitignore"))
	if os.IsNotExist(err) {
		t.Skip("no .gitignore here (a copy of the sources, say)")
	}
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(string(body), "\n") {
		switch strings.TrimSpace(line) {
		case "# own stuff", "prompts.md", "hack/", "hack.md":
			t.Errorf(".gitignore holds the personal pattern %q: move it to .git/info/exclude", line)
		}
	}
}

// A fixture that runs git init without naming the branch passes
// on a machine whose global configuration sets init.defaultBranch and fails on
// a clean image. Every one names its branch.
func TestGitFixturesNameTheirBranch(t *testing.T) {
	root := findWorkspaceRoot(t)
	for _, f := range trackedFiles(t, root) {
		if !strings.HasSuffix(f, "_test.go") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(root, f))
		if err != nil {
			continue
		}
		for i, line := range strings.Split(string(b), "\n") {
			if strings.Contains(line, "UnpinnedGitBranch") || strings.Contains(line, "t.Fatalf") || strings.Contains(line, "t.Errorf") ||
				strings.HasPrefix(strings.TrimSpace(line), "//") {
				continue
			}
			if conform.UnpinnedGitBranch(line) {
				t.Errorf("%s:%d runs git init without -b: %s", f, i+1, strings.TrimSpace(line))
			}
		}
	}
}
