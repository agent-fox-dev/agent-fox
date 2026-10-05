package agentfox

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Every erratum under docs/errata is linked from docs/README.md, so a reader of
// the index finds each divergence from a specification.
func TestEveryErratumIsIndexed(t *testing.T) {
	root := findWorkspaceRoot(t)
	entries, err := os.ReadDir(filepath.Join(root, "docs", "errata"))
	if err != nil {
		t.Fatal(err)
	}
	index := readDoc(t, "README.md")
	for _, e := range entries {
		if filepath.Ext(e.Name()) != ".md" {
			continue
		}
		if !strings.Contains(index, "(errata/"+e.Name()+")") {
			t.Errorf("docs/README.md does not link errata/%s", e.Name())
		}
	}
}

// GITHUB_API_URL and GITLAB_API_URL are described as what they are: the REST
// API base URL, used as given for GitHub, and a GitLab address to which /api/v4
// is added when it is not already there (issuex/github.go, issuex/gitlab.go).
func TestForgeAPIURLVariablesAreDescribedAsURLs(t *testing.T) {
	for _, name := range []string{"configuration.md", "cli.md"} {
		doc := readDoc(t, name)
		gh, ok := tableRow(doc, "| `GITHUB_API_URL`")
		if !ok {
			t.Fatalf("docs/%s has no GITHUB_API_URL row", name)
		}
		if strings.Contains(gh, "Enterprise host") || !strings.Contains(gh, "REST API base URL") || !strings.Contains(gh, "/api/v3") {
			t.Errorf("docs/%s GITHUB_API_URL row = %q, want the REST API base URL (https://HOST/api/v3)", name, gh)
		}
		gl, ok := tableRow(doc, "| `GITLAB_API_URL`")
		if !ok {
			t.Fatalf("docs/%s has no GITLAB_API_URL row", name)
		}
		if strings.Contains(gl, "self-hosted GitLab host") || !strings.Contains(gl, "/api/v4") {
			t.Errorf("docs/%s GITLAB_API_URL row = %q, want it to say /api/v4 is added", name, gl)
		}
	}
}

// The order in which the forge is chosen is written down, ending where the code
// ends: an ambiguous forge is not probed, and the tools go on without a client.
func TestForgeDetectionOrderIsDocumented(t *testing.T) {
	sec := docSection(t, readDoc(t, "configuration.md"), "Choosing the forge")
	for _, want := range []string{"github", "gitlab", "GITHUB_API_URL", "GITLAB_API_URL", "origin", "ambiguous", "No request"} {
		if !strings.Contains(sec, want) {
			t.Errorf("the forge section does not mention %q:\n%s", want, sec)
		}
	}
}

// The issuex descriptions name what the adapters do, not a four-item subset.
func TestIssuexDescriptionsNameTheWholeClient(t *testing.T) {
	root := findWorkspaceRoot(t)
	readme, err := os.ReadFile(filepath.Join(root, "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	for name, doc := range map[string]string{
		"README.md":           string(readme),
		"docs/development.md": readDoc(t, "development.md"),
	} {
		for _, want := range []string{"labels", "check runs", "reviews", "merge"} {
			if !strings.Contains(doc, want) {
				t.Errorf("%s does not mention %q among what issuex does", name, want)
			}
		}
	}
	if !strings.Contains(readDoc(t, "development.md"), "SetGitHubSleep") {
		t.Error("docs/development.md does not note that SetGitHubSleep / SetGitLabSleep are test seams")
	}
}

// The divergence from 04-REQ-3.2 is recorded.
func TestIssueURLHostErratumExists(t *testing.T) {
	root := findWorkspaceRoot(t)
	body, err := os.ReadFile(filepath.Join(root, "docs", "errata", "04_issue_url_host.md"))
	if err != nil {
		t.Fatalf("no erratum for 04-REQ-3.2: %v", err)
	}
	for _, want := range []string{"04-REQ-3.2", "api.github.com", "Repo"} {
		if !strings.Contains(string(body), want) {
			t.Errorf("the erratum does not mention %q", want)
		}
	}
}
