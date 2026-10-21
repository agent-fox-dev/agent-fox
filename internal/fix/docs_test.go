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

// docNeeds are the terms that each document of the scope has to contain.
var docNeeds = map[string][]string{
	"docs/cli.md": {
		// the five commands, each under a heading of its own
		"\n### fix\n", "\n### fix brief\n", "\n### fix run\n", "\n### fix submit\n", "\n### fix land\n",
		// the flags
		"--land", "--attempts", "--no-verify", "--base", "--run-id", "--dry-run", "--repo", "--context", "--brain", "--sandbox", "--budget", "--max-turns", "--timeout",
		// the defaults
		"`--brain` to `agentkit`", "`--sandbox` to the provider that\n`AF_SANDBOX` names, and without it `landlock` on Linux and `podman` anywhere else", "`--budget` to `10`", "`--max-turns` to `150`", "`--attempts` to `2`", "`--land` to `pr`", "45 minutes",
		// the result
		"attempts_max", "revert_check", "reverted_paths", "issue_created", "pull_request", "changed_files", "pass_was_already_failing",
		// the warnings
		"| `baseline_failing` |", "| `issue_closed` |", "| `question_not_found` |", "| `protected_path_reverted` |", "| `scope_mismatch` |", "| `fix_not_proven` |",
		"| `push_retried` |", "| `criteria_unmet` |", "| `pull_request_failed` |", "| `fix_not_landed` |", "| `not_verified` |", "| `attempt_failed` |",
		"| `commit_hook_failed` |", "| `no_forge_token` |", "| `park_failed` |", "| `push_failed` |", "| `issue_creation_failed` |", "| `pull_request_skipped` |",
		"| `dry_run_tree_kept` |", "| `question_tree_kept` |", "| `report_unwritable` |",
		// the category and the exit codes
		"| `preflight` |", "How a fix run ends", "needs_human", "unverified",
		// the records
		"`fix.brief`", "`fix.run`", "`fix.question`", "`apply`", "`measure`", "`refusal`",
	},
	"docs/configuration.md": {
		// the two files of the box, and the token that --land pr needs
		"the table of checks the run was pinned to", "the policy of the tool gate", "`.agent-fox/gate.json`", "`.agent-fox/guard.json`",
		"`af fix --land pr`: opening a pull request needs `GITHUB_TOKEN`", "`GITLAB_TOKEN` for a GitLab one",
		// the files of a run
		"### The files of a fix run", "fix.json", "brief.md", "original.md", "report.md", "submission.json",
	},
	"docs/model-usage.md": {
		"af fix", "mandate", "USD 10", "150 turns", "45 minutes", "per attempt", "--attempts", "round", "A dry run spends model money",
	},
	"docs/architecture.md": {
		"Fix, as built", "internal/fix", "Apply", "Measure", "fix.json", "report.md", "`codefix` | `af fix`", "af fix brief",
	},
	"README.md": {"| `internal/fix/` |", "`af fix`"},
}

// TestTheDocumentsDescribeFix_TS_09_72 verifies TS-09-72: the documents that the steering
// file names say what af fix is, in the words its surfaces use, and no longer say that it
// is yet to come.
// Verifies: 09-REQ-10.6
func TestTheDocumentsDescribeFix_TS_09_72(t *testing.T) {
	for file, terms := range docNeeds {
		text := repoFile(t, file)
		for _, term := range terms {
			if !strings.Contains(text, term) {
				t.Errorf("%s does not contain %q", file, term)
			}
		}
	}

	// What the documents said of the fix that was to come is rewritten.
	for file, stale := range map[string][]string{
		"docs/architecture.md": {"af fix start", "submit-analysis"},
		"docs/cli.md":          {"`af fix` in a later one", "The other commands of later specs (`fix`", "This release registers eleven commands"},
	} {
		text := repoFile(t, file)
		for _, term := range stale {
			if strings.Contains(text, term) {
				t.Errorf("%s still says %q", file, term)
			}
		}
	}
}
