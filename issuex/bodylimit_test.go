package issuex

import "testing"

// Each forge reports the largest body it accepts, and a client that reports
// none gets the smallest known limit, GitHub's.
func TestMaxBodyLengthPerForge(t *testing.T) {
	gh, err := NewGitHub(Options{Token: "t"})
	if err != nil {
		t.Fatal(err)
	}
	gl, err := NewGitLab(Options{Token: "t"})
	if err != nil {
		t.Fatal(err)
	}
	for name, c := range map[string]struct {
		client Client
		want   int
	}{
		"github":  {gh, GitHubMaxBodyLength},
		"gitlab":  {gl, GitLabMaxBodyLength},
		"unknown": {&NoOpClient{}, GitHubMaxBodyLength},
		"nil":     {nil, GitHubMaxBodyLength},
	} {
		if got := MaxBodyLength(c.client); got != c.want {
			t.Errorf("%s: MaxBodyLength = %d, want %d", name, got, c.want)
		}
	}
	if GitHubMaxBodyLength != 65536 || GitLabMaxBodyLength != 1048576 {
		t.Errorf("limits = %d, %d", GitHubMaxBodyLength, GitLabMaxBodyLength)
	}
}
