package policy

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestPathRule_DeniesAWriteIntoAProtectedPath: a redirection or a command that
// writes, moves, links or removes a protected path, resolved against the
// checkout, is denied; reading one, or writing beside one, is not.
func TestPathRule_DeniesAWriteIntoAProtectedPath(t *testing.T) {
	var cases []expectation
	for _, c := range []string{
		"echo x > .specs/a.md", "echo x >> ./.github/workflows/ci.yml", "rm .agent-fox/gate/labels.json", "mv a .git/hooks/x",
		"tee .gitlab-ci.yml", "touch .specs/n", "mkdir .specs/d", "truncate -s0 .git/x", "ln -s a .specs/l", "chmod +x .git/x", "sed -i s/a/b/ .specs/x.md",
		"echo x 2> .git/log", "echo x &> .specs/o", "echo x >| .specs/o", "echo x 1>> .specs/o",
		"echo x | tee -a .git/config", "tee .git/a .specs/b", "rm -rf .git", "rm -rf .specs/", "rm -f src/a.go .specs/b", "rm -- .specs/a", "mv .specs/a /tmp/b", "mv a b .git/",
		"cp -r a .specs/", "cp -t .specs/ a b", "cp --target-directory=.specs a", "ln a .git/hook", "ln -sf a b .specs/dir", "chmod 600 .git/config", "chmod -R u+w .specs",
		"sed -i.bak s/a/b/ .specs/x", "sed -ni p .specs/x", "sed -e s/a/b/ -i .specs/x", "sed --in-place s/a/b/ .specs/x", "sed --in-place=.b s/a/b/ .specs/x",
		"truncate --size=0 .specs/x", "mkdir -p .specs/a/b", "touch -t 200001010000 .git/x", "echo x > .gitlab-ci.yml", "echo x > .git/HEAD", "rm .agent-fox/gate/*/result.json",
	} {
		cases = append(cases, denied(c, RuleProtectedPath))
	}
	for _, c := range []string{
		"cat .specs/x.md", "echo x > src/a.go", "sed s/a/b/ .specs/x.md", "sed -n p .specs/x.md", "sed --expression=p .specs/x", "echo x > .specs/../src/a.go", "cat < .specs/x", "ls .specs",
		"cp .specs/a /tmp/b", "cp .specs/a .specs/b src/", "rm -rf src", "echo x > /tmp/out", "echo x > /work/other/.specs/a", "touch ../repo2/.specs/x",
		"echo x > .specsx/a", "echo x > a.specs", "echo x > .gitignore", "echo x > .github/CODEOWNERS", "echo x > .gitlab-ci.yml.bak", "mkdir src/.specs", "touch .github",
		"grep x .git/config", "git status > /dev/null", "echo x 2>&1", "echo x >&2", "echo x >&-", "tee", "rm", "echo '> .specs/a'", `echo "rm .specs/a"`, "ls > ./out.txt",
	} {
		cases = append(cases, allowed(c))
	}
	requireDecisions(t, defaultPolicy(), cases)

	d := bash(defaultPolicy(), "echo x > .specs/a.md")
	if !strings.Contains(d.Reason, ".specs/a.md") || !strings.Contains(d.Reason, "only af writes there") {
		t.Errorf("reason = %q, want it to name the path and say only af writes there", d.Reason)
	}
}

// TestPathRule_ProtectsThePathsOfThePolicyHoweverTheyAreSpelled: the policy's
// own list is what is protected, an entry is cleaned before it is matched, and
// an empty list protects nothing.
func TestPathRule_ProtectsThePathsOfThePolicyHoweverTheyAreSpelled(t *testing.T) {
	p := defaultPolicy()
	p.ProtectedPaths = []string{"docs/frozen/", "LICENSE"}
	requireDecisions(t, p, []expectation{
		denied("echo x > docs/frozen/a", RuleProtectedPath),
		denied("rm LICENSE", RuleProtectedPath),
		allowed("echo x > .specs/a"),
		allowed("echo x > docs/other"),
	})
	p.ProtectedPaths = nil
	requireDecisions(t, p, []expectation{allowed("echo x > .specs/a")})

	for _, entry := range []string{"docs", "docs/", "./docs/", "docs//", "docs/.", "a/../docs", "./docs", "DOCS/"} {
		requireDecisions(t, Policy{ProtectedPaths: []string{entry}}, []expectation{
			denied("echo x > docs/a.md", RuleProtectedPath),
			denied("rm -rf docs", RuleProtectedPath),
			denied("rm -rf *", RuleProtectedPath),
			denied("rm -rf d*", RuleProtectedPath),
			allowed("echo x > src/a.md"),
			allowed("echo x > docsx/a.md"),
		})
	}
	// An entry that is the checkout itself protects all of it.
	requireDecisions(t, Policy{ProtectedPaths: []string{"./"}}, []expectation{denied("echo x > a", RuleProtectedPath)})
}

