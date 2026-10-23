package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
)

// rawRun runs the shell of the binary and returns what it wrote, which is text for the help.
func rawRun(t *testing.T, stdin string, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	var out, errb bytes.Buffer
	code = newApp().Main(context.Background(), append([]string{"af"}, args...), strings.NewReader(stdin), &out, &errb)
	return code, out.String(), errb.String()
}

// modeEnv is an empty working directory, a recording forge that GITHUB_API_URL names, a recording
// service that AF_URL names, and a credential.
type modeEnv struct {
	dir   string
	forge *fakeService
	svc   *fakeService
}

func newModeEnv(t *testing.T) *modeEnv {
	t.Helper()
	e := &modeEnv{dir: inDir(t), forge: newFakeService(t, nil)}
	t.Setenv("GITHUB_API_URL", e.forge.URL)
	t.Setenv("GITHUB_TOKEN", "forge-sentinel")
	e.svc = queueService(t)
	return e
}

// noTrace fails the test if the forge or the service saw a request or a run directory was made.
func (e *modeEnv) noTrace(t *testing.T, what string, otherDirs ...string) {
	t.Helper()
	if n := len(e.forge.Requests()); n != 0 {
		t.Errorf("%s: the forge saw %d requests", what, n)
	}
	if n := len(e.svc.Requests()); n != 0 {
		t.Errorf("%s: the service saw %d requests", what, n)
	}
	noRunDir(t, e.dir)
	for _, d := range otherDirs {
		noRunDir(t, d)
	}
}

const issueURL = "https://github.com/o/r/issues/1"

// TestARefusedModeInvocationReachesNeitherTheForgeNorTheServiceAndMakesNoRunDirectory_TS_16_3
// verifies TS-16-3.
// Verifies: 16-REQ-1.3
func TestARefusedModeInvocationReachesNeitherTheForgeNorTheServiceAndMakesNoRunDirectory_TS_16_3(t *testing.T) {
	e := newModeEnv(t)
	other := t.TempDir()
	for _, args := range [][]string{
		{"fix", issueURL},
		{"triage", "42"},
		{"fix", issueURL, "--local", "--workspace", "w"},
		{"fix", issueURL, "--local", "--wait"},
		{"triage", "x", "--local", "--server", e.svc.URL},
		{"fix", "1", "--workspace", "w", "--dir", other},
		{"triage", "x", "--workspace", "w", "--brain", "scripted"},
		{"triage", "x", "--context", "--local"},
		{"fix", "1", "--context", "--workspace", "w"},
		{"fix", issueURL, "--dir", other},
	} {
		res := afRun(t, "", args...)
		if res.code != 2 || field(res.env, "status") != "usage" {
			t.Errorf("%v: exit %d, status %v, message %q, want 2 and usage", args, res.code, field(res.env, "status"), res.message())
		}
		e.noTrace(t, strings.Join(args, " "), other)
	}

	// No server: the credential and the server are checked after the flags, and nothing is sent.
	t.Setenv("AF_URL", "")
	res := afRun(t, "", "fix", "1", "--workspace", "w")
	if res.code != 2 {
		t.Errorf("no server: exit %d, want 2", res.code)
	}
	e.noTrace(t, "no server", other)
}

// TestWordsAfterDoubleDashAreTheInputAndOnlyTheWordsBeforeItAreScanned_TS_16_4 verifies TS-16-4.
// Verifies: 16-REQ-1.4
func TestWordsAfterDoubleDashAreTheInputAndOnlyTheWordsBeforeItAreScanned_TS_16_4(t *testing.T) {
	e := newModeEnv(t)
	for _, args := range [][]string{{"triage", "--", "--local"}, {"triage", "x", "--", "--workspace"}, {"fix", "--", "--workspace", "w"}} {
		res := afRun(t, "", args...)
		if res.code != 2 || !strings.Contains(res.message(), "choose where the run goes") {
			t.Errorf("%v: exit %d, message %q, want the no-mode usage error", args, res.code, res.message())
		}
	}
	e.noTrace(t, "words after --")

	res := afRun(t, "", "fix", "--workspace", "widgets", "--", "42")
	if res.code != 0 {
		t.Fatalf("fix --workspace widgets -- 42: exit %d, %s", res.code, res.message())
	}
	if reqs := e.svc.Requests(); len(reqs) != 1 || reqs[0].Path != "/api/v1/workspaces/widgets/fix" || !reflect.DeepEqual(reqs[0].bodyJSON(t), any(map[string]any{"issue": float64(42)})) {
		t.Errorf("requests %+v, want the fix queued for issue 42", reqs)
	}
}

