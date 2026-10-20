package docs

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// read returns a file of the repository, given by its path from the root.
func read(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", filepath.FromSlash(name)))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// TestTheNamedDocumentsDescribeTriage_TS_08_70 verifies TS-08-70:
// the documents that the steering file names say what af triage is, in the
// words its surfaces use, and the generated package docs of the skills exist.
// Verifies: 08-REQ-10.6
func TestTheNamedDocumentsDescribeTriage_TS_08_70(t *testing.T) {
	for file, terms := range map[string][]string{
		"docs/cli.md": {
			"### triage", "### triage brief", "### triage run", "### triage submit", "### triage land",
			"--land", "--label", "--run-id", "--brain", "--sandbox", "--budget", "--max-turns", "--dry-run", "--repo", "--context",
			"needs_human", "replaced_body", "commented", "created_issue", "planned", "rejected_path_calls", "rejected_submissions", "original_report_sha256",
			"af fix <issue url> --dir <dir>", "af triage land --run-id",
			"| `issue_closed` |", "| `report_changed_during_triage` |", "| `body_too_long` |", "| `labels_failed` |", "| `original_report_unparsed` |", "| `rejections_unreadable` |",
			"triage.input", "triage.brief", "triage.submission", "triage.question", "triage.land",
		},
		"docs/configuration.md": {
			"brief.md", "original.md", "triage.json", "submission.json", "rejections.jsonl", "diagnosis.md",
			"no new environment variable",
		},
		"docs/model-usage.md": {
			"af triage", "mandate", "repository map", "original report", "USD 2", "100 turns", "15 minutes", "--budget",
		},
		"docs/architecture.md": {
			"Triage, as built", "internal/triage", "<!-- agent-fox:triage v1", "agent-fox:original-report:begin", "agent-fox:original-report:end",
			"`issuetriage` | `af triage`",
		},
		"README.md": {"| `internal/triage/` |", "`af-fix`, `af-triage`", "the Go package `skills`", "`af triage`"},
	} {
		text := read(t, file)
		for _, term := range terms {
			if !strings.Contains(text, term) {
				t.Errorf("%s does not contain %q", file, term)
			}
		}
	}

	// The decision that the architecture left open is resolved, and the row that
	// said so is rewritten.
	arch := read(t, "docs/architecture.md")
	for _, stale := range []string{"unchanged for now; it is single-phase", "Whether `triage` moves to the same shape"} {
		if strings.Contains(arch, stale) {
			t.Errorf("docs/architecture.md still says %q", stale)
		}
	}
	for _, stale := range []string{"No command of this release registers them", "This release registers six work commands"} {
		if strings.Contains(read(t, "docs/cli.md"), stale) {
			t.Errorf("docs/cli.md still says %q", stale)
		}
	}

	if info, err := os.Stat(filepath.Join("generated", "skills.txt")); err != nil || info.Size() == 0 {
		t.Errorf("docs/generated/skills.txt: %v", err)
	}
}
