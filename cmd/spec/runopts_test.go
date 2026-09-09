package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// cmdWithAgentFlags builds a command carrying the agent flags plus the
// --source persistent flag they read.
func cmdWithAgentFlags(t *testing.T, source string, args ...string) *cobra.Command {
	t.Helper()
	cmd := &cobra.Command{Use: "x", RunE: func(*cobra.Command, []string) error { return nil }}
	cmd.Flags().String("source", source, "")
	addAgentFlags(cmd)
	cmd.SetOut(new(strings.Builder))
	cmd.SetErr(new(strings.Builder))
	cmd.SetArgs(args)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("parsing %v: %v", args, err)
	}
	return cmd
}

func TestWithoutReadSourceNothingChanges(t *testing.T) {
	// Reading a repository costs turns and tokens, so it is the operator's
	// decision rather than a default to discover in a bill.
	o, err := runOptionsFor(cmdWithAgentFlags(t, t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	if o.Workspace != nil {
		t.Error("a workspace was configured without --read-source")
	}
	if o.TrustProject {
		t.Error("the project was trusted without --trust-project")
	}
	if o.WorkDir != "" {
		t.Error("a work directory was set with neither flag")
	}
}

func TestReadSourceMakesTheSourceDirectoryTheWorkspace(t *testing.T) {
	// --source has been a global flag all along and reached nothing that used
	// it. This is what it is for.
	dir := t.TempDir()
	o, err := runOptionsFor(cmdWithAgentFlags(t, dir, "--read-source"))
	if err != nil {
		t.Fatal(err)
	}
	if o.Workspace == nil {
		t.Fatal("--read-source configured no workspace")
	}
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	if o.Workspace.Root != resolved {
		t.Errorf("workspace root = %q, want %q", o.Workspace.Root, resolved)
	}
	if o.WorkDir != o.Workspace.Root {
		t.Errorf("WorkDir = %q, want the workspace root", o.WorkDir)
	}
}

func TestTrustProjectSetsAWorkDirWithoutGrantingReadTools(t *testing.T) {
	// The two are separate grants: one admits repository-authored text into
	// the system prompt, the other hands the model file tools. Wanting a
	// project's steering directives is not wanting a source-reading run.
	o, err := runOptionsFor(cmdWithAgentFlags(t, t.TempDir(), "--trust-project"))
	if err != nil {
		t.Fatal(err)
	}
	if !o.TrustProject {
		t.Error("--trust-project did not take effect")
	}
	if o.Workspace != nil {
		t.Error("--trust-project handed the model file tools")
	}
	if o.WorkDir == "" {
		t.Error("--trust-project set no directory to discover in")
	}
}

func TestAMissingSourceDirectoryIsReportedBeforeAnythingIsSpent(t *testing.T) {
	_, err := runOptionsFor(cmdWithAgentFlags(t, filepath.Join(t.TempDir(), "nope"), "--read-source"))
	if err == nil {
		t.Fatal("expected an error for a source directory that does not exist")
	}
	if !strings.Contains(err.Error(), "workspace") {
		t.Errorf("the error does not say what failed: %v", err)
	}
}

func TestTheBoundsReachTheRunOptions(t *testing.T) {
	o, err := runOptionsFor(cmdWithAgentFlags(t, t.TempDir(), "--max-turns", "7", "--max-budget", "1.25"))
	if err != nil {
		t.Fatal(err)
	}
	if o.MaxTurns != 7 || o.MaxBudgetUSD != 1.25 {
		t.Errorf("MaxTurns=%d MaxBudgetUSD=%v", o.MaxTurns, o.MaxBudgetUSD)
	}
}

func TestVerboseInstallsAnEventTraceExceptInAgentMode(t *testing.T) {
	// In agent mode the caller is a program reading one JSON document, and a
	// running commentary on stderr is the same noise the banner is.
	o, err := runOptionsFor(cmdWithAgentFlags(t, t.TempDir(), "--verbose"))
	if err != nil {
		t.Fatal(err)
	}
	if o.OnEvent == nil {
		t.Error("--verbose installed no event trace")
	}

	t.Setenv("AF_AGENT", "1")
	o, err = runOptionsFor(cmdWithAgentFlags(t, t.TempDir(), "--verbose"))
	if err != nil {
		t.Fatal(err)
	}
	if o.OnEvent != nil {
		t.Error("--verbose traced events in agent mode")
	}
}

func TestFirstLineTrimsToOneTerminalLine(t *testing.T) {
	if got := firstLine("one\ntwo\nthree"); got != "one" {
		t.Errorf("firstLine = %q", got)
	}
	long := strings.Repeat("x", 400)
	got := firstLine(long)
	if len(got) > 200 || !strings.HasSuffix(got, "…") {
		t.Errorf("a long line was not trimmed: %d chars", len(got))
	}
}

func TestTheAgentFlagsAreOnTheCommandsThatCallAModel(t *testing.T) {
	// A flag offered where it does nothing is a flag someone will pass and
	// expect to work.
	root := newRootCmd()
	withFlags := map[string]bool{"refine": true, "generate": true}
	for _, sub := range root.Commands() {
		has := sub.Flags().Lookup(flagReadSource) != nil
		if want := withFlags[sub.Name()]; has != want {
			t.Errorf("%s: --%s present=%v, want %v", sub.Name(), flagReadSource, has, want)
		}
	}
}

func TestTheSourceFlagStillRefusesAPathThatIsNotADirectory(t *testing.T) {
	file := filepath.Join(t.TempDir(), "afile")
	if err := os.WriteFile(file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := newRootCmd()
	cmd.SetOut(new(strings.Builder))
	cmd.SetErr(new(strings.Builder))
	cmd.SetArgs([]string{"--source", file, "list"})
	if err := cmd.Execute(); err == nil {
		t.Error("a file was accepted as --source")
	}
}
