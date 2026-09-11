package ghapi

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agent-fox-dev/agentfox/issuex"
)

func TestParseIssueURL(t *testing.T) {
	hosts := GitHubHosts("")
	cases := []struct {
		in     string
		want   IssueRef
		wantOK bool
	}{
		{"https://github.com/acme/widgets/issues/42",
			IssueRef{Repo: Repo{Owner: "acme", Name: "widgets"}, Number: 42}, true},
		{"https://www.github.com/acme/widgets/issues/1",
			IssueRef{Repo: Repo{Owner: "acme", Name: "widgets"}, Number: 1}, true},
		{"https://github.com/acme/widgets/pull/7",
			IssueRef{Repo: Repo{Owner: "acme", Name: "widgets"}, Number: 7, IsPullRequest: true}, true},
		{"https://github.com/acme/widgets/issues/42#issuecomment-9",
			IssueRef{Repo: Repo{Owner: "acme", Name: "widgets"}, Number: 42}, true},

		// Not issue references, and therefore treated as text by the caller.
		{"https://github.com/acme/widgets", IssueRef{}, false},
		{"https://github.com/acme/widgets/blob/main/x.go", IssueRef{}, false},
		{"https://github.com/acme/widgets/issues", IssueRef{}, false},
		{"https://github.com/acme/widgets/issues/abc", IssueRef{}, false},
		{"https://github.com/acme/widgets/issues/0", IssueRef{}, false},
		{"https://gitlab.com/acme/widgets/issues/42", IssueRef{}, false},
		{"the widget/ package panics", IssueRef{}, false},
		{"", IssueRef{}, false},
	}
	for _, c := range cases {
		got, ok := parseIssueURL(c.in, hosts)
		if ok != c.wantOK {
			t.Errorf("parseIssueURL(%q) ok = %v, want %v", c.in, ok, c.wantOK)
			continue
		}
		if ok && got != c.want {
			t.Errorf("parseIssueURL(%q) = %+v, want %+v", c.in, got, c.want)
		}
	}
}

// A URL on an Enterprise host is only an issue reference when GITHUB_API_URL
// points at that host. Accepting any host would turn a URL on an unrelated
// forge into a github.com owner/repo that happens to share the name.
func TestParseIssueURLEnterpriseHost(t *testing.T) {
	hosts := GitHubHosts("https://api.ghe.example.com")
	for _, u := range []string{
		"https://ghe.example.com/acme/widgets/issues/42",
		"https://api.ghe.example.com/acme/widgets/issues/42",
	} {
		if _, ok := parseIssueURL(u, hosts); !ok {
			t.Errorf("parseIssueURL(%q): want ok with the enterprise host configured", u)
		}
	}
	if _, ok := parseIssueURL("https://ghe.example.com/acme/widgets/issues/42", GitHubHosts("")); ok {
		t.Error("an enterprise host must not be accepted without GITHUB_API_URL")
	}
}

func TestParseRemote(t *testing.T) {
	hosts := GitHubHosts("")
	cases := []struct {
		in     string
		want   Repo
		wantOK bool
	}{
		{"git@github.com:acme/widgets.git", Repo{Owner: "acme", Name: "widgets"}, true},
		{"https://github.com/acme/widgets.git", Repo{Owner: "acme", Name: "widgets"}, true},
		{"https://github.com/acme/widgets", Repo{Owner: "acme", Name: "widgets"}, true},
		{"ssh://git@github.com/acme/widgets.git", Repo{Owner: "acme", Name: "widgets"}, true},
		{"https://user:token@github.com/acme/widgets.git", Repo{Owner: "acme", Name: "widgets"}, true},
		{"https://github.com:443/acme/widgets", Repo{Owner: "acme", Name: "widgets"}, true},

		{"git@gitlab.com:acme/widgets.git", Repo{}, false},
		{"https://example.com/acme/widgets", Repo{}, false},
		{"/srv/git/widgets.git", Repo{}, false},
		{"", Repo{}, false},
	}
	for _, c := range cases {
		got, ok := parseRemote(c.in, hosts)
		if ok != c.wantOK {
			t.Errorf("parseRemote(%q) ok = %v, want %v", c.in, ok, c.wantOK)
			continue
		}
		if ok && got != c.want {
			t.Errorf("parseRemote(%q) = %+v, want %+v", c.in, got, c.want)
		}
	}
}

func TestParseRepo(t *testing.T) {
	if got, ok := ParseRepo("acme/widgets"); !ok || got != (Repo{Owner: "acme", Name: "widgets"}) {
		t.Errorf("ParseRepo(acme/widgets) = %+v, %v", got, ok)
	}
	if got, ok := ParseRepo("acme/widgets/extra"); !ok || got != (Repo{Owner: "acme/widgets", Name: "extra"}) {
		t.Errorf("ParseRepo(acme/widgets/extra) = %+v, %v", got, ok)
	}
	for _, bad := range []string{"acme", "/widgets", "acme/", ""} {
		if _, ok := ParseRepo(bad); ok {
			t.Errorf("ParseRepo(%q): want rejected", bad)
		}
	}
}

