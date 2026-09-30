package toolio

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"strings"
	"testing"
)

func TestEnvelopeOKOnlyWhenExitIsZero(t *testing.T) {
	r := NewRun("issue", "v1")
	if env := r.Envelope(ExitOK, map[string]string{"a": "b"}, nil); !env.OK {
		t.Error("exit 0 must be ok")
	}
	for _, code := range []int{ExitFailed, ExitUsage, ExitNeedsHuman, ExitUnverified} {
		if env := r.Envelope(code, nil, &ErrorInfo{Stage: "s", Category: "c", Message: "m"}); env.OK {
			t.Errorf("exit %d reported ok", code)
		}
	}
}

// An envelope that says ok and carries an error is a contradiction the caller
// should never have to resolve.
func TestEnvelopeRefusesOKWithAnError(t *testing.T) {
	r := NewRun("fix", "v1")
	env := r.Envelope(ExitOK, nil, &ErrorInfo{Stage: "verify", Category: "git", Message: "boom"})
	if env.OK || env.ExitCode != ExitFailed {
		t.Errorf("got ok=%v exit=%d, want a failure", env.OK, env.ExitCode)
	}
}

func TestEnvelopeSumsPhases(t *testing.T) {
	r := NewRun("spec", "v1")
	r.AddPhase(PhaseInfo{Name: "prd", Turns: 3, InputTokens: 100, OutputTokens: 20, CostUSD: 0.5})
	r.AddPhase(PhaseInfo{Name: "requirements", Turns: 2, InputTokens: 50, OutputTokens: 10, CostUSD: 0.25})

	env := r.Envelope(ExitOK, nil, nil)
	if env.Usage == nil {
		t.Fatal("Usage is nil")
	}
	if env.Usage.Turns != 5 || env.Usage.InputTokens != 150 || env.Usage.OutputTokens != 30 {
		t.Errorf("Usage = %+v", env.Usage)
	}
	if env.Usage.CostUSD != 0.75 {
		t.Errorf("CostUSD = %v", env.Usage.CostUSD)
	}
	if len(env.Usage.Phases) != 2 {
		t.Errorf("phases = %d", len(env.Usage.Phases))
	}
}

// Exactly one JSON object, on every path, is the contract: a caller that has
// to parse two formats and guess which one it got is not being served.
func TestEmitWritesOneJSONObject(t *testing.T) {
	var buf bytes.Buffer
	r := NewRun("issue", "v1")
	r.Warn("something to note")

	code := Emit(&buf, r.Envelope(ExitFailed, nil, &ErrorInfo{
		Stage: "analyse", Category: "budget", Message: "over budget",
	}))
	if code != ExitFailed {
		t.Errorf("Emit returned %d", code)
	}

	var env Envelope
	if err := json.Unmarshal(buf.Bytes(), &env); err != nil {
		t.Fatalf("stdout is not one JSON object: %v\n%s", err, buf.String())
	}
	if env.Tool != "issue" || env.OK || env.ExitCode != ExitFailed {
		t.Errorf("envelope = %+v", env)
	}
	if env.Error == nil || env.Error.Category != "budget" {
		t.Errorf("Error = %+v", env.Error)
	}
	if len(env.Warnings) != 1 {
		t.Errorf("Warnings = %v", env.Warnings)
	}
	if !strings.HasSuffix(buf.String(), "\n") {
		t.Error("the object should end with a newline")
	}
}

// A result that cannot be marshalled must not lose the exit code.
func TestEmitFallsBackWhenTheResultCannotEncode(t *testing.T) {
	var buf bytes.Buffer
	r := NewRun("fix", "v1")
	code := Emit(&buf, r.Envelope(ExitOK, map[string]any{"bad": make(chan int)}, nil))
	if code != ExitFailed {
		t.Errorf("Emit returned %d, want a failure", code)
	}
	var env Envelope
	if err := json.Unmarshal(buf.Bytes(), &env); err != nil {
		t.Fatalf("the fallback is not valid JSON: %v", err)
	}
	if env.Error == nil || env.Error.Stage != "emit" {
		t.Errorf("Error = %+v", env.Error)
	}
}

