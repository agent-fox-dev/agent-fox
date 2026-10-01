package toolio

import (
	"context"
	"testing"
)

type fakeNextResult struct {
	Stage string `json:"stage"`
}

func (fakeNextResult) Next() []Next {
	return []Next{{Tool: "impl", Input: ".specs/01_x", Flags: []string{}, Why: "because"}}
}

// 06-REQ-6: App.emit puts a NextProvider's entries under the envelope's own
// next key, on stdout and in the report file alike.
func TestAppPopulatesNextFromNextProvider(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "test-key")
	dir := t.TempDir()
	report := dir + "/report.json"

	app, _ := newApp(t, func(context.Context, Deps) (int, any, *ErrorInfo) {
		return ExitOK, &fakeNextResult{Stage: "done"}, nil
	})
	env, code, _ := runApp(t, app, []string{"--dir", dir, "--report-file", report, "some input"}, "")
	if code != ExitOK {
		t.Fatalf("code = %d", code)
	}
	if len(env.Next) != 1 || env.Next[0].Tool != "impl" || env.Next[0].Input != ".specs/01_x" {
		t.Errorf("stdout next = %+v", env.Next)
	}
	if w := readReportFile(t, report); len(w.Next) != 1 || w.Next[0].Why != "because" {
		t.Errorf("report next = %+v", w.Next)
	}
}

// ResumeNext renders exactly as needs_human.resume does.
func TestResumeNextMatchesNeedsHumanResume(t *testing.T) {
	if got, want := ResumeNext("fix", "").Command(), `fix <same input> --context "<answer>"`; got != want {
		t.Errorf("Command() = %q, want %q", got, want)
	}
}

func TestResumePlaceholder(t *testing.T) {
	cases := []struct {
		in   Input
		want string
	}{
		{Input{Kind: KindFile, Origin: "docs/brief.md"}, "docs/brief.md"},
		{Input{Kind: KindIssue, Origin: "https://github.com/o/r/issues/1"}, "https://github.com/o/r/issues/1"},
		{Input{Kind: KindText, Origin: "argument"}, "<same input>"},
		{Input{Kind: KindStdin, Origin: "stdin"}, "<same input>"},
	}
	for _, c := range cases {
		if got := ResumePlaceholder(c.in); got != c.want {
			t.Errorf("ResumePlaceholder(%s) = %q, want %q", c.in.Kind, got, c.want)
		}
	}
}
