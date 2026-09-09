package agentspec

import (
	"embed"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/agent-fox-dev/agentfox/afspec"
)

// templateFS embeds all prompt template files at compile time.
//
//go:embed templates/*.md
var templateFS embed.FS

// PromptTemplateNames lists the 9 embedded prompt template names.
var PromptTemplateNames = []string{
	"assessment_system",
	"assessment_user",
	"refinement_system",
	"refinement_user",
	"generation_system",
	"generation_user_base",
	"generation_user_requirements",
	"generation_user_test_spec",
	"generation_user_tasks",
}

// validNameRe matches safe template names: [a-zA-Z0-9_-]+
var validNameRe = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

// stripFrontmatter removes YAML frontmatter (content between opening and
// closing --- delimiters at the start of a file) from the given text.
// If no frontmatter is present, the text is returned unchanged.
func stripFrontmatter(text string) string {
	if !strings.HasPrefix(text, "---") {
		return text
	}
	// Find the closing --- delimiter after the opening one.
	rest := text[3:]
	idx := strings.Index(rest, "\n---")
	if idx < 0 {
		return text
	}
	// Skip past the closing --- and any trailing newline.
	after := rest[idx+4:]
	if strings.HasPrefix(after, "\n") {
		after = after[1:]
	}
	return after
}

// LoadPrompt loads a named prompt template from a project-local override path
// or the embedded defaults, stripping YAML frontmatter. The name must match
// [a-zA-Z0-9_-]+ to prevent path traversal.
//
// When a non-symlink override file exists at <projectDir>/.spec/prompts/<name>.md,
// it is used. Otherwise, the embedded default is used.
//
// Returns (content string, nil) on success, or ("", error) on failure.
func LoadPrompt(name, projectDir string) (string, error) {
	// Validate name to prevent path traversal.
	if !validNameRe.MatchString(name) {
		return "", fmt.Errorf("LoadPrompt: invalid template name %q", name)
	}

	// Try project-local override if projectDir is specified.
	if projectDir != "" {
		overridePath := filepath.Join(projectDir, ".spec", "prompts", name+".md")
		info, err := os.Lstat(overridePath)
		if err == nil && !info.IsDir() {
			// Reject symlinks — fall back to embedded default.
			if info.Mode()&os.ModeSymlink != 0 {
				// Symlink detected, ignore override.
			} else {
				data, err := os.ReadFile(overridePath)
				if err != nil {
					return "", fmt.Errorf("LoadPrompt: reading override file: %w", err)
				}
				return stripFrontmatter(string(data)), nil
			}
		}
	}

	// Fall back to embedded default.
	data, err := templateFS.ReadFile("templates/" + name + ".md")
	if err != nil {
		return "", fmt.Errorf("LoadPrompt: reading embedded template %q: %w", name, err)
	}
	return stripFrontmatter(string(data)), nil
}

// LoadPromptTemplate loads a prompt template via LoadPrompt and performs safe
// $variable substitution. Unmatched $variable references pass through unchanged.
//
// Returns (substitutedContent string, nil) on success, or ("", error) on failure.
func LoadPromptTemplate(name, projectDir string, vars map[string]string) (string, error) {
	content, err := LoadPrompt(name, projectDir)
	if err != nil {
		return "", err
	}

	// Perform safe substitution: replace $varname with value from vars.
	// Unmatched variables pass through unchanged.
	for k, v := range vars {
		content = strings.ReplaceAll(content, "$"+k, v)
	}

	return content, nil
}

// manifestEntry describes a project manifest file and the language/tooling it maps to.
type manifestEntry struct {
	filename  string
	language  string
	toolHints string
}

// manifestOrder defines the priority order for manifest file detection.
var manifestOrder = []manifestEntry{
	{"go.mod", "Go", "Go modules (go mod), go test, go build, go vet"},
	{"Cargo.toml", "Rust", "Cargo (cargo build, cargo test, cargo clippy)"},
	{"package.json", "TypeScript", "npm/yarn/pnpm, TypeScript compiler (tsc), Jest/Vitest"},
	{"pyproject.toml", "Python", "pip/uv/poetry, pytest, mypy/pyright, ruff"},
	{"Gemfile", "Ruby", "Bundler (bundle install), RSpec, RuboCop"},
	{"build.gradle", "Java", "Gradle (gradle build, gradle test)"},
	{"pom.xml", "Java", "Maven (mvn compile, mvn test)"},
}

// DetectProjectLanguage scans a project directory for manifest files and returns
// the detected language name and tooling hints string. Returns ("", "") when no
// recognized manifest files are present.
func DetectProjectLanguage(projectDir string) (language string, toolHints string) {
	for _, m := range manifestOrder {
		path := filepath.Join(projectDir, m.filename)
		if _, err := os.Stat(path); err == nil {
			return m.language, m.toolHints
		}
	}
	return "", ""
}

