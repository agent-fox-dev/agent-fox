package toolio_test

import (
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// copyTree copies the repository's working files (including uncommitted
// ones) into dst, leaving out version control, the specs and the docs: none
// of them is Go source the exhaustiveness test compiles.
func copyTree(t *testing.T, src, dst string) {
	t.Helper()
	err := filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		if d.IsDir() {
			switch rel {
			case ".git", ".specs", "docs", "bin", "dist":
				return filepath.SkipDir
			}
			return os.MkdirAll(filepath.Join(dst, rel), 0o755)
		}
		if !d.Type().IsRegular() {
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(dst, rel), raw, info.Mode().Perm())
	})
	if err != nil {
		t.Fatalf("copying the repository: %v", err)
	}
}

// scratchTest runs the four-tree exhaustiveness test in the scratch copy and
// returns its combined output and whether it passed.
func scratchTest(t *testing.T, dir string) (string, bool) {
	t.Helper()
	cmd := exec.Command("go", "test", "-count=1", "-run", "^TestTS10_10_FourResultTreesFullyClassified$", "./internal/toolio")
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	return string(out), err == nil
}

// TS-10-25 (smoke): A maintainer's unclassified field addition to
// codeimpl.Submission fails the test run by name, and fixing it restores a
// passing build.
//
// The run is `go test` of the exhaustiveness test, in a scratch copy of the
// working tree, not the whole `make test` (which is `go test ./... -count=1`
// and would run this test again, recursively): the shipped tree is never
// touched.
//
// Verifies: 10-PATH-3, 10-REQ-2.1, 10-REQ-2.3
// Real components: codeimpl.Submission, toolio.CheckTrust, the exhaustiveness
// test, go test
func TestTS10_25_UnclassifiedFieldFailsTestByNameAndFixRestoresIt_Smoke(t *testing.T) {
	if testing.Short() {
		t.Skip("compiles a scratch copy of the repository")
	}
	root := findWorkspaceRoot(t)
	scratch := filepath.Join(t.TempDir(), "agent-fox")
	copyTree(t, root, scratch)

	// The copy lives elsewhere, so the sibling module go.mod points at by a
	// relative path must be found by an absolute one.
	modPath := filepath.Join(scratch, "go.mod")
	mod, err := os.ReadFile(modPath)
	if err != nil {
		t.Fatal(err)
	}
	sibling, err := filepath.Abs(filepath.Join(root, "..", "agentkit-go"))
	if err != nil {
		t.Fatal(err)
	}
	rewritten := strings.ReplaceAll(string(mod), "=> ../agentkit-go", "=> "+sibling)
	if rewritten == string(mod) {
		t.Fatal("go.mod has no `=> ../agentkit-go` replace to point at the sibling module")
	}
	if err := os.WriteFile(modPath, []byte(rewritten), 0o644); err != nil {
		t.Fatal(err)
	}

	// The scratch copy as shipped passes: the failure below is the new
	// field's, nothing else's.
	if out, ok := scratchTest(t, scratch); !ok {
		t.Fatalf("the scratch copy fails before any change:\n%s", out)
	}

	typesPath := filepath.Join(scratch, "codeimpl", "types.go")
	raw, err := os.ReadFile(typesPath)
	if err != nil {
		t.Fatal(err)
	}
	const anchor = "type Submission struct {\n"
	if strings.Count(string(raw), anchor) != 1 {
		t.Fatalf("codeimpl/types.go: want exactly one %q", anchor)
	}
	write := func(field string) {
		t.Helper()
		src := strings.Replace(string(raw), anchor, anchor+field, 1)
		if err := os.WriteFile(typesPath, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	// A maintainer adds a string field and forgets the trust tag.
	write("\tNewField string `json:\"new_field,omitempty\" description:\"A scratch field.\"`\n")
	out, ok := scratchTest(t, scratch)
	if ok {
		t.Fatalf("an unclassified string field on codeimpl.Submission did not fail the test:\n%s", out)
	}
	if !strings.Contains(out, "Submission.NewField") {
		t.Errorf("the failure does not name Submission.NewField:\n%s", out)
	}

	// The maintainer declares the classification and the run passes again.
	write("\tNewField string `json:\"new_field,omitempty\" trust:\"model\" description:\"A scratch field.\"`\n")
	if out, ok := scratchTest(t, scratch); !ok {
		t.Errorf("declaring trust:\"model\" on the new field did not restore a passing run:\n%s", out)
	}
}