func TestIssueRefURL(t *testing.T) {
	r := IssueRef{Repo: Repo{Owner: "acme", Name: "widgets"}, Number: 42}
	if got := r.URL(); got != "https://github.com/acme/widgets/issues/42" {
		t.Errorf("URL() = %q", got)
	}
	r.IsPullRequest = true
	if got := r.URL(); got != "https://github.com/acme/widgets/pull/42" {
		t.Errorf("pull URL() = %q", got)
	}
	if got := r.String(); got != "acme/widgets#42" {
		t.Errorf("String() = %q", got)
	}
}

// TS-04-34 verifies internal/ghapi provides backwards-compatible type aliases,
// forwarding functions, and deprecation notices.
func TestTS0434_CompatibilityAndDeprecation(t *testing.T) {
	// 1. Check type aliases (04-REQ-9.2)
	var r Repo = issuex.Repo{Owner: "o", Name: "r"}
	var ref IssueRef = issuex.IssueRef{Repo: r, Number: 1}
	if r.Owner != "o" || r.Name != "r" {
		t.Errorf("aliased Repo fields = %+v, want Owner: o, Name: r", r)
	}
	if ref.Repo != r || ref.Number != 1 {
		t.Errorf("aliased IssueRef fields = %+v", ref)
	}

	// 2. Check forwarding functions (04-REQ-9.3)
	r2, ok := ParseRepo("o/r")
	if !ok || r2 != (issuex.Repo{Owner: "o", Name: "r"}) {
		t.Errorf("ParseRepo(o/r) = %+v, %v, want {o r}, true", r2, ok)
	}
	rMulti, ok := ParseRepo("group/subgroup/project")
	if !ok || rMulti != (issuex.Repo{Owner: "group/subgroup", Name: "project"}) {
		t.Errorf("ParseRepo(multi) = %+v, %v", rMulti, ok)
	}

	ref2, ok := ParseIssueURL("https://github.com/o/r/issues/1")
	if !ok || ref2.Number != 1 || ref2.Repo.Owner != "o" || ref2.Repo.Name != "r" {
		t.Errorf("ParseIssueURL = %+v, %v", ref2, ok)
	}

	rRemote, ok := ParseRemote("git@github.com:o/r.git")
	if !ok || rRemote.Owner != "o" || rRemote.Name != "r" {
		t.Errorf("ParseRemote = %+v, %v", rRemote, ok)
	}

	dRepo, dOK := DetectRepo(".")
	ixRepo, ixOK := issuex.DetectRepo(".")
	if dOK != ixOK || dRepo != ixRepo {
		t.Errorf("DetectRepo = (%+v, %v), issuex = (%+v, %v)", dRepo, dOK, ixRepo, ixOK)
	}

	// 3. Check doc deprecation comment in ghapi files (04-REQ-9.1, 04-REQ-9.4)
	findGhapiFile := func(name string) string {
		t.Helper()
		candidates := []string{
			name,
			filepath.Join("internal", "ghapi", name),
			filepath.Join("..", "..", "internal", "ghapi", name),
		}
		for _, p := range candidates {
			if b, err := os.ReadFile(p); err == nil {
				return string(b)
			}
		}
		t.Fatalf("could not find ghapi file %s", name)
		return ""
	}

	docContent := findGhapiFile("doc.go")
	const wantDeprecate = "// Deprecated: use github.com/agent-fox-dev/agentfox/issuex instead."
	if !strings.Contains(docContent, wantDeprecate) {
		t.Errorf("doc.go missing package deprecation notice %q", wantDeprecate)
	}

	clientContent := findGhapiFile("client.go")
	if !strings.Contains(clientContent, wantDeprecate) {
		t.Errorf("client.go missing deprecation notice %q", wantDeprecate)
	}

	refContent := findGhapiFile("ref.go")
	if !strings.Contains(refContent, "// Deprecated: use github.com/agent-fox-dev/agentfox/issuex") {
		t.Errorf("ref.go missing deprecation notices")
	}

	// 4. Verify ghapi.Client methods exist for backwards compatibility (04-REQ-9.4)
	var _ = (*Client)(nil).Authenticated
	var _ = (*Client)(nil).GetRepository
	var _ = (*Client)(nil).ReadIssue
	var _ = (*Client)(nil).CreateIssue
	var _ = (*Client)(nil).UpdateIssue
	var _ = (*Client)(nil).AddComment
	var _ = (*Client)(nil).AddLabels
	var _ = (*Client)(nil).ReadPullRequest
	var _ = (*Client)(nil).CreatePullRequest
}
