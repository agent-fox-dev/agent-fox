package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// setupRenderSpec writes a complete, valid spec into tmpDir/.specs/08_my_spec
// and returns the spec root.
func setupRenderSpec(t *testing.T, tmpDir string) string {
	t.Helper()
	return newSpecRoot(t, tmpDir, "08", "my_spec", specFixture{})
}

// runRender executes `spec render` with the given arguments and returns
// stdout. It fails the test when Execute returns an error.
func runRender(t *testing.T, specRoot string, args ...string) string {
	t.Helper()
	cmd := newRootCmd()
	stdout := new(bytes.Buffer)
	cmd.SetOut(stdout)
	cmd.SetErr(new(bytes.Buffer))
	cmd.SetArgs(append([]string{"--spec-dir", specRoot, "render"}, args...))
	if err := cmd.Execute(); err != nil {
		t.Fatalf("spec render %v = %v\noutput: %s", args, err, stdout.String())
	}
	return stdout.String()
}

// TestRenderEmitsMarkdownNotRawJSON is the point of the change: `spec render`
// prints what the library's renderer produces. Under v1 it printed the raw
// JSON files, so the Markdown renderer's output never reached an operator.
func TestRenderEmitsMarkdownNotRawJSON(t *testing.T) {
	specRoot := setupRenderSpec(t, t.TempDir())
	t.Setenv("AF_AGENT", "")

	output := runRender(t, specRoot, "08_my_spec")

	if strings.Contains(output, `"$schema"`) || strings.Contains(output, `"schema_version"`) {
		t.Errorf("the output contains raw JSON:\n%s", output)
	}
	for _, want := range []string{
		"# requirements",
		"### 08-REQ-1: Widget storage",
		"WHEN a client submits a widget with a name, THE widget service SHALL",
		"→ HTTP 201 with body {id: string}",
		"# test_spec",
		"### TS-08-3 (smoke):",
		"**Real components (must not be mocked):**",
		"# tasks",
		"### [ ] 1. Store and serve widgets (implement, pending)",
		"**Done when:**",
	} {
		if !strings.Contains(output, want) {
			t.Errorf("the render is missing %q\noutput:\n%s", want, output)
		}
	}
}

func TestRenderSeparatesArtifactsWithARule(t *testing.T) {
	specRoot := setupRenderSpec(t, t.TempDir())
	t.Setenv("AF_AGENT", "")

	output := runRender(t, specRoot, "08_my_spec")
	if strings.Count(output, "\n---") < 2 {
		t.Errorf("artifacts are not separated by a rule:\n%s", output)
	}
	if strings.HasPrefix(strings.TrimSpace(output), "{") {
		t.Error("the default render emitted a JSON envelope")
	}
}

func TestRenderCombinedFollowsSectionOrder(t *testing.T) {
	tmpDir := t.TempDir()
	specRoot := newSpecRoot(t, tmpDir, "08", "my_spec", specFixture{})
	writeArchitecture(t, filepath.Join(specRoot, "08_my_spec"))
	t.Setenv("AF_AGENT", "")

	output := runRender(t, specRoot, "--combined", "08_my_spec")

	sections := []string{"# PRD", "# Architecture", "# Requirements", "# Test Specification", "# Tasks"}
	last := -1
	for _, section := range sections {
		i := strings.Index(output, section)
		if i < 0 {
			t.Fatalf("section %q is missing from the combined render:\n%s", section, output)
		}
		if i < last {
			t.Errorf("section %q is out of order", section)
		}
		last = i
	}
	if strings.Contains(output, "schema_version: 2") {
		t.Error("the combined render leaked the PRD frontmatter")
	}
}

func TestRenderCombinedWithoutArchitecture(t *testing.T) {
	specRoot := setupRenderSpec(t, t.TempDir())
	t.Setenv("AF_AGENT", "")

	output := runRender(t, specRoot, "--combined", "08_my_spec")
	if strings.Contains(output, "# Architecture") {
		t.Error("an Architecture section appeared for a spec that has no architecture.md")
	}
	if !strings.Contains(output, "# Requirements") {
		t.Error("the combined render is missing its Requirements section")
	}
}

func TestRenderJSONCombined(t *testing.T) {
	specRoot := setupRenderSpec(t, t.TempDir())
	t.Setenv("AF_AGENT", "")

	output := runRender(t, specRoot, "--combined", "--json", "08_my_spec")

	var parsed map[string]any
	if err := json.Unmarshal([]byte(output), &parsed); err != nil {
		t.Fatalf("stdout is not valid JSON: %v\noutput: %s", err, output)
	}
	if ok, _ := parsed["ok"].(bool); !ok {
		t.Errorf("ok = %v; want true", parsed["ok"])
	}
	if parsed["format"] != "combined" {
		t.Errorf("format = %v; want combined", parsed["format"])
	}
	content, _ := parsed["content"].(string)
	if !strings.Contains(content, "# Requirements") || !strings.Contains(content, "08-REQ-1.1") {
		t.Errorf("the envelope carries no Markdown render:\n%s", content)
	}
}

