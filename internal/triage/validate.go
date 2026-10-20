package triage

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"
)

// Limits of a diagnosis (docs/cli.md lists them).
const (
	// MaxPayloadBytes is the largest payload the validator reads.
	MaxPayloadBytes = 65536
	// MaxRenderedBytes is the most the diagnosis may take in the body, without
	// the original-report section.
	MaxRenderedBytes = 16384

	maxTitleChars    = 120
	maxRootCause     = 6000
	maxFiles         = 30
	maxNote          = 300
	maxSuggestedFix  = 6000
	maxCriteria      = 15
	maxCriterion     = 300
	maxQuestionText  = 2000
	minOptions       = 2
	maxOptions       = 6
	maxOption        = 200
	maxSuggestedBase = 3
)

// markerText is the text no string of a diagnosis may hold: it starts the
// markers that the body is read back by.
const markerText = "<!-- agent-fox:"

// placeholderRun stands for the run id when the rendered size is measured: a
// run id has this length.
const placeholderRun = "00000000_000000_000000"

var (
	severities   = []string{"critical", "high", "medium", "low"}
	confidences  = []string{"confirmed", "probable", "suspected", "not_reproducible"}
	fieldNames   = []string{"title", "severity", "confidence", "root_cause", "affected_files", "suggested_fix", "acceptance_criteria", "question"}
	fileFields   = []string{"path", "line", "note"}
	questionKeys = []string{"text", "options"}

	// colonLine is a path that ends in :<digits>.
	colonLine = regexp.MustCompile(`^(.+):([0-9]+)$`)
	// driveLetter starts an absolute Windows path.
	driveLetter = regexp.MustCompile(`^[A-Za-z]:/`)
)

// Problem is one thing wrong with a diagnosis.
type Problem struct {
	// Pointer is the JSON pointer of the field, "" for the payload as a whole.
	Pointer string
	// Message says what is wrong.
	Message string
	// Unresolved is set for a cited path that is not a file of the tree; Path
	// is the path as it was cited.
	Unresolved bool
	Path       string
}

// String is the problem as a line of text.
func (p Problem) String() string {
	if p.Pointer == "" {
		return p.Message
	}
	return p.Pointer + ": " + p.Message
}

// Tree is what a diagnosis is checked against.
type Tree struct {
	// Roots are the absolute directories that stand for the root of the tree: a
	// cited path under one of them is made relative to it.
	Roots []string
	// Files is the file view of the tree: slash-separated paths relative to its
	// root, regular files only.
	Files []string
	// Lines is the number of lines of a file of the view; nil when it cannot be
	// told, which leaves the upper bound of a line number unchecked.
	Lines func(path string) (int, error)
}

// Validate checks a payload against the rules of a diagnosis and the tree, and
// returns the diagnosis in its normalized form (paths cleaned, `path:N` split)
// and every problem found, sorted by pointer and then by message. The
// diagnosis is meant to be used only when there is no problem.
func Validate(payload []byte, tree Tree) (Diagnosis, []Problem) {
	v := newValidator(tree)
	d := v.run(payload)
	slices.SortStableFunc(v.problems, func(a, b Problem) int {
		if c := strings.Compare(a.Pointer, b.Pointer); c != 0 {
			return c
		}
		return strings.Compare(a.Message, b.Message)
	})
	return d, v.problems
}

type validator struct {
	tree     Tree
	files    map[string]bool
	dirs     map[string]bool
	byBase   map[string][]string
	roots    []string
	problems []Problem
}

