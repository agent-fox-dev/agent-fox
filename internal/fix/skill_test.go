package fix

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// repoFile returns a file of the repository, given by its path from the root.
func repoFile(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", filepath.FromSlash(name)))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// skillBody is the skill without its frontmatter, and the frontmatter's keys.
func skillBody(t *testing.T, name string) (keys []string, body string) {
	t.Helper()
	text := repoFile(t, name)
	rest, ok := strings.CutPrefix(text, "---\n")
	if !ok {
		t.Fatalf("%s does not start with ---", name)
	}
	head, body, ok := strings.Cut(rest, "\n---\n")
	if !ok {
		t.Fatalf("the frontmatter of %s is not closed", name)
	}
	key := regexp.MustCompile(`(?m)^([a-z-]+):`)
	for _, m := range key.FindAllStringSubmatch(head, -1) {
		keys = append(keys, m[1])
	}
	return keys, body
}

// TestTheFixSkillIsTheToolbeltFlow_TS_09_71 verifies TS-09-71: skills/af-fix names the
// three commands of the flow in order and the gate, says that the agent never commits,
// pushes, branches or edits the issue, uses the branch form af makes, and has no
// instruction to do any of those things itself.
// Verifies: 09-REQ-10.5
func TestTheFixSkillIsTheToolbeltFlow_TS_09_71(t *testing.T) {
	keys, body := skillBody(t, "skills/af-fix")
	if strings.Join(keys, ",") != "name,description,argument-hint" {
		t.Errorf("frontmatter keys = %v, want name, description and argument-hint", keys)
	}
	text := repoFile(t, "skills/af-fix")

	last := -1
	for _, command := range []string{"af fix brief", "af fix submit", "af fix land"} {
		at := strings.Index(body, command)
		switch {
		case at < 0:
			t.Errorf("the skill does not mention %q", command)
		case at < last:
			t.Errorf("%q comes before the command it follows", command)
		default:
			last = at
		}
	}
	for _, term := range []string{"af gate run", "fix/<N>-<slug>"} {
		if !strings.Contains(body, term) {
			t.Errorf("the skill does not mention %q", term)
		}
	}

	// What the agent never does is said in the never-list, one line for each.
	_, never, ok := strings.Cut(body, "\n## Never\n")
	if !ok {
		t.Fatal("the skill has no ## Never section")
	}
	never, _, _ = strings.Cut(never, "\n## ")
	for _, term := range []string{"commit", "push", "branch", "edit the issue", "comment on the issue"} {
		if !strings.Contains(strings.ToLower(never), term) {
			t.Errorf("the never-list does not say that the agent never does %q", term)
		}
	}

	// Nothing in the skill, the frontmatter included, tells the agent to do what af does.
	for _, bad := range []string{"git commit", "git push", "git checkout -b", "git switch -c", "git branch ", "gh pr create", "glab mr create", "open a pull request", "open the pull request"} {
		if strings.Contains(strings.ToLower(text), bad) {
			t.Errorf("the skill contains %q", bad)
		}
	}
	// The method of the old skill is kept: read, refuse early, diagnose, test first, verify.
	for _, heading := range []string{"## 1. Read the report", "## 2. Refuse early", "## 3. Diagnose", "## 4. Fix, test first", "## 5. Verify"} {
		if !strings.Contains(body, "\n"+heading+"\n") {
			t.Errorf("the skill lost the section %q", heading)
		}
	}
}
