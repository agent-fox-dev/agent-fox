package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// specFixture describes a spec package to write to disk for a CLI test. The
// zero value produces a small, complete and valid format v2 spec: one
// requirement with two criteria, one execution path, three tests covering all
// of them, and two tasks that own every test.
type specFixture struct {
	// Status is the PRD lifecycle state (default "draft").
	Status string
	// IntentHash, when non-empty, is written into the frontmatter.
	IntentHash string
	// Glossary entries for requirements.json.
	Glossary map[string]string
	// Dependencies lists upstream spec IDs for tasks.json.
	Dependencies []string
	// TaskStates overrides the state of each task, in order.
	TaskStates []string
	// PathActors overrides the actors of the execution path's two steps.
	PathActors []string
	// PRDBody replaces the default PRD body.
	PRDBody string
	// RequirementsOverride, TestSpecOverride and TasksOverride replace the
	// generated artifact wholesale. Used by tests that need a specific defect.
	RequirementsOverride string
	TestSpecOverride     string
	TasksOverride        string
}

// writeSpecFixture creates specPath and writes prd.md and the three JSON
// artifacts of a spec whose identity is specID/specName.
func writeSpecFixture(t *testing.T, specPath, specID, specName string, fixture specFixture) {
	t.Helper()
	if err := os.MkdirAll(specPath, 0o755); err != nil {
		t.Fatalf("cannot create %s: %v", specPath, err)
	}

	write := func(name, content string) {
		if err := os.WriteFile(filepath.Join(specPath, name), []byte(content), 0o644); err != nil {
			t.Fatalf("cannot write %s: %v", name, err)
		}
	}

	write("prd.md", fixturePRD(specID, specName, fixture))

	requirements := fixture.RequirementsOverride
	if requirements == "" {
		requirements = fixtureRequirements(specID, specName, fixture)
	}
	write("requirements.json", requirements)

	testSpec := fixture.TestSpecOverride
	if testSpec == "" {
		testSpec = fixtureTestSpec(specID, specName)
	}
	write("test_spec.json", testSpec)

	tasks := fixture.TasksOverride
	if tasks == "" {
		tasks = fixtureTasks(specID, specName, fixture)
	}
	write("tasks.json", tasks)
}

// newSpecRoot creates a spec root under tmpDir holding one spec directory
// named {specID}_{specName}, and returns the spec root path.
func newSpecRoot(t *testing.T, tmpDir, specID, specName string, fixture specFixture) string {
	t.Helper()
	root := filepath.Join(tmpDir, ".specs")
	writeSpecFixture(t, filepath.Join(root, specID+"_"+specName), specID, specName, fixture)
	return root
}

func fixturePRD(specID, specName string, fixture specFixture) string {
	status := fixture.Status
	if status == "" {
		status = "draft"
	}
	intentHash := "null"
	if fixture.IntentHash != "" {
		intentHash = fmt.Sprintf("%q", fixture.IntentHash)
	}
	body := fixture.PRDBody
	if body == "" {
		body = "# Widget Service\n\n## Intent\n\nStore widgets and serve them back.\n"
	}
	return fmt.Sprintf(`---
spec_id: %q
spec_name: %q
title: "Widget Service"
status: %q
created_at: "2026-01-01T00:00:00Z"
updated_at: "2026-01-01T00:00:00Z"
intent_hash: %s
schema_version: 2
---
%s`, specID, specName, status, intentHash, body)
}

func fixtureRequirements(specID, specName string, fixture specFixture) string {
	glossary := "{}"
	if len(fixture.Glossary) > 0 {
		parts := make([]string, 0, len(fixture.Glossary))
		terms := sortedKeys(fixture.Glossary)
		for _, term := range terms {
			parts = append(parts, fmt.Sprintf("    %q: %q", term, fixture.Glossary[term]))
		}
		glossary = "{\n" + strings.Join(parts, ",\n") + "\n  }"
	}

	actors := fixture.PathActors
	if len(actors) < 2 {
		actors = []string{"client", "widget service"}
	}

	return fmt.Sprintf(`{
  "$schema": "https://agent-fox.dev/schemas/requirements.v2.json",
  "spec_id": %q,
  "spec_name": %q,
  "schema_version": 2,
  "introduction": "The widget service stores widgets and serves them back.",
  "glossary": %s,
  "requirements": [
    {
      "id": "%s-REQ-1",
      "title": "Widget storage",
      "rationale": "A widget that cannot be stored cannot be served.",
      "criteria": [
        {
          "id": "%s-REQ-1.1",
          "pattern": "event_driven",
          "condition": "a client submits a widget with a name",
          "system": "widget service",
          "action": "persist the widget and return its identifier",
          "contract": "HTTP 201 with body {id: string}"
        },
        {
          "id": "%s-REQ-1.2",
          "pattern": "unwanted",
          "condition": "the submitted widget has no name",
          "system": "widget service",
          "action": "reject the request and persist nothing",
          "contract": "HTTP 400 with body {error: string}"
        }
      ]
    }
  ],
  "execution_paths": [
    {
      "id": "%s-PATH-1",
      "title": "A client stores a widget and reads it back",
      "steps": [
        { "actor": %q, "action": "submits a widget" },
        { "actor": %q, "action": "persists it and returns the identifier" }
      ]
    }
  ]
}
`, specID, specName, glossary, specID, specID, specID, specID, actors[0], actors[1])
}

