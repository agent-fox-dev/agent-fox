package main

import (
	"bytes"
	"context"
	"flag"
	"sort"
	"strings"
	"testing"

	"github.com/agent-fox-dev/agentfox/internal/toolio"
)

// TS-06-53 (unit): fix's usage names the branch and the commits as still
// happening under --dry-run.
func TestTS06_53_FixUsageStatesDryRunKeepsBranchAndCommits(t *testing.T) {
	flat := strings.Join(strings.Fields(usage), " ")
	i := strings.Index(flat, "--dry-run")
	if i < 0 {
		t.Fatalf("fix's usage does not describe --dry-run:\n%s", usage)
	}
	para := flat[i:]
	if !strings.Contains(para, "branch") || !strings.Contains(para, "commit") {
		t.Errorf("fix's --dry-run text does not name the branch and the commits: %s", para)
	}
}

func TestFixFlagsMatchTheToolFlagTable(t *testing.T) {
	app := newApp()
	var common toolio.Common
	fs := flag.NewFlagSet("fix", flag.ContinueOnError)
	common.Register(fs)
	shared := map[string]bool{}
	fs.VisitAll(func(f *flag.Flag) { shared[f.Name] = true })

	own := flag.NewFlagSet("fix-own", flag.ContinueOnError)
	app.Flags(own)
	var names []string
	own.VisitAll(func(f *flag.Flag) {
		if shared[f.Name] {
			t.Errorf("--%s is defined by the tool as well as by Common", f.Name)
		}
		names = append(names, f.Name)
	})
	want := toolio.ToolFlags("fix")
	sort.Strings(names)
	sort.Strings(want)
	if strings.Join(names, ",") != strings.Join(want, ",") {
		t.Errorf("fix registers %v, the unsupported-flag table says %v", names, want)
	}
}

// Every shared flag that takes a value is known to normalizeArgs, so a value
// token is never mistaken for the input.
func TestKnownFixValueFlagsCoverSharedValueFlags(t *testing.T) {
	var common toolio.Common
	fs := flag.NewFlagSet("fix", flag.ContinueOnError)
	common.Register(fs)
	fs.VisitAll(func(f *flag.Flag) {
		if bf, ok := f.Value.(interface{ IsBoolFlag() bool }); ok && bf.IsBoolFlag() {
			return
		}
		if !knownFixValueFlags[f.Name] {
			t.Errorf("--%s takes a value but is missing from knownFixValueFlags", f.Name)
		}
	})
}

func TestFixAcceptsSharedDryRunAndTotalBudget(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := newApp().Main(context.Background(), normalizeArgs([]string{"--dry-run", "--total-budget", "3", "--version"}),
		strings.NewReader(""), &stdout, &stderr)
	if code != toolio.ExitOK {
		t.Fatalf("code = %d, stderr: %s", code, stderr.String())
	}
}
