package project

import (
	"maps"
	"slices"
	"testing"

	"github.com/agent-fox-dev/agentfox/internal/testutil"
)

// fixture writes files (path to content) under a new directory, and returns
// it with its files as Detect takes them: slash-separated and sorted, without
// what is under .agent-fox.
func fixture(t *testing.T, files map[string]string) (string, []string) {
	t.Helper()
	dir := t.TempDir()
	for name, content := range files {
		testutil.WriteFile(t, dir, name, content)
	}
	return dir, slices.Sorted(maps.Keys(testutil.Files(t, dir, ".agent-fox")))
}

// detect is Detect on a fixture, failing the test on an error.
func detect(t *testing.T, files map[string]string) Detection {
	t.Helper()
	dir, list := fixture(t, files)
	d, err := Detect(dir, list)
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	return d
}

// argvs are the argv of the checks of the gate.
func argvs(d Detection) [][]string {
	out := [][]string{}
	for _, c := range d.Gate.Checks {
		out = append(out, c.Argv)
	}
	return out
}

// unavailable is the gate with every check unavailable, for a comparison that
// is not about PATH.
func unavailable(g Table) Table {
	g.Checks = slices.Clone(g.Checks)
	for i := range g.Checks {
		g.Checks[i].Available = false
	}
	return g
}
