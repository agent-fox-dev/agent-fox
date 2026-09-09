package ghapi

import "testing"

func TestParseIssueURL(t *testing.T) {
	hosts := GitHubHosts("")
	cases := []struct {
		in     string
		want   IssueRef
		wantOK bool
	}{
		{"https://github.com/acme/widgets/issues/42",
			IssueRef{Repo: Repo{"acme", "widgets"}, Number: 42}, true},
		{"https://www.github.com/acme/widgets/issues/1",
			IssueRef{Repo: Repo{"acme", "widgets"}, Number: 1}, true},
		{"https://github.com/acme/widgets/pull/7",
			IssueRef{Repo: Repo{"acme", "widgets"}, Number: 7, IsPullRequest: true}, true},
		{"https://github.com/acme/widgets/issues/42#issuecomment-9",
			IssueRef{Repo: Repo{"acme", "widgets"}, Number: 42}, true},

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
		{"git@github.com:acme/widgets.git", Repo{"acme", "widgets"}, true},
		{"https://github.com/acme/widgets.git", Repo{"acme", "widgets"}, true},
		{"https://github.com/acme/widgets", Repo{"acme", "widgets"}, true},
		{"ssh://git@github.com/acme/widgets.git", Repo{"acme", "widgets"}, true},
		{"https://user:token@github.com/acme/widgets.git", Repo{"acme", "widgets"}, true},
		{"https://github.com:443/acme/widgets", Repo{"acme", "widgets"}, true},

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
	if got, ok := ParseRepo("acme/widgets"); !ok || got != (Repo{"acme", "widgets"}) {
		t.Errorf("ParseRepo(acme/widgets) = %+v, %v", got, ok)
	}
	for _, bad := range []string{"acme", "acme/widgets/extra", "/widgets", "acme/", ""} {
		if _, ok := ParseRepo(bad); ok {
			t.Errorf("ParseRepo(%q): want rejected", bad)
		}
	}
}

func TestIssueRefURL(t *testing.T) {
	r := IssueRef{Repo: Repo{"acme", "widgets"}, Number: 42}
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
