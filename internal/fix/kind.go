// Package fix is the fix agent of the toolbelt: preflight, brief, the rounds of a
// brain in a box, applying and measuring what it left, and landing or parking it.
package fix

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"io/fs"
	"path"
	"regexp"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/agent-fox-dev/agentfox/internal/submission"
)

// The name of the submission kind of a fix run, and of the tool a brain ends
// its run with.
const (
	KindName = "fix_report"
	ToolName = "submit_fix"
)

//go:embed report.schema.json
var reportSchema []byte

// Schema is the JSON Schema of a submission: the input schema of submit_fix.
func Schema() json.RawMessage { return reportSchema }

// Types are the types a commit may have, and DefaultType the one a report that
// names none gets.
var Types = []string{"fix", "feat", "refactor", "perf", "docs", "test", "chore"}

// DefaultType is the type of a report that names none.
const DefaultType = "fix"

// The statuses a criterion of a report may have.
const (
	StatusMet           = "met"
	StatusUnmet         = "unmet"
	StatusPartial       = "partial"
	StatusNotApplicable = "not_applicable"
)

var statuses = []string{StatusMet, StatusUnmet, StatusPartial, StatusNotApplicable}

// The limits of a report and of a question.
const (
	maxSubject      = 72
	maxText         = 4000 // root_cause and change
	maxFiles        = 500
	maxWhy          = 300
	maxTests        = 100
	maxTestName     = 200
	maxProves       = 300
	maxCriteria     = 50
	maxCriterion    = 500
	maxEvidence     = 1000
	maxDecisions    = 30
	maxDecision     = 500
	maxQuestion     = 2000
	maxOptions      = 6
	maxOption       = 200
	maxQuestionRoot = 8000
)

// kindDescription is what the model reads of submit_fix.
const kindDescription = "Hand back the result of your run: the report of the change you made in the tree, or, alone, a question when two readings of the report lead to opposite fixes. Call it once, when the change is made and you have run the tests that prove it; if it is refused, read every problem it lists, correct them all and call it again. af makes the commit and the pull request from this report and from its own measurements: do not commit, push or open anything."

// FixReport is the submission kind of a fix run: a report of the change, or a
// question.
var FixReport = submission.Kind{
	Name:        KindName,
	ToolName:    ToolName,
	Description: kindDescription,
	Schema:      reportSchema,
	Validate:    validateReport,
}

// File is a file of the change, and why it changed.
type File struct {
	Path string `json:"path"`
	Why  string `json:"why"`
}

// Test is a test that proves the change.
type Test struct {
	Path   string `json:"path"`
	Name   string `json:"name,omitempty"`
	Proves string `json:"proves"`
}

// Criterion is an acceptance criterion and whether the change meets it.
type Criterion struct {
	Criterion string `json:"criterion"`
	Status    string `json:"status"`
	Evidence  string `json:"evidence,omitempty"`
}

// Report is the report form of a submission, normalised.
type Report struct {
	Type      string      `json:"type"`
	Subject   string      `json:"subject"`
	RootCause string      `json:"root_cause"`
	Change    string      `json:"change"`
	Files     []File      `json:"files"`
	Tests     []Test      `json:"tests"`
	Criteria  []Criterion `json:"criteria"`
	Decisions []string    `json:"decisions"`
}

// Question is the question form of a submission.
type Question struct {
	Text      string   `json:"text"`
	Options   []string `json:"options"`
	RootCause string   `json:"root_cause,omitempty"`
}

// Submission is a normalised submission: a report or a question, never both.
type Submission struct {
	Report   *Report
	Question *Question
}

// Decode reads a normalised payload, the payload of an accepted submission.
func Decode(payload []byte) (Submission, error) {
	var raw struct {
		Question *Question `json:"question"`
	}
	if err := json.Unmarshal(payload, &raw); err != nil {
		return Submission{}, fmt.Errorf("the submission is not a fix report: %w", err)
	}
	if raw.Question != nil {
		return Submission{Question: raw.Question}, nil
	}
	var r Report
	if err := json.Unmarshal(payload, &r); err != nil {
		return Submission{}, fmt.Errorf("the submission is not a fix report: %w", err)
	}
	return Submission{Report: &r}, nil
}