// TestPathRule_FollowsLinksAndOtherSpellingsOfTheCheckout: a path that leads
// to a protected one by a link, or names the checkout by a link, is protected,
// for a shell and for the tools that write files.
func TestPathRule_FollowsLinksAndOtherSpellingsOfTheCheckout(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("links need privileges there")
	}
	real, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	checkout := filepath.Join(real, "repo")
	for _, d := range []string{".specs", "src"} {
		if err := os.MkdirAll(filepath.Join(checkout, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	alias := filepath.Join(real, "alias")
	for link, target := range map[string]string{filepath.Join(checkout, "link"): ".specs", filepath.Join(checkout, "src", "up"): "../.specs", alias: checkout} {
		if err := os.Symlink(target, link); err != nil {
			t.Fatal(err)
		}
	}
	specs, src := filepath.Join(checkout, ".specs", "x.md"), filepath.Join(checkout, "src", "a.go")

	for _, tc := range []struct {
		dir, tool, arg, want string
	}{
		{checkout, "execute", "echo x > link/x.md", Deny},
		{checkout, "execute", "rm link/x.md", Deny},
		{checkout, "execute", "rm -rf link", Deny},
		{checkout, "execute", "echo x > src/up/x.md", Deny},
		{checkout, "execute", "cd src && echo x > up/x.md", Deny},
		{checkout, "execute", "echo x > " + specs, Deny},
		{alias, "execute", "echo x > " + specs, Deny},
		{alias, "execute", "echo x > .specs/x.md", Deny},
		{checkout, "execute", "echo x > " + filepath.Join(alias, ".specs", "x.md"), Deny},
		{checkout, "execute", "echo x > src/a.go", Allow},
		{alias, "execute", "echo x > src/a.go", Allow},
		{alias, "execute", "echo x > " + src, Allow},
		{checkout, "write_file", "link/x.md", Deny},
		{checkout, "write_file", filepath.Join(checkout, "link", "x.md"), Deny},
		{alias, "write_file", specs, Deny},
		{checkout, "write_file", filepath.Join(alias, ".specs", "x.md"), Deny},
		{checkout, "write_file", "src/up/x.md", Deny},
		{checkout, "write_file", "src/a.go", Allow},
		{alias, "write_file", src, Allow},
		{checkout, "write_file", filepath.Join(alias, "src", "new", "deep.go"), Allow},
	} {
		field := map[string]string{"execute": "command", "write_file": "path"}[tc.tool]
		d := Decide(defaultPolicy(), tc.dir, tc.tool, map[string]any{field: tc.arg})
		if d.Decision != tc.want || tc.want == Deny && d.Rule != RuleProtectedPath {
			t.Errorf("%s in %s: %s: %+v, want %s", tc.tool, tc.dir, tc.arg, d, tc.want)
		}
	}
}

// TestDecideWrite_DeniesAProtectedPathAndAPlaceOutsideTheCheckout: a tool
// that writes a file may write in the checkout and the system temporary
// directory, and not into a protected path.
func TestDecideWrite_DeniesAProtectedPathAndAPlaceOutsideTheCheckout(t *testing.T) {
	tmp := filepath.Join(os.TempDir(), "af-policy-test", "x.txt")
	for _, tool := range []string{"write_file", "edit_file"} {
		for _, tc := range []struct {
			path, decision, rule string
		}{
			{".specs/x.md", Deny, RuleProtectedPath},
			{dir + "/.git/config", Deny, RuleProtectedPath},
			{"./.github/workflows/ci.yml", Deny, RuleProtectedPath},
			{".gitlab-ci.yml", Deny, RuleProtectedPath},
			{".agent-fox/gate/labels.json", Deny, RuleProtectedPath},
			{"src/../.specs/x", Deny, RuleProtectedPath},
			{".SPECS/x.md", Deny, RuleProtectedPath},
			{".specs/../.specs/x.md", Deny, RuleProtectedPath},
			{"/work/repo/.Git/x", Deny, RuleProtectedPath},
			{"./.specs/./x.md", Deny, RuleProtectedPath},
			{"/etc/x", Deny, RuleOutsideDir},
			{"../outside.txt", Deny, RuleOutsideDir},
			{"/work/repo2/a.go", Deny, RuleOutsideDir},
			{"/work/repo/../repo2/a.go", Deny, RuleOutsideDir},
			{"src/a.go", Allow, ""},
			{"a.go", Allow, ""},
			{dir + "/src/a.go", Allow, ""},
			{"docs/guide.md", Allow, ""},
			{".specsx/a", Allow, ""},
			{".github/CODEOWNERS", Allow, ""},
			{tmp, Allow, ""},
		} {
			d := Decide(defaultPolicy(), dir, tool, map[string]any{"path": tc.path, "content": "x"})
			if d.Decision != tc.decision || d.Rule != tc.rule {
				t.Errorf("%s %s: %+v, want %s by %q", tool, tc.path, d, tc.decision, tc.rule)
			}
			if d.Decision == Deny && !strings.Contains(d.Reason, tc.path) {
				t.Errorf("%s %s: a denial that does not name the path: %+v", tool, tc.path, d)
			}
			requireShape(t, tc.path, d)
		}
	}

	// A call that names no path in its own field cannot be judged.
	for _, in := range []map[string]any{nil, {}, {"path": 7}, {"path": ""}, {"path": "  "}, {"file_path": ".specs/x"}} {
		if d := Decide(defaultPolicy(), dir, "write_file", in); d.Decision != Deny || d.Rule != RuleUnparseable || d.Segment == "" || d.Reason == "" {
			t.Errorf("write_file with %v: %+v, want a denial by unparseable", in, d)
		}
	}
	// No protected paths protect none, and the checkout is still kept.
	if d := write(Policy{}, ".specs/x"); d.Decision != Allow {
		t.Errorf("no protected paths: %+v", d)
	}
	if d := write(Policy{}, "/etc/x"); d.Rule != RuleOutsideDir {
		t.Errorf("no protected paths, outside the checkout: %+v", d)
	}
	// A checkout of "" is the current directory.
	if d := Decide(defaultPolicy(), "", "write_file", map[string]any{"path": ".specs/x"}); d.Rule != RuleProtectedPath {
		t.Errorf("in the current directory: %+v", d)
	}
}

// TestDecideAt_JudgesARelativePathWhereTheSessionIs: from a directory below
// the checkout a relative path is judged there and against the checkout; a
// directory outside the checkout is not a place to judge from.
func TestDecideAt_JudgesARelativePathWhereTheSessionIs(t *testing.T) {
	cwd := dir + "/sub"
	for _, tc := range []struct {
		cwd, path, decision, rule string
	}{
		{cwd, "../.specs/x", Deny, RuleProtectedPath},
		{cwd, "a.go", Allow, ""},
		{cwd, "../src/a.go", Allow, ""},
		{cwd, ".specs/x", Deny, RuleProtectedPath},
		{cwd, "../../outside", Deny, RuleOutsideDir},
		{"sub", "../.specs/x", Deny, RuleProtectedPath},
		{"", ".specs/x", Deny, RuleProtectedPath},
		{dir, "a.go", Allow, ""},
		{"/elsewhere", "a.go", Allow, ""},
	} {
		d := DecideAt(defaultPolicy(), dir, tc.cwd, "write_file", map[string]any{"path": tc.path})
		if d.Decision != tc.decision || d.Rule != tc.rule {
			t.Errorf("write_file %s from %q: %+v, want %s by %q", tc.path, tc.cwd, d, tc.decision, tc.rule)
		}
	}
	if d := DecideAt(defaultPolicy(), dir, cwd, "execute", map[string]any{"command": "echo x > ../.specs/a"}); d.Rule != RuleProtectedPath {
		t.Errorf("a redirection from a subdirectory: %+v", d)
	}
}

// TestIsProtected_IsTheRuleAWriteIsJudgedBy: the paths of a change that are
// protected, by the rule that judges a write.
func TestIsProtected_IsTheRuleAWriteIsJudgedBy(t *testing.T) {
	for rel, want := range map[string]bool{
		".specs/a.md": true, ".specs/deep/er/b.md": true, ".specs": true, ".agent-fox/gate.json": true, ".git/config": true,
		".github/workflows/ci.yml": true, ".gitlab-ci.yml": true, ".SPECS/a.md": true, ".Github/Workflows/ci.yml": true,
		"./.specs/a.md": true, "src/../.specs/a.md": true,
		".github/CODEOWNERS": false, ".github": false, "src/.specs/a.md": false, ".gitlab-ci.yml.bak": false, "specs/a.md": false, "README.md": false, ".gitignore": false,
	} {
		if got := IsProtected(DefaultProtectedPaths(), rel); got != want {
			t.Errorf("IsProtected(%q) = %v, want %v", rel, got, want)
		}
	}
	if IsProtected(nil, ".specs/a.md") || IsProtected([]string{"docs/"}, ".specs/a.md") || !IsProtected([]string{"docs/"}, "docs/a.md") || !IsProtected([]string{"go.lock"}, "go.lock") {
		t.Error("IsProtected does not follow the list it is given")
	}
}
