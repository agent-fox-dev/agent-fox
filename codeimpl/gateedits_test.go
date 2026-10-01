package codeimpl

import (
	"strings"
	"testing"
)

func TestGateEditsFlagsTheCategories(t *testing.T) {
	ns := strings.Join([]string{
		"M\tMakefile",
		"M\t.golangci.yml",
		"D\tinternal/x/x_test.go",
		"M\tcmd/fix/testdata/schema.golden.json",
		"A\tcmd/new/testdata/new.golden.json",
		"M\tinternal/x/x.go",
		"M\t.specs/10_x/tasks.json",
	}, "\n")
	patch := "diff --git a/y_test.go b/y_test.go\n--- a/y_test.go\n+++ b/y_test.go\n@@ -1 +1,2 @@\n+\tt.Skip(\"flaky\")\n" +
		"diff --git a/z.go b/z.go\n--- a/z.go\n+++ b/z.go\n+\tt.Skip(\"not a test file\")\n"
	got := strings.Join(gateEdits(ns, patch, ".specs/10_x"), "\n")
	for _, want := range []string{"Makefile, .golangci.yml", "x_test.go", "schema.golden.json", "y_test.go"} {
		if !strings.Contains(got, want) {
			t.Errorf("no finding mentions %q:\n%s", want, got)
		}
	}
	for _, not := range []string{"new.golden.json", "z.go", "tasks.json", "x/x.go"} {
		if strings.Contains(got, not) {
			t.Errorf("a finding mentions %q:\n%s", not, got)
		}
	}
}

func TestGateEditsIsQuietForAnOrdinaryChange(t *testing.T) {
	if got := gateEdits("M\tinternal/x/x.go\nA\tinternal/x/x_test.go", "+++ b/internal/x/x_test.go\n+func TestX(t *testing.T) {}\n", ""); len(got) != 0 {
		t.Errorf("findings for an ordinary change: %v", got)
	}
}
