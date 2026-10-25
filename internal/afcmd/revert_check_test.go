package afcmd

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/agent-fox-dev/agentfox/internal/checks"
	"github.com/agent-fox-dev/agentfox/internal/testutil"
)

// changed is a repository whose change since base fixes lib.txt and adds a test, run by the
// gate, that test says holds: the test itself when it is empty.
func changed(t *testing.T, test string) (dir, base string) {
	t.Helper()
	testutil.Require(t, "git", "sh")
	dir = testutil.Repo(t, map[string]string{"lib.txt": "broken\n"})
	base = strings.TrimSpace(testutil.Git(t, dir, "rev-parse", "HEAD"))
	testutil.WriteFile(t, dir, "lib.txt", "fixed\n")
	testutil.WriteFile(t, dir, "tests/check.sh", test)
	writeGate(t, dir, []string{"tests", "sh", "tests/check.sh"})
	return dir, base
}

func TestRevertCheck_EveryMeasuredOutcomeIsDoneAndOnlyAProofIsNotWarnedOf(t *testing.T) {
	for name, tc := range map[string]struct {
		test    string
		since   string
		summary string
		warned  bool
	}{
		"proven":     {"grep -q fixed lib.txt\n", "", "proven: the tests fail without the change (1 files reverted)", false},
		"not proven": {"exit 0\n", "", "not proven: the tests still pass without the change (1 files reverted)", true},
		"a red tree": {"exit 1\n", "", "inconclusive: after_not_green", true},
		"no change":  {"exit 0\n", "HEAD", "no_changes: ", false},
	} {
		t.Run(name, func(t *testing.T) {
			dir, base := changed(t, tc.test)
			if tc.since == "HEAD" {
				testutil.Commit(t, dir, "the change")
				base = "HEAD"
			}
			tree, err := checks.TreeHash(t.Context(), dir)
			if err != nil {
				t.Fatal(err)
			}

			o := measure(t, "revert-check", "--dir", dir, "--since", base)

			if o.code != 0 || o.Status != "done" || !strings.HasPrefix(o.Summary, tc.summary) || o.warned("fix_not_proven") != tc.warned {
				t.Errorf("%v, summary %q; want done with %q, warned of fix_not_proven: %v", o, o.Summary, tc.summary, tc.warned)
			}
			if after, _ := checks.TreeHash(t.Context(), dir); after != tree {
				t.Error("the tree is not as it was")
			}
		})
	}
}

func TestRevertCheck_RefusesOrFailsWithTheCategoryOfWhatIsWrong(t *testing.T) {
	for name, tc := range map[string]struct {
		prepare  func(t *testing.T, dir string)
		since    string // "" is the base of the change, "-" no --since
		code     int
		category string
		mention  string
	}{
		"no --since":              {nil, "-", 2, "usage", "--since"},
		"a --since of no commit":  {nil, "nosuchref", 2, "usage", "--since"},
		"a gate.json that is not": {func(t *testing.T, dir string) { testutil.WriteFile(t, dir, ".agent-fox/gate.json", "{broken") }, "", 1, "config", "gate.json"},
		"a patch of an earlier check": {func(t *testing.T, dir string) { testutil.WriteFile(t, dir, ".agent-fox/revert.patch", "old patch") }, "", 1, "restore",
			filepath.Join(".agent-fox", "revert.patch")},
		"no git to run": {func(t *testing.T, _ string) { t.Setenv("PATH", t.TempDir()) }, "", 1, "git", ""},
	} {
		t.Run(name, func(t *testing.T) {
			dir, base := changed(t, "grep -q fixed lib.txt\n")
			if tc.prepare != nil {
				tc.prepare(t, dir)
			}
			args := []string{"revert-check", "--dir", dir}
			switch tc.since {
			case "":
				args = append(args, "--since", base)
			case "-":
			default:
				args = append(args, "--since", tc.since)
			}

			o := measure(t, args...)

			if o.code != tc.code || o.category() != tc.category || !strings.Contains(o.message(), tc.mention) {
				t.Errorf("%v; want exit code %d, category %s and a message that mentions %q", o, tc.code, tc.category, tc.mention)
			}
		})
	}
}
