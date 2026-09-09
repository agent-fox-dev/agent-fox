package afspec

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// marshal.go
// ---------------------------------------------------------------------------

func TestMarshalJSONOrdersFieldsBySchema(t *testing.T) {
	spec := loadFixture(t, fixtureValidSpec)
	data, err := MarshalJSON(spec.Requirements)
	if err != nil {
		t.Fatalf("MarshalJSON = %v", err)
	}

	order := []string{`"$schema"`, `"spec_id"`, `"spec_name"`, `"schema_version"`,
		`"introduction"`, `"glossary"`, `"requirements"`, `"execution_paths"`}
	last := -1
	out := string(data)
	for _, key := range order {
		i := strings.Index(out, key)
		if i < 0 {
			t.Fatalf("key %s is missing", key)
		}
		if i < last {
			t.Errorf("key %s is out of schema order", key)
		}
		last = i
	}
}

func TestMarshalJSONSortsMapKeys(t *testing.T) {
	req := RequirementsV2Json{Glossary: RequirementsV2JsonGlossary{"zebra": "z", "alpha": "a", "middle": "m"}}
	data, err := MarshalJSON(&req)
	if err != nil {
		t.Fatal(err)
	}
	out := string(data)
	if strings.Index(out, "alpha") > strings.Index(out, "middle") ||
		strings.Index(out, "middle") > strings.Index(out, "zebra") {
		t.Errorf("glossary keys are not sorted:\n%s", out)
	}
}

// TestMarshalJSONDoesNotEscapeHTML guards round-trip fidelity for spec text
// containing <, > or &, which encoding/json escapes by default.
func TestMarshalJSONDoesNotEscapeHTML(t *testing.T) {
	req := RequirementsV2Json{Introduction: `write {"error": <message>} when a && b`}
	data, err := MarshalJSON(&req)
	if err != nil {
		t.Fatal(err)
	}
	out := string(data)
	if strings.Contains(out, `\u003c`) || strings.Contains(out, `\u0026`) {
		t.Errorf("angle brackets or ampersands were escaped:\n%s", out)
	}
	if !strings.Contains(out, "<message>") {
		t.Errorf("the literal text is missing:\n%s", out)
	}
}

func TestMarshalJSONIsDeterministic(t *testing.T) {
	spec := loadFixture(t, fixtureValidSpec)
	first, err := MarshalJSON(spec.Tasks)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		again, err := MarshalJSON(spec.Tasks)
		if err != nil {
			t.Fatal(err)
		}
		if string(again) != string(first) {
			t.Fatal("two marshals of the same artifact differ")
		}
	}
}

// TestMarshalJSONIsStableUnderDecodeReencode checks that our output decodes
// with encoding/json and re-marshals to the same bytes. The two marshallers do
// not agree field for field on purpose: an explicitly empty array such as the
// integration task's "criteria": [] is preserved here and dropped by
// encoding/json's omitempty, and dropping it would break round-trip fidelity.
func TestMarshalJSONIsStableUnderDecodeReencode(t *testing.T) {
	spec := loadFixture(t, fixtureValidSpec)
	first, err := MarshalJSON(spec.Tasks)
	if err != nil {
		t.Fatal(err)
	}

	var decoded TasksV2Json
	if err := json.Unmarshal(first, &decoded); err != nil {
		t.Fatalf("our output does not decode: %v", err)
	}
	second, err := MarshalJSON(&decoded)
	if err != nil {
		t.Fatal(err)
	}
	if string(second) != string(first) {
		t.Errorf("marshal → unmarshal → marshal is not stable:\n%s", firstDifference(string(first), string(second)))
	}

	if decoded.Tasks[2].Criteria == nil {
		t.Error(`the integration task's explicit "criteria": [] decoded as absent`)
	}
}

func TestMarshalJSONNil(t *testing.T) {
	data, err := MarshalJSON(nil)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "null\n" {
		t.Errorf("= %q; want %q", data, "null\n")
	}
}

// ---------------------------------------------------------------------------
// prd.go
// ---------------------------------------------------------------------------

func TestRenderPRDOmitsEmptyOptionalFields(t *testing.T) {
	spec := &Spec{
		SpecID: "05", SpecName: "feature", Title: "Feature", Status: "draft",
		CreatedAt: "2026-01-01T00:00:00Z", UpdatedAt: "2026-01-01T00:00:00Z",
		SchemaVersion: SchemaVersion, PRDBody: "# Feature\n",
	}
	out := string(renderPRD(spec))
	for _, absent := range []string{"owner:", "source:", "supersedes:", "tags:"} {
		if strings.Contains(out, absent) {
			t.Errorf("empty optional field %q was written:\n%s", absent, out)
		}
	}
	if !strings.Contains(out, "intent_hash: null") {
		t.Errorf("intent_hash is not written as null:\n%s", out)
	}
}