// TestAModeWordThatIsTheValueOfAValueTakingFlagIsThatFlagsValue_TS_16_5 verifies TS-16-5.
// Verifies: 16-REQ-1.5
func TestAModeWordThatIsTheValueOfAValueTakingFlagIsThatFlagsValue_TS_16_5(t *testing.T) {
	e := newModeEnv(t)
	for _, args := range [][]string{
		{"triage", "x", "--context", "--local"},
		{"triage", "x", "--context", "--workspace", "w"},
		{"fix", "1", "--output", "--local"},
		{"fix", "1", "--emit-events=--local"},
		{"fix", "1", "--timeout", "--local"},
		{"fix", "1", "--server", "--local"},
	} {
		res := afRun(t, "", args...)
		if res.code != 2 || !strings.Contains(res.message(), "choose where the run goes") {
			t.Errorf("%v: exit %d, message %q, want the no-mode usage error", args, res.code, res.message())
		}
		e.noTrace(t, strings.Join(args, " "))
	}
	// A flag that takes no value leaves the next word alone: here --workspace is read.
	if res := afRun(t, "", "fix", "1", "--dry-run", "--workspace", "w"); res.code != 2 || !strings.Contains(res.message(), "--dry-run") {
		t.Errorf("--dry-run --workspace w: exit %d, message %q, want --dry-run refused in service mode", res.code, res.message())
	}
}

// TestModeFlagsAreReadWithOneDashOrTwoAndAMissingOrEmptyWorkspaceIsAUsageError_TS_16_6 verifies
// TS-16-6.
// Verifies: 16-REQ-1.6
func TestModeFlagsAreReadWithOneDashOrTwoAndAMissingOrEmptyWorkspaceIsAUsageError_TS_16_6(t *testing.T) {
	e := newModeEnv(t)
	for _, form := range [][]string{{"--workspace", "w"}, {"-workspace", "w"}, {"--workspace=w"}, {"-workspace=w"}} {
		before := len(e.svc.Requests())
		res := afRun(t, "", append([]string{"fix", "1"}, form...)...)
		if res.code != 0 || len(e.svc.Requests()) != before+1 || e.svc.Requests()[before].Path != "/api/v1/workspaces/w/fix" {
			t.Errorf("%v: exit %d, %s, want the job queued on w", form, res.code, res.message())
		}
	}
	before := len(e.svc.Requests())
	for _, args := range [][]string{{"fix", "1", "--workspace"}, {"fix", "1", "--workspace="}, {"fix", "1", "--workspace", ""}, {"triage", "x", "-workspace="}} {
		res := afRun(t, "", args...)
		if res.code != 2 || !strings.Contains(res.message(), "--workspace") {
			t.Errorf("%v: exit %d, message %q, want 2 naming --workspace", args, res.code, res.message())
		}
	}
	if n := len(e.svc.Requests()) - before; n != 0 {
		t.Errorf("%d requests were sent for refused invocations", n)
	}
	e.noTrace2(t)
}

// noTrace2 is noTrace for the forge and the run directory only.
func (e *modeEnv) noTrace2(t *testing.T) {
	t.Helper()
	if n := len(e.forge.Requests()); n != 0 {
		t.Errorf("the forge saw %d requests", n)
	}
	noRunDir(t, e.dir)
}

