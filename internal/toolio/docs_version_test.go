package toolio_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agent-fox-dev/agentfox/internal/schematest"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

// docSectionV returns the text of the level-2 section whose heading line
// contains heading, up to (not including) the next level-2 heading.
func docSectionV(t *testing.T, doc, heading string) string {
	t.Helper()
	lines := strings.Split(doc, "\n")
	start := -1
	for i, l := range lines {
		if strings.HasPrefix(l, "## ") && strings.Contains(l, heading) {
			start = i
			break
		}
	}
	if start < 0 {
		t.Fatalf("no section %q", heading)
	}
	end := len(lines)
	for i := start + 1; i < len(lines); i++ {
		if strings.HasPrefix(lines[i], "## ") {
			end = i
			break
		}
	}
	return strings.Join(lines[start:end], "\n")
}

// tableRowV returns the first line of sec that starts with prefix.
func tableRowV(sec, prefix string) (string, bool) {
	for _, l := range strings.Split(sec, "\n") {
		if strings.HasPrefix(l, prefix) {
			return l, true
		}
	}
	return "", false
}

// TS-12-55 (integration): SchemaVersion is 3.0.0 and each tool's --schema
// output equals its regenerated golden file.
// Verifies: 12-REQ-10.1
func TestTS12_55_SchemaVersionAndGoldenFiles(t *testing.T) {
	if testing.Short() {
		t.Skip("builds four binaries")
	}
	tools := []string{"fix", "impl", "triage", "spec"}
	for _, tool := range tools {
		t.Run(tool, func(t *testing.T) {
			live := schematest.Live(t, tool)
			root := schematest.Root(t)
			golden, err := os.ReadFile(schematest.GoldenFile(root, tool))
			if err != nil {
				t.Fatalf("cannot read golden file: %v", err)
			}
			if string(live) != string(golden) {
				t.Errorf("%s: --schema output differs from golden file", tool)
			}

			var doc map[string]json.RawMessage
			if err := json.Unmarshal(live, &doc); err != nil {
				t.Fatalf("cannot parse --schema output: %v", err)
			}

			// schema_version is 3.0.0
			var sv string
			if err := json.Unmarshal(doc["schema_version"], &sv); err != nil {
				t.Fatalf("cannot parse schema_version: %v", err)
			}
			if sv != "3.0.0" {
				t.Errorf("schema_version = %q, want 3.0.0", sv)
			}

			text := string(live)

			// lists --emit-events
			if !strings.Contains(text, `"emit-events"`) {
				t.Errorf("golden does not list --emit-events")
			}
			// does not list --events or --events-file
			if strings.Contains(text, `"events-file"`) {
				t.Errorf("golden still lists --events-file")
			}
			// Check for --events as a standalone flag (not emit-events)
			var flags struct {
				Flags struct {
					Properties map[string]json.RawMessage `json:"properties"`
				} `json:"flags"`
			}
			if err := json.Unmarshal(live, &flags); err == nil {
				if _, ok := flags.Flags.Properties["events"]; ok {
					t.Errorf("golden still lists --events flag")
				}
				if _, ok := flags.Flags.Properties["events-file"]; ok {
					t.Errorf("golden still lists --events-file flag")
				}
			}

			// envelope has session_id property
			if !strings.Contains(text, `"session_id"`) {
				t.Errorf("golden does not have session_id in envelope")
			}

			// events_file in artifact-kind enum
			if !strings.Contains(text, `"events_file"`) {
				t.Errorf("golden does not have events_file in artifact-kind enum")
			}
		})
	}

	// Also verify the Go constant
	if toolio.SchemaVersion != "3.0.0" {
		t.Errorf("toolio.SchemaVersion = %q, want 3.0.0", toolio.SchemaVersion)
	}
}

