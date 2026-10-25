package skills

import (
	"io/fs"
	"strings"
	"testing"
)

// frontmatter is the keys of the frontmatter of a skill, one "key: value" a line, and the rest.
func frontmatter(t *testing.T, text string) (map[string]string, string) {
	t.Helper()
	head, rest, ok := strings.Cut(strings.TrimPrefix(text, "---\n"), "\n---\n")
	if !ok || !strings.HasPrefix(text, "---\n") {
		t.Fatalf("the skill has no frontmatter: %.40q", text)
	}
	keys := map[string]string{}
	for _, line := range strings.Split(head, "\n") {
		if k, v, ok := strings.Cut(line, ": "); ok {
			keys[k] = strings.Trim(v, `"`)
		}
	}
	return keys, rest
}

func TestEachSkillNamesItselfAndSaysWhatItIsFor(t *testing.T) {
	entries, err := fs.ReadDir(FS, ".")
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, e := range entries {
		data, err := FS.ReadFile(e.Name())
		if err != nil {
			t.Fatal(err)
		}
		keys, rest := frontmatter(t, string(data))
		if len(keys) != 3 || keys["name"] != e.Name() || keys["description"] == "" || keys["argument-hint"] == "" {
			t.Errorf("%s: the frontmatter is %v, want its name, a description and an argument hint", e.Name(), keys)
		}
		if got := body(string(data)); got != rest || !strings.HasPrefix(got, "\n# "+e.Name()+"\n") {
			t.Errorf("%s: the body starts %.40q, want the text after the frontmatter, from its title", e.Name(), got)
		}
		names[e.Name()] = true
	}
	if !names["af-triage"] || !names["af-fix"] {
		t.Errorf("the skills are %v, want af-triage and af-fix among them", names)
	}
}

func TestTriageAndFixAreTheBodiesOfTheirSkills(t *testing.T) {
	for name, got := range map[string]string{"af-triage": Triage(), "af-fix": Fix()} {
		data, err := FS.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, want := frontmatter(t, string(data)); got != want {
			t.Errorf("the body of %s is %.40q, want %.40q", name, got, want)
		}
	}
	if skill("af-none") != "" {
		t.Error("a skill that is not embedded has a body")
	}
}

func TestBodyDropsTheFrontmatterOnly(t *testing.T) {
	for text, want := range map[string]string{
		"---\nname: x\n---\n\n# x\n---\nmore\n": "\n# x\n---\nmore\n",
		"# no frontmatter\n---\n":               "# no frontmatter\n---\n",
		"---\nname: x\nnever closed\n":          "---\nname: x\nnever closed\n",
		"---\n---\nbody":                        "---\n---\nbody",
		"":                                      "",
	} {
		if got := body(text); got != want {
			t.Errorf("body(%q) = %q, want %q", text, got, want)
		}
	}
}
