package specgen

import (
	"strings"
	"testing"
)

// Issue #221: the generation and PRD prompts do not present Go as the
// language: the worked example says it is one language's, the stub markers
// are introduced by the project's own, and the repository's manifests are
// named for every ecosystem.
func TestThePromptsAreLanguageNeutral(t *testing.T) {
	read := func(name string) string {
		b, err := templates.ReadFile("templates/" + name)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	tasks := read("generation_user_tasks.md")
	if !strings.Contains(tasks, "use this project's own") {
		t.Error("the tasks example does not say it is one language's")
	}
	if i := strings.Index(tasks, "stub markers"); i < 0 || strings.Index(tasks[i:], `panic("not implemented")`) < strings.Index(tasks[i:], "language block") {
		t.Error("the stub-marker list opens with Go's before the project's own")
	}
	prd := read("prd_system.md")
	for _, want := range []string{"package.json", "Cargo.toml", "pyproject.toml"} {
		if !strings.Contains(prd, want) {
			t.Errorf("prd_system.md names only Go's manifests; lacks %s", want)
		}
	}
}