// TS-12-56 (unit): docs/cli.md describes --emit-events, the new names, events
// and the 3.0.0 changelog.
// Verifies: 12-REQ-10.2
func TestTS12_56_DocsCliEmitEventsAndChangelog(t *testing.T) {
	text := readCLIDoc(t)

	// Shared flags table has --emit-events row
	shared := docSectionV(t, text, "Shared flags")
	if _, ok := tableRowV(shared, "| `--emit-events`"); !ok {
		t.Errorf("shared-flags table has no --emit-events row")
	}

	// No --events or --events-file row (the combined row)
	if strings.Contains(shared, "| `--events` · `--events-file`") {
		t.Errorf("shared-flags table still has the combined --events · --events-file row")
	}
	// No standalone --events-file row
	if _, ok := tableRowV(shared, "| `--events-file`"); ok {
		t.Errorf("shared-flags table still has --events-file row")
	}

	// --report-file / --detail row states session_id in the file name
	detailRow := docsTableRow(text, "| `--detail`")
	if detailRow != "" && !strings.Contains(detailRow, "session_id") {
		t.Errorf("--detail/--report-file row does not mention session_id in the file name")
	}

	// Machine-readable progress section mentions session_id
	eventSec := docSectionV(t, text, "Machine-readable progress")
	if !strings.Contains(eventSec, "session_id") {
		t.Errorf("Machine-readable progress section does not mention session_id")
	}

	// run_end.report_file
	if !strings.Contains(eventSec, "report_file") {
		t.Errorf("Machine-readable progress section does not mention report_file")
	}

	// No --verbose/--show-text gate language
	if strings.Contains(eventSec, "Under `--show-text` the stream also carries") {
		t.Errorf("Machine-readable progress section still has the --show-text gate for text events")
	}
	if strings.Contains(eventSec, "under `--verbose` only") {
		t.Errorf("Machine-readable progress section still has the --verbose gate for tool_call")
	}

	// warnings table has events_file_not_written
	if !strings.Contains(text, "events_file_not_written") {
		t.Errorf("docs/cli.md does not mention events_file_not_written")
	}

	// artifact kinds have events_file
	if !strings.Contains(text, "`events_file`") {
		t.Errorf("docs/cli.md does not mention events_file artifact kind")
	}

	// 3.0.0 changelog entry exists
	if !strings.Contains(text, "**3.0.0**") {
		t.Errorf("docs/cli.md has no 3.0.0 changelog entry")
	}
}

// TS-12-57 (unit): docs/configuration.md has a State directory section and
// docs_test asserts it.
// Verifies: 12-REQ-10.3
func TestTS12_57_ConfigurationStateDirectory(t *testing.T) {
	root := findWorkspaceRoot(t)
	doc, err := os.ReadFile(filepath.Join(root, "docs", "configuration.md"))
	if err != nil {
		t.Fatalf("reading docs/configuration.md: %v", err)
	}
	text := string(doc)

	// heading '## State directory' exists
	if !strings.Contains(text, "## State directory") {
		t.Errorf("docs/configuration.md has no '## State directory' heading")
	}
	// '## Report files' does not exist
	if strings.Contains(text, "## Report files") {
		t.Errorf("docs/configuration.md still has '## Report files' heading")
	}
	// mentions runs/, events/, 0700, 0600 and the new file names
	for _, want := range []string{"runs/", "events/", "0700", "0600"} {
		if !strings.Contains(text, want) {
			t.Errorf("docs/configuration.md does not mention %q", want)
		}
	}
	// XDG_STATE_HOME row says tests set it
	if !strings.Contains(text, "XDG_STATE_HOME") {
		t.Errorf("docs/configuration.md does not mention XDG_STATE_HOME")
	}
}

// TS-12-58 (unit): docs/model-usage.md names --emit-events or the events file
// for watching spend.
// Verifies: 12-REQ-10.4
func TestTS12_58_ModelUsageEmitEvents(t *testing.T) {
	root := findWorkspaceRoot(t)
	doc, err := os.ReadFile(filepath.Join(root, "docs", "model-usage.md"))
	if err != nil {
		t.Fatalf("reading docs/model-usage.md: %v", err)
	}
	text := string(doc)

	// mentions --emit-events or the events file
	if !strings.Contains(text, "--emit-events") && !strings.Contains(text, "events file") {
		t.Errorf("docs/model-usage.md does not mention --emit-events or the events file")
	}
	// does not contain '--events jsonl' or '--events-file'
	if strings.Contains(text, "--events jsonl") {
		t.Errorf("docs/model-usage.md still mentions '--events jsonl'")
	}
	if strings.Contains(text, "--events-file") {
		t.Errorf("docs/model-usage.md still mentions '--events-file'")
	}
}

