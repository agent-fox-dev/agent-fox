package specgen

import (
	"strings"
	"testing"
)

// steering.md reaches the PRD and generation prompts without the model having
// to go looking for it (#60).
func TestSteeringReachesThePRDAndGenerationPrompts(t *testing.T) {
	block := steeringBlock("# Steering\n\nAlways write tests first.\n")
	if !strings.Contains(block, "BEGIN STEERING") || !strings.Contains(block, "Always write tests first.") {
		t.Fatalf("block = %q", block)
	}
	prd := prdUserPrompt("/r", "text", "", "idea", "", "", "", block, "")
	if !strings.Contains(prd, "Always write tests first.") {
		t.Error("the PRD prompt lacks the steering")
	}
	gen := generationUserPrompt("requirements", "01", "x", "/r", "prd", "", block, "", "", "")
	if !strings.Contains(gen, "Always write tests first.") {
		t.Error("the generation prompt lacks the steering")
	}
	if steeringBlock("  \n") != "" {
		t.Error("an empty steering produced a block")
	}
	if !strings.Contains(prdSystemPrompt(), "Steering section") {
		t.Error("the PRD system prompt does not mention steering")
	}
}
