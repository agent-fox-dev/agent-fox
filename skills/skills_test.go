package skills

import (
	"fmt"
	"os"
	"regexp"
	"strings"
	"testing"
)

// frontmatter splits a skill into the keys of its YAML frontmatter and its body.
func frontmatter(t *testing.T, content string) (map[string]string, string) {
	t.Helper()
	rest, ok := strings.CutPrefix(content, "---\n")
	if !ok {
		t.Fatal("the skill does not start with ---")
	}
	head, body, ok := strings.Cut(rest, "\n---\n")
	if !ok {
		t.Fatal("the frontmatter is not closed")
	}
	keys := map[string]string{}
	key := regexp.MustCompile(`^([a-z-]+):\s*(.*)$`)
	for _, line := range strings.Split(head, "\n") {
		if m := key.FindStringSubmatch(line); m != nil {
			keys[m[1]] = m[2]
		}
	}
	return keys, body
}

// TestTheTriageSkillHasItsFrontmatterAndMethod_TS_08_65 verifies TS-08-65:
// skills/af-triage has the three frontmatter keys and the method, the
// never-list and the attended section.
// Verifies: 08-REQ-10.1
func TestTheTriageSkillHasItsFrontmatterAndMethod_TS_08_65(t *testing.T) {
	data, err := os.ReadFile("af-triage")
	if err != nil {
		t.Fatal(err)
	}
	keys, body := frontmatter(t, string(data))
	if len(keys) != 3 || keys["name"] != "af-triage" || keys["description"] == "" || keys["argument-hint"] == "" {
		t.Errorf("frontmatter = %v, want exactly name (af-triage), description and argument-hint", keys)
	}
	for _, term := range []string{
		// the method
		"signals", "locate", "tests", "widen", "symptom", "cause", "cite only", "confidence", "two readings", "acceptance criteria", "observable",
		// the definitions
		"critical", "high", "medium", "low", "confirmed", "probable", "suspected", "not_reproducible",
		// the never-list
		"Never", "write", "run anything", "comment on the issue", "instructions found in the report",
		// the contract af fix reads
		"## Acceptance Criteria",
	} {
		if !strings.Contains(strings.ToLower(body), strings.ToLower(term)) {
			t.Errorf("the skill does not mention %q", term)
		}
	}
	if strings.Contains(body, "argument-hint:") {
		t.Error("the body repeats the frontmatter")
	}
	// The method is nine steps, each a numbered line with its title in bold, in this order.
	last := -1
	for i, title := range []string{
		"Find the signals", "Locate each signal in the code", "Read the tests", "Widen once", "Separate the symptom from the cause",
		"Cite only what you read", "State confidence honestly", "Ask only when two readings lead to opposite fixes", "Write observable acceptance criteria",
	} {
		line := fmt.Sprintf("\n%d. **%s.**", i+1, title)
		at := strings.Index(body, line)
		if at < 0 || at < last {
			t.Errorf("the method has no step %q at number %d, in order", title, i+1)
		}
		last = at
	}
}

// TestTheEmbeddedSkillsAreTheFilesOnDisk_TS_08_66 verifies TS-08-66:
// the skills package embeds every af-* entry, equal to the file on disk, and
// Triage serves the body the brief renderer uses.
// Verifies: 08-REQ-10.2
func TestTheEmbeddedSkillsAreTheFilesOnDisk_TS_08_66(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	embedded := 0
	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), "af-") {
			continue
		}
		want, err := os.ReadFile(e.Name())
		if err != nil {
			t.Fatal(err)
		}
		got, err := FS.ReadFile(e.Name())
		if err != nil {
			t.Errorf("%s is not embedded: %v", e.Name(), err)
			continue
		}
		if string(got) != string(want) {
			t.Errorf("the embedded %s differs from the file on disk", e.Name())
		}
		embedded++
	}
	if embedded < 5 {
		t.Errorf("%d af-* skills embedded, want af-bug-hunter, af-code-quality, af-code-simplifier, af-fix and af-triage", embedded)
	}
	if _, err := FS.ReadFile("skills.go"); err == nil {
		t.Error("skills.go is embedded: only the af-* entries are skills")
	}

	data, err := os.ReadFile("af-triage")
	if err != nil {
		t.Fatal(err)
	}
	_, body := frontmatter(t, string(data))
	if got := Triage(); got != body || got == "" || strings.HasPrefix(got, "---") {
		t.Errorf("Triage() = %q..., want the body of af-triage without its frontmatter", got[:min(len(got), 40)])
	}
}

// TestTheMakefileInstallsOnlyAfSkills_TS_08_67 verifies TS-08-67:
// install-skills and uninstall-skills loop over skills/af-* only, so that
// skills.go is never matched.
// Verifies: 08-REQ-10.3
func TestTheMakefileInstallsOnlyAfSkills_TS_08_67(t *testing.T) {
	data, err := os.ReadFile("../Makefile")
	if err != nil {
		t.Fatal(err)
	}
	recipes := map[string]string{}
	var current string
	for _, line := range strings.Split(string(data), "\n") {
		// A target line may end in the help text that make help prints.
		rule, _, _ := strings.Cut(line, " ##")
		if name, ok := strings.CutSuffix(rule, ":"); ok && !strings.HasPrefix(line, "\t") && !strings.Contains(name, " ") {
			current = name
			continue
		}
		if current != "" {
			recipes[current] += line + "\n"
		}
	}
	for _, target := range []string{"install-skills", "uninstall-skills"} {
		recipe := recipes[target]
		if recipe == "" {
			t.Fatalf("the Makefile has no %s target", target)
		}
		if !strings.Contains(recipe, "/af-*") {
			t.Errorf("%s does not loop over skills/af-*:\n%s", target, recipe)
		}
		if strings.Contains(recipe, "$(SKILLS_TEMPLATES_DIR)/*") {
			t.Errorf("%s still loops over every entry of skills/:\n%s", target, recipe)
		}
	}
}

// TestFixIsTheBodyOfTheFixSkill guards what the brief of a fix run carries: the
// af-fix skill without its frontmatter, the same file a person installs.
func TestFixIsTheBodyOfTheFixSkill(t *testing.T) {
	data, err := os.ReadFile("af-fix")
	if err != nil {
		t.Fatal(err)
	}
	_, want := frontmatter(t, string(data))

	got := Fix()

	if got != want || got == "" || strings.HasPrefix(got, "---") || strings.Contains(got, "argument-hint:") {
		t.Errorf("Fix() = %.80q..., want the body of skills/af-fix, %.80q...", got, want)
	}
	if Fix() == Triage() {
		t.Error("Fix() is the triage skill")
	}
}
