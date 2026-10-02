package toolio

import (
	"fmt"
	"strings"
	"testing"
)

// TS-13-22 (unit): removedFlagMessages contains variant with a message directing to --effort and --model
func TestTS13_22_RemovedFlagMessagesContainsVariant(t *testing.T) {
	msg, ok := removedFlagMessages["variant"]
	if !ok {
		t.Fatal("removedFlagMessages does not contain 'variant'")
	}
	if !strings.Contains(msg, "--effort") {
		t.Errorf("variant removal message should contain --effort: %q", msg)
	}
	if !strings.Contains(msg, "--model") {
		t.Errorf("variant removal message should contain --model: %q", msg)
	}
}

// TS-13-23 (unit): Passing --variant produces the removed-flag message (exit 2) directing to --effort and --model
func TestTS13_23_VariantProducesRemovedFlagMessage(t *testing.T) {
	// Simulate the error the flag package returns for an undefined flag.
	err := fmt.Errorf("flag provided but not defined: -variant")
	msg, ok := unsupportedFlagMessage("fix", err)
	if !ok {
		t.Fatal("unsupportedFlagMessage did not recognize --variant")
	}
	if !strings.Contains(msg, "--effort") {
		t.Errorf("message should contain --effort: %q", msg)
	}
	if !strings.Contains(msg, "--model") {
		t.Errorf("message should contain --model: %q", msg)
	}
}