func TestRenderPRDWritesPopulatedOptionalFields(t *testing.T) {
	spec := &Spec{
		SpecID: "05", SpecName: "feature", Title: "Feature", Status: "draft",
		CreatedAt: "2026-01-01T00:00:00Z", UpdatedAt: "2026-01-01T00:00:00Z",
		SchemaVersion: SchemaVersion, Owner: "someone", Source: "docs/prd.md",
		Supersedes: []string{"04"}, Tags: []string{"a", "b"}, PRDBody: "# Feature\n",
	}
	out := string(renderPRD(spec))
	for _, want := range []string{
		`owner: "someone"`, `source: "docs/prd.md"`,
		`supersedes: ["04"]`, `tags: ["a", "b"]`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q:\n%s", want, out)
		}
	}
}

func TestParsePRDRejectsMissingDelimiters(t *testing.T) {
	cases := map[string]string{
		"no opening delimiter": "spec_id: \"01\"\n---\n# Body\n",
		"no closing delimiter": "---\nspec_id: \"01\"\n# Body\n",
	}
	for name, content := range cases {
		t.Run(name, func(t *testing.T) {
			if _, _, err := parsePRD([]byte(content), "prd.md"); err == nil {
				t.Error("a malformed prd.md was accepted")
			}
		})
	}
}

func TestParsePRDRequiresIdentityFields(t *testing.T) {
	_, _, err := parsePRD([]byte("---\ntitle: \"x\"\n---\n# Body\n"), "prd.md")
	if err == nil {
		t.Fatal("frontmatter without spec_id was accepted")
	}
	if !strings.Contains(err.Error(), "spec_id") {
		t.Errorf("the error does not name the missing field: %v", err)
	}
}

// ---------------------------------------------------------------------------
// bootstrap.go
// ---------------------------------------------------------------------------

func TestBootstrapFinalizeReportsMissingArtifacts(t *testing.T) {
	b := NewBootstrapSpec("01", "test_feature")
	spec, errs := b.Finalize()
	if spec != nil {
		t.Error("Finalize returned a Spec despite missing artifacts")
	}
	if len(errs) != 4 {
		t.Fatalf("errors = %v; want one per missing artifact", errs)
	}
	for _, e := range errs {
		if e.Rule != "bootstrap" {
			t.Errorf("rule = %q; want bootstrap", e.Rule)
		}
	}
}

func TestBootstrapFinalizeValidatesTheAssembledSpec(t *testing.T) {
	source := loadFixture(t, fixtureValidSpec)

	b := NewBootstrapSpec("01", "test_feature")
	b.Requirements = source.Requirements
	b.TestSpec = source.TestSpec
	b.Tasks = source.Tasks
	b.PRDBody = source.PRDBody

	spec, errs := b.Finalize()
	if len(errs) > 0 {
		t.Fatalf("Finalize on complete artifacts = %v", errs)
	}
	if spec.SchemaVersion != SchemaVersion {
		t.Errorf("schema_version = %d; want %d", spec.SchemaVersion, SchemaVersion)
	}
}

func TestBootstrapFinalizeRejectsIdentityMismatch(t *testing.T) {
	source := loadFixture(t, fixtureValidSpec)

	b := NewBootstrapSpec("07", "other_feature")
	b.Requirements = source.Requirements
	b.TestSpec = source.TestSpec
	b.Tasks = source.Tasks
	b.PRDBody = source.PRDBody

	if _, errs := b.Finalize(); len(errs) == 0 {
		t.Fatal("artifacts declaring a different spec_id were accepted")
	}
}

// ---------------------------------------------------------------------------
// errors.go
// ---------------------------------------------------------------------------

func TestErrorsUnwrapToSpecError(t *testing.T) {
	base := &SpecError{Msg: "boom"}
	cases := []error{
		&LoadError{Msg: "load", File: "prd.md", Err: base},
		&SaveError{Msg: "save", Err: base},
		&LifecycleError{Msg: "lifecycle", Err: base},
		&IntentError{Msg: "intent", Err: base},
		&BootstrapError{Msg: "bootstrap", Err: base},
	}
	for _, err := range cases {
		var specErr *SpecError
		if !errors.As(err, &specErr) {
			t.Errorf("%T does not unwrap to *SpecError", err)
		}
		if err.Error() == "" {
			t.Errorf("%T has an empty Error()", err)
		}
	}
}

// ---------------------------------------------------------------------------
// estimate_tokens.go
// ---------------------------------------------------------------------------

func TestEstimateTokens(t *testing.T) {
	if EstimateTokens("") != 0 {
		t.Errorf("EstimateTokens(\"\") = %d; want 0", EstimateTokens(""))
	}
	short, long := EstimateTokens("a short string"), EstimateTokens(strings.Repeat("a longer string ", 100))
	if long <= short {
		t.Errorf("a longer text estimated %d tokens, a shorter one %d", long, short)
	}
}

