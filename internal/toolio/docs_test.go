package toolio_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// docsTableRow returns the pipe-table line of text whose first cell starts
// with prefix, or "" when there is none.
func docsTableRow(text, prefix string) string {
	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(line, prefix) {
			return line
		}
	}
	return ""
}

func readCLIDoc(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(findWorkspaceRoot(t), "docs", "cli.md"))
	if err != nil {
		t.Fatalf("read docs/cli.md: %v", err)
	}
	return string(b)
}

// TS-08-24 (unit): docs/cli.md's shared-flags table documents --output, its
// atomicity, its ordering before stdout, and the report-file collision.
func TestTS08_24_DocsSharedFlagsOutputRow(t *testing.T) {
	text := readCLIDoc(t)
	row := docsTableRow(text, "| `--output`")
	if row == "" {
		t.Fatalf("docs/cli.md shared-flags table has no --output row")
	}
	for _, want := range []string{"second copy", "atomic", "before", "stdout", "--report-file", "output_matches_report_file"} {
		if !strings.Contains(row, want) {
			t.Errorf("--output row does not mention %q:\n%s", want, row)
		}
	}
}

// TS-08-25 (unit): docs/cli.md's warning-codes table lists output_not_written
// and output_matches_report_file alongside report_file_not_written.
func TestTS08_25_DocsWarningCodesRows(t *testing.T) {
	text := readCLIDoc(t)
	for _, code := range []string{"output_not_written", "output_matches_report_file", "report_file_not_written"} {
		row := docsTableRow(text, "| `"+code+"`")
		if row == "" {
			t.Errorf("warning-codes table has no row for %s", code)
			continue
		}
		if !strings.Contains(row, "| low | emit |") && code != "report_file_not_written" {
			t.Errorf("row for %s should be low severity, stage emit: %s", code, row)
		}
	}
}