func TestSortedUnique(t *testing.T) {
	got := SortedUnique([]string{"b", "a", "b", "", "  ", " c "})
	want := []string{"a", "b", "c"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v, want %v", got, want)
		}
	}
}

// A report is frequently a multi-word string, and requiring it last is a
// papercut a caller hits every time.
func TestSplitArgsAcceptsFlagsOnEitherSide(t *testing.T) {
	newFS := func() (*flag.FlagSet, *string, *bool) {
		fs := flag.NewFlagSet("t", flag.ContinueOnError)
		fs.SetOutput(bytes.NewBuffer(nil))
		repo := fs.String("repo", "", "")
		dry := fs.Bool("dry-run", false, "")
		return fs, repo, dry
	}

	cases := [][]string{
		{"--repo", "a/b", "--dry-run", "the report"},
		{"the report", "--repo", "a/b", "--dry-run"},
		{"--repo=a/b", "the report", "--dry-run"},
		{"--dry-run", "the report", "--repo", "a/b"},
	}
	for _, argv := range cases {
		fs, repo, dry := newFS()
		got, err := SplitArgs(fs, argv)
		if err != nil {
			t.Fatalf("SplitArgs(%v): %v", argv, err)
		}
		if got != "the report" || *repo != "a/b" || !*dry {
			t.Errorf("SplitArgs(%v) = %q, repo=%q, dry=%v", argv, got, *repo, *dry)
		}
	}
}

func TestSplitArgsRejectsTwoPositionals(t *testing.T) {
	fs := flag.NewFlagSet("t", flag.ContinueOnError)
	fs.SetOutput(bytes.NewBuffer(nil))
	fs.Bool("dry-run", false, "")
	if _, err := SplitArgs(fs, []string{"one", "two"}); err == nil {
		t.Fatal("want an error naming the extra input")
	}
}

func TestSplitArgsKeepsBareDashAsStdin(t *testing.T) {
	fs := flag.NewFlagSet("t", flag.ContinueOnError)
	fs.SetOutput(bytes.NewBuffer(nil))
	fs.Bool("dry-run", false, "")
	got, err := SplitArgs(fs, []string{"--dry-run", "-"})
	if err != nil {
		t.Fatalf("SplitArgs: %v", err)
	}
	if got != "-" {
		t.Errorf("got %q, want -", got)
	}
}

// Everything after -- is the input, even when it looks like a flag.
func TestSplitArgsHonoursDoubleDash(t *testing.T) {
	fs := flag.NewFlagSet("t", flag.ContinueOnError)
	fs.SetOutput(bytes.NewBuffer(nil))
	fs.Bool("dry-run", false, "")
	got, err := SplitArgs(fs, []string{"--dry-run", "--", "--not-a-flag"})
	if err != nil {
		t.Fatalf("SplitArgs: %v", err)
	}
	if got != "--not-a-flag" {
		t.Errorf("got %q", got)
	}
}

// A pipeline that fails before it has a result returns a typed nil pointer,
// and an interface holding one is not nil: without care the envelope grows a
// "result": null that a caller has to handle as a third case.
func TestEnvelopeDropsATypedNilResult(t *testing.T) {
	type result struct {
		Stage string `json:"stage"`
	}
	var missing *result

	r := NewRun("spec", "v1")
	env := r.Envelope(ExitFailed, missing, &ErrorInfo{Stage: "prd", Category: "api", Message: "boom"})
	if env.Result != nil {
		t.Errorf("Result = %#v, want it dropped", env.Result)
	}

	var buf bytes.Buffer
	Emit(&buf, env)
	if strings.Contains(buf.String(), `"result"`) {
		t.Errorf("the envelope carries a null result:\n%s", buf.String())
	}

	// A real result still survives.
	env = r.Envelope(ExitOK, &result{Stage: "landed"}, nil)
	if env.Result == nil {
		t.Error("a present result was dropped")
	}
}