// ---------------------------------------------------------------------------
// lint.go
// ---------------------------------------------------------------------------

func TestRunLintSpecs(t *testing.T) {
	root := specRoot(t, map[string]string{"01_test_feature": fixtureValidSpec})

	result, err := RunLintSpecs(root, true)
	if err != nil {
		t.Fatalf("RunLintSpecs = %v", err)
	}
	if result.ExitCode != 0 {
		t.Errorf("exit code = %d on a valid spec; findings: %v", result.ExitCode, result.Findings)
	}
}

func TestRunLintSpecsReportsErrors(t *testing.T) {
	root := specRoot(t, map[string]string{"01_test_feature": fixtureValidSpec})

	// Orphan a test so that C7 fires.
	path := filepath.Join(root, "01_test_feature", "tasks.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var tasks TasksV2Json
	if err := json.Unmarshal(data, &tasks); err != nil {
		t.Fatal(err)
	}
	tasks.Tasks[0].Tests = []string{"TS-01-1"}
	out, err := MarshalJSON(&tasks)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, out, 0o644); err != nil {
		t.Fatal(err)
	}

	result, err := RunLintSpecs(root, true)
	if err != nil {
		t.Fatal(err)
	}
	if result.ExitCode == 0 {
		t.Fatal("linting a spec with orphaned tests exited 0")
	}
	found := false
	for _, f := range result.Findings {
		if f.Rule == "C7" {
			found = true
		}
	}
	if !found {
		t.Errorf("no C7 finding: %v", result.Findings)
	}
}

// TestLintSkipsFullyImplementedSpecs covers the v2 definition of "fully
// implemented": every task done or dropped.
func TestLintSkipsFullyImplementedSpecs(t *testing.T) {
	root := specRoot(t, map[string]string{"01_test_feature": fixtureValidSpec})
	path := filepath.Join(root, "01_test_feature", "tasks.json")

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var tasks TasksV2Json
	if err := json.Unmarshal(data, &tasks); err != nil {
		t.Fatal(err)
	}
	// Break the plan and mark every task done: skipped, so no findings.
	tasks.Tasks[0].Tests = []string{"TS-01-1"}
	for i := range tasks.Tasks {
		tasks.Tasks[i].State = TaskStateDone
	}
	out, err := MarshalJSON(&tasks)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, out, 0o644); err != nil {
		t.Fatal(err)
	}

	result, err := RunLintSpecs(root, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Findings) != 0 {
		t.Errorf("a fully implemented spec produced findings: %v", result.Findings)
	}

	result, err = RunLintSpecs(root, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Findings) == 0 {
		t.Error("--all did not lint the fully implemented spec")
	}
}

func TestSortFindingsAndExitCode(t *testing.T) {
	findings := []LintFinding{
		{SpecName: "02", Severity: "warning", Rule: "scope"},
		{SpecName: "01", Severity: "error", Rule: "C7"},
		{SpecName: "01", Severity: "warning", Rule: "vague"},
	}
	sorted := SortFindings(findings)
	if sorted[0].SpecName != "01" || sorted[0].Severity != "error" {
		t.Errorf("sorted[0] = %+v; want the spec 01 error first", sorted[0])
	}

	if ComputeExitCode(sorted) == 0 {
		t.Error("findings containing an error produced exit code 0")
	}
	if ComputeExitCode([]LintFinding{{Severity: "warning"}}) != 0 {
		t.Error("warnings alone must not fail the lint")
	}
	if ComputeExitCode(nil) != 0 {
		t.Error("no findings produced a non-zero exit code")
	}
}

// ---------------------------------------------------------------------------
// schemas.go
// ---------------------------------------------------------------------------

func TestBundledSchemasAreTheV2Set(t *testing.T) {
	schemas := Schemas()
	want := []string{
		PRDFrontmatterSchemaName, RequirementsSchemaName,
		TestSpecSchemaName, TasksSchemaName,
	}
	if len(schemas) != len(want) {
		t.Errorf("bundled schemas = %d; want %d", len(schemas), len(want))
	}
	for _, name := range want {
		if len(schemas[name]) == 0 {
			t.Errorf("schema %q is not bundled", name)
		}
	}
	for name := range schemas {
		if strings.Contains(name, ".v1.") {
			t.Errorf("a version 1 schema is still bundled: %q", name)
		}
	}
}

func TestSchemasCompile(t *testing.T) {
	compiled, err := getCompiledSchemas()
	if err != nil {
		t.Fatalf("getCompiledSchemas = %v", err)
	}
	if len(compiled) != 4 {
		t.Errorf("compiled = %d schemas; want 4", len(compiled))
	}
}