// TestLocalFollowsGoBooleanSyntax_TS_16_7 verifies TS-16-7: --local, --local=true and -local=1
// choose the loop, --local=false counts as not given, and a value ParseBool refuses is a usage error.
// Verifies: 16-REQ-1.7
func TestLocalFollowsGoBooleanSyntax_TS_16_7(t *testing.T) {
	e := newModeEnv(t)
	for _, form := range []string{"--local", "--local=true", "-local=1", "--local=T", "-local=TRUE"} {
		res := afRun(t, "", "fix", form)
		if res.code != 2 || !strings.Contains(res.message(), "no input given") {
			t.Errorf("%s: exit %d, message %q, want the loop's refusal of a missing input", form, res.code, res.message())
		}
	}
	for _, form := range []string{"--local=false", "-local=0", "--local=F"} {
		res := afRun(t, "", "fix", "1", form)
		if res.code != 2 || !strings.Contains(res.message(), "choose where the run goes") {
			t.Errorf("%s alone: exit %d, message %q, want the no-mode usage error", form, res.code, res.message())
		}
		before := len(e.svc.Requests())
		res = afRun(t, "", "fix", "1", form, "--workspace", "w")
		if res.code != 0 || len(e.svc.Requests()) != before+1 {
			t.Errorf("%s with --workspace w: exit %d, %s, want service mode", form, res.code, res.message())
		}
	}
	for _, form := range []string{"--local=maybe", "-local=", "--local=2"} {
		res := afRun(t, "", "fix", "1", form)
		if res.code != 2 || !strings.Contains(res.message(), "--local") || strings.Contains(res.message(), "choose where the run goes") {
			t.Errorf("%s: exit %d, message %q, want 2 naming --local", form, res.code, res.message())
		}
	}
	if n := len(e.forge.Requests()); n != 0 {
		t.Errorf("the forge saw %d requests", n)
	}
}

// TestHelpForTriageAndFixDescribesBothModesAndTheirFlags_TS_16_8 verifies TS-16-8.
// Verifies: 16-REQ-1.8
func TestHelpForTriageAndFixDescribesBothModesAndTheirFlags_TS_16_8(t *testing.T) {
	e := newModeEnv(t)
	for _, c := range []struct {
		name string
		args []string
		cmd  string
	}{
		{"triage --help", []string{"triage", "--help"}, "triage"},
		{"help triage", []string{"help", "triage"}, "triage"},
		{"fix --help", []string{"fix", "--help"}, "fix"},
		{"help fix", []string{"help", "fix"}, "fix"},
		{"fix -h", []string{"fix", "-h"}, "fix"},
	} {
		code, stdout, _ := rawRun(t, "", c.args...)
		if code != 0 {
			t.Errorf("%s: exit %d", c.name, code)
		}
		terms := []string{"--local", "--workspace", "--server", "--wait", "--brain", "--dir", "--context", "--timeout", "--output", "--emit-events", "af " + c.cmd}
		if c.cmd == "triage" {
			terms = append(terms, "--label")
		} else {
			terms = append(terms, "--base", "--attempts", "--no-verify")
		}
		for _, term := range terms {
			if !strings.Contains(stdout, term) {
				t.Errorf("%s: the help does not mention %s:\n%s", c.name, term, stdout)
			}
		}
	}

	// A --help that is the value of a flag is the value, as before.
	res := afRun(t, "", "triage", "x", "--context", "--help")
	if res.code != 2 || !strings.Contains(res.message(), "choose where the run goes") {
		t.Errorf("--context --help: exit %d, message %q, want the no-mode usage error", res.code, res.message())
	}
	e.noTrace(t, "help")
}

