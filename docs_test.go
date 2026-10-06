package agentfox

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agent-fox-dev/agentfox/internal/toolio"
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

// TS-05-41 (unit): docs/cli.md's error-category table has a retryable column,
// and its warning-codes table has one row per declared warning code, naming the
// code's stage and a legal severity
// Verifies: 05-REQ-6.2
func TestDocsCliRetryableAndWarningCodes_TS_05_41(t *testing.T) {
	doc := readDoc(t, "cli.md")

	// The error-category table: its header names the retryable column, and
	// every row says yes or no in it.
	header := "| Category | Means | `retryable` |"
	i := strings.Index(doc, header)
	if i < 0 {
		t.Fatalf("docs/cli.md has no error-category table headed %q", header)
	}
	rows := 0
	for _, l := range strings.Split(doc[i+len(header):], "\n")[2:] {
		if !strings.HasPrefix(l, "|") {
			break
		}
		rows++
		cells := strings.Split(strings.Trim(l, "| "), "|")
		if last := strings.TrimSpace(cells[len(cells)-1]); last != "yes" && last != "no" {
			t.Errorf("error-category row %q: retryable cell is %q, want yes or no", l, last)
		}
	}
	if rows == 0 {
		t.Error("the error-category table has no rows")
	}

	// The warning-codes table: a Code | Severity | Stage | Tool(s) header and a
	// row for every declared code, with the stage the code is raised from.
	idx := strings.Index(doc, "### Warning codes")
	if idx < 0 {
		t.Fatal("docs/cli.md has no ### Warning codes section")
	}
	table := doc[idx:]
	if end := strings.Index(table[len("### Warning codes"):], "\n#"); end >= 0 {
		table = table[:len("### Warning codes")+end]
	}
	if !strings.Contains(table, "| Code | Severity | Stage |") {
		t.Error("the warning-codes table has no Code | Severity | Stage header")
	}
	for _, code := range toolio.DeclaredWarnCodes() {
		row, ok := tableRow(table, "| `"+string(code)+"` |")
		if !ok {
			t.Errorf("the warning-codes table has no row for %q", code)
			continue
		}
		cells := strings.Split(strings.Trim(row, "| "), "|")
		if len(cells) < 3 {
			t.Errorf("row for %q has too few cells: %s", code, row)
			continue
		}
		severity, stage := strings.TrimSpace(cells[1]), strings.TrimSpace(cells[2])
		if severity != "high" && severity != "low" {
			t.Errorf("%q: severity cell is %q, want high or low", code, severity)
		}
		if want, _ := toolio.WarnStage(code); stage != want {
			t.Errorf("%q: the table says stage %q, the code raises it at stage %q", code, stage, want)
		}
	}
}

// TS-05-42 (unit): docs/cli.md documents --context in the shared-flags table,
// as repeatable, and says where its block goes
// Verifies: 05-REQ-6.3
func TestDocsCliContextFlag_TS_05_42(t *testing.T) {
	shared := docSection(t, readDoc(t, "cli.md"), "Shared flags")
	row, ok := tableRow(shared, "| `--context`")
	if !ok {
		t.Fatal("the shared-flags table has no --context row")
	}
	for _, want := range []string{"repeatable", "## Additional context from the caller", "not to the input itself"} {
		if !strings.Contains(row, want) {
			t.Errorf("the --context row does not say %q: %s", want, row)
		}
	}
}

// TS-05-43 (unit): README.md's exit-code sentence names the matching status
// value for each of the five exit codes, in that sentence
// Verifies: 05-REQ-6.4
func TestReadmeExitCodeStatusNames_TS_05_43(t *testing.T) {
	root := findWorkspaceRoot(t)
	body, err := os.ReadFile(filepath.Join(root, "README.md"))
	if err != nil {
		t.Fatalf("reading README.md: %v", err)
	}
	text := strings.Join(strings.Fields(string(body)), " ")
	i := strings.Index(text, "Exit codes are shared:")
	if i < 0 {
		t.Fatal("README.md has no \"Exit codes are shared:\" sentence")
	}
	sentence := text[i:]
	if end := strings.Index(sentence, ". "); end >= 0 {
		// The sentence ends at the first full stop outside parentheses; the
		// exit-code list runs to "(`status: \"unverified\"`)."
		sentence = sentence[:strings.Index(sentence, "unverified")+len("unverified")+3]
	}
	for code, status := range map[string]string{"0": "done", "1": "failed", "2": "usage", "3": "needs_human", "4": "unverified"} {
		if !strings.Contains(sentence, "`"+code+"`") || !strings.Contains(sentence, `status: "`+status+`"`) {
			t.Errorf("README.md's exit-code sentence does not pair exit %s with status %q:\n%s", code, status, sentence)
		}
	}
}