// TS-05-6 (unit): status and summary are always present, never omitted, on every envelope regardless of outcome.
func TestTS05_6_StatusAndSummaryAlwaysPresent(t *testing.T) {
	type plainResult struct{}
	run := NewRun("fix", "v1")
	for _, code := range []int{ExitOK, ExitFailed} {
		var errInfo *ErrorInfo
		if code == ExitFailed {
			errInfo = &ErrorInfo{Stage: "preflight", Category: "usage", Message: "bad flag"}
		}
		env := run.Envelope(code, plainResult{}, errInfo)
		b, err := json.Marshal(env)
		if err != nil {
			t.Fatalf("Marshal failed: %v", err)
		}
		if !bytes.Contains(b, []byte(`"status"`)) {
			t.Errorf("code %d: missing status key in %s", code, string(b))
		}
		if !bytes.Contains(b, []byte(`"summary"`)) {
			t.Errorf("code %d: missing summary key in %s", code, string(b))
		}
		if env.Status == "" {
			t.Errorf("code %d: env.Status is empty", code)
		}
		if env.Summary == "" {
			t.Errorf("code %d: env.Summary is empty", code)
		}
	}
}

// TS-05-7 (unit): status is derived one-to-one from the exit code, in both directions, for every code in the table.
func TestTS05_7_StatusDerivedFromExitCode(t *testing.T) {
	run := NewRun("test", "v1")
	table := map[int]string{
		ExitOK:         "done",
		ExitFailed:     "failed",
		ExitUsage:      "usage",
		ExitNeedsHuman: "needs_human",
		ExitUnverified: "unverified",
	}
	for code, want := range table {
		var errInfo *ErrorInfo
		if code != ExitOK {
			errInfo = &ErrorInfo{Stage: "test", Category: "test", Message: "err"}
		}
		env := run.Envelope(code, nil, errInfo)
		if env.Status != want {
			t.Errorf("exit code %d: got status %q, want %q", code, env.Status, want)
		}
		if (env.Status == "done") != env.OK {
			t.Errorf("status %q disagrees with ok %v", env.Status, env.OK)
		}
		if env.ExitCode != code {
			t.Errorf("exit code mismatch: got %d, want %d", env.ExitCode, code)
		}
	}
}

// TS-05-8 (unit): A Result implementing Summary() supplies the envelope's summary, truncated to 200 characters.
type ts05_8_stub struct{}

func (ts05_8_stub) Summary() string {
	return strings.Repeat("x", 250)
}

func TestTS05_8_ResultSummaryTruncatedTo200(t *testing.T) {
	run := NewRun("fix", "v1")
	env := run.Envelope(ExitOK, ts05_8_stub{}, nil)
	if len(env.Summary) != 200 {
		t.Fatalf("summary length = %d, want 200", len(env.Summary))
	}
	if env.Summary != strings.Repeat("x", 200) {
		t.Errorf("summary content mismatch")
	}
}

// TS-05-9 (unit): A Result with no Summary() falls back to error.message when ok is false.
func TestTS05_9_ResultWithoutSummaryFallsBackToErrorMessageWhenFailed(t *testing.T) {
	type plainResult struct{}
	run := NewRun("fix", "v1")
	env := run.Envelope(ExitFailed, plainResult{}, &ErrorInfo{
		Stage:    "land",
		Category: "forge",
		Message:  "the pull request could not be opened",
	})
	if env.Summary != "the pull request could not be opened" {
		t.Errorf("got summary %q, want %q", env.Summary, "the pull request could not be opened")
	}
}

// TS-05-10 (unit): A Result with no Summary() falls back to "<tool>: done" when ok is true.
func TestTS05_10_ResultWithoutSummaryFallsBackToToolDoneWhenOK(t *testing.T) {
	type plainResult struct{}
	run := NewRun("fix", "v1")
	env := run.Envelope(ExitOK, plainResult{}, nil)
	if env.Summary != "fix: done" {
		t.Errorf("got summary %q, want %q", env.Summary, "fix: done")
	}
}

// TS-05-11 (unit): A high-severity warning on an ok:true run appends a fixed, deterministic clause noting the count and severity to the summary.
type ts05_11_stub struct {
	summary string
}

func (s ts05_11_stub) Summary() string {
	return s.summary
}