// TS-12-59 (unit): No file outside removed-flag tests references the removed
// flags, text.delta or the pid-based report name.
// Verifies: 12-REQ-10.5
func TestTS12_59_NoStaleReferences(t *testing.T) {
	root := findWorkspaceRoot(t)

	// Scan Go sources, tests and docs for stale references.
	var hits []string
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			base := filepath.Base(path)
			if base == ".git" || base == "vendor" || base == "node_modules" || base == ".specs" {
				return filepath.SkipDir
			}
			return nil
		}
		ext := filepath.Ext(path)
		if ext != ".go" && ext != ".md" {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		text := string(b)
		rel, _ := filepath.Rel(root, path)

		// Allowed files: removed-flag error tests, the 3.0.0 changelog,
		// PRD reference docs, and test files that test the removed-flag errors.
		isAllowed := func() bool {
			// toolflags.go and its test: they define the removed-flag messages
			if strings.HasSuffix(rel, "toolflags.go") || strings.HasSuffix(rel, "toolflags_test.go") {
				return true
			}
			// emit_events_test.go: tests the removed-flag errors
			if strings.HasSuffix(rel, "emit_events_test.go") {
				return true
			}
			// The 3.0.0 changelog in cli.md
			if strings.HasSuffix(rel, "docs/cli.md") {
				return true
			}
			// This test file itself
			if strings.HasSuffix(rel, "docs_version_test.go") {
				return true
			}
			// PRD reference docs are historical and not updated
			if strings.Contains(rel, "docs/prds/") {
				return true
			}
			// Test files that test the removed-flag errors
			if strings.HasSuffix(rel, "app_test.go") || strings.HasSuffix(rel, "join_test.go") ||
				strings.HasSuffix(rel, "events_file_test.go") || strings.HasSuffix(rel, "smoke_test.go") ||
				strings.HasSuffix(rel, "flagschema_test.go") || strings.HasSuffix(rel, "events_test.go") {
				return true
			}
			return false
		}

		if isAllowed() {
			return nil
		}

		// Check for stale patterns
		// '--events-file' (the flag)
		if strings.Contains(text, "--events-file") {
			hits = append(hits, rel+": contains --events-file")
		}
		// '--events ' or '--events\t' (the flag, not --emit-events)
		for _, line := range strings.Split(text, "\n") {
			if strings.Contains(line, "--events ") && !strings.Contains(line, "--emit-events") {
				hits = append(hits, rel+": contains '--events ' (not --emit-events): "+strings.TrimSpace(line))
				break
			}
			if strings.Contains(line, "--events\t") && !strings.Contains(line, "--emit-events") {
				hits = append(hits, rel+": contains '--events\\t' (not --emit-events): "+strings.TrimSpace(line))
				break
			}
		}
		// 'EventsJSONL' (the removed constant)
		if strings.Contains(text, "EventsJSONL") {
			hits = append(hits, rel+": contains EventsJSONL")
		}
		// 'EventsText' (the removed constant)
		if strings.Contains(text, "EventsText") {
			hits = append(hits, rel+": contains EventsText")
		}
		// 'ValidEvents' (the removed function)
		if strings.Contains(text, "ValidEvents") {
			hits = append(hits, rel+": contains ValidEvents")
		}
		// DefaultReportPath(..., os.Getpid()) - pid-based report name
		// Check for the actual call pattern, not just both strings in the same file.
		for _, line := range strings.Split(text, "\n") {
			if strings.Contains(line, "os.Getpid()") && strings.Contains(line, "DefaultReportPath") {
				hits = append(hits, rel+": contains DefaultReportPath with os.Getpid(): "+strings.TrimSpace(line))
				break
			}
		}

		return nil
	})
	if err != nil {
		t.Fatalf("walking tree: %v", err)
	}

	for _, h := range hits {
		t.Errorf("stale reference: %s", h)
	}

	// Check TS-06-13's normalisation drops session_id
	reportTest, err := os.ReadFile(filepath.Join(root, "internal", "toolio", "report_test.go"))
	if err != nil {
		t.Fatalf("reading report_test.go: %v", err)
	}
	if !strings.Contains(string(reportTest), `"session_id"`) {
		t.Errorf("TS-06-13 normalisation does not drop session_id")
	}

	// Check docs_events_test.go asserts --emit-events rather than --events/--events-file/jsonl
	eventsTest, err := os.ReadFile(filepath.Join(root, "docs_events_test.go"))
	if err != nil {
		t.Fatalf("reading docs_events_test.go: %v", err)
	}
	if !strings.Contains(string(eventsTest), "--emit-events") {
		t.Errorf("docs_events_test.go does not assert --emit-events")
	}
}
