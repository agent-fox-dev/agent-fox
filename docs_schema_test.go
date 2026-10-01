package agentfox

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const adrVersionFile = "06-version-the-envelope-interface.md"

func readADR(t *testing.T) string {
	t.Helper()
	body, err := os.ReadFile(filepath.Join(findWorkspaceRoot(t), "docs", "adr", adrVersionFile))
	if err != nil {
		t.Fatalf("reading docs/adr/%s: %v", adrVersionFile, err)
	}
	return string(body)
}

// TS-09-33 (unit): The written compatibility rule classifies the four named
// kinds of envelope change as additive
// Verifies: 09-REQ-5.5
func TestTS09_33_ADRAdditiveKinds(t *testing.T) {
	text := readADR(t)
	for _, phrase := range []string{"new top-level", "new field on an existing object", "open set", "new event type"} {
		if !strings.Contains(text, phrase) {
			t.Errorf("ADR missing additive phrase %q", phrase)
		}
	}
}

// TS-09-34 (unit): The written compatibility rule classifies removal,
// renaming, type change and enum narrowing as breaking
// Verifies: 09-REQ-5.6
func TestTS09_34_ADRBreakingKinds(t *testing.T) {
	text := readADR(t)
	for _, phrase := range []string{"removing", "renaming", "changing a field's type", "narrowing", "major"} {
		if !strings.Contains(text, phrase) {
			t.Errorf("ADR missing breaking phrase %q", phrase)
		}
	}
}

// TS-09-40 (unit): docs/cli.md's shared-flags table has a --schema row
// describing its default and effect
// Verifies: 09-REQ-7.1
func TestTS09_40_SharedFlagsSchemaRow(t *testing.T) {
	shared := docSection(t, readDoc(t, "cli.md"), "Shared flags")
	row, ok := tableRow(shared, "| `--schema`")
	if !ok {
		t.Fatalf("shared-flags table has no --schema row")
	}
	if !strings.Contains(row, "false") {
		t.Errorf("--schema row does not state its default is false: %s", row)
	}
	if !strings.Contains(row, "self-description") {
		t.Errorf("--schema row does not describe the self-description document: %s", row)
	}
}

// TS-09-41 (unit): docs/cli.md has a Self-description (--schema) section with
// a worked example and an explanation of flags, result and exit_codes
// Verifies: 09-REQ-7.2
func TestTS09_41_SelfDescriptionSection(t *testing.T) {
	section := docSection(t, readDoc(t, "cli.md"), "Self-description")
	if !strings.Contains(section, "```jsonc") && !strings.Contains(section, "```json") {
		t.Errorf("Self-description section has no JSON example")
	}
	for _, word := range []string{"flags", "result", "exit_codes", "schema_version"} {
		if !strings.Contains(section, word) {
			t.Errorf("Self-description section does not mention %q", word)
		}
	}
}

// TS-09-42 (unit): docs/cli.md has an Interface versions section listing
// 1.0.0 and 2.0.0 with the additive/breaking rule
// Verifies: 09-REQ-7.3
func TestTS09_42_InterfaceVersionsSection(t *testing.T) {
	section := docSection(t, readDoc(t, "cli.md"), "Interface versions")
	for _, word := range []string{"1.0.0", "2.0.0", "additive", "breaking"} {
		if !strings.Contains(section, word) {
			t.Errorf("Interface versions section does not mention %q", word)
		}
	}
}

// TS-09-43 (unit): the ADR records context, decision and consequence as an
// accepted decision
// Verifies: 09-REQ-7.4
func TestTS09_43_ADRStructure(t *testing.T) {
	text := readADR(t)
	for _, word := range []string{"ccepted", "Context", "Decision", "Consequence"} {
		if !strings.Contains(text, word) {
			t.Errorf("ADR missing %q", word)
		}
	}
}