// docSection returns the text of the level-2 section whose heading line
// contains heading, up to (not including) the next level-2 heading.
func docSection(t *testing.T, doc, heading string) string {
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

// tableRow returns the first line of sec that starts with prefix.
func tableRow(sec, prefix string) (string, bool) {
	for _, l := range strings.Split(sec, "\n") {
		if strings.HasPrefix(l, prefix) {
			return l, true
		}
	}
	return "", false
}

func readDoc(t *testing.T, name string) string {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(findWorkspaceRoot(t), "docs", name))
	if err != nil {
		t.Fatalf("reading docs/%s: %v", name, err)
	}
	return string(body)
}

// TS-06-60 (unit): docs/cli.md's shared-flags table documents
// --detail/--report-file, --input-kind and the relocated --total-budget
// Verifies: 06-REQ-10.1, 06-REQ-10.3, 06-REQ-10.4
func TestTS06_60_SharedFlagsTable(t *testing.T) {
	doc := readDoc(t, "cli.md")
	shared := docSection(t, doc, "Shared flags")

	row, ok := tableRow(shared, "| `--detail`")
	if !ok {
		t.Fatalf("shared-flags table has no --detail row")
	}
	for _, want := range []string{"--report-file", "summary", "full"} {
		if !strings.Contains(row, want) {
			t.Errorf("--detail row does not mention %q", want)
		}
	}

	row, ok = tableRow(shared, "| `--input-kind`")
	if !ok {
		t.Fatalf("shared-flags table has no --input-kind row")
	}
	for _, want := range []string{"file", "text", "issue", "stdin", "usage error", "before"} {
		if !strings.Contains(row, want) {
			t.Errorf("--input-kind row does not mention %q", want)
		}
	}

	row, ok = tableRow(shared, "| `--total-budget`")
	if !ok {
		t.Fatalf("--total-budget is not in the shared-flags table")
	}
	for _, tool := range []string{"triage", "fix", "spec", "impl"} {
		if !strings.Contains(row, tool) {
			t.Errorf("--total-budget row does not state its granularity for %s", tool)
		}
	}
	if _, ok := tableRow(docSection(t, doc, "`impl`"), "| `--total-budget`"); ok {
		t.Errorf("--total-budget is still in impl's own flag table")
	}
	if _, ok := tableRow(shared, "| `--dry-run`"); !ok {
		t.Errorf("shared-flags table has no --dry-run row")
	}
	for _, tool := range []string{"triage", "fix", "spec", "impl"} {
		if _, ok := tableRow(docSection(t, doc, "`"+tool+"`"), "| `--dry-run`"); ok {
			t.Errorf("--dry-run is still in %s's own flag table", tool)
		}
	}
}

// TS-06-61 (unit): docs/cli.md gives each tool's own section a --detail
// summary subset table and the local meaning of --dry-run
// Verifies: 06-REQ-10.2
func TestTS06_61_ToolSectionsSummaryAndDryRun(t *testing.T) {
	doc := readDoc(t, "cli.md")
	kept := map[string][]string{
		"triage": {"action", "repo", "url", "number", "title", "severity", "confidence",
			"affected_files", "labels", "rejected_path_calls"},
		"fix": {"stage", "branch", "base_branch", "commit", "changed_files", "verdict",
			"criteria_outcome", "pull_request_url", "dry_run", "verification"},
		"spec": {"spec_dir", "spec_id", "spec_name", "status", "artifacts", "validation",
			"traceability", "open_questions", "split"},
		"impl": {"stage", "spec_dir", "spec_id", "spec_name", "title", "status", "branch",
			"tasks_total", "tasks_done", "tasks_skipped", "tasks_remaining", "tasks",
			"verdict", "pull_request_url", "verification"},
	}
	for tool, fields := range kept {
		sec := docSection(t, doc, "`"+tool+"`")
		if !strings.Contains(sec, "--detail summary") {
			t.Errorf("%s: section does not introduce its --detail summary subset", tool)
		}
		for _, f := range fields {
			if _, ok := tableRow(sec, "| `"+f+"`"); !ok {
				t.Errorf("%s: summary table has no row for %q", tool, f)
			}
		}
		local := false
		for _, l := range strings.Split(sec, "\n\n") {
			if strings.Contains(l, "--dry-run") && strings.Contains(l, "locally") {
				local = true
			}
		}
		if !local {
			t.Errorf("%s: section does not say what --dry-run still does locally", tool)
		}
	}
}

