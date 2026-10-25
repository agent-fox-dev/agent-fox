package jobs_test

import (
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/agent-fox-dev/agentfox/internal/service/jobs"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

func TestValidate_AcceptsEachFormOfInputWithoutItsContext(t *testing.T) {
	for name, tc := range map[string]struct {
		kind  jobs.Kind
		body  jobs.Body
		issue int64 // 0: none
		input string
	}{
		"a triage of an issue number": {jobs.KindTriage, jobs.Body{Issue: json.RawMessage(`7`)}, 7, `{"issue":7}`},
		"a triage of an issue URL": {jobs.KindTriage, jobs.Body{Issue: json.RawMessage(`"https://github.example.com/acme/widgets/issues/8"`)},
			8, `{"issue":"https://github.example.com/acme/widgets/issues/8"}`},
		"a triage of a text with a title": {jobs.KindTriage, jobs.Body{Text: ptr("the build is broken"), Title: ptr("broken"), Context: []string{"see the CI", "and the log"}},
			0, `{"text":"the build is broken","title":"broken"}`},
		"a triage of a file": {jobs.KindTriage, jobs.Body{File: &jobs.File{Name: "report.txt", Content: "it fails"}, Context: []string{"a note"}},
			0, `{"file":{"name":"report.txt","content":"it fails"}}`},
		"a fix of an issue":     {jobs.KindFix, jobs.Body{Issue: json.RawMessage(`42`), Context: []string{"be careful"}}, 42, `{"issue":42}`},
		"a fix of owner/repo#N": {jobs.KindFix, jobs.Body{Issue: json.RawMessage(`"acme/widgets#43"`)}, 43, `{"issue":"acme/widgets#43"}`},
	} {
		t.Run(name, func(t *testing.T) {
			p, err := tc.body.Validate(tc.kind)
			if err != nil {
				t.Fatal(err)
			}
			if got, _ := json.Marshal(p.Input); string(got) != tc.input {
				t.Errorf("input = %s, want %s", got, tc.input)
			}
			if (tc.issue == 0) != (p.Issue == nil) || p.Issue != nil && *p.Issue != tc.issue {
				t.Errorf("issue = %v, want %d", p.Issue, tc.issue)
			}
			if p.Context == nil || !slices.Equal(p.Context, tc.body.Context) {
				t.Errorf("context = %#v, want %q and never nil", p.Context, tc.body.Context)
			}
		})
	}
}

func TestValidate_RefusesTheFirstFieldThatBreaksItsRuleNamingIt(t *testing.T) {
	long := func(n int) string { return strings.Repeat("a", n) }
	notes := func(n int, each string) []string { return slices.Repeat([]string{each}, n) }
	file := func(name, content string) *jobs.File { return &jobs.File{Name: name, Content: content} }
	issue := json.RawMessage(`1`)
	for name, tc := range map[string]struct {
		kind  jobs.Kind
		body  jobs.Body
		field string
	}{
		"an empty text":                        {jobs.KindTriage, jobs.Body{Text: ptr("")}, "text"},
		"a text of white space":                {jobs.KindTriage, jobs.Body{Text: ptr(" \n\t ")}, "text"},
		"a text that is not UTF-8":             {jobs.KindTriage, jobs.Body{Text: ptr("a\xffb")}, "text"},
		"a text with a NUL byte":               {jobs.KindTriage, jobs.Body{Text: ptr("a\x00b")}, "text"},
		"a text over the limit":                {jobs.KindTriage, jobs.Body{Text: ptr(long(toolio.MaxInputBytes + 1))}, "text"},
		"a title that is not UTF-8":            {jobs.KindTriage, jobs.Body{Text: ptr("x"), Title: ptr("a\xffb")}, "title"},
		"a title with a newline":               {jobs.KindTriage, jobs.Body{Text: ptr("x"), Title: ptr("a\nb")}, "title"},
		"a title with a return":                {jobs.KindTriage, jobs.Body{Text: ptr("x"), Title: ptr("a\rb")}, "title"},
		"a title of 257 bytes":                 {jobs.KindTriage, jobs.Body{Text: ptr("x"), Title: ptr(long(257))}, "title"},
		"a title with an issue":                {jobs.KindTriage, jobs.Body{Issue: issue, Title: ptr("t")}, "title"},
		"a file name that is not UTF-8":        {jobs.KindTriage, jobs.Body{File: file("a\xff", "x")}, "file.name"},
		"a file name with a slash":             {jobs.KindTriage, jobs.Body{File: file("a/b", "x")}, "file.name"},
		"a file name with a NUL byte":          {jobs.KindTriage, jobs.Body{File: file("a\x00b", "x")}, "file.name"},
		"an empty file name":                   {jobs.KindTriage, jobs.Body{File: file("", "x")}, "file.name"},
		"a file name of 256 bytes":             {jobs.KindTriage, jobs.Body{File: file(long(256), "x")}, "file.name"},
		"a file content that is not UTF-8":     {jobs.KindTriage, jobs.Body{File: file("a", "\xff")}, "file.content"},
		"an empty file content":                {jobs.KindTriage, jobs.Body{File: file("a", "  ")}, "file.content"},
		"a file content over the limit":        {jobs.KindTriage, jobs.Body{File: file("a", long(toolio.MaxInputBytes+1))}, "file.content"},
		"eleven context strings":               {jobs.KindTriage, jobs.Body{Issue: issue, Context: notes(11, "n")}, "context"},
		"an empty context string":              {jobs.KindTriage, jobs.Body{Issue: issue, Context: []string{"a", ""}}, "context"},
		"a context string of white space":      {jobs.KindTriage, jobs.Body{Issue: issue, Context: []string{"  "}}, "context"},
		"a context string of 8 KiB and a byte": {jobs.KindTriage, jobs.Body{Issue: issue, Context: []string{long(8193)}}, "context"},
		"a context of 32 KiB and a byte":       {jobs.KindTriage, jobs.Body{Issue: issue, Context: append(notes(4, long(8192))[:3], long(8191), "ab")}, "context"},
		"none of issue, text and file":         {jobs.KindTriage, jobs.Body{}, "issue"},
		"an issue that is null":                {jobs.KindTriage, jobs.Body{Issue: json.RawMessage(`null`)}, "issue"},
		"an issue and a text":                  {jobs.KindTriage, jobs.Body{Issue: issue, Text: ptr("x")}, "issue"},
		"a text and a file":                    {jobs.KindTriage, jobs.Body{Text: ptr("x"), File: file("a", "x")}, "issue"},
		"an issue of zero":                     {jobs.KindTriage, jobs.Body{Issue: json.RawMessage(`0`)}, "issue"},
		"an issue that is a pull request":      {jobs.KindTriage, jobs.Body{Issue: json.RawMessage(`"https://github.example.com/acme/widgets/pull/3"`)}, "issue"},
		"a fix without an issue":               {jobs.KindFix, jobs.Body{Context: []string{"a"}}, "issue"},
		"a fix of null":                        {jobs.KindFix, jobs.Body{Issue: json.RawMessage(`null`)}, "issue"},
		"a fix with a text":                    {jobs.KindFix, jobs.Body{Issue: issue, Text: ptr("x")}, "text"},
		"a fix with a title":                   {jobs.KindFix, jobs.Body{Issue: issue, Title: ptr("x")}, "title"},
		"a fix with a file":                    {jobs.KindFix, jobs.Body{File: file("a", "x")}, "file"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := tc.body.Validate(tc.kind)
			var invalid *jobs.ValidationError
			if !errors.As(err, &invalid) || invalid.Field != tc.field || !strings.HasPrefix(err.Error(), tc.field+": ") {
				t.Errorf("err = %v, want a *ValidationError of %s", err, tc.field)
			}
		})
	}
}

func TestValidate_AcceptsFieldsAtTheirLimits(t *testing.T) {
	long := func(n int) string { return strings.Repeat("a", n) }
	for name, body := range map[string]jobs.Body{
		"a text of the largest size":        {Text: ptr(long(toolio.MaxInputBytes))},
		"a title of 256 bytes":              {Text: ptr("x"), Title: ptr(long(256))},
		"a file name of 255 bytes":          {File: &jobs.File{Name: long(255), Content: "x"}},
		"a file content of the largest":     {File: &jobs.File{Name: "a", Content: long(toolio.MaxInputBytes)}},
		"ten context strings":               {Issue: json.RawMessage(`1`), Context: slices.Repeat([]string{"n"}, 10)},
		"four context strings of 8 KiB":     {Issue: json.RawMessage(`2`), Context: slices.Repeat([]string{long(8192)}, 4)},
		"an issue string of 2048 bytes":     {Issue: json.RawMessage(`"a/b` + long(2042) + `#99"`)},
		"an issue of the largest int64 too": {Issue: json.RawMessage(`9223372036854775807`)},
	} {
		if _, err := body.Validate(jobs.KindTriage); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
}