// FormatSpecLandscape formats spec landscape entries into markdown tables
// split into active and archived sections. Returns an empty string for an
// empty input slice.
func FormatSpecLandscape(landscape []map[string]any) string {
	if len(landscape) == 0 {
		return ""
	}

	var active, archived []map[string]any
	for _, entry := range landscape {
		status, _ := entry["status"].(string)
		if strings.EqualFold(status, "archived") {
			archived = append(archived, entry)
		} else {
			active = append(active, entry)
		}
	}

	var sb strings.Builder

	writeTable := func(entries []map[string]any) {
		sb.WriteString("| Spec ID | Title | Status |\n")
		sb.WriteString("|---------|-------|--------|\n")
		for _, e := range entries {
			specID, _ := e["spec_id"].(string)
			title, _ := e["title"].(string)
			status, _ := e["status"].(string)
			sb.WriteString(fmt.Sprintf("| %s | %s | %s |\n", specID, title, status))
		}
	}

	if len(active) > 0 {
		sb.WriteString("### Active Specs\n\n")
		writeTable(active)
		sb.WriteString("\n")
	}

	if len(archived) > 0 {
		sb.WriteString("### Archived Specs\n\n")
		writeTable(archived)
		sb.WriteString("\n")
	}

	return sb.String()
}

// formatAssessmentBlock formats an Assessment into a human-readable block
// for inclusion in prompt templates.
func formatAssessmentBlock(a Assessment) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Quality: %s\n", a.Quality))
	sb.WriteString(fmt.Sprintf("Summary: %s\n", a.Summary))
	if len(a.Gaps) > 0 {
		sb.WriteString("Gaps:\n")
		for _, gap := range a.Gaps {
			sb.WriteString(fmt.Sprintf("- %s\n", gap))
		}
	}
	if len(a.Questions) > 0 {
		sb.WriteString("Questions:\n")
		for _, q := range a.Questions {
			qText, _ := q["question"].(string)
			if qText == "" {
				qText, _ = q["text"].(string)
			}
			sb.WriteString(fmt.Sprintf("- %s\n", qText))
		}
	}
	return sb.String()
}

// formatQABlock formats question-answer pairs into a human-readable block.
// Keys are sorted lexicographically to produce deterministic output.
func formatQABlock(answers map[string]string) string {
	if len(answers) == 0 {
		return "No questions and answers provided."
	}
	keys := make([]string, 0, len(answers))
	for q := range answers {
		keys = append(keys, q)
	}
	sort.Strings(keys)
	var sb strings.Builder
	for _, q := range keys {
		sb.WriteString(fmt.Sprintf("Q: %s\nA: %s\n\n", q, answers[q]))
	}
	return sb.String()
}

// AssessmentSystemPrompt loads the assessment_system template via LoadPrompt
// and returns its content.
func AssessmentSystemPrompt(projectDir string) (string, error) {
	return LoadPrompt("assessment_system", projectDir)
}

// AssessmentUserPrompt loads and renders the assessment_user template via
// LoadPromptTemplate, substituting $spec_name, $spec_landscape_block, and $prd_text.
func AssessmentUserPrompt(prdText, specName, projectDir string, specLandscape []map[string]any) (string, error) {
	landscapeBlock := FormatSpecLandscape(specLandscape)

	return LoadPromptTemplate("assessment_user", projectDir, map[string]string{
		"spec_name":            specName,
		"prd_text":             prdText,
		"spec_landscape_block": landscapeBlock,
	})
}

// RefinementSystemPrompt loads the refinement_system template via LoadPrompt
// and returns its content.
func RefinementSystemPrompt(projectDir string) (string, error) {
	return LoadPrompt("refinement_system", projectDir)
}

// RefinementUserPrompt loads and renders the refinement_user template via
// LoadPromptTemplate, substituting $prd_text, $assessment_block, $qa_block,
// and $spec_landscape_block.
func RefinementUserPrompt(prdText string, answers map[string]string, prevAssessment Assessment, projectDir string, specLandscape []map[string]any) (string, error) {
	assessmentBlock := formatAssessmentBlock(prevAssessment)
	qaBlock := formatQABlock(answers)
	landscapeBlock := FormatSpecLandscape(specLandscape)

	return LoadPromptTemplate("refinement_user", projectDir, map[string]string{
		"prd_text":             prdText,
		"assessment_block":     assessmentBlock,
		"qa_block":             qaBlock,
		"spec_landscape_block": landscapeBlock,
	})
}

// GenerationSystemPrompt loads the generation_system template via LoadPrompt
// and returns its content.
func GenerationSystemPrompt(projectDir string) (string, error) {
	return LoadPrompt("generation_system", projectDir)
}