var (
	reportKeys   = []string{"type", "subject", "root_cause", "change", "files", "tests", "criteria", "decisions"}
	fileKeys     = []string{"path", "why"}
	testKeys     = []string{"path", "name", "proves"}
	criterionKey = []string{"criterion", "status", "evidence"}
	questionKeys = []string{"text", "options", "root_cause"}

	// typePrefix is a conventional-commit prefix, which af adds and a subject
	// must not carry.
	typePrefix  = regexp.MustCompile(`(?i)^(fix|feat|refactor|perf|docs|test|chore)(\([^)]*\))?!?:`)
	driveLetter = regexp.MustCompile(`^[A-Za-z]:`)
)

// validateReport checks a payload and returns it normalised, or every problem found.
func validateReport(_ context.Context, tree fs.FS, payload json.RawMessage) (json.RawMessage, []submission.Problem) {
	v := &validator{tree: tree}
	dec := json.NewDecoder(bytes.NewReader(payload))
	var obj map[string]any
	if err := dec.Decode(&obj); err != nil || obj == nil {
		v.add("", "must be one JSON object, a report or a question")
		return nil, v.problems
	}
	var out any
	if _, asked := obj["question"]; asked {
		out = v.questionForm(obj)
	} else {
		out = v.reportForm(obj)
	}
	slices.SortStableFunc(v.problems, func(a, b submission.Problem) int {
		if c := strings.Compare(a.Path, b.Path); c != 0 {
			return c
		}
		return strings.Compare(a.Message, b.Message)
	})
	if len(v.problems) > 0 {
		return nil, v.problems
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(out); err != nil {
		return nil, []submission.Problem{{Message: "the report could not be written out: " + err.Error()}}
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}

type validator struct {
	tree     fs.FS
	problems []submission.Problem
}

func (v *validator) add(pointer, format string, args ...any) {
	v.problems = append(v.problems, submission.Problem{Path: pointer, Message: fmt.Sprintf(format, args...)})
}

// jsonKind names a JSON value for a message.
func jsonKind(x any) string {
	switch x.(type) {
	case nil:
		return "null"
	case string:
		return "a string"
	case float64:
		return "a number"
	case bool:
		return "a boolean"
	case []any:
		return "an array"
	case map[string]any:
		return "an object"
	}
	return "something else"
}

// normal is a string as it is stored: CRLF and CR as LF, and trimmed.
func normal(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	return strings.TrimSpace(s)
}

// unknownKeys reports every key of obj that is not one of known.
func (v *validator) unknownKeys(pointer string, obj map[string]any, known []string, what string) {
	var unknown []string
	for k := range obj {
		if !slices.Contains(known, k) {
			unknown = append(unknown, k)
		}
	}
	slices.Sort(unknown)
	for _, k := range unknown {
		v.add(pointer+"/"+escape(k), "is not a field of %s; the fields are %s", what, strings.Join(known, ", "))
	}
}

// escape is a key as a JSON pointer token.
func escape(k string) string { return strings.NewReplacer("~", "~0", "/", "~1").Replace(k) }

// text reads the string field key of obj, normalised, and checks its length in
// characters. A field that is absent is "" and a problem when it is required.
func (v *validator) text(obj map[string]any, key, pointer string, required bool, minChars, maxChars int) (string, bool) {
	raw, present := obj[key]
	if !present || raw == nil {
		if required {
			v.add(pointer, "is required; it needs %d to %d characters", minChars, maxChars)
		}
		return "", false
	}
	s, ok := raw.(string)
	if !ok {
		v.add(pointer, "must be a string, not %s", jsonKind(raw))
		return "", false
	}
	return v.checkText(pointer, s, minChars, maxChars)
}

// checkText normalises s and checks it.
func (v *validator) checkText(pointer, s string, minChars, maxChars int) (string, bool) {
	s = normal(s)
	n := utf8.RuneCountInString(s)
	switch {
	case strings.ContainsRune(s, 0):
		v.add(pointer, "contains a NUL byte")
		return s, false
	case n < minChars:
		v.add(pointer, "is empty; it needs %d to %d characters", minChars, maxChars)
		return s, false
	case n > maxChars:
		v.add(pointer, "has %d characters, and the limit is %d", n, maxChars)
		return s, false
	}
	return s, true
}

// array reads the array field key of obj, and checks how many items it has.
func (v *validator) array(obj map[string]any, key string, required bool, minItems, maxItems int) []any {
	pointer := "/" + key
	raw, present := obj[key]
	if !present || raw == nil {
		if required {
			v.add(pointer, "is required; give %d to %d items", minItems, maxItems)
		}
		return nil
	}
	items, ok := raw.([]any)
	if !ok {
		v.add(pointer, "must be an array, not %s", jsonKind(raw))
		return nil
	}
	if n := len(items); n < minItems || n > maxItems {
		v.add(pointer, "has %d items, and it needs %d to %d", n, minItems, maxItems)
	}
	return items
}

// questionForm validates {question: {text, options?, root_cause?}} and nothing else.
func (v *validator) questionForm(obj map[string]any) *struct {
	Question Question `json:"question"`
} {
	var others []string
	for k := range obj {
		if k != "question" {
			others = append(others, k)
		}
	}
	slices.Sort(others)
	for _, k := range others {
		v.add("/"+escape(k), "is not allowed with a question: give the question alone, or the report without one")
	}
	q, ok := obj["question"].(map[string]any)
	if !ok {
		v.add("/question", "must be an object {text, options, root_cause}, not %s", jsonKind(obj["question"]))
		return nil
	}
	v.unknownKeys("/question", q, questionKeys, "a question")
	out := Question{Options: []string{}}
	out.Text, _ = v.text(q, "text", "/question/text", true, 1, maxQuestion)
	if raw, present := q["options"]; present && raw != nil {
		items, isArray := raw.([]any)
		switch {
		case !isArray:
			v.add("/question/options", "must be an array of strings, not %s", jsonKind(raw))
		case len(items) > maxOptions:
			v.add("/question/options", "has %d items, and it needs at most %d", len(items), maxOptions)
		}
		if isArray {
			for i, item := range items {
				pointer := fmt.Sprintf("/question/options/%d", i)
				s, isString := item.(string)
				if !isString {
					v.add(pointer, "must be a string, not %s", jsonKind(item))
					continue
				}
				s, _ = v.checkText(pointer, s, 1, maxOption)
				out.Options = append(out.Options, s)
			}
		}
	}
	out.RootCause, _ = v.text(q, "root_cause", "/question/root_cause", false, 0, maxQuestionRoot)
	return &struct {
		Question Question `json:"question"`
	}{out}
}

// reportForm validates the report form.
func (v *validator) reportForm(obj map[string]any) *Report {
	v.unknownKeys("", obj, append(slices.Clone(reportKeys), "question"), "a report")
	r := &Report{Type: DefaultType, Files: []File{}, Tests: []Test{}, Criteria: []Criterion{}, Decisions: []string{}}

	if t, ok := v.text(obj, "type", "/type", false, 0, 20); ok && t != "" {
		if slices.Contains(Types, t) {
			r.Type = t
		} else {
			v.add("/type", "is %q, and must be one of %s", t, strings.Join(Types, ", "))
		}
	}
	r.Subject = v.subject(obj)
	r.RootCause, _ = v.text(obj, "root_cause", "/root_cause", true, 1, maxText)
	r.Change, _ = v.text(obj, "change", "/change", true, 1, maxText)

	seen := map[string]bool{}
	for i, item := range v.array(obj, "files", true, 1, maxFiles) {
		pointer := fmt.Sprintf("/files/%d", i)
		f, ok := item.(map[string]any)
		if !ok {
			v.add(pointer, "must be an object {path, why}, not %s", jsonKind(item))
			continue
		}
		v.unknownKeys(pointer, f, fileKeys, "a file")
		file := File{}
		file.Path = v.relPath(f, pointer+"/path")
		if file.Path != "" {
			if seen[file.Path] {
				v.add(pointer+"/path", "%s is listed twice", file.Path)
			}
			seen[file.Path] = true
		}
		file.Why, _ = v.text(f, "why", pointer+"/why", true, 1, maxWhy)
		r.Files = append(r.Files, file)
	}
	slices.SortStableFunc(r.Files, func(a, b File) int { return strings.Compare(a.Path, b.Path) })

	for i, item := range v.array(obj, "tests", false, 0, maxTests) {
		pointer := fmt.Sprintf("/tests/%d", i)
		t, ok := item.(map[string]any)
		if !ok {
			v.add(pointer, "must be an object {path, name, proves}, not %s", jsonKind(item))
			continue
		}
		v.unknownKeys(pointer, t, testKeys, "a test")
		test := Test{}
		test.Path = v.relPath(t, pointer+"/path")
		if test.Path != "" {
			if info, err := fs.Stat(v.tree, test.Path); err != nil || !info.Mode().IsRegular() {
				v.add(pointer+"/path", "%s is not a regular file of the tree you submit: a test has to be a file", test.Path)
			}
		}
		test.Name, _ = v.text(t, "name", pointer+"/name", false, 0, maxTestName)
		test.Proves, _ = v.text(t, "proves", pointer+"/proves", true, 1, maxProves)
		r.Tests = append(r.Tests, test)
	}

	for i, item := range v.array(obj, "criteria", false, 0, maxCriteria) {
		pointer := fmt.Sprintf("/criteria/%d", i)
		c, ok := item.(map[string]any)
		if !ok {
			v.add(pointer, "must be an object {criterion, status, evidence}, not %s", jsonKind(item))
			continue
		}
		v.unknownKeys(pointer, c, criterionKey, "a criterion")
		crit := Criterion{}
		crit.Criterion, _ = v.text(c, "criterion", pointer+"/criterion", true, 1, maxCriterion)
		if status, ok := v.text(c, "status", pointer+"/status", true, 1, 20); ok {
			if slices.Contains(statuses, status) {
				crit.Status = status
			} else {
				v.add(pointer+"/status", "is %q, and must be one of %s", status, strings.Join(statuses, ", "))
			}
		}
		crit.Evidence, _ = v.text(c, "evidence", pointer+"/evidence", false, 0, maxEvidence)
		r.Criteria = append(r.Criteria, crit)
	}

	for i, item := range v.array(obj, "decisions", false, 0, maxDecisions) {
		pointer := fmt.Sprintf("/decisions/%d", i)
		s, ok := item.(string)
		if !ok {
			v.add(pointer, "must be a string, not %s", jsonKind(item))
			continue
		}
		s, _ = v.checkText(pointer, s, 1, maxDecision)
		r.Decisions = append(r.Decisions, s)
	}
	return r
}

// subject reads the subject of the commit.
func (v *validator) subject(obj map[string]any) string {
	s, ok := v.text(obj, "subject", "/subject", true, 1, maxSubject)
	if !ok {
		return s
	}
	switch {
	case strings.Contains(s, "\n"):
		v.add("/subject", "must be one line")
	case strings.HasSuffix(s, "."):
		v.add("/subject", "must not end with a period")
	case typePrefix.MatchString(s):
		v.add("/subject", "must not start with a type and a colon: af adds the type, from the field type")
	}
	return s
}

// relPath reads a path of the repository: relative, with slashes, clean, and
// not under .git/ or .agent-fox/.
func (v *validator) relPath(obj map[string]any, pointer string) string {
	key := pointer[strings.LastIndex(pointer, "/")+1:]
	p, ok := v.text(obj, key, pointer, true, 1, 4096)
	if !ok {
		return ""
	}
	switch {
	case strings.Contains(p, `\`):
		v.add(pointer, "must use slashes, not backslashes")
	case strings.HasPrefix(p, "/") || driveLetter.MatchString(p):
		v.add(pointer, "must be relative to the root of the repository, not absolute")
	case slices.Contains(strings.Split(p, "/"), ".."):
		v.add(pointer, `must not contain ".."`)
	case path.Clean(p) != p || p == ".":
		v.add(pointer, "must be a clean path: no \"./\", no empty element, no trailing slash")
	default:
		if first, _, _ := strings.Cut(p, "/"); strings.EqualFold(first, ".git") || strings.EqualFold(first, ".agent-fox") {
			v.add(pointer, "must not be under %s/: only af writes there", first)
			return ""
		}
		return p
	}
	return ""
}