func TestTS05_11_HighSeverityWarningAppendsDeterministicClause(t *testing.T) {
	run := NewRun("fix", "v1")
	run.warnWithSeverity(WarnCode("input_truncated"), "high", "cut")
	run.warnWithSeverity(WarnCode("comment_not_posted"), "low", "not posted")

	env1 := run.Envelope(ExitOK, ts05_11_stub{"fix: committed and landed"}, nil)
	env2 := run.Envelope(ExitOK, ts05_11_stub{"fix: committed and landed"}, nil)

	if !strings.HasPrefix(env1.Summary, "fix: committed and landed") {
		t.Errorf("env1.Summary does not start with prefix: %q", env1.Summary)
	}
	if !strings.Contains(env1.Summary, "1") || !strings.Contains(env1.Summary, "high") {
		t.Errorf("env1.Summary does not contain '1' and 'high': %q", env1.Summary)
	}
	if env1.Summary != env2.Summary {
		t.Errorf("env1.Summary (%q) != env2.Summary (%q)", env1.Summary, env2.Summary)
	}
}

// TS-05-12 (unit): The envelope's JSON keys are emitted in the fixed field order, with usage.phases last.
func TestTS05_12_EnvelopeKeyOrder(t *testing.T) {
	fullEnvelope := Envelope{
		Tool:       "fix",
		Version:    "v1",
		OK:         false,
		Status:     "failed",
		ExitCode:   ExitFailed,
		Summary:    "something failed",
		Error:      &ErrorInfo{Stage: "verify", Category: "git", Message: "failed"},
		NeedsHuman: &NeedsHuman{},
		Warnings:   []string{"warning 1"},
		Result:     map[string]any{"key": "val"},
		Input:      &InputInfo{Kind: "text", Origin: "arg", Bytes: 10},
		Usage: &UsageInfo{
			InputTokens:  10,
			OutputTokens: 20,
			CostUSD:      0.01,
			Turns:        1,
			Phases:       []PhaseInfo{{Name: "phase1"}},
		},
		Model:      &ModelInfo{Spec: "model-1", ID: "id-1", Vendor: "vendor", API: "api"},
		DurationMS: 123,
		StartedAt:  "2025-01-01T00:00:00Z",
	}

	b, err := json.Marshal(fullEnvelope)
	if err != nil {
		t.Fatalf("json.Marshal failed: %v", err)
	}

	order, err := extractTopLevelKeyOrder(b)
	if err != nil {
		t.Fatalf("extractTopLevelKeyOrder failed: %v", err)
	}

	wantOrder := []string{
		"tool", "version", "ok", "status", "exit_code", "summary",
		"error", "needs_human", "warnings", "result", "input",
		"usage", "model", "duration_ms", "started_at",
	}
	if len(order) != len(wantOrder) {
		t.Fatalf("got keys %v (len %d), want %v (len %d)", order, len(order), wantOrder, len(wantOrder))
	}
	for i := range wantOrder {
		if order[i] != wantOrder[i] {
			t.Errorf("key [%d] = %q, want %q (full order: %v)", i, order[i], wantOrder[i], order)
		}
	}

	usageOrder, err := extractNestedKeyOrder(b, "usage")
	if err != nil {
		t.Fatalf("extractNestedKeyOrder failed: %v", err)
	}
	if len(usageOrder) == 0 || usageOrder[len(usageOrder)-1] != "phases" {
		t.Errorf("usageOrder last key = %v, want 'phases'", usageOrder)
	}
}

func extractTopLevelKeyOrder(data []byte) ([]string, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	if delim, ok := tok.(json.Delim); !ok || delim != '{' {
		return nil, fmt.Errorf("expected {, got %v", tok)
	}
	var keys []string
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return nil, err
		}
		key, ok := tok.(string)
		if !ok {
			return nil, fmt.Errorf("expected string key, got %v", tok)
		}
		keys = append(keys, key)
		var val any
		if err := dec.Decode(&val); err != nil {
			return nil, err
		}
	}
	return keys, nil
}

func extractNestedKeyOrder(data []byte, parentKey string) ([]string, error) {
	var top map[string]json.RawMessage
	if err := json.Unmarshal(data, &top); err != nil {
		return nil, err
	}
	raw, ok := top[parentKey]
	if !ok {
		return nil, fmt.Errorf("key %q not found", parentKey)
	}
	return extractTopLevelKeyOrder(raw)
}
