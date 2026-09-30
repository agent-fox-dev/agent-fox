package agentfox

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TS-05-40 (unit): docs/cli.md's output example includes status, summary and
// needs_human fields
// Verifies: 05-REQ-6.1
func TestDocsCliOutputExample_TS_05_40(t *testing.T) {
	root := findWorkspaceRoot(t)
	body, err := os.ReadFile(filepath.Join(root, "docs", "cli.md"))
	if err != nil {
		t.Fatalf("reading docs/cli.md: %v", err)
	}
	text := string(body)
	for _, key := range []string{`"status"`, `"summary"`, `"needs_human"`} {
		if !strings.Contains(text, key) {
			t.Errorf("docs/cli.md output example missing %s", key)
		}
	}
}

// TS-05-41 (unit): docs/cli.md's error-category table gains a retryable
// column and a new warning-codes table
// Verifies: 05-REQ-6.2
func TestDocsCliRetryableAndWarningCodes_TS_05_41(t *testing.T) {
	root := findWorkspaceRoot(t)
	body, err := os.ReadFile(filepath.Join(root, "docs", "cli.md"))
	if err != nil {
		t.Fatalf("reading docs/cli.md: %v", err)
	}
	text := string(body)
	if !strings.Contains(text, "retryable") {
		t.Errorf("docs/cli.md missing a retryable column")
	}
	if !strings.Contains(text, "input_truncated") {
		t.Errorf("docs/cli.md missing the warning-codes table (no input_truncated)")
	}
	if !strings.Contains(text, "severity") {
		t.Errorf("docs/cli.md warning-codes table missing a severity column")
	}
	if !strings.Contains(text, "stage") {
		t.Errorf("docs/cli.md warning-codes table missing a stage column")
	}
}

// TS-05-42 (unit): docs/cli.md documents --context under the shared flags
// section
// Verifies: 05-REQ-6.3
func TestDocsCliContextFlag_TS_05_42(t *testing.T) {
	root := findWorkspaceRoot(t)
	body, err := os.ReadFile(filepath.Join(root, "docs", "cli.md"))
	if err != nil {
		t.Fatalf("reading docs/cli.md: %v", err)
	}
	text := string(body)
	if !strings.Contains(text, "--context") {
		t.Errorf("docs/cli.md does not document --context")
	}
}

// TS-05-43 (unit): README.md's exit-code sentence names the matching status
// value for each of the five exit codes
// Verifies: 05-REQ-6.4
func TestReadmeExitCodeStatusNames_TS_05_43(t *testing.T) {
	root := findWorkspaceRoot(t)
	body, err := os.ReadFile(filepath.Join(root, "README.md"))
	if err != nil {
		t.Fatalf("reading README.md: %v", err)
	}
	text := string(body)
	for _, s := range []string{"done", "failed", "usage", "needs_human", "unverified"} {
		if !strings.Contains(text, s) {
			t.Errorf("README.md exit-code sentence missing status word %q", s)
		}
	}
}
