package specgen

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// callSubmitPRD drives the real tool handler with the arguments a model would
// have produced.
func callSubmitPRD(t *testing.T, args map[string]any) (*prdSink, string, bool) {
	t.Helper()
	var sink prdSink
	tool := submitPRDTool(&sink)
	raw, err := json.Marshal(args)
	if err != nil {
		t.Fatal(err)
	}
	res := tool.Execute(context.Background(), raw)
	return &sink, res.Error + " " + res.Detail, !res.OK
}

func validPRDArgs() map[string]any {
	return map[string]any{
		"spec_name": "widget_counter",
		"title":     "Widget Counter",
		"body": "# Widget Counter\n\n## Intent\n\nCount widgets once per request.\n\n" +
			"## Goals\n\n- Count accurately.\n\n## Non-goals\n\n- Reporting.\n",
	}
}

func TestSubmitPRDAcceptsAFinishedPRD(t *testing.T) {
	sink, _, isErr := callSubmitPRD(t, validPRDArgs())
	if isErr {
		t.Fatal("a valid PRD was rejected")
	}
	got, ok := sink.get()
	if !ok {
		t.Fatal("nothing was recorded")
	}
	if got.SpecName != "widget_counter" || got.Title != "Widget Counter" {
		t.Errorf("got %+v", got)
	}
}

// The three checks are the three that would otherwise fail later and more
// expensively. Each is repairable on the next turn, so each is an error
// result rather than a Go error.
func TestSubmitPRDRejectsWhatWouldFailLater(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(map[string]any)
		want   string
	}{
		{"a spec name the format cannot use",
			func(a map[string]any) { a["spec_name"] = "Widget Counter" }, "[a-z][a-z0-9_]*"},
		{"an empty title, which fails the frontmatter schema",
			func(a map[string]any) { a["title"] = "  " }, "title is empty"},
		{"a body with no Intent section, which cannot be activated",
			func(a map[string]any) { a["body"] = "# Widget Counter\n\n## Goals\n\n- Count.\n" }, "## Intent"},
		{"frontmatter the model added anyway",
			func(a map[string]any) {
				a["body"] = "---\ntitle: x\n---\n\n## Intent\n\nCount widgets.\n"
			}, "frontmatter"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			args := validPRDArgs()
			c.mutate(args)
			sink, msg, isErr := callSubmitPRD(t, args)
			if !isErr {
				t.Fatal("accepted")
			}
			if _, ok := sink.get(); ok {
				t.Error("a rejected submission must not be recorded")
			}
			if !strings.Contains(msg, c.want) {
				t.Errorf("the message must say what to fix; got %q", msg)
			}
		})
	}
}

func TestHasIntent(t *testing.T) {
	for _, body := range []string{
		"## Intent\n\nx\n",
		"# Title\n\n##  Intent  \n\nx\n",
		"# Title\n\n## intent\n\nx\n",
	} {
		if !HasIntent(body) {
			t.Errorf("HasIntent(%q) = false", body)
		}
	}
	for _, body := range []string{
		"# Intent\n\nx\n",              // a level-1 heading is not the section
		"## Intentional design\n\nx\n", // a heading that merely starts with it
		"Intent: count widgets\n",
	} {
		if HasIntent(body) {
			t.Errorf("HasIntent(%q) = true", body)
		}
	}
}

func TestValidSpecName(t *testing.T) {
	for _, s := range []string{"a", "widget_counter", "spec2", "a_1_b"} {
		if !ValidSpecName(s) {
			t.Errorf("ValidSpecName(%q) = false", s)
		}
	}
	for _, s := range []string{"", "Widget", "1st", "widget-counter", "widget counter", "_x"} {
		if ValidSpecName(s) {
			t.Errorf("ValidSpecName(%q) = true", s)
		}
	}
}