// TS-06-62 (unit): docs/cli.md's output section gains one worked example
// each for artifacts, side_effects and next
// Verifies: 06-REQ-10.5
func TestTS06_62_OutputExamples(t *testing.T) {
	doc := readDoc(t, "cli.md")
	sec := docSection(t, doc, "The output")
	var blocks []string
	var cur []string
	in := false
	for _, l := range strings.Split(sec, "\n") {
		if strings.HasPrefix(l, "```") {
			if in {
				blocks = append(blocks, strings.Join(cur, "\n"))
				cur = nil
			}
			in = !in
			continue
		}
		if in {
			cur = append(cur, l)
		}
	}
	for _, key := range []string{`"artifacts": [`, `"side_effects": [`, `"next": [`} {
		found := false
		for _, b := range blocks {
			if strings.Contains(b, key) {
				found = true
			}
		}
		if !found {
			t.Errorf("The output section has no worked example containing %s", key)
		}
	}
}

// TS-06-63 (unit): docs/configuration.md documents the state directory,
// covering runs/ and events/, the new file names, and the --report-file override
// Verifies: 06-REQ-10.6
func TestTS06_63_ConfigurationReportFiles(t *testing.T) {
	doc := readDoc(t, "configuration.md")
	if !strings.Contains(doc, "## State directory") {
		t.Errorf("docs/configuration.md has no State directory heading")
	}
	for _, want := range []string{"$XDG_STATE_HOME/agent-fox", ".local/state", "--report-file"} {
		if !strings.Contains(doc, want) {
			t.Errorf("docs/configuration.md does not mention %q", want)
		}
	}
}

// TS-14-21 (unit): the docs describe the --repo-map-tokens flag that
// Common.Register registers, with the default the code registers, and name the
// internal/repomap package in the architecture and development documents
// Verifies: 14-REQ-6.1
func TestTS_14_21_DocsRepoMapTokens(t *testing.T) {
	// The default in the docs is the one the code registers.
	var c toolio.Common
	fs := flag.NewFlagSet("docs", flag.ContinueOnError)
	c.Register(fs)
	f := fs.Lookup("repo-map-tokens")
	if f == nil {
		t.Fatal("--repo-map-tokens is not registered by Common.Register")
	}

	shared := docSection(t, readDoc(t, "cli.md"), "Shared flags")
	row, ok := tableRow(shared, "| `--repo-map-tokens`")
	if !ok {
		t.Fatal("shared-flags table has no --repo-map-tokens row")
	}
	for _, want := range []string{"`" + f.DefValue + "`", "repository map", "user prompt", "`0` disables"} {
		if !strings.Contains(row, want) {
			t.Errorf("--repo-map-tokens row does not mention %q: %s", want, row)
		}
	}

	arch := readDoc(t, "architecture.md")
	for _, want := range []string{"internal/repomap", "repository map", "user prompt", "--repo-map-tokens"} {
		if !strings.Contains(arch, want) {
			t.Errorf("docs/architecture.md does not mention %q", want)
		}
	}

	dev := readDoc(t, "development.md")
	if !strings.Contains(docSection(t, dev, "Go package structure"), "**internal/repomap**") {
		t.Error("docs/development.md's Go package structure has no internal/repomap entry")
	}
	if !strings.Contains(docSection(t, dev, "Repository layout"), "repomap/") {
		t.Error("docs/development.md's repository layout has no repomap/ line")
	}
}
