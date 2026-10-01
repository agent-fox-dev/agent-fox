package main

import (
	"flag"
	"testing"
)

// A repository or shell can set the landing mode and branch prefix once
// (#73); an explicit flag still wins, and the flag's own default stays fixed.
func TestEnvDefaultsApplyOnlyToFlagsNotGiven(t *testing.T) {
	newFS := func(args ...string) (*flag.FlagSet, *string, *string) {
		fs := flag.NewFlagSet("fix", flag.ContinueOnError)
		land, prefix := "pr", ""
		fs.StringVar(&land, "land", "pr", "")
		fs.StringVar(&prefix, "branch-prefix", "", "")
		if err := fs.Parse(args); err != nil {
			t.Fatal(err)
		}
		return fs, &land, &prefix
	}
	t.Setenv("AF_LAND", "none")
	t.Setenv("AF_BRANCH_PREFIX", "feature")

	fs, land, prefix := newFS()
	applyEnvDefaults(fs, map[string]*string{"land": land, "branch-prefix": prefix})
	if *land != "none" || *prefix != "feature" {
		t.Errorf("land=%q prefix=%q, want the environment's values", *land, *prefix)
	}

	fs, land, prefix = newFS("--land", "branch", "--branch-prefix", "hotfix")
	applyEnvDefaults(fs, map[string]*string{"land": land, "branch-prefix": prefix})
	if *land != "branch" || *prefix != "hotfix" {
		t.Errorf("flags lost to the environment: land=%q prefix=%q", *land, *prefix)
	}

	// Explicitly passing the default is still explicit.
	fs, land, _ = newFS("--land", "pr")
	applyEnvDefaults(fs, map[string]*string{"land": land})
	if *land != "pr" {
		t.Errorf("an explicit --land pr was overridden: %q", *land)
	}
}
