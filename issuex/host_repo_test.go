package issuex

import "testing"

// An issue URL's repo (host github.com / gitlab.com) must resolve to the
// forge's API base URL, not the web host.
func TestNewWithOptions_RepoHostResolvesAPIBase(t *testing.T) {
	t.Setenv("GITHUB_API_URL", "")
	t.Setenv("GITLAB_API_URL", "")
	t.Setenv("GITHUB_TOKEN", "")
	t.Setenv("GH_TOKEN", "")
	t.Setenv("GITLAB_TOKEN", "")

	gh, err := NewWithOptions(Options{Repo: Repo{Host: "github.com", Owner: "o", Name: "r"}})
	if err != nil {
		t.Fatal(err)
	}
	if got := gh.(*githubClient).baseURL; got != "https://api.github.com" {
		t.Errorf("github baseURL = %q, want https://api.github.com", got)
	}

	gl, err := NewWithOptions(Options{Repo: Repo{Host: "gitlab.com", Owner: "g", Name: "p"}})
	if err != nil {
		t.Fatal(err)
	}
	if got := gl.(*gitlabClient).baseURL; got != "https://gitlab.com/api/v4" {
		t.Errorf("gitlab baseURL = %q, want https://gitlab.com/api/v4", got)
	}
}