// GenerationUserPrompt loads generation_user_base and the artifact-specific
// template (generation_user_requirements, generation_user_test_spec, or
// generation_user_tasks) via LoadPromptTemplate, composes them with the
// appropriate variables, and returns the combined prompt.
//
// Returns an error if artifactName is not one of requirements, test_spec, or tasks.
func GenerationUserPrompt(prdText, artifactName, specID, projectDir string, priorArtifacts map[string]any, dependentInterfaces []map[string]any, specLandscape []map[string]any) (string, error) {
	// Validate artifact name.
	validArtifacts := map[string]string{
		"requirements": "generation_user_requirements",
		"test_spec":    "generation_user_test_spec",
		"tasks":        "generation_user_tasks",
	}
	templateName, ok := validArtifacts[artifactName]
	if !ok {
		return "", fmt.Errorf("GenerationUserPrompt: unrecognized artifact name %q", artifactName)
	}

	// Format blocks.
	landscapeBlock := FormatSpecLandscape(specLandscape)

	var priorArtifactsBlock string
	if len(priorArtifacts) > 0 {
		// Sort keys for deterministic output.
		keys := make([]string, 0, len(priorArtifacts))
		for k := range priorArtifacts {
			keys = append(keys, k)
		}
		sort.Strings(keys)

		var pab strings.Builder
		pab.WriteString("Previously generated artifacts:\n\n")
		for _, k := range keys {
			pab.WriteString(fmt.Sprintf("### %s\n\n", k))
			pab.WriteString(renderPriorArtifact(k, priorArtifacts[k]))
			pab.WriteString("\n")
		}
		priorArtifactsBlock = pab.String()
	}

	var dependentInterfacesBlock string
	if len(dependentInterfaces) > 0 {
		data, err := json.MarshalIndent(dependentInterfaces, "", "  ")
		if err == nil {
			dependentInterfacesBlock = "Dependent interfaces:\n" + string(data)
		}
	}

	// Detect project language for language_block.
	lang, hints := DetectProjectLanguage(projectDir)
	var languageBlock string
	if lang != "" {
		languageBlock = fmt.Sprintf("Project language: %s\nTooling: %s", lang, hints)
	}

	// Load the base template with variable substitution.
	basePrompt, err := LoadPromptTemplate("generation_user_base", projectDir, map[string]string{
		"artifact_name":              artifactName,
		"spec_id":                    specID,
		"prd_text":                   prdText,
		"spec_landscape_block":       landscapeBlock,
		"dependent_interfaces_block": dependentInterfacesBlock,
		"prior_artifacts_block":      priorArtifactsBlock,
		"language_block":             languageBlock,
	})
	if err != nil {
		return "", fmt.Errorf("GenerationUserPrompt: loading base template: %w", err)
	}

	// Load the artifact-specific template.
	artifactPrompt, err := LoadPrompt(templateName, projectDir)
	if err != nil {
		return "", fmt.Errorf("GenerationUserPrompt: loading artifact template: %w", err)
	}

	combined := basePrompt + "\n\n" + artifactPrompt
	return collapseBlankLines(combined), nil
}

// collapseBlankLines replaces any run of three or more consecutive newlines
// with exactly two newlines. This removes excess blank lines that arise when
// empty optional blocks (landscape, interfaces, prior artifacts, language) are
// substituted into prompt templates.
func collapseBlankLines(s string) string {
	for strings.Contains(s, "\n\n\n") {
		s = strings.ReplaceAll(s, "\n\n\n", "\n\n")
	}
	return s
}

// renderPriorArtifact renders a prior artifact as the Markdown the library
// itself produces.
//
// Format v2 §12.1 requires that a downstream step receive the *complete*
// upstream artifact: every ID and every sentence. The v1 pipeline passed a
// summary of IDs and titles here, which is why the test generator had to
// re-derive edge cases from the PRD and the task generator never saw a single
// test ID. A compact Markdown rendering is acceptable, an ID-only summary is
// not — so requirements go through afspec's own renderer, and tests through a
// table that keeps id, kind, verifies and title.
func renderPriorArtifact(key string, value any) string {
	m, ok := value.(map[string]any)
	if !ok {
		return marshalFallback(value)
	}

	step := afspec.GenerationStep(key)
	decoded, err := afspec.DecodeArtifact(step, m)
	if err != nil {
		return marshalFallback(m)
	}

	switch artifact := decoded.(type) {
	case *afspec.RequirementsV2Json:
		return artifact.Render()
	case *afspec.TestSpecV2Json:
		return renderTestTable(artifact)
	case *afspec.TasksV2Json:
		return artifact.Render()
	default:
		return marshalFallback(m)
	}
}

// renderTestTable renders every test as one table row. The task generator
// needs each test's ID, kind and verifies list to own it (rules C7 and C9);
// the full given/when/then would multiply the prompt size without helping it
// decide which task a test belongs to.
func renderTestTable(ts *afspec.TestSpecV2Json) string {
	var sb strings.Builder
	sb.WriteString("| Test | Kind | Verifies | Title |\n")
	sb.WriteString("|------|------|----------|-------|\n")
	for _, t := range ts.Tests {
		sb.WriteString(fmt.Sprintf("| %s | %s | %s | %s |\n",
			t.Id, t.Kind, strings.Join(t.Verifies, ", "), t.Title))
	}
	return sb.String()
}

// marshalFallback renders a value as indented JSON when it cannot be decoded
// as a known artifact.
func marshalFallback(v any) string {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Sprintf("%v", v)
	}
	return string(data)
}