func TestRenderJSONIndividual(t *testing.T) {
	specRoot := setupRenderSpec(t, t.TempDir())
	t.Setenv("AF_AGENT", "")

	output := runRender(t, specRoot, "--json", "08_my_spec")

	var parsed map[string]any
	if err := json.Unmarshal([]byte(output), &parsed); err != nil {
		t.Fatalf("stdout is not valid JSON: %v\noutput: %s", err, output)
	}
	if parsed["format"] != "individual" {
		t.Errorf("format = %v; want individual", parsed["format"])
	}
	artifacts, ok := parsed["artifacts"].(map[string]any)
	if !ok {
		t.Fatalf("artifacts is %T; want an object", parsed["artifacts"])
	}
	for _, key := range []string{"prd", "requirements", "test_spec", "tasks"} {
		content, _ := artifacts[key].(string)
		if content == "" {
			t.Errorf("artifact %q is empty", key)
		}
	}
	if strings.Contains(artifacts["requirements"].(string), `"$schema"`) {
		t.Error("the requirements artifact is raw JSON, not Markdown")
	}
}

func TestRenderAgentModeAutoEnablesJSON(t *testing.T) {
	specRoot := setupRenderSpec(t, t.TempDir())
	t.Setenv("AF_AGENT", "1")

	output := runRender(t, specRoot, "08_my_spec")
	var parsed map[string]any
	if err := json.Unmarshal([]byte(output), &parsed); err != nil {
		t.Fatalf("agent mode did not emit JSON: %v\noutput: %s", err, output)
	}
	if parsed["format"] != "individual" {
		t.Errorf("format = %v; want individual", parsed["format"])
	}
}

// TestRenderScopedToATask covers §11.1: the scoped render is what a coder
// receives — the task's requirements and tests in full, everything else as one
// line each.
func TestRenderScopedToATask(t *testing.T) {
	specRoot := setupRenderSpec(t, t.TempDir())
	t.Setenv("AF_AGENT", "")

	output := runRender(t, specRoot, "--task", "2", "08_my_spec")

	if !strings.Contains(output, "### [ ] 2. Verify the store-and-read path end to end") {
		t.Errorf("the target task is not rendered in full:\n%s", output)
	}
	if !strings.Contains(output, "- [ ] 1. Store and serve widgets") {
		t.Errorf("the other task is not summarised as one line:\n%s", output)
	}
	if strings.Contains(output, "### [ ] 1. Store and serve widgets") {
		t.Errorf("a task outside the scope was rendered in full:\n%s", output)
	}
	if !strings.Contains(output, "TS-08-3") {
		t.Errorf("the task's own test is missing:\n%s", output)
	}
	if strings.Contains(output, "TS-08-1") {
		t.Errorf("a test belonging to another task was rendered:\n%s", output)
	}
	// The smoke test verifies 08-PATH-1, so the path is in scope.
	if !strings.Contains(output, "### 08-PATH-1:") {
		t.Errorf("the path the task's smoke test verifies is missing:\n%s", output)
	}
}

func TestRenderMaxTokensTruncates(t *testing.T) {
	tmpDir := t.TempDir()
	specRoot := newSpecRoot(t, tmpDir, "08", "my_spec", specFixture{})
	writeArchitecture(t, filepath.Join(specRoot, "08_my_spec"))
	t.Setenv("AF_AGENT", "")

	full := runRender(t, specRoot, "--combined", "08_my_spec")
	if !strings.Contains(full, "# Architecture") {
		t.Fatal("the unbudgeted render has no Architecture section to drop")
	}

	truncated := runRender(t, specRoot, "--combined", "--max-tokens", "10", "08_my_spec")
	if strings.Contains(truncated, "# Architecture") {
		t.Error("the architecture section survived a budget it cannot fit in")
	}
	if len(truncated) >= len(full) {
		t.Error("the budgeted render is not smaller than the full one")
	}
}

