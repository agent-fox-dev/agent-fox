package toolio_test

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/agent-fox-dev/agentfox/codefix"
	"github.com/agent-fox-dev/agentfox/codeimpl"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
	"github.com/agent-fox-dev/agentfox/issuetriage"
	"github.com/agent-fox-dev/agentfox/specgen"
)

// TS-09-29 (unit): Envelope.SchemaVersion is tagged json:"schema_version",
// sits immediately after Version, and is never omitted.
func TestTS09_29_EnvelopeSchemaVersionFieldShape(t *testing.T) {
	fields := reflect.VisibleFields(reflect.TypeOf(toolio.Envelope{}))
	idx := func(name string) int {
		for i, f := range fields {
			if f.Name == name {
				return i
			}
		}
		t.Fatalf("Envelope has no field %s", name)
		return -1
	}
	v, sv := idx("Version"), idx("SchemaVersion")
	if sv != v+1 {
		t.Errorf("SchemaVersion is at index %d, want %d (immediately after Version)", sv, v+1)
	}
	if tag := fields[sv].Tag.Get("json"); tag != "schema_version" {
		t.Errorf("json tag = %q, want %q (no omitempty)", tag, "schema_version")
	}
	b, err := json.Marshal(toolio.Envelope{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"schema_version":""`) {
		t.Errorf("a zero Envelope must still marshal schema_version: %s", b)
	}
}

// TS-09-31 (unit): toolio.SchemaVersion is the literal string 2.0.0.
func TestTS09_31_SchemaVersionConstant(t *testing.T) {
	if toolio.SchemaVersion != "2.0.0" {
		t.Errorf("toolio.SchemaVersion = %q, want 2.0.0", toolio.SchemaVersion)
	}
}

// Run.Envelope sets SchemaVersion unconditionally, from the one constant
// (the unit half of TS-09-30).
func TestTS09_30_RunEnvelopeSetsSchemaVersion(t *testing.T) {
	env := toolio.NewRun("fix", "v1").Envelope(toolio.ExitOK, nil, nil)
	if env.SchemaVersion != toolio.SchemaVersion {
		t.Errorf("Envelope.SchemaVersion = %q, want %q", env.SchemaVersion, toolio.SchemaVersion)
	}
}

func TestEnvelopeOKOnlyWhenExitIsZero(t *testing.T) {
	r := toolio.NewRun("issue", "v1")
	if env := r.Envelope(toolio.ExitOK, map[string]string{"a": "b"}, nil); !env.OK {
		t.Error("exit 0 must be ok")
	}
	for _, code := range []int{toolio.ExitFailed, toolio.ExitUsage, toolio.ExitNeedsHuman, toolio.ExitUnverified} {
		if env := r.Envelope(code, nil, &toolio.ErrorInfo{Stage: "s", Category: "c", Message: "m"}); env.OK {
			t.Errorf("exit %d reported ok", code)
		}
	}
}

// An envelope that says ok and carries an error is a contradiction the caller
// should never have to resolve.
func TestEnvelopeRefusesOKWithAnError(t *testing.T) {
	r := toolio.NewRun("fix", "v1")
	env := r.Envelope(toolio.ExitOK, nil, &toolio.ErrorInfo{Stage: "verify", Category: "git", Message: "boom"})
	if env.OK || env.ExitCode != toolio.ExitFailed {
		t.Errorf("got ok=%v exit=%d, want a failure", env.OK, env.ExitCode)
	}
}

func TestEnvelopeSumsPhases(t *testing.T) {
	r := toolio.NewRun("spec", "v1")
	r.AddPhase(toolio.PhaseInfo{Name: "prd", Turns: 3, InputTokens: 100, OutputTokens: 20, CostUSD: 0.5})
	r.AddPhase(toolio.PhaseInfo{Name: "requirements", Turns: 2, InputTokens: 50, OutputTokens: 10, CostUSD: 0.25})

	env := r.Envelope(toolio.ExitOK, nil, nil)
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
	r := toolio.NewRun("issue", "v1")
	r.Warn(toolio.WarnCommentsUnreadable, "low", "something to note")

	code := toolio.Emit(&buf, r.Envelope(toolio.ExitFailed, nil, &toolio.ErrorInfo{
		Stage: "analyse", Category: "budget", Message: "over budget",
	}))
	if code != toolio.ExitFailed {
		t.Errorf("Emit returned %d", code)
	}

	var env toolio.Envelope
	if err := json.Unmarshal(buf.Bytes(), &env); err != nil {
		t.Fatalf("stdout is not one JSON object: %v\n%s", err, buf.String())
	}
	if env.Tool != "issue" || env.OK || env.ExitCode != toolio.ExitFailed {
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
	r := toolio.NewRun("fix", "v1")
	code := toolio.Emit(&buf, r.Envelope(toolio.ExitOK, map[string]any{"bad": make(chan int)}, nil))
	if code != toolio.ExitFailed {
		t.Errorf("Emit returned %d, want a failure", code)
	}
	var env toolio.Envelope
	if err := json.Unmarshal(buf.Bytes(), &env); err != nil {
		t.Fatalf("the fallback is not valid JSON: %v", err)
	}
	if env.Error == nil || env.Error.Stage != "emit" {
		t.Errorf("Error = %+v", env.Error)
	}
}

func TestSortedUnique(t *testing.T) {
	got := toolio.SortedUnique([]string{"b", "a", "b", "", "  ", " c "})
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
		got, err := toolio.SplitArgs(fs, argv)
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
	if _, err := toolio.SplitArgs(fs, []string{"one", "two"}); err == nil {
		t.Fatal("want an error naming the extra input")
	}
}

func TestSplitArgsKeepsBareDashAsStdin(t *testing.T) {
	fs := flag.NewFlagSet("t", flag.ContinueOnError)
	fs.SetOutput(bytes.NewBuffer(nil))
	fs.Bool("dry-run", false, "")
	got, err := toolio.SplitArgs(fs, []string{"--dry-run", "-"})
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
	got, err := toolio.SplitArgs(fs, []string{"--dry-run", "--", "--not-a-flag"})
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

	r := toolio.NewRun("spec", "v1")
	env := r.Envelope(toolio.ExitFailed, missing, &toolio.ErrorInfo{Stage: "prd", Category: "api", Message: "boom"})
	if env.Result != nil {
		t.Errorf("Result = %#v, want it dropped", env.Result)
	}

	var buf bytes.Buffer
	toolio.Emit(&buf, env)
	if strings.Contains(buf.String(), `"result"`) {
		t.Errorf("the envelope carries a null result:\n%s", buf.String())
	}

	// A real result still survives.
	env = r.Envelope(toolio.ExitOK, &result{Stage: "landed"}, nil)
	if env.Result == nil {
		t.Error("a present result was dropped")
	}
}

// TS-05-6 (unit): status and summary are always present, never omitted, on every envelope regardless of outcome.
func TestTS05_6_StatusAndSummaryAlwaysPresent(t *testing.T) {
	type plainResult struct{}
	run := toolio.NewRun("fix", "v1")
	for _, code := range []int{toolio.ExitOK, toolio.ExitFailed} {
		var errInfo *toolio.ErrorInfo
		if code == toolio.ExitFailed {
			errInfo = &toolio.ErrorInfo{Stage: "preflight", Category: "usage", Message: "bad flag"}
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
	run := toolio.NewRun("test", "v1")
	table := map[int]string{
		toolio.ExitOK:         "done",
		toolio.ExitFailed:     "failed",
		toolio.ExitUsage:      "usage",
		toolio.ExitNeedsHuman: "needs_human",
		toolio.ExitUnverified: "unverified",
	}
	for code, want := range table {
		var errInfo *toolio.ErrorInfo
		if code != toolio.ExitOK {
			errInfo = &toolio.ErrorInfo{Stage: "test", Category: "test", Message: "err"}
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
	run := toolio.NewRun("fix", "v1")
	env := run.Envelope(toolio.ExitOK, ts05_8_stub{}, nil)
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
	run := toolio.NewRun("fix", "v1")
	env := run.Envelope(toolio.ExitFailed, plainResult{}, &toolio.ErrorInfo{
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
	run := toolio.NewRun("fix", "v1")
	env := run.Envelope(toolio.ExitOK, plainResult{}, nil)
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
	run := toolio.NewRun("fix", "v1")
	run.Warn(toolio.WarnCode("input_truncated"), "high", "cut")
	run.Warn(toolio.WarnCode("comment_not_posted"), "low", "not posted")

	env1 := run.Envelope(toolio.ExitOK, ts05_11_stub{"fix: committed and landed"}, nil)
	env2 := run.Envelope(toolio.ExitOK, ts05_11_stub{"fix: committed and landed"}, nil)

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
	fullEnvelope := toolio.Envelope{
		Tool:       "fix",
		Version:    "v1",
		OK:         false,
		Status:     "failed",
		ExitCode:   toolio.ExitFailed,
		Summary:    "something failed",
		Error:      &toolio.ErrorInfo{Stage: "verify", Category: "git", Message: "failed"},
		NeedsHuman: &toolio.NeedsHuman{},
		Warnings:   []toolio.Warning{{Code: "input_truncated", Severity: "high", Stage: "input", Message: "warning 1"}},
		Result:     map[string]any{"key": "val"},
		Input:      &toolio.InputInfo{Kind: "text", Origin: "arg", Bytes: 10},
		Usage: &toolio.UsageInfo{
			InputTokens:  10,
			OutputTokens: 20,
			CostUSD:      0.01,
			Turns:        1,
			Phases:       []toolio.PhaseInfo{{Name: "phase1"}},
		},
		Model:      &toolio.ModelInfo{Spec: "model-1", ID: "id-1", Vendor: "vendor", API: "api"},
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
		"tool", "version", "schema_version", "ok", "status", "exit_code", "summary",
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

// TS-05-21 (property): retryable is true only for the api and aborted categories, for any category in the shared vocabulary
func TestTS05_21_RetryableOnlyForApiAndAborted(t *testing.T) {
	allCategories := []string{
		"usage", "input", "auth", "model", "invalid_spec", "blocked", "ambiguous",
		"empty_change", "internal", "budget", "max_turns", "no_result", "unverified",
		"git", "forge", "disk", "api", "aborted",
	}
	for _, cat := range allCategories {
		info := toolio.BuildErrorInfo(cat)
		want := cat == "api" || cat == "aborted"
		if info.Retryable != want {
			t.Errorf("BuildErrorInfo(%q).Retryable = %v, want %v", cat, info.Retryable, want)
		}
		if got := toolio.RetryableFor(cat); got != want {
			t.Errorf("RetryableFor(%q) = %v, want %v", cat, got, want)
		}
		// Also verify via Run.Envelope
		r := toolio.NewRun("fix", "v1")
		env := r.Envelope(toolio.ExitFailed, nil, &toolio.ErrorInfo{Stage: "step", Category: cat})
		if env.Error == nil {
			t.Fatalf("Envelope Error is nil for category %q", cat)
		}
		if env.Error.Retryable != want {
			t.Errorf("Envelope with category %q has Retryable = %v, want %v", cat, env.Error.Retryable, want)
		}
	}
}

// TS-05-22 (unit): error.resumable is set from the result's Resumable() method via a type assertion
type ts05_22_stub struct {
	v bool
}

func (s ts05_22_stub) Resumable() bool {
	return s.v
}

func TestTS05_22_ErrorResumableSetFromMethod(t *testing.T) {
	run := toolio.NewRun("impl", "v1")
	errInfo := &toolio.ErrorInfo{Stage: "verify", Category: "unverified", Message: "checks failed"}
	env1 := run.Envelope(toolio.ExitUnverified, ts05_22_stub{true}, errInfo)
	env2 := run.Envelope(toolio.ExitUnverified, ts05_22_stub{false}, errInfo)

	if env1.Error == nil || !env1.Error.Resumable {
		t.Errorf("env1.Error.Resumable = false, want true")
	}
	if env2.Error == nil || env2.Error.Resumable {
		t.Errorf("env2.Error.Resumable = true, want false")
	}
}

// TS-05-23 (unit): codefix.Result.Resumable always returns false, because branch names are never deterministic
func TestTS05_23_CodefixResultResumableAlwaysFalse(t *testing.T) {
	r := codefix.Result{Branch: "fix/issue-42-abcdef"}
	if r.Resumable() != false {
		t.Errorf("r.Resumable() = %v, want false", r.Resumable())
	}
	r2 := codefix.Result{}
	if r2.Resumable() != false {
		t.Errorf("r2.Resumable() = %v, want false", r2.Resumable())
	}
}

// TS-05-24 (unit): codeimpl.Result.Resumable returns true exactly when Branch is non-empty
func TestTS05_24_CodeimplResultResumableWhenBranchNonEmpty(t *testing.T) {
	r1 := codeimpl.Result{Branch: "impl/09-widget"}
	if r1.Resumable() != true {
		t.Errorf("codeimpl.Result with Branch non-empty Resumable() = %v, want true", r1.Resumable())
	}
	r2 := codeimpl.Result{Branch: ""}
	if r2.Resumable() != false {
		t.Errorf("codeimpl.Result with empty Branch Resumable() = %v, want false", r2.Resumable())
	}
}

// TS-05-25 (unit): specgen.Result.Resumable returns true exactly when SplitPlan is non-empty
func TestTS05_25_SpecgenResultResumableWhenSplitPlanNonEmpty(t *testing.T) {
	r1 := specgen.Result{SplitPlan: ".specs/09_widget.split.json"}
	if r1.Resumable() != true {
		t.Errorf("specgen.Result with SplitPlan non-empty Resumable() = %v, want true", r1.Resumable())
	}
	r2 := specgen.Result{SplitPlan: ""}
	if r2.Resumable() != false {
		t.Errorf("specgen.Result with empty SplitPlan Resumable() = %v, want false", r2.Resumable())
	}
}

// TS-05-26 (unit): issuetriage.Result.Resumable always returns false
func TestTS05_26_IssuetriageResultResumableAlwaysFalse(t *testing.T) {
	r := issuetriage.Result{Action: "filed", Number: 57}
	if r.Resumable() != false {
		t.Errorf("issuetriage.Result.Resumable() = %v, want false", r.Resumable())
	}
}

// TS-05-33 (unit): Run.Warn accepts a WarnCode and severity, and the envelope reports each warning as a code/severity/stage/message object.
func TestTS05_33_WarnAcceptsCodeAndSeverity(t *testing.T) {
	run := toolio.NewRun("issue", "v1")
	run.Warn(toolio.WarnCode("input_truncated"), "high", "the input was truncated at %d bytes", 262144)
	env := run.Envelope(toolio.ExitOK, nil, nil)
	if len(env.Warnings) != 1 {
		t.Fatalf("len(Warnings) = %d, want 1", len(env.Warnings))
	}
	w := env.Warnings[0]
	if w.Code != "input_truncated" || w.Severity != "high" || w.Message != "the input was truncated at 262144 bytes" {
		t.Errorf("Warning = %+v", w)
	}
}

// TS-05-34 (unit): a warning's stage is sourced from the single map[WarnCode]string table, never a call-site parameter.
func TestTS05_34_StageSourcedFromTable(t *testing.T) {
	run := toolio.NewRun("impl", "v1")
	run.Warn(toolio.WarnCode("commit_not_parked"), "high", "the commit could not be parked")
	env := run.Envelope(toolio.ExitOK, nil, nil)
	if len(env.Warnings) != 1 {
		t.Fatalf("len(Warnings) = %d, want 1", len(env.Warnings))
	}
	if got := env.Warnings[0].Stage; got != "park" {
		t.Errorf("Stage = %q, want %q", got, "park")
	}
}

// TS-05-37 (unit): severity is read as given at the call site, not derived from code alone.
func TestTS05_37_SeverityIsCallSiteArgument(t *testing.T) {
	run1 := toolio.NewRun("impl", "v1")
	run1.Warn(toolio.WarnCode("spec_edit_reverted"), "high", "reverted")
	run2 := toolio.NewRun("impl", "v1")
	run2.Warn(toolio.WarnCode("spec_edit_reverted"), "low", "reverted")

	env1 := run1.Envelope(toolio.ExitOK, nil, nil)
	env2 := run2.Envelope(toolio.ExitOK, nil, nil)
	if env1.Warnings[0].Severity != "high" {
		t.Errorf("env1 Severity = %q, want high", env1.Warnings[0].Severity)
	}
	if env2.Warnings[0].Severity != "low" {
		t.Errorf("env2 Severity = %q, want low", env2.Warnings[0].Severity)
	}
}

// TS-05-39 (unit): a call to Run.Warn is reflected in the envelope's warnings array as a full object matching the call site and the table.
func TestTS05_39_WarnReflectedAsFullObject(t *testing.T) {
	run := toolio.NewRun("impl", "v1")
	someErr := fmt.Errorf("permission denied")
	run.Warn(toolio.WarnCode("pull_request_not_opened"), "high", "the pull request could not be opened: %v", someErr)
	env := run.Envelope(toolio.ExitOK, nil, nil)
	if len(env.Warnings) != 1 {
		t.Fatalf("len(Warnings) = %d, want 1", len(env.Warnings))
	}
	w := env.Warnings[0]
	if w.Code != "pull_request_not_opened" || w.Severity != "high" || w.Stage != "land" || !strings.Contains(w.Message, someErr.Error()) {
		t.Errorf("Warning = %+v", w)
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