// TestLocalModeRefusesServerAndWaitNamingTheFlag_TS_16_9 verifies TS-16-9.
// Verifies: 16-REQ-2.1
func TestLocalModeRefusesServerAndWaitNamingTheFlag_TS_16_9(t *testing.T) {
	e := newModeEnv(t)
	for _, c := range []struct {
		args []string
		flag string
	}{
		{[]string{"fix", "42", "--local", "--server", e.svc.URL}, "--server"},
		{[]string{"fix", "42", "--local", "--server=" + e.svc.URL}, "--server"},
		{[]string{"triage", "42", "--local", "--wait"}, "--wait"},
		{[]string{"triage", "42", "-wait", "--local"}, "--wait"},
	} {
		res := afRun(t, "", c.args...)
		if res.code != 2 || field(res.env, "status") != "usage" || !strings.Contains(res.message(), c.flag) || !strings.Contains(res.message(), "service mode") || strings.Contains(res.message(), "unknown flag") {
			t.Errorf("%v: exit %d, message %q, want 2 naming %s and saying it is a flag of service mode", c.args, res.code, res.message(), c.flag)
		}
		e.noTrace(t, strings.Join(c.args, " "))
	}
}

// TestServiceModeRefusesEveryLoopOnlyFlagNamingItAndSendsNoRequest_TS_16_10 verifies TS-16-10.
// Verifies: 16-REQ-2.2
func TestServiceModeRefusesEveryLoopOnlyFlagNamingItAndSendsNoRequest_TS_16_10(t *testing.T) {
	e := newModeEnv(t)
	both := []string{"dir", "repo", "dry-run", "land", "brain", "sandbox", "borrowed-hands", "budget", "max-turns", "script"}
	extra := map[string][]string{"triage": {"label"}, "fix": {"base", "attempts", "no-verify"}}
	for _, cmd := range []string{"triage", "fix"} {
		for _, name := range append(slices.Clone(both), extra[cmd]...) {
			for _, dash := range []string{"--", "-"} {
				res := afRun(t, "", cmd, "1", "--workspace", "w", dash+name, "v")
				if res.code != 2 || field(res.env, "status") != "usage" || !strings.Contains(res.message(), "--"+name) ||
					!strings.Contains(res.message(), "--local") || strings.Contains(res.message(), "unknown flag") {
					t.Errorf("%s %s%s: exit %d, message %q, want 2 naming --%s and saying it belongs to --local", cmd, dash, name, res.code, res.message(), name)
				}
			}
		}
	}
	e.noTrace(t, "loop-only flags")

	// The flags that are not the loop's own are not refused for the other command.
	res := afRun(t, "", "fix", "1", "--workspace", "w", "--label", "x")
	if res.code != 2 || !strings.Contains(res.message(), "label") {
		t.Errorf("fix --label: exit %d, message %q, want 2 naming it (unknown to fix)", res.code, res.message())
	}
}

// TestContextTimeoutOutputAndEmitEventsAreAcceptedInBothModes_TS_16_11 verifies TS-16-11.
// Verifies: 16-REQ-2.3
func TestContextTimeoutOutputAndEmitEventsAreAcceptedInBothModes_TS_16_11(t *testing.T) {
	e := newModeEnv(t)
	adir := filepath.Join(e.dir, "adir")
	if err := os.Mkdir(adir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, cmd := range []string{"triage", "fix"} {
		for _, flag := range [][]string{{"--context", "c"}, {"--timeout", "30s"}, {"--output", filepath.Join(t.TempDir(), "out.json")}, {"--emit-events", filepath.Join(t.TempDir(), "ev.jsonl")}} {
			service := afRun(t, "", append([]string{cmd, "1", "--workspace", "w"}, flag...)...)
			if service.code != 0 {
				t.Errorf("%s %v in service mode: exit %d, %s", cmd, flag, service.code, service.message())
			}
			// The loop is given a directory as its input, so that it refuses it before it does anything.
			local := afRun(t, "", append([]string{cmd, adir, "--local"}, flag...)...)
			if strings.Contains(local.message(), "unknown flag") || strings.Contains(local.message(), "refused") || strings.Contains(local.message(), "choose where") {
				t.Errorf("%s %v in local mode: message %q, want no complaint about the flag", cmd, flag, local.message())
			}
			if !strings.Contains(local.message(), "adir") {
				t.Errorf("%s %v in local mode: message %q, want the loop's own refusal of the directory", cmd, flag, local.message())
			}
		}
	}
	if n := len(e.forge.Requests()); n != 0 {
		t.Errorf("the forge saw %d requests", n)
	}
}