// TestRenderFallsBackForAnIncompleteSpec keeps `spec render` useful while a
// spec is still being generated: the library cannot decode it, so the files
// that exist are printed as they are.
func TestRenderFallsBackForAnIncompleteSpec(t *testing.T) {
	tmpDir := t.TempDir()
	specRoot := newSpecRoot(t, tmpDir, "08", "my_spec", specFixture{})
	if err := os.Remove(filepath.Join(specRoot, "08_my_spec", "test_spec.json")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AF_AGENT", "")

	output := runRender(t, specRoot, "08_my_spec")
	if !strings.Contains(output, "# requirements") {
		t.Errorf("the artifact that is present was not rendered:\n%s", output)
	}
	if strings.Contains(output, "# test_spec") {
		t.Errorf("the missing artifact appeared in the output:\n%s", output)
	}
}

func TestRenderFailsWhenNoArtifactExists(t *testing.T) {
	tmpDir := t.TempDir()
	specRoot := filepath.Join(tmpDir, ".specs")
	specPath := filepath.Join(specRoot, "08_my_spec")
	if err := os.MkdirAll(specPath, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(specPath, "prd.md"), []byte("# Only a PRD\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("AF_AGENT", "")

	cmd := newRootCmd()
	cmd.SetOut(new(bytes.Buffer))
	cmd.SetErr(new(bytes.Buffer))
	cmd.SetArgs([]string{"--spec-dir", specRoot, "render", "08_my_spec"})
	if err := cmd.Execute(); err == nil {
		t.Fatal("spec render exited 0 for a spec with no JSON artifacts")
	}
}

func TestRenderRejectsAScaffold(t *testing.T) {
	tmpDir := t.TempDir()
	prdPath := filepath.Join(tmpDir, "prd.md")
	if err := os.WriteFile(prdPath, []byte("# New Feature\n\n## Intent\n\nSomething.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	specRoot := filepath.Join(tmpDir, ".specs")

	newCmd := newRootCmd()
	newCmd.SetOut(new(bytes.Buffer))
	newCmd.SetErr(new(bytes.Buffer))
	newCmd.SetArgs([]string{"--spec-dir", specRoot, "new", prdPath, "--name", "fresh"})
	if err := newCmd.Execute(); err != nil {
		t.Fatalf("spec new failed: %v", err)
	}

	cmd := newRootCmd()
	stdout := new(bytes.Buffer)
	cmd.SetOut(stdout)
	cmd.SetErr(new(bytes.Buffer))
	cmd.SetArgs([]string{"--spec-dir", specRoot, "render", "01_fresh"})
	err := cmd.Execute()
	if err == nil {
		t.Fatalf("spec render exited 0 for a scaffold\noutput: %s", stdout.String())
	}
	if !strings.Contains(err.Error(), "spec generate") {
		t.Errorf("the error does not say what to do next: %v", err)
	}
}

func TestRenderRejectsAMissingSpec(t *testing.T) {
	specRoot := setupRenderSpec(t, t.TempDir())

	cmd := newRootCmd()
	cmd.SetOut(new(bytes.Buffer))
	cmd.SetErr(new(bytes.Buffer))
	cmd.SetArgs([]string{"--spec-dir", specRoot, "render", "99_nonexistent"})
	if err := cmd.Execute(); err == nil {
		t.Fatal("spec render exited 0 for a spec that does not exist")
	}
}

func TestRenderRequiresASpecArgument(t *testing.T) {
	cmd := newRootCmd()
	cmd.SetOut(new(bytes.Buffer))
	cmd.SetErr(new(bytes.Buffer))
	cmd.SetArgs([]string{"render"})
	if err := cmd.Execute(); err == nil {
		t.Fatal("spec render with no SPEC argument exited 0")
	}
}

func TestStripFrontmatter(t *testing.T) {
	cases := map[string]struct{ in, want string }{
		"with frontmatter": {
			in:   "---\nspec_id: \"01\"\n---\n# Title\n\nBody.\n",
			want: "# Title\n\nBody.\n",
		},
		"without frontmatter": {
			in:   "# Title\n\nBody.\n",
			want: "# Title\n\nBody.\n",
		},
		"unterminated frontmatter is left alone": {
			in:   "---\nspec_id: \"01\"\n# Title\n",
			want: "---\nspec_id: \"01\"\n# Title\n",
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if got := stripFrontmatter(tc.in); got != tc.want {
				t.Errorf("= %q; want %q", got, tc.want)
			}
		})
	}
}

// writeArchitecture adds an architecture.md to a spec directory.
func writeArchitecture(t *testing.T, specPath string) {
	t.Helper()
	content := "# Architecture\n\n## Components\n\n- **Store**: persists widgets.\n- **Handler**: serves HTTP.\n"
	if err := os.WriteFile(filepath.Join(specPath, "architecture.md"), []byte(content), 0o644); err != nil {
		t.Fatalf("cannot write architecture.md: %v", err)
	}
}