func newValidator(tree Tree) *validator {
	v := &validator{tree: tree, files: map[string]bool{}, dirs: map[string]bool{}, byBase: map[string][]string{}}
	for _, f := range tree.Files {
		v.files[f] = true
		base := path.Base(f)
		v.byBase[base] = append(v.byBase[base], f)
		for dir := path.Dir(f); dir != "." && dir != "/" && !v.dirs[dir]; dir = path.Dir(dir) {
			v.dirs[dir] = true
		}
	}
	for _, r := range tree.Roots {
		r = strings.TrimRight(strings.ReplaceAll(r, `\`, "/"), "/")
		if r != "" {
			v.roots = append(v.roots, r)
		}
	}
	return v
}

func (v *validator) add(pointer, format string, args ...any) {
	v.problems = append(v.problems, Problem{Pointer: pointer, Message: fmt.Sprintf(format, args...)})
}

// run reads the payload and checks it field by field.
func (v *validator) run(payload []byte) Diagnosis {
	var d Diagnosis
	if len(payload) > MaxPayloadBytes {
		v.add("", "the payload is %d bytes, and a diagnosis is at most %d", len(payload), MaxPayloadBytes)
		return d
	}
	dec := json.NewDecoder(bytes.NewReader(payload))
	dec.UseNumber()
	var top any
	if err := dec.Decode(&top); err != nil {
		v.add("", "the payload is not valid JSON: %v", err)
		return d
	}
	if _, err := dec.Token(); err != io.EOF {
		v.add("", "the payload has more after its object, and a diagnosis is one JSON object and nothing else")
		return d
	}
	obj, ok := top.(map[string]any)
	if !ok {
		v.add("", "the payload is %s, and a diagnosis is one JSON object", jsonKind(top))
		return d
	}
	v.unknownKeys("", obj, fieldNames, "a diagnosis")

	question, hasQuestion := obj["question"]
	hasQuestion = hasQuestion && question != nil

	d.Title = v.title(obj)
	d.Severity = v.enum(obj, "severity", severities)
	d.Confidence = v.enum(obj, "confidence", confidences)
	d.RootCause = v.text(obj, "root_cause", "/root_cause", 1, maxRootCause)

	// A diagnosis that found nothing, or that asks before it commits to a fix,
	// has nothing to list yet.
	relaxed := d.Confidence == "not_reproducible" || hasQuestion
	d.AffectedFiles = v.affectedFiles(obj, relaxed)
	d.SuggestedFix = v.fix(obj, relaxed)
	d.AcceptanceCriteria = v.criteria(obj, relaxed)
	if hasQuestion {
		d.Question = v.question(question, d.Confidence)
	}

	if size := renderedSize(d, placeholderRun); size > MaxRenderedBytes {
		v.add("", "the rendered diagnosis is %d bytes, and the limit is %d: shorten the root cause, the notes or the fix", size, MaxRenderedBytes)
	}
	return d
}

// jsonKind names the JSON type of a decoded value.
func jsonKind(x any) string {
	switch x.(type) {
	case nil:
		return "null"
	case []any:
		return "an array"
	case string:
		return "a string"
	case json.Number:
		return "a number"
	case bool:
		return "a boolean"
	}
	return "not an object"
}

// pointerEscape is a key as a JSON pointer token.
func pointerEscape(s string) string {
	return strings.NewReplacer("~", "~0", "/", "~1").Replace(s)
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
		v.add(pointer+"/"+pointerEscape(k), "is not a field of %s; the fields are %s", what, strings.Join(known, ", "))
	}
}

// checkText reports a NUL byte and the marker text in s.
func (v *validator) checkText(pointer, s string) {
	if strings.ContainsRune(s, 0) {
		v.add(pointer, "contains a NUL byte")
	}
	if strings.Contains(s, markerText) {
		v.add(pointer, "contains the text %q, which marks the issue body", markerText)
	}
}

// str reads a required-or-optional string. present is false for a missing or
// null value; ok is false when the value is there and is no string, which is
// reported.
func (v *validator) str(obj map[string]any, key, pointer string) (s string, present, ok bool) {
	raw, there := obj[key]
	if !there || raw == nil {
		return "", false, true
	}
	s, isString := raw.(string)
	if !isString {
		v.add(pointer, "must be a string, not %s", jsonKind(raw))
		return "", true, false
	}
	return s, true, true
}

// text reads a string of min to max bytes that is required.
func (v *validator) text(obj map[string]any, key, pointer string, min, max int) string {
	s, present, ok := v.str(obj, key, pointer)
	switch {
	case !ok:
		return ""
	case !present:
		v.add(pointer, "is required")
		return ""
	}
	v.checkBytes(pointer, s, min, max)
	v.checkText(pointer, s)
	return s
}

// checkBytes reports a string of fewer than min bytes (or only space, for min 1)
// and one of more than max.
func (v *validator) checkBytes(pointer, s string, min, max int) {
	switch {
	case min > 0 && strings.TrimSpace(s) == "":
		v.add(pointer, "is empty; it needs %d to %d bytes", min, max)
	case len(s) > max:
		v.add(pointer, "is %d bytes, and at most %d are allowed", len(s), max)
	}
}

// fix reads the suggested fix, which a relaxed diagnosis may leave empty.
func (v *validator) fix(obj map[string]any, relaxed bool) string {
	if relaxed {
		raw, present := obj["suggested_fix"]
		if text, isString := raw.(string); !present || raw == nil || (isString && strings.TrimSpace(text) == "") {
			return ""
		}
	}
	return v.text(obj, "suggested_fix", "/suggested_fix", 1, maxSuggestedFix)
}

func (v *validator) title(obj map[string]any) string {
	s, present, ok := v.str(obj, "title", "/title")
	switch {
	case !ok:
		return ""
	case !present:
		v.add("/title", "is required")
		return ""
	case strings.TrimSpace(s) == "":
		v.add("/title", "is empty; it needs one line of 1 to %d characters", maxTitleChars)
	case strings.ContainsAny(s, "\r\n"):
		v.add("/title", "must be one line")
	case utf8.RuneCountInString(s) > maxTitleChars:
		v.add("/title", "is %d characters, and at most %d are allowed", utf8.RuneCountInString(s), maxTitleChars)
	}
	v.checkText("/title", s)
	return s
}

func (v *validator) enum(obj map[string]any, key string, values []string) string {
	pointer := "/" + key
	s, present, ok := v.str(obj, key, pointer)
	switch {
	case !ok:
		return ""
	case !present:
		v.add(pointer, "is required; it is one of %s", strings.Join(values, ", "))
		return ""
	case !slices.Contains(values, s):
		v.add(pointer, "is %q, and must be one of %s", s, strings.Join(values, ", "))
		return ""
	}
	return s
}

func (v *validator) affectedFiles(obj map[string]any, relaxed bool) []AffectedFile {
	raw, present := obj["affected_files"]
	if !present || raw == nil {
		if !relaxed {
			v.add("/affected_files", "is required; cite 1 to %d files", maxFiles)
		}
		return nil
	}
	items, ok := raw.([]any)
	if !ok {
		v.add("/affected_files", "must be an array of {path, line, note}, not %s", jsonKind(raw))
		return nil
	}
	if (len(items) == 0 && !relaxed) || len(items) > maxFiles {
		v.add("/affected_files", "has %d items, and it needs 1 to %d", len(items), maxFiles)
	}
	var out []AffectedFile
	for i, item := range items {
		out = append(out, v.affectedFile(fmt.Sprintf("/affected_files/%d", i), item))
	}
	return out
}

func (v *validator) affectedFile(pointer string, item any) AffectedFile {
	obj, ok := item.(map[string]any)
	if !ok {
		v.add(pointer, "must be an object {path, line, note}, not %s", jsonKind(item))
		return AffectedFile{}
	}
	v.unknownKeys(pointer, obj, fileFields, "an affected file")
	var f AffectedFile

	file, hasPath, okPath := v.str(obj, "path", pointer+"/path")
	switch {
	case !okPath:
	case !hasPath || strings.TrimSpace(file) == "":
		v.add(pointer+"/path", "is required")
	default:
		v.checkText(pointer+"/path", file)
	}

	line, hasLine := 0, false
	if raw, present := obj["line"]; present && raw != nil {
		hasLine = true
		n, isNumber := raw.(json.Number)
		parsed, err := int64(0), error(nil)
		if isNumber {
			parsed, err = n.Int64()
		}
		switch {
		case !isNumber || err != nil:
			v.add(pointer+"/line", "must be an integer of at least 1")
		case parsed < 1:
			v.add(pointer+"/line", "is %d, and lines count from 1", parsed)
		default:
			line = int(parsed)
		}
	}

	if hasPath && okPath && strings.TrimSpace(file) != "" {
		clean, fromPath, usedColon, problem := v.resolve(file, hasLine)
		if problem != "" {
			v.problems = append(v.problems, Problem{Pointer: pointer + "/path", Message: problem, Unresolved: true, Path: file})
		} else {
			f.Path = clean
			if usedColon {
				line, hasLine = fromPath, true
				if fromPath < 1 {
					v.add(pointer+"/line", "is %d in %q, and lines count from 1", fromPath, file)
				}
			}
			if hasLine && line >= 1 && v.tree.Lines != nil {
				if count, err := v.tree.Lines(clean); err != nil {
					v.add(pointer+"/line", "cannot be checked: %s could not be read: %v", clean, err)
				} else if line > count {
					v.add(pointer+"/line", "is %d, and %s has %d lines", line, clean, count)
				}
			}
		}
	}
	f.Line = line

	note, hasNote, okNote := v.str(obj, "note", pointer+"/note")
	switch {
	case !okNote:
	case !hasNote:
		v.add(pointer+"/note", "is required; say in a few words what the file has to do with the problem")
	default:
		v.checkBytes(pointer+"/note", note, 0, maxNote)
		v.checkText(pointer+"/note", note)
		f.Note = note
	}
	return f
}

// resolve finds the file a cited path names, by the steps of the rules: take off
// a leading ./, make an absolute path under the tree relative to it, use slash
// separators, and, when the path as given is no file, a line was not given and
// the path ends in :<digits>, try the part before the colon, whose digits are
// then the line (usedColon). A problem is the message for a path that does not
// resolve.
func (v *validator) resolve(cited string, hasLine bool) (clean string, fromPath int, usedColon bool, problem string) {
	unresolved := func(format string, args ...any) (string, int, bool, string) {
		return "", 0, false, fmt.Sprintf("unresolved path %q: %s", cited, fmt.Sprintf(format, args...))
	}
	p := strings.ReplaceAll(cited, `\`, "/")
	for strings.HasPrefix(p, "./") {
		p = p[2:]
	}
	if strings.HasPrefix(p, "/") || driveLetter.MatchString(p) {
		rel, ok := v.underRoot(p)
		if !ok {
			return unresolved("it is absolute and outside the repository; cite it from the root of the repository")
		}
		p = rel
	}
	if slices.Contains(strings.Split(p, "/"), "..") {
		return unresolved(`it contains ".."; cite the path from the root of the repository`)
	}
	p = path.Clean(p)
	if p == "." {
		return unresolved("it is the repository itself; cite a file")
	}
	if first, _, _ := strings.Cut(p, "/"); first == ".git" || first == ".agent-fox" {
		return unresolved("%s is not part of the repository's files", first+"/")
	}
	if v.files[p] {
		return p, 0, false, ""
	}
	if !hasLine {
		if m := colonLine.FindStringSubmatch(p); m != nil && v.files[m[1]] {
			if n, err := strconv.Atoi(m[2]); err == nil {
				return m[1], n, true, ""
			}
		}
	}
	if v.dirs[p] {
		return unresolved("it is a directory; cite a file in it")
	}
	msg := "no such file in the repository"
	if same := v.byBase[path.Base(p)]; len(same) > 0 {
		msg += "; files with the same name: " + strings.Join(same[:min(len(same), maxSuggestedBase)], ", ")
	}
	return unresolved("%s", msg)
}

// underRoot makes an absolute path relative to the root of the tree it is
// under.
func (v *validator) underRoot(p string) (string, bool) {
	for _, r := range v.roots {
		if p == r {
			return ".", true
		}
		if rel, ok := strings.CutPrefix(p, r+"/"); ok {
			return rel, true
		}
	}
	return "", false
}

func (v *validator) criteria(obj map[string]any, relaxed bool) []string {
	raw, present := obj["acceptance_criteria"]
	if !present || raw == nil {
		if !relaxed {
			v.add("/acceptance_criteria", "is required; give 1 to %d criteria that can be observed", maxCriteria)
		}
		return nil
	}
	items, ok := raw.([]any)
	if !ok {
		v.add("/acceptance_criteria", "must be an array of strings, not %s", jsonKind(raw))
		return nil
	}
	if (len(items) == 0 && !relaxed) || len(items) > maxCriteria {
		v.add("/acceptance_criteria", "has %d items, and it needs 1 to %d", len(items), maxCriteria)
	}
	var out []string
	for i, item := range items {
		pointer := fmt.Sprintf("/acceptance_criteria/%d", i)
		s, isString := item.(string)
		if !isString {
			v.add(pointer, "must be a string, not %s", jsonKind(item))
			continue
		}
		v.checkBytes(pointer, s, 1, maxCriterion)
		v.checkText(pointer, s)
		out = append(out, s)
	}
	return out
}

func (v *validator) question(raw any, confidence string) *Question {
	obj, ok := raw.(map[string]any)
	if !ok {
		v.add("/question", "must be an object {text, options}, not %s", jsonKind(raw))
		return nil
	}
	v.unknownKeys("/question", obj, questionKeys, "a question")
	q := &Question{Options: []string{}}
	q.Text = v.text(obj, "text", "/question/text", 1, maxQuestionText)

	if rawOptions, present := obj["options"]; present && rawOptions != nil {
		items, isArray := rawOptions.([]any)
		if !isArray {
			v.add("/question/options", "must be an array of strings, not %s", jsonKind(rawOptions))
		} else {
			if n := len(items); n != 0 && (n < minOptions || n > maxOptions) {
				v.add("/question/options", "has %d items, and it needs none, or %d to %d", n, minOptions, maxOptions)
			}
			for i, item := range items {
				pointer := fmt.Sprintf("/question/options/%d", i)
				s, isString := item.(string)
				if !isString {
					v.add(pointer, "must be a string, not %s", jsonKind(item))
					continue
				}
				v.checkBytes(pointer, s, 1, maxOption)
				v.checkText(pointer, s)
				q.Options = append(q.Options, s)
			}
		}
	}
	if confidence == "confirmed" || confidence == "probable" {
		v.add("/question", "a question goes only with the confidence suspected or not_reproducible, not %s: a diagnosis that asks is not confident", confidence)
	}
	return q
}
