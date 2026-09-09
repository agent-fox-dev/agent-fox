package toolio

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/agent-fox-dev/agentfox/internal/ghapi"
)

func TestResolveClassifiesText(t *testing.T) {
	in, err := Resolve(context.Background(), "the widget counter double-counts on retry", nil, nil)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if in.Kind != KindText || in.Origin != "argument" {
		t.Errorf("got %+v, want text/argument", in)
	}
	if in.Body != "the widget counter double-counts on retry" {
		t.Errorf("Body = %q", in.Body)
	}
}

func TestResolveReadsAFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "crash.log")
	if err := os.WriteFile(path, []byte("panic: nil map\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	in, err := Resolve(context.Background(), path, nil, nil)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if in.Kind != KindFile {
		t.Errorf("Kind = %s, want file", in.Kind)
	}
	if in.Body != "panic: nil map\n" {
		t.Errorf("Body = %q", in.Body)
	}
}

// A path that exists but is a directory is not an error: "src/" is a
// plausible thing to name in a bug report, so it falls through to text.
func TestResolveTreatsADirectoryAsText(t *testing.T) {
	in, err := Resolve(context.Background(), t.TempDir(), nil, nil)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if in.Kind != KindText {
		t.Errorf("Kind = %s, want text", in.Kind)
	}
}

// A path that does NOT exist is text too. Refusing it as a missing file would
// reject "the widget/ package panics".
func TestResolveTreatsAMissingPathAsText(t *testing.T) {
	in, err := Resolve(context.Background(), "internal/nope/missing.go panics on empty input", nil, nil)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if in.Kind != KindText {
		t.Errorf("Kind = %s, want text", in.Kind)
	}
}

func TestResolveReadsStdin(t *testing.T) {
	in, err := Resolve(context.Background(), "-", strings.NewReader("piped report\n"), nil)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if in.Kind != KindStdin || in.Origin != "stdin" {
		t.Errorf("got %+v", in)
	}
	if in.Body != "piped report\n" {
		t.Errorf("Body = %q", in.Body)
	}
}

func TestResolveRejectsEmptyInput(t *testing.T) {
	for _, arg := range []string{"", "   "} {
		if _, err := Resolve(context.Background(), arg, nil, nil); !errors.Is(err, ErrNoInput) {
			t.Errorf("Resolve(%q) = %v, want ErrNoInput", arg, err)
		}
	}
	if _, err := Resolve(context.Background(), "-", strings.NewReader("  \n"), nil); !errors.Is(err, ErrNoInput) {
		t.Errorf("empty stdin: want ErrNoInput, got %v", err)
	}
}

func TestResolveFetchesAnIssue(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/comments") {
			_ = json.NewEncoder(w).Encode([]ghapi.Comment{
				{Body: "here is the traceback", User: ghapi.User{Login: "maintainer"}},
			})
			return
		}
		_ = json.NewEncoder(w).Encode(ghapi.Issue{
			Number: 42, Title: "it crashes", Body: "on save", State: "open",
			User: ghapi.User{Login: "reporter"},
		})
	}))
	defer srv.Close()
	gh := ghapi.NewWithOptions(ghapi.Options{BaseURL: srv.URL, Token: "t", UserAgent: "test"})

	in, err := Resolve(context.Background(), "https://github.com/acme/widgets/issues/42", nil, gh)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if in.Kind != KindIssue {
		t.Fatalf("Kind = %s, want github", in.Kind)
	}
	if in.Issue == nil || in.Issue.Number != 42 {
		t.Fatalf("Issue = %+v", in.Issue)
	}
	for _, want := range []string{"it crashes", "on save", "here is the traceback", "maintainer"} {
		if !strings.Contains(in.Body, want) {
			t.Errorf("Body is missing %q:\n%s", want, in.Body)
		}
	}
}

// A truncated report must say so: one that ends mid-stack-trace with no
// marker reads to a model as a complete stack trace that had no more frames.
func TestTruncateMarksTheCut(t *testing.T) {
	long := strings.Repeat("a line of log output\n", MaxInputBytes/10)
	got, cut := Truncate(long)
	if !cut {
		t.Fatal("want truncated")
	}
	if len(got) > MaxInputBytes+200 {
		t.Errorf("len = %d, want about %d", len(got), MaxInputBytes)
	}
	if !strings.Contains(got, "truncated at") {
		t.Error("the cut must be visible in the text")
	}
	short := "one line\n"
	if got, cut := Truncate(short); cut || got != short {
		t.Errorf("short input was altered: %q, %v", got, cut)
	}
}

func TestRenderThreadIsDeterministic(t *testing.T) {
	ref := ghapi.IssueRef{Repo: ghapi.Repo{Owner: "acme", Name: "widgets"}, Number: 7}
	thread := ghapi.Thread{
		Issue: ghapi.Issue{Title: "t", Body: "b", State: "open",
			User: ghapi.User{Login: "u"}, Labels: []ghapi.Label{{Name: "bug"}}},
		Comments: []ghapi.Comment{{Body: "c", User: ghapi.User{Login: "v"}}},
	}
	first := RenderThread(ref, thread)
	if second := RenderThread(ref, thread); first != second {
		t.Error("RenderThread is not deterministic")
	}
	if !strings.Contains(first, "Labels: bug") {
		t.Errorf("labels missing:\n%s", first)
	}
}
