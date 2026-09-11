package agentfox

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TS-04-35 (integration): make test and make lint succeed across all migrated packages
// Verifies: 04-REQ-10.5
func TestVerifyMigratedPackages_TS_04_35(t *testing.T) {
	if os.Getenv("AF_VERIFY_SUBTEST") == "1" {
		t.Skip("skipping in recursive make test invocation")
	}

	root := findWorkspaceRoot(t)

	testCmd := exec.Command("make", "test")
	testCmd.Dir = root
	testCmd.Env = append(os.Environ(), "AF_VERIFY_SUBTEST=1")
	testOut, testErr := testCmd.CombinedOutput()
	if testErr != nil {
		t.Fatalf("make test failed: %v\noutput:\n%s", testErr, string(testOut))
	}

	lintCmd := exec.Command("make", "lint")
	lintCmd.Dir = root
	lintCmd.Env = append(os.Environ(), "AF_VERIFY_SUBTEST=1")
	lintOut, lintErr := lintCmd.CombinedOutput()
	if lintErr != nil {
		t.Fatalf("make lint failed: %v\noutput:\n%s", lintErr, string(lintOut))
	}
}

func findWorkspaceRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("os.Getwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("could not find workspace root containing go.mod")
		}
		dir = parent
	}
}