func fixtureTestSpec(specID, specName string) string {
	return fmt.Sprintf(`{
  "$schema": "https://agent-fox.dev/schemas/test_spec.v2.json",
  "spec_id": %q,
  "spec_name": %q,
  "schema_version": 2,
  "tests": [
    {
      "id": "TS-%s-1",
      "kind": "unit",
      "verifies": ["%s-REQ-1.1"],
      "title": "A named widget is persisted and its identifier returned",
      "given": ["an empty store"],
      "when": "a widget with a name is submitted",
      "then": ["the status is 201", "the body carries a non-empty id"]
    },
    {
      "id": "TS-%s-2",
      "kind": "unit",
      "verifies": ["%s-REQ-1.2"],
      "title": "A widget with no name is rejected and nothing is stored",
      "given": ["an empty store"],
      "when": "a widget without a name is submitted",
      "then": ["the status is 400", "the store is still empty"]
    },
    {
      "id": "TS-%s-3",
      "kind": "smoke",
      "verifies": ["%s-PATH-1"],
      "title": "A client stores a widget end to end",
      "given": ["the service is running against a real store"],
      "when": "a widget is submitted and read back",
      "then": ["the read returns the widget that was submitted"],
      "real_components": ["widget service", "store"]
    }
  ]
}
`, specID, specName, specID, specID, specID, specID, specID, specID)
}

func fixtureTasks(specID, specName string, fixture specFixture) string {
	states := []string{"pending", "pending"}
	for i, state := range fixture.TaskStates {
		if i < len(states) {
			states[i] = state
		}
	}

	deps := "[]"
	if len(fixture.Dependencies) > 0 {
		items := make([]string, 0, len(fixture.Dependencies))
		for _, dep := range fixture.Dependencies {
			items = append(items, fmt.Sprintf(`{ "spec": %q, "reason": "uses its loader" }`, dep))
		}
		deps = "[\n    " + strings.Join(items, ",\n    ") + "\n  ]"
	}

	return fmt.Sprintf(`{
  "$schema": "https://agent-fox.dev/schemas/tasks.v2.json",
  "spec_id": %q,
  "spec_name": %q,
  "schema_version": 2,
  "test_commands": {
    "all_tests": "go test ./... -count=1",
    "linter": "go vet ./..."
  },
  "dependencies": %s,
  "tasks": [
    {
      "id": 1,
      "kind": "implement",
      "title": "Store and serve widgets",
      "criteria": ["%s-REQ-1"],
      "tests": ["TS-%s-1", "TS-%s-2"],
      "steps": [
        "Write the two unit tests and confirm they fail",
        "Add the widget row type and the store's Insert method"
      ],
      "touches": ["store/widget.go"],
      "state": %q
    },
    {
      "id": 2,
      "kind": "integration",
      "title": "Verify the store-and-read path end to end",
      "criteria": [],
      "tests": ["TS-%s-3"],
      "steps": [
        "Write the smoke test against a real store and confirm it passes",
        "Trace the path through the handler and the store; confirm no stub remains"
      ],
      "depends_on": [1],
      "state": %q
    }
  ]
}
`, specID, specName, deps, specID, specID, specID, states[0], specID, states[1])
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	for i := 1; i < len(keys); i++ {
		for j := i; j > 0 && keys[j] < keys[j-1]; j-- {
			keys[j], keys[j-1] = keys[j-1], keys[j]
		}
	}
	return keys
}

// writeSessionJSON writes a _session.json holding the given state.
func writeSessionJSON(t *testing.T, specPath, state string) {
	t.Helper()
	data, err := json.MarshalIndent(map[string]any{
		"state":               state,
		"mode":                "standard",
		"prd_path":            "prd.md",
		"assessment_history":  []any{},
		"qa_exchanges":        []any{},
		"generated_artifacts": []string{"requirements.json", "test_spec.json", "tasks.json"},
		"last_error":          nil,
	}, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(specPath, "_session.json"), data, 0o644); err != nil {
		t.Fatalf("cannot write _session.json: %v", err)
	}
}
