package main

import (
	"bytes"
	"context"
	"encoding/json"
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
		"CLAUDE_CODE_USE_VERTEX", "CLAUDE_CODE_USE_BEDROCK"} {
		_ = os.Unsetenv(v)
	}
	os.Exit(statetest.Run(m))
}

// TestSchemaGolden (TS-09-35, TS-09-38): impl --schema is byte-for-byte the
// checked-in golden file, and its flags and result documents compile against
// the JSON Schema 2020-12 meta-schema. UPDATE_GOLDEN=1 rewrites the file.
func TestSchemaGolden(t *testing.T) {
	schematest.CheckGolden(t, "impl")
}

// TS-06-53 (unit): impl's usage names the branch and the commits as still
// happening under --dry-run.
func TestTS06_53_ImplUsageStatesDryRunKeepsBranchAndCommits(t *testing.T) {
	flat := strings.Join(strings.Fields(usage), " ")
	i := strings.Index(flat, "--dry-run")
	if i < 0 {
		t.Fatalf("impl's usage does not describe --dry-run:\n%s", usage)
	}
	para := flat[i:]
	if !strings.Contains(para, "branch") || !strings.Contains(para, "commit") {
		t.Errorf("impl's --dry-run text does not name the branch and the commits: %s", para)
	}
}

func TestImplFlagsMatchTheToolFlagTable(t *testing.T) {
	app := newApp()
	var common toolio.Common
	fs := flag.NewFlagSet("impl", flag.ContinueOnError)
	common.Register(fs)
	shared := map[string]bool{}
	fs.VisitAll(func(f *flag.Flag) { shared[f.Name] = true })

	own := flag.NewFlagSet("impl-own", flag.ContinueOnError)
	app.Flags(own)
	var names []string
	own.VisitAll(func(f *flag.Flag) {
		if shared[f.Name] {
			t.Errorf("--%s is defined by the tool as well as by Common", f.Name)
		}
		names = append(names, f.Name)
	})
	want := toolio.ToolFlags("impl")
	sort.Strings(names)
	sort.Strings(want)
	if strings.Join(names, ",") != strings.Join(want, ",") {
		t.Errorf("impl registers %v, the unsupported-flag table says %v", names, want)
	}
}

// TS-06-58 (unit): impl invoked with --label, a flag issue defines, exits 2
// naming both.
func TestTS06_58_ImplRejectsLabelNamingIssue(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	var stdout, stderr bytes.Buffer
	code := newApp().Main(context.Background(), []string{"--label", "x", "09_spec"},
		strings.NewReader(""), &stdout, &stderr)
	if code != toolio.ExitUsage {
		t.Fatalf("code = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "--label") || !strings.Contains(stderr.String(), "issue") {
		t.Errorf("stderr does not name the flag and the accepting tool:\n%s", stderr.String())
	}
	var env toolio.Envelope
	if err := json.Unmarshal(stdout.Bytes(), &env); err != nil {
		t.Fatalf("stdout: %v\n%s", err, stdout.String())
	}
	if env.Error == nil || !strings.Contains(env.Error.Message, "--label") || !strings.Contains(env.Error.Message, "issue") {
		t.Errorf("Error = %+v", env.Error)
	}
}

// TS-06-59 (unit): a flag no tool defines keeps Go's generic message.
func TestTS06_59_ImplKeepsGenericMessageForUnknownFlag(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	var stdout, stderr bytes.Buffer
	code := newApp().Main(context.Background(), []string{"--nonexistent-flag", "x", "09_spec"},
		strings.NewReader(""), &stdout, &stderr)
	if code != toolio.ExitUsage {
		t.Fatalf("code = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), "flag provided but not defined: -nonexistent-flag") {
		t.Errorf("stderr lacks Go's message:\n%s", stderr.String())
	}
}

func TestImplAcceptsSharedDryRunAndTotalBudget(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := newApp().Main(context.Background(), []string{"--dry-run", "--total-budget", "3", "--version"},
		strings.NewReader(""), &stdout, &stderr)
	if code != toolio.ExitOK {
		t.Fatalf("code = %d, stderr: %s", code, stderr.String())
	}
}
