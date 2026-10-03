package main

import (
	"bytes"
	"context"
	"flag"
	"os"
	"sort"
	"strings"
	"testing"

	"github.com/agent-fox-dev/agentfox/internal/schematest"
	"github.com/agent-fox-dev/agentfox/internal/statetest"
	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

func TestMain(m *testing.M) {
	// Unset model-vendor variables so that tests exercise the intended
	// credential paths regardless of the developer's shell environment.
	for _, v := range []string{"AF_MODEL", "AGENTKIT_MODEL", "AF_MODEL_VENDOR",
		"CLAUDE_CODE_USE_VERTEX", "CLAUDE_CODE_USE_BEDROCK",
		"ANTHROPIC_VERTEX_PROJECT_ID", "ANTHROPIC_VERTEX_BASE_URL"} {
		_ = os.Unsetenv(v)
	}
	os.Exit(statetest.Run(m))
}

// TestSchemaGolden (TS-09-35, TS-09-38): triage --schema is byte-for-byte the
// checked-in golden file, and its flags and result documents compile against
// the JSON Schema 2020-12 meta-schema. UPDATE_GOLDEN=1 rewrites the file.
func TestSchemaGolden(t *testing.T) {
	schematest.CheckGolden(t, "triage")
}

// TS-06-53 (unit): triage's usage says what --dry-run still does locally:
// nothing.
func TestTS06_53_IssueUsageStatesDryRunDoesNothingLocally(t *testing.T) {
	flat := strings.Join(strings.Fields(usage), " ")
	if !strings.Contains(flat, "--dry-run") || !strings.Contains(flat, "does nothing") {
		t.Errorf("triage's usage does not say --dry-run does nothing locally:\n%s", usage)
	}
	for _, word := range []string{"branch", "commit"} {
		if strings.Contains(flat, word) {
			t.Errorf("triage's usage mentions %q as a local effect of --dry-run", word)
		}
	}
}

// --dry-run and --total-budget come from Common alone: the tool's own flags
// must not define them again (which would panic), and the tool's row in the
// unsupported-flag table must match what it registers.
func TestIssueFlagsMatchTheToolFlagTable(t *testing.T) {
	app := newApp()
	var common toolio.Common
	fs := flag.NewFlagSet("triage", flag.ContinueOnError)
	common.Register(fs)
	shared := map[string]bool{}
	fs.VisitAll(func(f *flag.Flag) { shared[f.Name] = true })

	own := flag.NewFlagSet("triage-own", flag.ContinueOnError)
	app.Flags(own)
	var names []string
	own.VisitAll(func(f *flag.Flag) {
		if shared[f.Name] {
			t.Errorf("--%s is defined by the tool as well as by Common", f.Name)
		}
		names = append(names, f.Name)
	})
	want := toolio.ToolFlags("triage")
	sort.Strings(names)
	sort.Strings(want)
	if strings.Join(names, ",") != strings.Join(want, ",") {
		t.Errorf("triage registers %v, the unsupported-flag table says %v", names, want)
	}
	if !app.SinglePhase {
		t.Error("triage has one phase: --total-budget must fold into --budget")
	}
}

func TestIssueAcceptsSharedDryRun(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := newApp().Main(context.Background(), []string{"--dry-run", "--total-budget", "1", "--version"},
		strings.NewReader(""), &stdout, &stderr)
	if code != toolio.ExitOK {
		t.Fatalf("code = %d, stderr: %s", code, stderr.String())
	}
}
